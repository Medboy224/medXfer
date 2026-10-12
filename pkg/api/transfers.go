package api

// The daemon's transfers on protocol v1 (bible §8.3, WP-4.3c): one offer covers a file,
// several files or a folder, and the receiver's scheduler requests every chunk. The
// dashboard's actions keep their names (pause, resume, skip_file, pause_file, resume_file,
// cancel, respond_offer) and now drive that scheduler; between devices they travel as
// pause, resume and skip{transfer_id, item} (decision D6).

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

// errBusy refuses a second transfer while one is active (LIM-08).
var errBusy = errors.New("a transfer is already in progress with this device")

// outgoingOffer is what this daemon offered its peer, until the peer accepts or rejects it.
type outgoingOffer struct {
	offer protocol.Offer
	files []string
	l     *transfer.Listener // the offer's data connections, joinable by the peer only (CHAN-06)
}

// close withdraws the offer: nobody may join it any more.
func (o *outgoingOffer) close() {
	if o != nil && o.l != nil {
		o.l.Close()
	}
}

// offerName is how the dashboard names an offer.
func offerName(o protocol.Offer) string {
	if len(o.Items) == 1 {
		return o.Items[0].Path
	}
	return fmt.Sprintf("%s (%d files)", o.Root, len(o.Items))
}

// queueOf is the queue the dashboard shows for an offer, every item pending.
func queueOf(o protocol.Offer) []BatchFileInfo {
	items := make([]BatchFileInfo, len(o.Items))
	for i, it := range o.Items {
		items[i] = BatchFileInfo{Index: it.Item, RelPath: it.Path, Size: it.Size, Status: "pending"}
	}
	return items
}

// chunkSize is the chunk size of this daemon's offers: the configured one if the protocol
// allows it (XFER-02), the default otherwise.
func (s *DaemonServer) chunkSize() uint32 {
	c := uint32(s.config.ChunkSizeMB) << 20
	if c < protocol.MinChunkSize || c > protocol.MaxChunkSize {
		return protocol.DefaultChunkSize
	}
	return c
}

// queue returns a copy of the dashboard's queue. Called with s.mu held.
func (s *DaemonServer) queue() []BatchFileInfo {
	return append([]BatchFileInfo(nil), s.currentBatchItems...)
}

// setStatus sets item i's status in the queue, unless it already ended or the user decided
// otherwise (keep). It reports whether it changed. Called with s.mu held.
func (s *DaemonServer) setStatus(i int, status string, keep ...string) bool {
	for k := range s.currentBatchItems {
		it := &s.currentBatchItems[k]
		if it.Index != i {
			continue
		}
		for _, st := range keep {
			if it.Status == st {
				return false
			}
		}
		it.Status = status
		return true
	}
	return false
}

// onlyPausedLeft reports whether every item not yet ended is paused, with at least one:
// the transfer then waits for the user (batch_paused_waiting). Called with s.mu held.
func (s *DaemonServer) onlyPausedLeft() bool {
	paused := 0
	for _, it := range s.currentBatchItems {
		switch it.Status {
		case "completed", "skipped", "failed":
		case "paused":
			paused++
		default:
			return false
		}
	}
	return paused > 0
}

// endTransfer forgets transfer id once it is over. Called without s.mu.
func (s *DaemonServer) endTransfer(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTransferID != id {
		return
	}
	if s.transferCancel != nil {
		s.transferCancel()
		s.transferCancel = nil
	}
	s.activeReceiver = nil
	s.activeTransferID = ""
	s.isPaused = false
}

// dropTransfers forgets every transfer of the session that ends. Called with s.mu held.
func (s *DaemonServer) dropTransfers() {
	if s.transferCancel != nil {
		s.transferCancel()
		s.transferCancel = nil
	}
	s.activeReceiver = nil
	s.activeTransferID = ""
	s.isPaused = false
	s.lastOffered.close()
	s.lastOffered, s.pendingOffer, s.incomingOffer = nil, nil, nil
}

// sendOverSession offers paths to the paired device: one offer, joinable from now on by
// that device only (CHAN-06). A new offer replaces the previous one; none is made while a
// transfer is active (LIM-08).
func (s *DaemonServer) sendOverSession(paths []string, reqID string) error {
	s.mu.RLock()
	sess := s.activeSession
	devName := s.config.DeviceName
	busy := s.transferCancel != nil
	chunk := s.chunkSize()
	s.mu.RUnlock()
	fail := func(err error) error {
		s.Broadcast(NewEvent("action_error", map[string]string{"error": err.Error()}, reqID))
		return err
	}
	if sess == nil {
		return fmt.Errorf("no active session with peer")
	}
	if busy {
		return fail(errBusy)
	}

	offer, files, err := transfer.BuildOffer(paths, chunk)
	if err != nil {
		return fail(err)
	}
	tid, tl, err := s.registerTransfer(sess)
	if err != nil {
		return fail(err)
	}
	offer.TransferID = tid.String()
	o := &outgoingOffer{offer: offer, files: files, l: tl}

	s.mu.Lock()
	s.lastOffered.close()
	s.lastOffered = o
	s.currentBatchItems = queueOf(offer)
	s.isPaused = false
	items := s.queue()
	s.mu.Unlock()

	msg, follow := session.OfferMessages(session.Message{Type: "offer", DeviceName: devName, DataPort: s.devicePort(), TransferID: offer.TransferID}, offer)
	for _, m := range append([]session.Message{msg}, follow...) {
		if err := sess.Send(m); err != nil {
			s.mu.Lock()
			if s.lastOffered == o {
				s.lastOffered = nil
			}
			s.mu.Unlock()
			o.close()
			return fail(fmt.Errorf("failed sending offer: %w", err))
		}
	}

	if len(offer.Items) > 1 {
		s.Broadcast(NewEvent("batch_offered", map[string]interface{}{
			"summary":     offerName(offer),
			"total_files": len(offer.Items),
			"total_bytes": offer.TotalBytes,
			"items":       items,
		}, reqID))
	} else {
		s.Broadcast(NewEvent("file_offered", map[string]interface{}{
			"file_name": offer.Items[0].Path,
			"file_size": offer.TotalBytes,
		}, reqID))
	}
	return nil
}

// takeOffer receives an offer, or a page of the offer being received (D2). Once all its
// items are there and it is valid (LIM-05), it waits for the user's respond_offer.
func (s *DaemonServer) takeOffer(msg session.Message) {
	s.mu.Lock()
	if msg.Type != session.TypeOfferItems {
		m := msg
		s.incomingOffer = &m
	}
	in := s.incomingOffer
	if in == nil || in.Offer == nil || msg.TransferID != in.TransferID {
		s.incomingOffer = nil
		s.mu.Unlock()
		if msg.Type != session.TypeOfferItems {
			s.refuseOffer(msg.TransferID, "", fmt.Errorf("%w: no items", protocol.ErrBadOffer))
		}
		return
	}
	var err error
	if msg.Type == session.TypeOfferItems {
		err = in.Offer.AddPage(msg.Items)
	}
	if err == nil && !in.Offer.Complete() {
		s.mu.Unlock()
		return
	}
	s.incomingOffer = nil
	if err == nil {
		in.Offer.Count = 0
		if err = in.Offer.Validate(); err == nil && s.transferCancel != nil {
			err = errBusy
		}
	}
	if err != nil {
		s.mu.Unlock()
		code := ""
		if errors.Is(err, errBusy) {
			code = "busy"
		}
		s.refuseOffer(in.TransferID, code, err)
		return
	}
	offer := *in.Offer
	s.pendingOffer = in
	s.currentBatchItems = queueOf(offer)
	s.isPaused = false
	items := s.queue()
	devName := in.DeviceName
	if devName == "" {
		devName = s.pairedDeviceName
	}
	if devName == "" {
		devName = "Peer"
	}
	sess := s.activeSession
	s.mu.Unlock()

	senderIP := ""
	if sess != nil {
		senderIP = sess.RemoteIP()
	}
	s.Broadcast(NewEvent("incoming_offer", IncomingOfferData{
		SenderIP:   senderIP,
		DeviceName: devName,
		IsBatch:    len(offer.Items) > 1,
		FileName:   offerName(offer),
		FileSize:   offer.TotalBytes,
		TotalFiles: len(offer.Items),
		BatchID:    offer.TransferID,
		Items:      items,
	}))
}

// refuseOffer answers an offer this daemon cannot take with reject, and tells the dashboard.
func (s *DaemonServer) refuseOffer(id, code string, err error) {
	s.mu.RLock()
	sess := s.activeSession
	s.mu.RUnlock()
	if sess != nil {
		_ = sess.Send(session.Message{Type: "reject", TransferID: id, Code: code, Error: err.Error()})
	}
	s.Broadcast(NewEvent("log", map[string]string{
		"level":   "WARN",
		"message": fmt.Sprintf("Refused the peer's offer: %v", err),
	}))
}

// receive downloads an accepted offer into saveDir, driven by the dashboard's queue: the
// items the user already skipped or paused stay so.
func (s *DaemonServer) receive(sess *session.Channel, m *session.Message, saveDir string, policy engine.CollisionPolicy, reqID string) {
	offer := *m.Offer
	tid, err := transfer.ParseID(offer.TransferID)
	if err != nil {
		s.refuseOffer(offer.TransferID, "", err)
		return
	}
	rx := transfer.NewReceiver(saveDir, transfer.JoinFunc(s.streamChanConfig(), tid))
	rx.Policy = policy
	ev := newDaemonEvents(s, offer, false, nil)
	rx.Events = ev

	ctx, cancel := context.WithCancel(s.ctx)
	s.mu.Lock()
	if s.transferCancel != nil {
		s.mu.Unlock()
		cancel()
		s.refuseOffer(offer.TransferID, "busy", errBusy)
		return
	}
	rx.Workers = s.config.Workers
	s.transferCancel = cancel
	s.activeReceiver = rx
	s.activeTransferID = offer.TransferID
	for _, it := range s.currentBatchItems {
		switch it.Status {
		case "skipped":
			rx.SkipItem(it.Index)
		case "paused":
			rx.PauseItem(it.Index)
		}
	}
	if s.isPaused {
		rx.Pause()
	}
	s.mu.Unlock()

	_ = sess.Send(session.Message{Type: "accept", TransferID: offer.TransferID, ChunkSize: offer.ChunkSize})
	if len(offer.Items) > 1 {
		s.Broadcast(NewEvent("batch_accepted", map[string]interface{}{"save_dir": saveDir}, reqID))
	}

	go func() {
		addr := net.JoinHostPort(sess.RemoteIP(), strconv.Itoa(m.DataPort))
		results, err := rx.Run(ctx, addr, offer)
		ev.done()
		canceled := ctx.Err() != nil
		s.endTransfer(offer.TransferID)
		if err != nil {
			if !canceled {
				s.Broadcast(NewEvent("transfer_error", map[string]string{"file": offerName(offer), "error": err.Error()}))
			}
			return
		}
		failed := 0
		for _, r := range results {
			if r.State == transfer.ItemFailed {
				failed++
			}
		}
		_ = sess.Send(session.Message{Type: "complete", TransferID: offer.TransferID})
		message := "File received successfully"
		if len(offer.Items) > 1 {
			message = "All batch files mirrored successfully"
		}
		if failed > 0 {
			message = fmt.Sprintf("Transfer finished: %d of %d files failed", failed, len(results))
		}
		s.mu.RLock()
		items := s.queue()
		s.mu.RUnlock()
		s.broadcastTransferComplete(map[string]interface{}{"message": message, "items": items})
	}()
}

// serveAccepted serves the offer the peer accepted on its data connections.
func (s *DaemonServer) serveAccepted(msg session.Message) {
	s.mu.Lock()
	o := s.lastOffered
	s.lastOffered = nil
	if o == nil || msg.TransferID != o.offer.TransferID {
		s.mu.Unlock()
		o.close()
		s.Broadcast(NewEvent("log", map[string]string{"level": "WARN", "message": "The peer accepted an offer this device no longer makes"}))
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.transferCancel = cancel
	s.activeTransferID = o.offer.TransferID
	items := s.queue()
	s.mu.Unlock()

	s.Broadcast(NewEvent("transfer_accepted", map[string]interface{}{}))
	if len(o.offer.Items) > 1 {
		s.Broadcast(NewEvent("batch_accepted", map[string]interface{}{"summary": offerName(o.offer), "items": items}))
	}
	// The sender sees the offer as a whole: the receiver's scheduler picks the items.
	s.Broadcast(NewEvent("transfer_start", map[string]interface{}{
		"current_file":      offerName(o.offer),
		"file_size":         o.offer.TotalBytes,
		"file_index":        1,
		"total_files":       len(o.offer.Items),
		"batch_total_bytes": o.offer.TotalBytes,
	}))
	go func() {
		defer o.close()
		defer s.endTransfer(o.offer.TransferID)
		sender, err := transfer.NewSender(o.offer, o.files)
		if err != nil {
			s.Broadcast(NewEvent("transfer_error", map[string]string{"file": offerName(o.offer), "error": err.Error()}))
			return
		}
		ev := newDaemonEvents(s, o.offer, true, cancel)
		sender.SetEvents(ev)
		err = sender.Serve(ctx, o.l)
		ev.done()
		if err != nil && ctx.Err() == nil {
			s.Broadcast(NewEvent("transfer_error", map[string]string{"file": offerName(o.offer), "error": err.Error()}))
		}
	}()
}

// peerCompleted ends this side of a transfer the receiver has finished.
func (s *DaemonServer) peerCompleted(msg session.Message) {
	s.mu.Lock()
	if msg.TransferID != "" && msg.TransferID != s.activeTransferID {
		s.mu.Unlock()
		return
	}
	if s.transferCancel != nil {
		s.transferCancel()
		s.transferCancel = nil
	}
	for _, it := range s.currentBatchItems {
		s.setStatus(it.Index, "completed", "skipped", "failed")
	}
	items := s.queue()
	s.mu.Unlock()
	s.broadcastTransferComplete(map[string]interface{}{
		"message": "Receiver confirmed transfer complete",
		"items":   items,
	})
}

// Item actions: the dashboard's skip_file, pause_file and resume_file, or the same request
// from the peer. The receiver's scheduler applies them; the other side is told.
const (
	itemSkip   = "skip"
	itemPause  = "pause"
	itemResume = "resume"
)

func (s *DaemonServer) itemAction(action string, i int, fromPeer bool, reqID string) {
	s.mu.Lock()
	var event string
	switch action {
	case itemSkip:
		event = "file_skipped"
		s.setStatus(i, "skipped", "completed", "failed")
	case itemPause:
		event = "file_paused"
		s.setStatus(i, "paused", "completed", "skipped", "failed")
	case itemResume:
		event = "file_resumed"
		s.setStatus(i, "pending", "completed", "skipped", "failed", "transferring")
	}
	if rx := s.activeReceiver; rx != nil {
		switch action {
		case itemSkip:
			rx.SkipItem(i)
		case itemPause:
			rx.PauseItem(i)
		case itemResume:
			rx.ResumeItem(i)
		}
	}
	id := s.activeTransferID
	if id == "" && s.lastOffered != nil {
		id = s.lastOffered.offer.TransferID
	}
	if id == "" && s.pendingOffer != nil {
		id = s.pendingOffer.TransferID
	}
	sess := s.activeSession
	items := s.queue()
	waiting := s.activeReceiver != nil && s.onlyPausedLeft()
	s.mu.Unlock()

	if sess != nil && !fromPeer {
		_ = sess.Send(session.Message{Type: action, TransferID: id, Item: &i})
	}
	if reqID != "" {
		s.Broadcast(NewEvent(event, map[string]interface{}{"item_index": i, "items": items}, reqID))
	} else {
		s.Broadcast(NewEvent(event, map[string]interface{}{"item_index": i, "items": items}))
	}
	if waiting {
		s.broadcastPausedWaiting(items)
	}
}

func (s *DaemonServer) broadcastPausedWaiting(items []BatchFileInfo) {
	s.Broadcast(NewEvent("batch_paused_waiting", map[string]interface{}{
		"message": "Remaining files are paused. Click Resume on any file to continue.",
		"items":   items,
	}))
}

// pauseAll pauses or resumes the whole transfer, here and, unless asked by the peer, there.
func (s *DaemonServer) pauseAll(pause, fromPeer bool, reqID string) {
	s.mu.Lock()
	s.isPaused = pause
	if rx := s.activeReceiver; rx != nil {
		if pause {
			rx.Pause()
		} else {
			rx.Resume()
		}
	}
	sess := s.activeSession
	id := s.activeTransferID
	s.mu.Unlock()

	typ, event, by := "resume", "transfer_resumed", "user"
	if pause {
		typ, event = "pause", "transfer_paused"
	}
	if fromPeer {
		by = "peer"
	} else if sess != nil {
		_ = sess.Send(session.Message{Type: typ, TransferID: id})
	}
	data := map[string]string{"message": fmt.Sprintf("Transfer %sd by %s", typ, by)}
	if reqID != "" {
		s.Broadcast(NewEvent(event, data, reqID))
	} else {
		s.Broadcast(NewEvent(event, data))
	}
}

// cancelTransfer ends the transfer in progress, and withdraws the offer waiting for an answer.
func (s *DaemonServer) cancelTransfer(fromPeer bool, reqID string) {
	s.mu.Lock()
	s.isPaused = false
	if s.transferCancel != nil {
		s.transferCancel()
		s.transferCancel = nil
	}
	s.activeReceiver = nil
	id := s.activeTransferID
	s.activeTransferID = ""
	s.lastOffered.close()
	s.lastOffered = nil
	sess := s.activeSession
	s.mu.Unlock()

	if fromPeer {
		s.Broadcast(NewEvent("transfer_canceled", map[string]string{"message": "Transfer canceled by peer"}))
		return
	}
	if sess != nil {
		_ = sess.Send(session.Message{Type: "cancel", TransferID: id})
	}
	s.Broadcast(NewEvent("transfer_canceled", map[string]string{"message": "Transfer canceled by user"}, reqID))
}

// daemonEvents shows a transfer on the dashboard: progress of the whole offer, and the
// queue's item states on the receiver's side.
type daemonEvents struct {
	s      *DaemonServer
	offer  protocol.Offer
	sender bool
	cancel context.CancelFunc // sender: called once the receiver is done (#44)
	name   string

	mu         sync.Mutex
	lastUpdate time.Time
	current    int // the item last started, for the progress line
	finished   bool
}

func newDaemonEvents(s *DaemonServer, offer protocol.Offer, sender bool, cancel context.CancelFunc) *daemonEvents {
	return &daemonEvents{s: s, offer: offer, sender: sender, cancel: cancel, name: offerName(offer)}
}

// OnStage feeds the transfer report with the time of each stage of each chunk (DEV-03).
func (e *daemonEvents) OnStage(stage string, parallel int, d time.Duration, bytes int64) {
	e.s.RecordTransferStage(e.name, len(e.offer.Items), e.offer.TotalBytes, stage, parallel, d, bytes)
}

func (e *daemonEvents) OnProgress(p transfer.Progress) {
	e.mu.Lock()
	if p.Finished && !p.Partial {
		p.Bytes = p.Total // a resume or a file already there: the receiver has everything
	}
	// The sender is done when the receiver says so (DONE, #33, #44).
	if e.sender && !e.finished && p.Finished {
		e.finished = true
		if e.cancel != nil {
			e.cancel()
		}
	}
	now := time.Now()
	if now.Sub(e.lastUpdate) < 100*time.Millisecond && p.Bytes < p.Total {
		e.mu.Unlock()
		return // at most 10 updates per second on the WebSocket
	}
	e.lastUpdate = now
	current := e.current
	e.mu.Unlock()

	percent := 100.0
	if p.Total > 0 {
		percent = float64(p.Bytes) / float64(p.Total) * 100
	}
	s := e.s
	s.mu.RLock()
	paused := s.isPaused
	s.mu.RUnlock()
	speed, eta := p.SpeedMBps, 0
	if paused {
		speed = 0
	} else if speed > 0 && p.Total > p.Bytes {
		eta = int(float64(p.Total-p.Bytes) / (speed * 1024 * 1024))
	}
	name := offerName(e.offer)
	if current >= 0 && current < len(e.offer.Items) {
		name = e.offer.Items[current].Path
	}

	s.RecordTransferSample(engine.TransferStats{
		BytesTransferred: p.Bytes, TotalBytes: p.Total, SpeedMBps: p.SpeedMBps,
		ActiveStreams: p.Streams, ProgressPercent: percent,
	}, percent, offerName(e.offer), len(e.offer.Items), e.offer.TotalBytes)

	s.Broadcast(NewEvent("transfer_progress", TransferProgressData{
		CurrentFile:     name,
		FileIndex:       min(p.ItemsDone+1, p.Items),
		TotalFiles:      p.Items,
		FileBytes:       p.Bytes,
		FileTotalBytes:  p.Total,
		BatchBytes:      p.Bytes,
		BatchTotalBytes: p.Total,
		SpeedMBps:       speed,
		FilePercent:     percent,
		BatchPercent:    percent,
		EtaSeconds:      eta,
		IsPaused:        paused,
	}))
}

func (e *daemonEvents) OnItem(ev transfer.ItemEvent) {
	s := e.s
	s.mu.Lock()
	var event string
	switch ev.State {
	case transfer.ItemStarted:
		s.setStatus(ev.Item, "transferring", "paused", "skipped", "completed", "failed")
	case transfer.ItemDone:
		s.setStatus(ev.Item, "completed")
		event = "item_completed"
	case transfer.ItemSkipped:
		s.setStatus(ev.Item, "skipped")
		event = "file_skipped"
	case transfer.ItemFailed:
		s.setStatus(ev.Item, "failed")
		event = "item_failed"
	}
	items := s.queue()
	waiting := ev.State != transfer.ItemStarted && s.onlyPausedLeft()
	s.mu.Unlock()

	if ev.State == transfer.ItemStarted {
		e.mu.Lock()
		e.current = ev.Item
		e.mu.Unlock()
		s.Broadcast(NewEvent("transfer_start", map[string]interface{}{
			"current_file":      ev.Path,
			"file_size":         ev.Size,
			"file_index":        ev.Item + 1,
			"total_files":       len(e.offer.Items),
			"batch_total_bytes": e.offer.TotalBytes,
		}))
		if len(e.offer.Items) > 1 {
			s.Broadcast(NewEvent("batch_item_transferring", map[string]interface{}{"item_index": ev.Item, "items": items}))
		}
		return
	}
	data := map[string]interface{}{"item_index": ev.Item, "items": items}
	if ev.Err != nil {
		data["error"] = ev.Err.Error()
		s.Broadcast(NewEvent("transfer_error", map[string]string{"file": ev.Path, "error": ev.Err.Error()}))
	}
	s.Broadcast(NewEvent(event, data))
	if waiting {
		s.broadcastPausedWaiting(items)
	}
}

// done is called once the transfer is over.
func (e *daemonEvents) done() {
	e.mu.Lock()
	e.finished = true
	e.mu.Unlock()
}
