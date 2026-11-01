package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/manifest"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

func (s *DaemonServer) dispatch(rs Responder, req RequestMessage) {
	switch req.Action {
	case "get_status":
		rs.Reply(NewEvent("status", s.getStatus(), req.ID))

	case "set_config":
		s.handleSetConfig(rs, req)

	case "scan":
		s.handleScan(rs, req)

	case "pair":
		s.handlePair(rs, req)

	case "pair_show":
		s.handlePairShow(rs, req)

	case "pair_uri":
		s.handlePairURI(rs, req)

	case "pair_pin_show":
		s.handlePairPINShow(rs, req)

	case "pin_refuse":
		s.handlePINRefuse(rs, req)

	case "pair_pin":
		s.handlePairPIN(rs, req)

	case "set_discoverable":
		s.handleSetDiscoverable(rs, req)

	case "set_network_info":
		s.handleSetNetworkInfo(rs, req)

	case "trust_list":
		s.handleTrustList(rs, req)

	case "trust_forget":
		s.handleTrustForget(rs, req)

	case "trust_rename":
		s.handleTrustRename(rs, req)

	case "export_bundle":
		s.handleExportBundle(rs, req)

	case "trust_offer":
		s.handleTrustOffer(rs, req)

	case "trust_answer":
		s.handleTrustAnswer(rs, req)

	case "disconnect":
		s.handleDisconnect(rs, req)

	case "send":
		s.handleSend(rs, req)

	case "respond_offer":
		s.handleRespondOffer(rs, req)

	case "cancel":
		s.handleCancel(rs, req)

	case "pause":
		s.handlePause(rs, req)

	case "resume":
		s.handleResume(rs, req)

	case "skip_file":
		s.handleSkipFile(rs, req)

	case "pause_file":
		s.handlePauseFile(rs, req)

	case "resume_file":
		s.handleResumeFile(rs, req)

	case "share_web_files":
		s.handleShareWebFiles(rs, req)

	case "clear_web_files":
		s.handleClearWebFiles(rs, req)

	case "toggle_web_share":
		s.handleToggleWebShare(rs, req)

	case "regenerate_share_pin":
		s.handleRegenerateSharePIN(rs, req)

	case "set_web_share_auto_accept":
		s.handleSetWebShareAutoAccept(rs, req)

	case "accept_web_upload":
		s.handleAcceptWebUpload(rs, req)

	case "reject_web_upload":
		s.handleRejectWebUpload(rs, req)

	case "web_share_pause":
		s.PauseWebShareTransfer()

	case "web_share_resume":
		s.ResumeWebShareTransfer()

	case "web_share_cancel":
		s.CancelWebShareTransfer()

	case "test_disk":
		s.handleTestDisk(rs, req)

	case "test_network":
		s.handleTestNetwork(rs, req)

	case "get_transfer_summary":
		s.handleGetTransferSummary(rs, req)

	default:
		rs.Reply(NewEvent("action_error", map[string]string{
			"error": fmt.Sprintf("unknown action '%s'", req.Action),
		}, req.ID))
	}
}

func (s *DaemonServer) handleSetConfig(rs Responder, req RequestMessage) {
	var cfg Config
	if err := json.Unmarshal(req.Payload, &cfg); err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
		return
	}

	s.mu.Lock()
	if name := CleanDeviceName(cfg.DeviceName); name != "" {
		s.config.DeviceName = name // sent after authentication only, never announced (DISC-06)
	}
	if cfg.DownloadDir != "" {
		s.config.DownloadDir = cfg.DownloadDir
	}
	if cfg.CollisionPolicy != "" {
		s.config.CollisionPolicy = cfg.CollisionPolicy
	}
	if cfg.Workers > 0 {
		s.config.Workers = cfg.Workers
	}
	if cfg.ChunkSizeMB > 0 {
		s.config.ChunkSizeMB = cfg.ChunkSizeMB
	}
	currentCfg := s.config
	s.mu.Unlock()

	if err := SaveConfigTo(filepath.Join(s.dataDir, "config.json"), currentCfg); err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": fmt.Sprintf("failed saving settings: %v", err)}, req.ID))
		return
	}
	s.Broadcast(NewEvent("status", s.getStatus(), req.ID))
}

func (s *DaemonServer) handleScan(rs Responder, req RequestMessage) {
	go func() {
		peers, err := discovery.DiscoverPeers(2 * time.Second)
		if err != nil {
			rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
			return
		}

		var filtered []discovery.Peer
		seen := make(map[string]bool)
		for _, p := range peers {
			if !seen[p.HostIP] {
				seen[p.HostIP] = true
				filtered = append(filtered, p)
			}
		}

		rs.Reply(NewEvent("peers_list", s.recognize(filtered), req.ID))
	}()
}

func (s *DaemonServer) handlePair(rs Responder, req RequestMessage) {
	var payload PairPayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil || (payload.IP == "" && payload.Code == "") {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid pair payload (IP or Code required)"}, req.ID))
		return
	}

	// The code only finds the device on the network (WP-3.6b): trust comes from its key.
	if payload.Code != "" && payload.IP == "" {
		peer, err := discovery.ResolvePairingCode(s.ctx, payload.Code, 3*time.Second)
		if err != nil {
			rs.Reply(NewEvent("action_error", map[string]string{"error": fmt.Sprintf("failed to resolve pairing code '%s': %v", payload.Code, err)}, req.ID))
			return
		}
		payload.IP = peer.HostIP
		if payload.Port <= 0 && peer.Port > 0 {
			payload.Port = peer.Port
		}
	}

	port := payload.Port
	if port <= 0 {
		port = 18887
	}
	target := net.JoinHostPort(payload.IP, strconv.Itoa(int(port)))
	go func() {
		if _, err := s.pairWith(target, nil, payload.Trust, req.ID); err != nil && !errors.Is(err, errPINNeeded) {
			rs.Reply(pairFailed(target, err, req.ID))
		}
	}()
}

// pairFailed is the action_error of a pairing broken off (no answer at the address, a
// handshake cut short), its cause kept in the journal too: the application shows it in a toast
// that goes in seconds, and a failure seen once in a real test stayed unexplained (#169,
// 30 October 2026).
//
// code "unreachable": the connection never opened (no answer, refused, no route), the other
// device's firewall the likely cause; the application says so instead of the raw error (real
// test of 1 November: a PC let medXfer in on its cable only, not on its Wi-Fi).
func pairFailed(target string, err error, id string) EventMessage {
	msg := fmt.Sprintf("failed to pair with %s: %v", target, err)
	journalf("log WARN %s", msg)
	data := map[string]string{"error": msg, "target": target}
	if transfer.DialFailed(err) {
		data["code"] = codeUnreachable
	}
	return NewEvent("action_error", data, id)
}

// The codes of action_error and transfer_failed that the application turns into its own words.
const (
	codeUnreachable       = "unreachable"        // pairing: the other device never answered
	codeSenderUnreachable = "sender_unreachable" // transfer: the receiver never reached the sender
)

func (s *DaemonServer) handleDisconnect(rs Responder, req RequestMessage) {
	s.mu.Lock()
	if s.sessionGraceTimer != nil {
		s.sessionGraceTimer.Stop()
		s.sessionGraceTimer = nil
	}
	s.isReconnecting = false
	s.stopRedial()
	if s.activeSession != nil {
		s.activeSession.Send(session.Message{Type: "disconnect"})
		s.activeSession.Close()
		s.activeSession = nil
	}
	s.pairedDeviceName = ""
	s.dropTransfers()
	s.mu.Unlock()

	s.Broadcast(NewEvent("disconnected", map[string]string{"reason": "user requested disconnect"}, req.ID))
}

func (s *DaemonServer) handleSend(rs Responder, req RequestMessage) {
	var payload SendPayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "missing or invalid paths"}, req.ID))
		return
	}
	src, err := sourceOf(payload)
	if err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
		return
	}

	cleanTarget := targetHost(payload.TargetIP)

	s.mu.RLock()
	sess := s.activeSession
	s.mu.RUnlock()

	// If a specific target IP was provided and we have no session or session is with someone else:
	if payload.TargetIP != "" && (sess == nil || sess.RemoteIP() != cleanTarget) {
		go s.connectAndSend(payload.TargetIP, src, req.ID)
		return
	}

	// If we have an active session, send over it
	if sess != nil {
		go func() {
			err := s.sendOverSession(src, req.ID)
			if err != nil && payload.TargetIP != "" {
				// Old session failed (stale/broken connection), immediately reconnect and retry!
				s.connectAndSend(payload.TargetIP, src, req.ID)
			} else if err != nil {
				src.close()
				rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
			}
		}()
		return
	}

	src.close()
	rs.Reply(NewEvent("action_error", map[string]string{"error": "not paired with any device and no target_ip specified"}, req.ID))
}

// connectAndSend pairs with targetIP, then offers src. On any failure src's descriptors are
// closed: a new send, with new descriptors, tries again.
func (s *DaemonServer) connectAndSend(targetIP string, src sendSource, id string) {
	if _, err := s.pairWith(targetIP, nil, false, id); err != nil {
		src.close()
		if !errors.Is(err, errPINNeeded) { // pin_needed: send again once paired by PIN
			s.Broadcast(pairFailed(targetIP, err, id))
		}
		return
	}
	if err := s.sendOverSession(src, id); err != nil {
		src.close()
	}
}

func (s *DaemonServer) handleRespondOffer(rs Responder, req RequestMessage) {
	var payload RespondOfferPayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid respond_offer payload"}, req.ID))
		return
	}

	s.mu.Lock()
	offer := s.pendingOffer
	s.pendingOffer = nil
	sess := s.activeSession
	s.mu.Unlock()

	if sess == nil || offer == nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "no pending transfer offer to respond to"}, req.ID))
		return
	}

	if !payload.Accept {
		sess.Send(session.Message{Type: "reject", TransferID: offer.TransferID})
		s.Broadcast(NewEvent("transfer_rejected", map[string]string{"message": "Transfer rejected by user"}, req.ID))
		return
	}

	s.mu.Lock()
	if payload.SaveDir != "" {
		s.config.DownloadDir = payload.SaveDir
	}
	if payload.CollisionPolicy != "" {
		s.config.CollisionPolicy = payload.CollisionPolicy
	}
	saveDir := s.config.DownloadDir
	policy := s.parseCollisionPolicy(s.config.CollisionPolicy)
	s.mu.Unlock()

	// An offer that does not fit is refused before its first byte (STO-07, D9). It stays
	// pending: the user may free some space, or choose another folder.
	if err := s.spaceFor(*offer.Offer, saveDir, policy).Err(); err != nil {
		s.mu.Lock()
		if s.pendingOffer == nil {
			s.pendingOffer = offer
		}
		s.mu.Unlock()
		rs.Reply(NewEvent("action_error", map[string]string{
			"code": "NO_SPACE", "error": fmt.Sprintf("cannot receive into %s: %v", saveDir, err),
		}, req.ID))
		return
	}

	// No disk probe: the stages of each chunk (disk_write, fsync) tell whether the disk
	// limits the transfer, without writing anything more (D10, #36).
	s.receive(sess, offer, saveDir, policy, req.ID)
}

func (s *DaemonServer) handlePause(rs Responder, req RequestMessage) {
	s.pauseAll(true, false, req.ID)
}

func (s *DaemonServer) handleResume(rs Responder, req RequestMessage) {
	s.pauseAll(false, false, req.ID)
}

func (s *DaemonServer) handleCancel(rs Responder, req RequestMessage) {
	s.cancelTransfer(false, req.ID)
}

func (s *DaemonServer) handleSkipFile(rs Responder, req RequestMessage) {
	var payload SkipFilePayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid skip_file payload"}, req.ID))
		return
	}
	s.itemAction(itemSkip, payload.ItemIndex, false, req.ID)
}

func (s *DaemonServer) handlePauseFile(rs Responder, req RequestMessage) {
	var payload PauseFilePayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid pause_file payload"}, req.ID))
		return
	}
	s.itemAction(itemPause, payload.ItemIndex, false, req.ID)
}

func (s *DaemonServer) handleResumeFile(rs Responder, req RequestMessage) {
	var payload ResumeFilePayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid resume_file payload"}, req.ID))
		return
	}
	s.itemAction(itemResume, payload.ItemIndex, false, req.ID)
}

func (s *DaemonServer) handleShareWebFiles(rs Responder, req RequestMessage) {
	var payload struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(req.Payload, &payload); err != nil || len(payload.Paths) == 0 {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "no paths specified to share"}, req.ID))
		return
	}

	s.mu.Lock()
	s.webShareEnabled = true
	if len(payload.Paths) == 1 {
		fi, err := os.Stat(payload.Paths[0])
		if err == nil && !fi.IsDir() {
			s.webSharedFile = payload.Paths[0]
			s.webSharedManifest = nil
			s.mu.Unlock()
			s.Broadcast(NewEvent("web_files_shared", map[string]interface{}{
				"count":       1,
				"name":        filepath.Base(payload.Paths[0]),
				"size":        fi.Size(),
				"total_bytes": fi.Size(),
			}, req.ID))
			s.Broadcast(NewEvent("status", s.getStatus(), req.ID))
			return
		}
	}

	m, err := manifest.Build(payload.Paths)
	if err != nil {
		s.mu.Unlock()
		rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
		return
	}
	s.webSharedManifest = m
	s.webSharedFile = ""
	s.mu.Unlock()

	s.Broadcast(NewEvent("web_files_shared", map[string]interface{}{
		"count":       len(m.Items),
		"root_name":   m.RootName,
		"total_bytes": m.TotalBytes,
	}, req.ID))
	s.Broadcast(NewEvent("status", s.getStatus(), req.ID))
}

func (s *DaemonServer) handleClearWebFiles(rs Responder, req RequestMessage) {
	s.mu.Lock()
	s.webSharedManifest = nil
	s.webSharedFile = ""
	s.mu.Unlock()

	s.Broadcast(NewEvent("web_files_cleared", map[string]interface{}{
		"message": "Web portal files cleared",
	}, req.ID))
}

func (s *DaemonServer) handleToggleWebShare(rs Responder, req RequestMessage) {
	s.mu.Lock()
	var payload struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(req.Payload, &payload); err == nil && payload.Enabled != nil {
		s.webShareEnabled = *payload.Enabled
	} else {
		s.webShareEnabled = !s.webShareEnabled
	}
	s.mu.Unlock()

	s.Broadcast(NewEvent("status", s.getStatus(), req.ID))
}

func (s *DaemonServer) handleRegenerateSharePIN(rs Responder, req RequestMessage) {
	digits := 6
	var payload struct {
		Digits int `json:"digits"`
	}
	if err := json.Unmarshal(req.Payload, &payload); err == nil && payload.Digits > 0 {
		digits = payload.Digits
	}
	s.regeneratePINInternal(digits)
}

func (s *DaemonServer) handleSetWebShareAutoAccept(rs Responder, req RequestMessage) {
	var payload struct {
		AutoAccept bool `json:"auto_accept"`
	}
	if err := json.Unmarshal(req.Payload, &payload); err == nil {
		s.mu.Lock()
		s.webShareAutoAccept = payload.AutoAccept
		s.mu.Unlock()
	}

	s.Broadcast(NewEvent("status", s.getStatus(), req.ID))
}

func (s *DaemonServer) handleAcceptWebUpload(rs Responder, req RequestMessage) {
	var payload struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(req.Payload, &payload); err != nil || payload.Ticket == "" {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid ticket"}, req.ID))
		return
	}

	s.mu.Lock()
	ticket, exists := s.pendingWebUploads[payload.Ticket]
	if exists && ticket != nil {
		ticket.Approved = true
		select {
		case ticket.DoneChan <- true:
		default:
		}
	}
	s.mu.Unlock()

	if !exists {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "ticket expired or not found"}, req.ID))
		return
	}

	s.Broadcast(NewEvent("web_share_upload_accepted", map[string]interface{}{
		"ticket": payload.Ticket,
	}, req.ID))
}

func (s *DaemonServer) handleRejectWebUpload(rs Responder, req RequestMessage) {
	var payload struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(req.Payload, &payload); err != nil || payload.Ticket == "" {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid ticket"}, req.ID))
		return
	}

	s.mu.Lock()
	ticket, exists := s.pendingWebUploads[payload.Ticket]
	if exists && ticket != nil {
		ticket.Approved = false
		select {
		case ticket.DoneChan <- false:
		default:
		}
		delete(s.pendingWebUploads, payload.Ticket)
	}
	s.mu.Unlock()

	s.Broadcast(NewEvent("web_share_upload_rejected", map[string]interface{}{
		"ticket": payload.Ticket,
	}, req.ID))
}

func (s *DaemonServer) handleTestDisk(rs Responder, req RequestMessage) {
	var payload TestDiskPayload
	if len(req.Payload) > 0 {
		_ = json.Unmarshal(req.Payload, &payload)
	}

	targetDir := payload.Dir
	if targetDir == "" {
		s.mu.RLock()
		targetDir = s.config.DownloadDir
		s.mu.RUnlock()
	}
	if targetDir == "" {
		targetDir = "."
	}

	sizeBytes := payload.SizeBytes
	if sizeBytes <= 0 {
		sizeBytes = 32 * 1024 * 1024 // 32 MB default
	}

	s.Broadcast(NewEvent("log", map[string]string{
		"level":   "DIAG",
		"message": fmt.Sprintf("Starting disk benchmark on \"%s\" (%d MB)...", targetDir, sizeBytes/(1024*1024)),
	}))

	go func() {
		res, err := engine.BenchmarkDisk(targetDir, sizeBytes)
		if err != nil {
			rs.Reply(NewEvent("action_error", map[string]string{
				"error": fmt.Sprintf("disk benchmark failed: %v", err),
			}, req.ID))
			return
		}

		s.Broadcast(NewEvent("log", map[string]string{
			"level": "DIAG",
			"message": fmt.Sprintf("Disk Benchmark on \"%s\" -> Write: %.1f MB/s | Read: %.1f MB/s [%s]",
				res.TargetDir, res.WriteSpeedMBps, res.ReadSpeedMBps, res.Rating),
		}))

		if res.Warning != "" {
			s.Broadcast(NewEvent("log", map[string]string{
				"level":   "WARNING",
				"message": fmt.Sprintf("Storage warning: %s", res.Warning),
			}))
		}

		rs.Reply(NewEvent("benchmark_disk_result", res, req.ID))
	}()
}

// targetHost is the host of a target given as "host" or "host:port", IPv6 included
// ("[fe80::1]:18887", "fe80::1"), never cut at its first colon (§3.7, #156).
func targetHost(target string) string {
	if host, _, err := net.SplitHostPort(target); err == nil {
		return host
	}
	return strings.TrimSuffix(strings.TrimPrefix(target, "["), "]")
}

// awaitPong waits up to timeout for the answer to the ping sent at ts, and returns the round
// trip in milliseconds. A pong of another ping (one that came after its own timeout, in an
// earlier measurement) is dropped (§3.7, #156).
func awaitPong(pongs <-chan session.Message, ts int64, timeout time.Duration) (float64, bool) {
	expired := time.After(timeout)
	for {
		select {
		case pong := <-pongs:
			if pong.BenchPingTS != ts {
				continue
			}
			return float64(time.Now().UnixNano()-ts) / 1e6, true
		case <-expired:
			return 0, false
		}
	}
}

func (s *DaemonServer) handleTestNetwork(rs Responder, req RequestMessage) {
	s.mu.RLock()
	sess := s.activeSession
	pairedName := s.pairedDeviceName
	s.mu.RUnlock()

	if sess == nil {
		rs.Reply(NewEvent("action_error", map[string]string{
			"error": "Not paired with any device. Please pair with your phone or PC before running network benchmark.",
		}, req.ID))
		return
	}

	var payload TestNetworkPayload
	if len(req.Payload) > 0 {
		_ = json.Unmarshal(req.Payload, &payload)
	}
	sizeBytes := payload.SizeBytes
	if sizeBytes <= 0 {
		sizeBytes = 8 * 1024 * 1024 // 8 MB default burst
	}

	remoteIP := sess.RemoteIP()
	s.Broadcast(NewEvent("log", map[string]string{
		"level":   "DIAG",
		"message": fmt.Sprintf("Starting network link benchmark to %s (%s)...", remoteIP, pairedName),
	}))

	go func() {
		// 1. RTT Ping Probe (4 iterations)
		var rtts []float64
		for i := 0; i < 4; i++ {
			t0 := time.Now()
			_ = sess.Send(session.Message{
				Type:        "bench_ping",
				BenchPingTS: t0.UnixNano(),
			})

			// The time on the monotonic clock: UnixNano's steps reach a millisecond on Windows.
			if _, ok := awaitPong(s.benchPongChan, t0.UnixNano(), time.Second); ok {
				rtts = append(rtts, float64(time.Since(t0))/float64(time.Millisecond))
			}
			time.Sleep(20 * time.Millisecond)
		}

		if len(rtts) == 0 { // not a measure: say so rather than 0 ms and 0 MB/s
			rs.Reply(NewEvent("action_error", map[string]string{
				"error": "the other device did not answer the benchmark (an older version?)",
			}, req.ID))
			return
		}

		minRTT := 9999.0
		maxRTT := 0.0
		sumRTT := 0.0
		for _, r := range rtts {
			if r < minRTT {
				minRTT = r
			}
			if r > maxRTT {
				maxRTT = r
			}
			sumRTT += r
		}
		avgRTT := 0.0
		if len(rtts) > 0 {
			avgRTT = sumRTT / float64(len(rtts))
		} else {
			minRTT = 0.0
		}

		// 2. Bandwidth Burst Probe
		_ = sess.Send(session.Message{
			Type:      "bench_burst_req",
			BenchSize: sizeBytes,
		})

		var throughput float64
		select {
		case readyMsg := <-s.benchBurstAckChan:
			if readyMsg.Type == "bench_burst_ready" && readyMsg.DataPort > 0 {
				targetAddr := net.JoinHostPort(remoteIP, strconv.Itoa(int(readyMsg.DataPort)))
				clientSpeed, cErr := engine.RunNetworkBurstClient(targetAddr, sizeBytes, 4*time.Second)
				if cErr == nil {
					throughput = clientSpeed
				}
				// Wait for peer ack or timeout
				select {
				case ackMsg := <-s.benchBurstAckChan:
					if ackMsg.Type == "bench_burst_ack" && ackMsg.FileSize > 0 {
						peerSpeed := float64(ackMsg.FileSize) / 100.0
						if peerSpeed > 0 {
							throughput = peerSpeed // Receiver side measurement is most accurate
						}
					}
				case <-time.After(1 * time.Second):
				}
			}
		case <-time.After(3 * time.Second):
		}

		linkType, rating := engine.ClassifyNetworkLink(avgRTT, throughput)

		result := engine.NetBenchResult{
			RemoteIP:       remoteIP,
			MinRTTMs:       minRTT,
			AvgRTTMs:       avgRTT,
			MaxRTTMs:       maxRTT,
			ThroughputMBps: throughput,
			DurationMs:     int64(sumRTT),
			LinkType:       linkType,
			Rating:         rating,
		}

		speedStr := "N/A"
		if throughput > 0 {
			speedStr = fmt.Sprintf("%.1f MB/s", throughput)
		}
		s.Broadcast(NewEvent("log", map[string]string{
			"level": "DIAG",
			"message": fmt.Sprintf("Network benchmark to %s -> Throughput: %s | RTT: %.2fms (min: %.2fms, max: %.2fms) [%s]",
				remoteIP, speedStr, avgRTT, minRTT, maxRTT, linkType),
		}))

		rs.Reply(NewEvent("benchmark_network_result", result, req.ID))
	}()
}

func (s *DaemonServer) handleGetTransferSummary(rs Responder, req RequestMessage) {
	rep := s.GetLastSummaryReport()
	if rep == nil {
		rs.Reply(NewEvent("last_transfer_summary", map[string]interface{}{
			"summary_report": nil,
		}, req.ID))
		return
	}
	rs.Reply(NewEvent("last_transfer_summary", map[string]interface{}{
		"summary_report":   rep,
		"formatted_report": rep.FormattedReport,
	}, req.ID))
}
