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
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/store"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

// errBusy refuses a second transfer while one is active (LIM-08).
var errBusy = errors.New("a transfer is already in progress with this device")

// outgoingOffer is what this daemon offered its peer, until the peer accepts or rejects it.
type outgoingOffer struct {
	offer protocol.Offer
	files []string
	open  []*os.File         // files open already (descriptors), instead of files: closed with the offer
	l     *transfer.Listener // the offer's data connections, joinable by the peer only (CHAN-06)
}

// close withdraws the offer: nobody may join it any more, and its descriptors are closed.
func (o *outgoingOffer) close() {
	if o == nil {
		return
	}
	if o.l != nil {
		o.l.Close()
	}
	for _, f := range o.open {
		_ = f.Close()
	}
}

// sendSource is what a send offers: paths, or files open already (STO-16, WP-B.0).
type sendSource struct {
	paths []string
	files []*os.File
	names []string
}

// sourceOf reads a send payload. Descriptors become files at once: the core owns them.
func sourceOf(p SendPayload) (sendSource, error) {
	if len(p.Items) > 0 && len(p.Paths) > 0 {
		for _, it := range p.Items {
			if it.FD >= 0 {
				_ = os.NewFile(uintptr(it.FD), it.Name).Close()
			}
		}
		return sendSource{}, errors.New("send: paths or items, not both")
	}
	if len(p.Items) == 0 {
		if len(p.Paths) == 0 {
			return sendSource{}, errors.New("missing or invalid paths")
		}
		return sendSource{paths: p.Paths}, nil
	}
	var src sendSource
	for _, it := range p.Items {
		if it.FD < 0 {
			src.close()
			return sendSource{}, fmt.Errorf("send: invalid descriptor %d", it.FD)
		}
		src.files = append(src.files, os.NewFile(uintptr(it.FD), it.Name))
		src.names = append(src.names, it.Name)
	}
	return src, nil
}

// close closes the source's descriptors: the send did not go through.
func (src sendSource) close() {
	for _, f := range src.files {
		_ = f.Close()
	}
}

// offer describes the source as one offer.
func (src sendSource) offer(chunk uint32) (protocol.Offer, []string, error) {
	if src.files != nil {
		offer, err := transfer.BuildOfferFiles(src.files, src.names, chunk)
		return offer, nil, err
	}
	return transfer.BuildOffer(src.paths, chunk)
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
	s.endedTransferID = id // the receiver's "complete" may still be on its way (#129)
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
	s.endedTransferID = ""
	s.isPaused = false
	s.lastOffered.close()
	s.lastOffered, s.pendingOffer, s.incomingOffer = nil, nil, nil
}

// sendOverSession offers src to the paired device: one offer, joinable from now on by
// that device only (CHAN-06). A new offer replaces the previous one; none is made while a
// transfer is active (LIM-08). Once the offer is made, it owns src's descriptors; on an error,
// the caller still does (it may try again on a new session).
func (s *DaemonServer) sendOverSession(src sendSource, reqID string) error {
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

	offer, files, err := src.offer(chunk)
	if err != nil {
		return fail(err)
	}
	tid, tl, err := s.registerTransfer(sess)
	if err != nil {
		return fail(err)
	}
	offer.TransferID = tid.String()
	o := &outgoingOffer{offer: offer, files: files, open: src.files, l: tl}

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
			o.l.Close() // withdrawn; its descriptors stay with the caller, which may try again
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
	} else {
		err = in.Offer.CheckHead() // audit 6: the count is checked on the first frame
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
	saveDir, policy := s.config.DownloadDir, s.parseCollisionPolicy(s.config.CollisionPolicy)
	s.mu.Unlock()

	senderIP := ""
	if sess != nil {
		senderIP = sess.RemoteIP()
	}
	space := s.spaceFor(offer, saveDir, policy)
	s.Broadcast(NewEvent("incoming_offer", IncomingOfferData{
		SenderIP:   senderIP,
		DeviceName: devName,
		IsBatch:    len(offer.Items) > 1,
		FileName:   offerName(offer),
		FileSize:   offer.TotalBytes,
		TotalFiles: len(offer.Items),
		BatchID:    offer.TransferID,
		Items:      items,

		FreeBytes:   space.Free,
		NeededBytes: space.Need,
		EnoughSpace: space.Enough(),
	}))
}

// spaceFor says whether offer fits in saveDir, before it is accepted (STO-07, D9).
func (s *DaemonServer) spaceFor(offer protocol.Offer, saveDir string, policy store.Policy) transfer.Space {
	rx := transfer.NewReceiver(saveDir, nil)
	rx.SenderKey = s.streamChanConfig().Expected // what a resume finds is not needed again
	rx.Policy = policy
	space, err := rx.CheckSpace(offer)
	if err != nil {
		return transfer.Space{Free: -1}
	}
	return space
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
func (s *DaemonServer) receive(sess *session.Channel, m *session.Message, saveDir string, policy store.Policy, reqID string) {
	offer := *m.Offer
	tid, err := transfer.ParseID(offer.TransferID)
	if err != nil {
		s.refuseOffer(offer.TransferID, "", err)
		return
	}
	cfg := s.streamChanConfig()
	rx := transfer.NewReceiver(saveDir, transfer.JoinFunc(cfg, tid))
	rx.SenderKey = cfg.Expected // part of each file's resume key (D2)
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
			// Its measures end with it: the next transfer starts its own (WP-B.5). Stopped by
			// a cancel or by the peer's failure, they have ended already, with the reason.
			if canceled {
				s.FinishTransferTracker(diag.OutcomeCanceled)
				return
			}
			s.FinishTransferTracker(diag.OutcomeFailed)
			s.transferFailed(offer, err)
			return
		}
		failed := 0
		for _, r := range results {
			if r.State == transfer.ItemFailed {
				failed++
			}
		}
		s.mu.Lock()
		s.completedTransferID = offer.TransferID // its "complete" may be lost: a check finds it
		s.mu.Unlock()
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
		var sender *transfer.Sender
		var err error
		if o.open != nil {
			sender, err = transfer.NewSenderFiles(o.offer, o.open)
		} else {
			sender, err = transfer.NewSender(o.offer, o.files)
		}
		if err != nil {
			s.transferFailed(o.offer, err)
			return
		}
		ev := newDaemonEvents(s, o.offer, true, cancel)
		sender.SetEvents(ev)
		s.mu.Lock()
		s.outEvents = ev // the receiver's progress goes to it (session.TypeProgress)
		s.mu.Unlock()
		err = sender.Serve(ctx, o.l)
		ev.done()
		s.mu.Lock()
		if s.outEvents == ev {
			s.outEvents = nil
		}
		s.mu.Unlock()
		// Its measures end with it: the next transfer starts its own. What stopped it (the
		// receiver's "complete", a cancel, the peer's failure) ended them first, with the reason.
		if err == nil && ev.receiverDone() {
			s.completeSent(o.offer.TransferID) // a receiver that canceled sends DONE too: no effect
			s.FinishTransferTracker(diag.OutcomeCanceled)
			return
		}
		if err == nil && ev.gaveUp() {
			err = errReceiverGone
		}
		if err != nil && (ctx.Err() == nil || ev.gaveUp()) {
			s.FinishTransferTracker(diag.OutcomeFailed)
			s.transferFailed(o.offer, err)
			return
		}
		s.FinishTransferTracker(diag.OutcomeCanceled)
	}()
}

// pausedNow reports whether the transfer waits for the user: paused whole, or every item left
// paused. The receiver then holds no data connection, and the sender does not give up.
func (s *DaemonServer) pausedNow() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isPaused || s.onlyPausedLeft()
}

// checkTransfer asks the peer of a session just back whether it still has the transfer in
// progress here (session.TypeTransferCheck).
func (s *DaemonServer) checkTransfer(sess *session.Channel, id string) {
	if id != "" {
		_ = sess.Send(session.Message{Type: session.TypeTransferCheck, TransferID: id})
	}
}

// answerCheck answers the peer's transfer_check: "complete" for a transfer completed here,
// "failed" for one this side has no more, nothing for the one in progress.
func (s *DaemonServer) answerCheck(msg session.Message) {
	s.mu.RLock()
	sess, active, completed := s.activeSession, s.activeTransferID, s.completedTransferID
	s.mu.RUnlock()
	if sess == nil || msg.TransferID == "" || msg.TransferID == active {
		return
	}
	if msg.TransferID == completed {
		_ = sess.Send(session.Message{Type: "complete", TransferID: msg.TransferID})
		return
	}
	_ = sess.Send(session.Message{Type: session.TypeFailed, TransferID: msg.TransferID, Error: "the other device no longer has this transfer"})
}

// transferFailed tells the app (transfer_failed) and the peer (session "failed") that the
// transfer failed on this side. Before, only this side's app knew: the peer waited for a
// transfer that was over, both screens "in progress" (WP-B.8).
func (s *DaemonServer) transferFailed(offer protocol.Offer, err error) {
	s.mu.RLock()
	sess := s.activeSession
	s.mu.RUnlock()
	if sess != nil {
		_ = sess.Send(session.Message{Type: session.TypeFailed, TransferID: offer.TransferID, Error: err.Error()})
	}
	s.Broadcast(NewEvent("transfer_failed", map[string]string{
		"transfer_id": offer.TransferID, "file": offerName(offer), "error": err.Error(), "by": "self",
	}))
}

// peerFailed ends this side of a transfer that failed on the peer's.
func (s *DaemonServer) peerFailed(msg session.Message) {
	s.mu.Lock()
	if s.activeTransferID == "" || (msg.TransferID != "" && msg.TransferID != s.activeTransferID) {
		s.mu.Unlock()
		return // over already, or another one
	}
	id := s.activeTransferID
	s.FinishTransferTracker(diag.OutcomeFailed) // before the cancel ends the run (see serveAccepted)
	if s.transferCancel != nil {
		s.transferCancel()
		s.transferCancel = nil
	}
	s.activeReceiver = nil
	s.activeTransferID = ""
	s.isPaused = false
	s.mu.Unlock()
	s.Broadcast(NewEvent("transfer_failed", map[string]string{"transfer_id": id, "error": msg.Error, "by": "peer"}))
}

// peerCompleted ends this side of a transfer the receiver has finished.
// The sender's Serve may end, with endTransfer, before the receiver's "complete" arrives: the
// transfer that just ended is still completed, once (#129).
func (s *DaemonServer) peerCompleted(msg session.Message) { s.completeSent(msg.TransferID) }

// completeSent ends a sent transfer the receiver finished, once: told by its DONE on a data
// connection or by its "complete" on the session, whichever comes first. The DONE usually
// does, and is the only one when the session is lost (WP-B.8).
func (s *DaemonServer) completeSent(id string) {
	s.mu.Lock()
	if id != "" && (id == s.sentCompletedID || (id != s.activeTransferID && id != s.endedTransferID)) {
		s.mu.Unlock()
		return
	}
	if id != "" && id == s.endedTransferID {
		s.endedTransferID = ""
	}
	s.sentCompletedID = id
	s.FinishTransferTracker(diag.OutcomeCompleted) // before the cancel ends the run (see serveAccepted)
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
	rx := s.activeReceiver
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

	// After the unlock: ending an item, the receiver's scheduler tells the events, which take
	// s.mu (OnItem). Called under it, a skip during the transfer stopped the core (#174).
	if rx != nil {
		switch action {
		case itemSkip:
			rx.SkipItem(i)
		case itemPause:
			rx.PauseItem(i)
		case itemResume:
			rx.ResumeItem(i)
		}
	}

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
	rx := s.activeReceiver
	sess := s.activeSession
	id := s.activeTransferID
	s.mu.Unlock()
	// The receiver is never called under s.mu: its events take it (#174).
	if rx != nil {
		if pause {
			rx.Pause()
		} else {
			rx.Resume()
		}
	}

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
		s.FinishTransferTracker(diag.OutcomeCanceled) // before the cancel ends the run
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

	peer       *session.Progress // sender: the receiver's progress, shown instead of what was served
	lastReport time.Time         // receiver: when its progress last went to the sender
	seen       time.Time         // sender: the last time a data connection was open, or a pause
	idle       bool              // sender: given up at senderIdleLimit
}

// receiverDone reports whether the receiver said, on a data connection, that it is done (DONE).
func (e *daemonEvents) receiverDone() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.finished && !e.idle
}

// gaveUp reports whether the sender gave up at senderIdleLimit.
func (e *daemonEvents) gaveUp() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.idle
}

// reportEvery bounds how often the receiver tells the sender its progress.
const reportEvery = 250 * time.Millisecond

// senderIdleLimit is how long a sender serves a transfer with no data connection open, outside
// a pause, before it gives up: longer than a receiver waits for it (a chunk read of a minute,
// then transfer.DefaultReconnectWindow), so the receiver gives up first and says so. Without
// it, a receiver gone and its "failed" lost left the sender serving for ever (WP-B.8).
var senderIdleLimit = 4 * time.Minute

// errReceiverGone is why a sender gives up at senderIdleLimit.
var errReceiverGone = errors.New("the receiver stopped asking for data")

// takePeer keeps the receiver's progress, which the sender's next events show.
func (e *daemonEvents) takePeer(p *session.Progress) {
	e.mu.Lock()
	e.peer = p
	e.mu.Unlock()
}

func newDaemonEvents(s *DaemonServer, offer protocol.Offer, sender bool, cancel context.CancelFunc) *daemonEvents {
	return &daemonEvents{s: s, offer: offer, sender: sender, cancel: cancel, name: offerName(offer)}
}

// OnStage feeds the transfer report with the time of each stage of each chunk (DEV-03).
func (e *daemonEvents) OnStage(stage string, parallel int, d time.Duration, bytes int64) {
	e.s.RecordTransferStage(e.offer.TransferID, e.name, len(e.offer.Items), e.offer.TotalBytes, stage, parallel, d, bytes)
}

func (e *daemonEvents) OnProgress(p transfer.Progress) {
	waiting := e.sender && e.s.pausedNow()
	e.mu.Lock()
	if e.sender && !e.finished {
		now := time.Now()
		if p.Streams > 0 || waiting || e.seen.IsZero() {
			e.seen = now
		} else if now.Sub(e.seen) > senderIdleLimit {
			e.finished, e.idle = true, true
			if e.cancel != nil {
				e.cancel()
			}
		}
	}
	if e.sender && e.peer != nil {
		// What the receiver has, not what was served: both screens read the same (WP-B.10).
		p.Bytes = e.peer.Bytes
		p.Active = nil
		for _, it := range e.peer.Items {
			if it.Index >= 0 && it.Index < len(e.offer.Items) {
				p.Active = append(p.Active, transfer.ItemProgress{Item: it.Index, Bytes: it.Bytes, Size: e.offer.Items[it.Index].Size})
			}
		}
	}
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
	report := !e.sender && (now.Sub(e.lastReport) >= reportEvery || p.Bytes >= p.Total)
	if report {
		e.lastReport = now
	}
	e.mu.Unlock()
	if report {
		e.s.reportProgress(e.offer.TransferID, p)
	}

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
	file := fileProgress(e.offer, p, current, percent)
	if file.Path != "" {
		name = file.Path
	}
	var items []ItemProgressData
	for _, a := range p.Active {
		if a.Item >= 0 && a.Item < len(e.offer.Items) {
			items = append(items, ItemProgressData{Index: a.Item, Path: e.offer.Items[a.Item].Path,
				Bytes: a.Bytes, Size: a.Size, Percent: share(a.Bytes, a.Size)})
		}
	}

	s.RecordTransferSample(e.offer.TransferID, engine.TransferStats{
		BytesTransferred: p.Bytes, TotalBytes: p.Total, SpeedMBps: p.SpeedMBps,
		ActiveStreams: p.Streams, ProgressPercent: percent,
	}, percent, offerName(e.offer), len(e.offer.Items), e.offer.TotalBytes)
	loads, neck := s.liveStages()

	s.Broadcast(NewEvent("transfer_progress", TransferProgressData{
		CurrentFile:     name,
		FileIndex:       file.Index + 1,
		TotalFiles:      p.Items,
		FileBytes:       file.Bytes,
		FileTotalBytes:  file.Size,
		BatchBytes:      p.Bytes,
		BatchTotalBytes: p.Total,
		SpeedMBps:       speed,
		FilePercent:     file.Percent,
		BatchPercent:    percent,
		EtaSeconds:      eta,
		IsPaused:        paused,
		Items:           items,

		StageMs:              loads,
		Bottleneck:           neck.Stage,
		BottleneckConfidence: neck.Confidence,
	}))
}

// reportProgress tells the sender how far this receiver is (session.TypeProgress).
func (s *DaemonServer) reportProgress(id string, p transfer.Progress) {
	s.mu.RLock()
	sess := s.activeSession
	s.mu.RUnlock()
	if sess == nil {
		return
	}
	report := &session.Progress{Bytes: p.Bytes}
	for _, a := range p.Active {
		report.Items = append(report.Items, session.ItemProgress{Index: a.Item, Bytes: a.Bytes})
	}
	_ = sess.Send(session.Message{Type: session.TypeProgress, TransferID: id, Progress: report})
}

// share is done of size in percent; an empty file is whole.
func share(done, size int64) float64 {
	if size <= 0 {
		return 100
	}
	return min(float64(done)/float64(size)*100, 100)
}

// fileProgress is the file the progress shows (WP-B.0): the first item in progress, not the
// batch, which made both bars show the same percentage. A single file is the batch. Between
// two files, the item the transfer is at, empty or, at the end, whole.
func fileProgress(offer protocol.Offer, p transfer.Progress, current int, batchPercent float64) ItemProgressData {
	if len(p.Active) > 0 {
		a := p.Active[0]
		if a.Item >= 0 && a.Item < len(offer.Items) {
			return ItemProgressData{Index: a.Item, Path: offer.Items[a.Item].Path, Bytes: a.Bytes, Size: a.Size,
				Percent: share(a.Bytes, a.Size)}
		}
	}
	if len(offer.Items) == 1 {
		return ItemProgressData{Path: offer.Items[0].Path, Bytes: p.Bytes, Size: p.Total, Percent: batchPercent}
	}
	i := min(max(current, 0), max(len(offer.Items)-1, 0))
	if i >= len(offer.Items) {
		return ItemProgressData{}
	}
	size := offer.Items[i].Size
	done := int64(0)
	if p.Total > 0 && p.Bytes >= p.Total {
		done = size
	}
	return ItemProgressData{Index: i, Path: offer.Items[i].Path, Bytes: done, Size: size, Percent: share(done, size)}
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
		if errors.Is(ev.Err, store.ErrPathConflict) {
			data["code"] = "PATH_CONFLICT" // STO-11: nothing was deleted
		}
		s.Broadcast(NewEvent("transfer_error", map[string]string{"file": ev.Path, "error": ev.Err.Error()}))
	}
	s.Broadcast(NewEvent(event, data))
	if waiting {
		s.broadcastPausedWaiting(items)
	}
}

// OnDiskFull: the disk filled up and the receiver paused itself, keeping its partial files
// (STO-07). The dashboard and the peer see the transfer paused; resume goes on once there is
// room again.
func (e *daemonEvents) OnDiskFull(err error) {
	s := e.s
	s.mu.Lock()
	s.isPaused = true
	sess, id, saveDir := s.activeSession, s.activeTransferID, s.config.DownloadDir
	s.mu.Unlock()
	if sess != nil {
		_ = sess.Send(session.Message{Type: "pause", TransferID: id})
	}
	message := "The disk is full: transfer paused, the files received so far are kept. Free some space, then resume."
	s.Broadcast(NewEvent("disk_full", map[string]string{
		"code": "DISK_FULL", "message": message, "error": err.Error(), "save_dir": saveDir,
	}))
	s.Broadcast(NewEvent("transfer_paused", map[string]string{"message": message}))
}

// done is called once the transfer is over.
func (e *daemonEvents) done() {
	e.mu.Lock()
	e.finished = true
	e.mu.Unlock()
}
