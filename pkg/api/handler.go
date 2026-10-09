package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/hotspot"
	"github.com/Medboy224/medXfer/pkg/manifest"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/gorilla/websocket"
)

func (s *DaemonServer) dispatch(conn *websocket.Conn, req RequestMessage) {
	switch req.Action {
	case "get_status":
		s.sendTo(conn, NewEvent("status", s.getStatus(), req.ID))

	case "set_config":
		s.handleSetConfig(conn, req)

	case "scan":
		s.handleScan(conn, req)

	case "pair":
		s.handlePair(conn, req)

	case "disconnect":
		s.handleDisconnect(conn, req)

	case "send":
		s.handleSend(conn, req)

	case "respond_offer":
		s.handleRespondOffer(conn, req)

	case "cancel":
		s.handleCancel(conn, req)

	case "pause":
		s.handlePause(conn, req)

	case "resume":
		s.handleResume(conn, req)

	case "skip_file":
		s.handleSkipFile(conn, req)

	case "pause_file":
		s.handlePauseFile(conn, req)

	case "resume_file":
		s.handleResumeFile(conn, req)

	case "hotspot_start":
		s.handleHotspotStart(conn, req)

	case "hotspot_stop":
		s.handleHotspotStop(conn, req)

	case "hotspot_status":
		s.handleHotspotStatus(conn, req)

	case "share_web_files":
		s.handleShareWebFiles(conn, req)

	case "clear_web_files":
		s.handleClearWebFiles(conn, req)

	case "toggle_web_share":
		s.handleToggleWebShare(conn, req)

	case "regenerate_share_pin":
		s.handleRegenerateSharePIN(conn, req)

	case "set_web_share_auto_accept":
		s.handleSetWebShareAutoAccept(conn, req)

	case "accept_web_upload":
		s.handleAcceptWebUpload(conn, req)

	case "reject_web_upload":
		s.handleRejectWebUpload(conn, req)

	case "web_share_pause":
		s.PauseWebShareTransfer()

	case "web_share_resume":
		s.ResumeWebShareTransfer()

	case "web_share_cancel":
		s.CancelWebShareTransfer()

	case "open_hotspot_settings":
		go func() {
			_ = exec.Command("cmd", "/c", "start", "ms-settings:network-mobilehotspot").Start()
		}()

	case "test_disk":
		s.handleTestDisk(conn, req)

	case "test_network":
		s.handleTestNetwork(conn, req)

	case "get_transfer_summary":
		s.handleGetTransferSummary(conn, req)

	default:
		s.sendTo(conn, NewEvent("action_error", map[string]string{
			"error": fmt.Sprintf("unknown action '%s'", req.Action),
		}, req.ID))
	}
}

func (s *DaemonServer) handleSetConfig(conn *websocket.Conn, req RequestMessage) {
	var cfg Config
	if err := json.Unmarshal(req.Payload, &cfg); err != nil {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
		return
	}

	s.mu.Lock()
	if cfg.DeviceName != "" {
		s.config.DeviceName = cfg.DeviceName
		if s.discSrv != nil {
			s.discSrv.SetDeviceName(cfg.DeviceName)
		}
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

	if err := SaveConfig(currentCfg); err != nil {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": fmt.Sprintf("failed saving settings: %v", err)}, req.ID))
		return
	}
	s.Broadcast(NewEvent("status", s.getStatus(), req.ID))
}

func (s *DaemonServer) handleScan(conn *websocket.Conn, req RequestMessage) {
	go func() {
		peers, err := discovery.DiscoverPeers(2 * time.Second)
		if err != nil {
			s.sendTo(conn, NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
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

		s.sendTo(conn, NewEvent("peers_list", filtered, req.ID))
	}()
}

func (s *DaemonServer) handlePair(conn *websocket.Conn, req RequestMessage) {
	var payload PairPayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil || (payload.IP == "" && payload.Code == "") {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "invalid pair payload (IP or Code required)"}, req.ID))
		return
	}

	if payload.Code != "" && payload.IP == "" {
		peer, err := discovery.ResolvePairingCode(s.ctx, payload.Code, 3*time.Second)
		if err != nil {
			s.sendTo(conn, NewEvent("action_error", map[string]string{"error": fmt.Sprintf("failed to resolve pairing code '%s': %v", payload.Code, err)}, req.ID))
			return
		}
		payload.IP = peer.HostIP
		if payload.Port <= 0 && peer.Port > 0 {
			payload.Port = peer.Port
		}
	}

	if payload.Code == "" {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "pairing code is required to pair with device"}, req.ID))
		return
	}

	port := payload.Port
	if port <= 0 {
		port = 18887
	}

	target := fmt.Sprintf("%s:%d", payload.IP, port)
	connPeer, err := session.DialTLSPeer(target)
	if err != nil {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": fmt.Sprintf("failed to pair with %s: %v", target, err)}, req.ID))
		return
	}

	ch := session.NewChannel(connPeer)
	s.mu.RLock()
	devName := s.config.DeviceName
	s.mu.RUnlock()

	_ = ch.Send(session.Message{
		Type:        "pair_request",
		DeviceName:  devName,
		PairingCode: payload.Code,
	})

	// Wait for peer response (5s deadline)
	_ = connPeer.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := ch.Read()
	_ = connPeer.SetReadDeadline(time.Time{})

	if err != nil || resp.Type != "pair_accept" {
		reason := "pairing rejected by peer"
		if resp.Type == "pair_reject" {
			reason = "invalid pairing code"
			if resp.FileName != "" {
				reason = resp.FileName
			}
		} else if err != nil {
			reason = err.Error()
		}
		ch.Close()
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": fmt.Sprintf("failed to pair with %s: %s", target, reason)}, req.ID))
		return
	}

	peerDevName := resp.DeviceName
	if peerDevName == "" {
		peerDevName = payload.IP
	}

	s.mu.Lock()
	if s.sessionGraceTimer != nil {
		s.sessionGraceTimer.Stop()
		s.sessionGraceTimer = nil
	}
	s.isReconnecting = false
	if s.activeSession != nil {
		s.activeSession.Close()
	}
	s.activeSession = ch
	s.pairedDeviceName = peerDevName
	s.mu.Unlock()

	s.Broadcast(NewEvent("paired", map[string]string{
		"ip":          payload.IP,
		"device_name": peerDevName,
	}, req.ID))

	go s.listenToSession(ch)
}

func (s *DaemonServer) handleDisconnect(conn *websocket.Conn, req RequestMessage) {
	s.mu.Lock()
	if s.sessionGraceTimer != nil {
		s.sessionGraceTimer.Stop()
		s.sessionGraceTimer = nil
	}
	s.isReconnecting = false
	if s.activeSession != nil {
		s.activeSession.Send(session.Message{Type: "disconnect"})
		s.activeSession.Close()
		s.activeSession = nil
	}
	s.pairedDeviceName = ""
	if s.transferCancel != nil {
		s.transferCancel()
		s.transferCancel = nil
	}
	s.mu.Unlock()

	s.Broadcast(NewEvent("disconnected", map[string]string{"reason": "user requested disconnect"}, req.ID))
}

func (s *DaemonServer) handleSend(conn *websocket.Conn, req RequestMessage) {
	var payload SendPayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil || len(payload.Paths) == 0 {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "missing or invalid paths"}, req.ID))
		return
	}

	cleanTarget := payload.TargetIP
	if strings.Contains(cleanTarget, ":") {
		cleanTarget = strings.Split(cleanTarget, ":")[0]
	}

	s.mu.RLock()
	sess := s.activeSession
	s.mu.RUnlock()

	// If a specific target IP was provided and we have no session or session is with someone else:
	if payload.TargetIP != "" && (sess == nil || sess.RemoteIP() != cleanTarget) {
		if payload.Code == "" {
			s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "pairing code is required for direct send"}, req.ID))
			return
		}
		go s.connectAndSend(payload.TargetIP, payload.Code, payload.Paths, req.ID, payload.UseTarStream)
		return
	}

	// If we have an active session, send over it
	if sess != nil {
		go func() {
			err := s.sendOverSession(payload.Paths, req.ID, payload.UseTarStream)
			if err != nil && payload.TargetIP != "" {
				// Old session failed (stale/broken connection), immediately reconnect and retry!
				s.connectAndSend(payload.TargetIP, payload.Code, payload.Paths, req.ID, payload.UseTarStream)
			} else if err != nil {
				s.sendTo(conn, NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
			}
		}()
		return
	}

	if payload.TargetIP != "" {
		if payload.Code == "" {
			s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "pairing code is required for direct send"}, req.ID))
			return
		}
		go s.connectAndSend(payload.TargetIP, payload.Code, payload.Paths, req.ID, payload.UseTarStream)
		return
	}

	s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "not paired with any device and no target_ip specified"}, req.ID))
}

func (s *DaemonServer) connectAndSend(targetIP, code string, paths []string, id string, useTarStream ...bool) {
	target := targetIP
	if !strings.Contains(target, ":") {
		target = fmt.Sprintf("%s:18887", targetIP)
	}
	connPeer, err := session.DialTLSPeer(target)
	if err != nil {
		s.Broadcast(NewEvent("action_error", map[string]string{"error": fmt.Sprintf("failed to connect to %s: %v", target, err)}, id))
		return
	}

	ch := session.NewChannel(connPeer)
	s.mu.RLock()
	devName := s.config.DeviceName
	s.mu.RUnlock()

	_ = ch.Send(session.Message{
		Type:        "pair_request",
		DeviceName:  devName,
		PairingCode: code,
	})

	_ = connPeer.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := ch.Read()
	_ = connPeer.SetReadDeadline(time.Time{})

	if err != nil || resp.Type != "pair_accept" {
		reason := "pairing rejected by peer"
		if resp.Type == "pair_reject" {
			reason = "invalid pairing code"
			if resp.FileName != "" {
				reason = resp.FileName
			}
		}
		ch.Close()
		s.Broadcast(NewEvent("action_error", map[string]string{"error": fmt.Sprintf("failed to pair with %s: %s", target, reason)}, id))
		return
	}

	cleanIP := strings.Split(targetIP, ":")[0]
	peerDevName := resp.DeviceName
	if peerDevName == "" {
		peerDevName = cleanIP
	}

	s.mu.Lock()
	if s.activeSession != nil {
		s.activeSession.Close()
	}
	s.activeSession = ch
	s.pairedDeviceName = peerDevName
	s.mu.Unlock()

	s.Broadcast(NewEvent("paired", map[string]string{
		"ip":          cleanIP,
		"device_name": peerDevName,
	}))
	go s.listenToSession(ch)

	// Dispatch offer immediately
	tarOpt := len(useTarStream) > 0 && useTarStream[0]
	_ = s.sendOverSession(paths, id, tarOpt)
}

func (s *DaemonServer) sendOverSession(paths []string, reqID string, useTarStream ...bool) error {
	s.mu.RLock()
	sess := s.activeSession
	devName := s.config.DeviceName
	s.mu.RUnlock()

	if sess == nil {
		return fmt.Errorf("no active session with peer")
	}

	isFolder := false
	if len(paths) == 1 {
		if fi, err := os.Stat(paths[0]); err == nil && fi.IsDir() {
			isFolder = true
		}
	}
	isFolderOrMulti := len(paths) > 1 || isFolder

	if isFolderOrMulti {
		m, err := manifest.Build(paths)
		if err != nil {
			s.Broadcast(NewEvent("action_error", map[string]string{"error": err.Error()}, reqID))
			return err
		}

		var batchItems []BatchFileInfo
		for i, it := range m.Items {
			batchItems = append(batchItems, BatchFileInfo{
				Index:   i,
				RelPath: it.RelPath,
				Size:    it.Size,
				Status:  "pending",
			})
		}

		// Tar streaming is used when explicitly requested via "Raw Archive Stream"
		isStream := false
		if len(useTarStream) > 0 && useTarStream[0] {
			isStream = true
		}

		s.mu.Lock()
		s.lastOfferedManifest = m
		s.currentBatchItems = batchItems
		s.skippedFiles = make(map[int]bool)
		s.batchCanceled = false
		s.isPaused = false
		s.lastOfferedIsStream = isStream
		s.activePort++
		s.mu.Unlock()

		err = sess.Send(session.Message{
			Type:       "batch_offer",
			DeviceName: devName,
			Batch:      m,
			IsStream:   isStream,
		})
		if err != nil {
			s.Broadcast(NewEvent("action_error", map[string]string{"error": fmt.Sprintf("failed sending batch offer: %v", err)}, reqID))
			return err
		}

		s.Broadcast(NewEvent("batch_offered", map[string]interface{}{
			"summary":     m.SummaryString(),
			"total_files": m.TotalFiles,
			"total_bytes": m.TotalBytes,
			"items":       batchItems,
		}, reqID))
		return nil

	} else {
		filePath := paths[0]
		fi, err := os.Stat(filePath)
		if err != nil {
			s.Broadcast(NewEvent("action_error", map[string]string{"error": err.Error()}, reqID))
			return err
		}

		s.mu.Lock()
		s.activePort++
		port := s.activePort
		s.lastOfferedFile = filePath
		s.lastOfferedPort = port
		s.mu.Unlock()

		fileID := engine.GenerateFileIDFromInfo(fi)
		err = sess.Send(session.Message{
			Type:       "offer",
			DeviceName: devName,
			FileName:   filepath.Base(filePath),
			FileSize:   fi.Size(),
			FileID:     fileID,
			DataPort:   port,
		})
		if err != nil {
			s.Broadcast(NewEvent("action_error", map[string]string{"error": fmt.Sprintf("failed sending offer: %v", err)}, reqID))
			return err
		}

		s.Broadcast(NewEvent("file_offered", map[string]interface{}{
			"file_name": filepath.Base(filePath),
			"file_size": fi.Size(),
		}, reqID))
		return nil
	}
}

func (s *DaemonServer) sendOneShot(paths []string, targetIP, reqID string) {
	// One-shot mode sends directly to targetIP (base port 18888)
	isFolderOrMulti := len(paths) > 1
	if len(paths) == 1 {
		if fi, err := os.Stat(paths[0]); err == nil && fi.IsDir() {
			isFolderOrMulti = true
		}
	}

	port := 18888
	if strings.Contains(targetIP, ":") {
		parts := strings.Split(targetIP, ":")
		if p, err := strconv.Atoi(parts[1]); err == nil {
			port = p
		}
	}

	if isFolderOrMulti {
		m, err := manifest.Build(paths)
		if err != nil {
			s.Broadcast(NewEvent("action_error", map[string]string{"error": err.Error()}, reqID))
			return
		}

		offer := &discovery.TransferOffer{
			FileName: m.SummaryString(),
			FileSize: m.TotalBytes,
			FileID:   m.BatchID,
			IsBatch:  true,
			Batch:    m,
		}
		discServer := discovery.NewDiscoveryServer("sender", port, offer)
		discServer.Start(s.ctx)

		var baseBytes int64 = 0
		for idx, item := range m.Items {
			itemPort := port + idx
			itemCtx, itemCancel := context.WithCancel(s.ctx)
			s.mu.Lock()
			s.transferCancel = itemCancel
			s.mu.Unlock()

			sender := engine.NewSender(s.config.Workers, uint32(s.config.ChunkSizeMB*1024*1024))
			listener := newDaemonListener(s, item.RelPath, item.Size, idx, m.TotalFiles, baseBytes, m.TotalBytes)
			bindAddr := fmt.Sprintf("0.0.0.0:%d", itemPort)
			_ = sender.ServeAndSendWithRelPath(itemCtx, bindAddr, item.FullPath, item.RelPath, listener, 0)
			itemCancel()
			baseBytes += item.Size
		}

		s.broadcastTransferComplete(map[string]interface{}{
			"message": "Folder batch transfer complete",
		})
	} else {
		filePath := paths[0]
		fi, err := os.Stat(filePath)
		if err != nil {
			s.Broadcast(NewEvent("action_error", map[string]string{"error": err.Error()}, reqID))
			return
		}

		offer := &discovery.TransferOffer{
			FileName: filepath.Base(filePath),
			FileSize: fi.Size(),
			FileID:   engine.GenerateFileID(filePath),
		}
		discServer := discovery.NewDiscoveryServer("sender", port, offer)
		discServer.Start(s.ctx)

		var ctx context.Context
		s.mu.Lock()
		ctx, s.transferCancel = context.WithCancel(s.ctx)
		s.mu.Unlock()

		sender := engine.NewSender(s.config.Workers, uint32(s.config.ChunkSizeMB*1024*1024))
		listener := newDaemonListener(s, filepath.Base(filePath), fi.Size(), 0, 1, 0, fi.Size())
		bindAddr := fmt.Sprintf("0.0.0.0:%d", port)
		_ = sender.ServeAndSend(ctx, bindAddr, filePath, listener, 0)
	}
}

func (s *DaemonServer) handleRespondOffer(conn *websocket.Conn, req RequestMessage) {
	var payload RespondOfferPayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "invalid respond_offer payload"}, req.ID))
		return
	}

	s.mu.Lock()
	offer := s.pendingOffer
	s.pendingOffer = nil
	sess := s.activeSession
	s.mu.Unlock()

	if sess == nil || offer == nil {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "no pending transfer offer to respond to"}, req.ID))
		return
	}

	if !payload.Accept {
		sess.Send(session.Message{Type: "reject"})
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
	policyStr := s.config.CollisionPolicy
	s.mu.Unlock()

	// Trigger non-blocking Pre-Flight disk probe
	go func(targetDir string) {
		probeSpeed, isSlow, err := engine.QuickDiskProbe(targetDir)
		if err == nil {
			rating := "High-Speed SSD (Optimal)"
			if isSlow {
				rating = "Slow Storage / Potential Bottleneck"
			}
			s.Broadcast(NewEvent("log", map[string]string{
				"level":   "DIAG",
				"message": fmt.Sprintf("Pre-Flight Storage Probe on \"%s\" -> %.1f MB/s [%s]", targetDir, probeSpeed, rating),
			}))
			if isSlow {
				s.Broadcast(NewEvent("log", map[string]string{
					"level":   "WARNING",
					"message": fmt.Sprintf("Physical write speed to \"%s\" is %.1f MB/s. Transfer rate will be limited by drive write speed.", targetDir, probeSpeed),
				}))
			}
			s.Broadcast(NewEvent("transfer_preflight", TransferPreflightData{
				TargetDir:        targetDir,
				DiskWriteSpeed:   probeSpeed,
				DiskRating:       rating,
				IsDiskBottleneck: isSlow,
				Warning: func() string {
					if isSlow {
						return fmt.Sprintf("Target drive writes at %.1f MB/s and may bottleneck transfer.", probeSpeed)
					}
					return ""
				}(),
			}))
		}
	}(saveDir)

	policy := s.parseCollisionPolicy(policyStr)

	if offer.Type == "batch_offer" {
		sess.Send(session.Message{Type: "batch_accept"})
		s.Broadcast(NewEvent("batch_accepted", map[string]interface{}{
			"save_dir": saveDir,
		}, req.ID))
	} else {
		resumeBytes, _ := engine.PeekResumeOffset(saveDir, offer.FileName, offer.FileID, offer.FileSize, 2*1024*1024)
		sess.Send(session.Message{Type: "accept", ResumeBytes: resumeBytes})

		receiver := engine.NewReceiver(saveDir, s.config.Workers)
		receiver.SetCollisionPolicy(policy)

		var ctx context.Context
		s.mu.Lock()
		ctx, s.transferCancel = context.WithCancel(s.ctx)
		s.activeReceiver = receiver
		if s.isPaused {
			receiver.Pause()
		}
		s.mu.Unlock()

		go func(offer *session.Message, resume int64) {
			listener := newDaemonListener(s, offer.FileName, offer.FileSize, 0, 1, 0, offer.FileSize)
			targetAddr := fmt.Sprintf("%s:%d", sess.RemoteIP(), offer.DataPort)

			err := receiver.Pull(ctx, targetAddr, listener, offer.FileID)
			if err == nil {
				sess.Send(session.Message{Type: "complete"})
				s.broadcastTransferComplete(map[string]interface{}{
					"message": "File received successfully",
				})
			} else if ctx.Err() == nil {
				s.Broadcast(NewEvent("transfer_error", map[string]string{"error": err.Error()}))
			}
		}(offer, resumeBytes)
	}
}

func (s *DaemonServer) handlePause(conn *websocket.Conn, req RequestMessage) {
	s.mu.Lock()
	s.isPaused = true
	if s.activeReceiver != nil {
		s.activeReceiver.Pause()
	}
	sess := s.activeSession
	s.mu.Unlock()

	if sess != nil {
		_ = sess.Send(session.Message{Type: "pause"})
	}
	s.Broadcast(NewEvent("transfer_paused", map[string]string{"message": "Transfer paused by user"}, req.ID))
}

func (s *DaemonServer) handleResume(conn *websocket.Conn, req RequestMessage) {
	s.mu.Lock()
	s.isPaused = false
	if s.activeReceiver != nil {
		s.activeReceiver.Resume()
	}
	sess := s.activeSession
	s.mu.Unlock()

	if sess != nil {
		_ = sess.Send(session.Message{Type: "resume"})
	}
	s.Broadcast(NewEvent("transfer_resumed", map[string]string{"message": "Transfer resumed by user"}, req.ID))
}

func (s *DaemonServer) handleCancel(conn *websocket.Conn, req RequestMessage) {
	s.mu.Lock()
	s.isPaused = false
	s.batchCanceled = true
	if s.activeReceiver != nil {
		s.activeReceiver.Resume()
	}
	if s.itemCancel != nil {
		s.itemCancel()
		s.itemCancel = nil
	}
	if s.transferCancel != nil {
		s.transferCancel()
		s.transferCancel = nil
	}
	sess := s.activeSession
	s.mu.Unlock()

	if sess != nil {
		_ = sess.Send(session.Message{Type: "cancel"})
	}
	s.Broadcast(NewEvent("transfer_canceled", map[string]string{"message": "Transfer canceled by user"}, req.ID))
}

func (s *DaemonServer) handleSkipFile(conn *websocket.Conn, req RequestMessage) {
	var payload SkipFilePayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "invalid skip_file payload"}, req.ID))
		return
	}

	s.mu.Lock()
	skipIdx := payload.ItemIndex
	s.skippedFiles[skipIdx] = true
	for i := range s.currentBatchItems {
		if s.currentBatchItems[i].Index == skipIdx {
			s.currentBatchItems[i].Status = "skipped"
		}
	}
	currIdx := s.currentBatchIndex
	itemCancel := s.itemCancel
	sess := s.activeSession
	items := s.currentBatchItems
	s.mu.Unlock()

	if sess != nil {
		_ = sess.Send(session.Message{Type: "skip_file", ItemIndex: skipIdx})
	}
	s.Broadcast(NewEvent("file_skipped", map[string]interface{}{
		"item_index": skipIdx,
		"items":      items,
	}, req.ID))

	if skipIdx == currIdx && itemCancel != nil {
		itemCancel()
	}
}

func (s *DaemonServer) handlePauseFile(conn *websocket.Conn, req RequestMessage) {
	var payload PauseFilePayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "invalid pause_file payload"}, req.ID))
		return
	}

	s.mu.Lock()
	pauseIdx := payload.ItemIndex
	s.pausedFiles[pauseIdx] = true
	for i := range s.currentBatchItems {
		if s.currentBatchItems[i].Index == pauseIdx {
			s.currentBatchItems[i].Status = "paused"
		}
	}
	currIdx := s.currentBatchIndex
	itemCancel := s.itemCancel
	sess := s.activeSession
	items := s.currentBatchItems
	s.mu.Unlock()

	if sess != nil {
		_ = sess.Send(session.Message{Type: "pause_file", ItemIndex: pauseIdx})
	}
	s.Broadcast(NewEvent("file_paused", map[string]interface{}{
		"item_index": pauseIdx,
		"items":      items,
	}, req.ID))

	if pauseIdx == currIdx && itemCancel != nil {
		itemCancel()
	}
}

func (s *DaemonServer) handleResumeFile(conn *websocket.Conn, req RequestMessage) {
	var payload ResumeFilePayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "invalid resume_file payload"}, req.ID))
		return
	}

	s.mu.Lock()
	resumeIdx := payload.ItemIndex
	delete(s.pausedFiles, resumeIdx)
	for i := range s.currentBatchItems {
		if s.currentBatchItems[i].Index == resumeIdx && s.currentBatchItems[i].Status == "paused" {
			s.currentBatchItems[i].Status = "pending"
		}
	}
	sess := s.activeSession
	items := s.currentBatchItems
	s.mu.Unlock()

	if sess != nil {
		_ = sess.Send(session.Message{Type: "resume_file", ItemIndex: resumeIdx})
	}
	s.Broadcast(NewEvent("file_resumed", map[string]interface{}{
		"item_index": resumeIdx,
		"items":      items,
	}, req.ID))

	select {
	case s.batchResumeChan <- struct{}{}:
	default:
	}
}

// daemonListener bridges engine.TransferListener callbacks into JSON WebSocket broadcasts
type daemonListener struct {
	server          *DaemonServer
	currentFile     string
	fileSize        int64
	fileIndex       int
	totalFiles      int
	baseBatchBytes  int64
	totalBatchBytes int64
	lastUpdate      time.Time
}

func newDaemonListener(server *DaemonServer, fileName string, fileSize int64, fileIdx, totalFiles int, baseBatchBytes, totalBatchBytes int64) *daemonListener {
	if totalFiles <= 0 {
		totalFiles = 1
	}
	if totalBatchBytes <= 0 {
		totalBatchBytes = fileSize
	}
	return &daemonListener{
		server:          server,
		currentFile:     fileName,
		fileSize:        fileSize,
		fileIndex:       fileIdx,
		totalFiles:      totalFiles,
		baseBatchBytes:  baseBatchBytes,
		totalBatchBytes: totalBatchBytes,
		lastUpdate:      time.Now(),
	}
}

func (l *daemonListener) OnStart(fileName string, fileSize int64, chunkCount uint32) {
	l.server.Broadcast(NewEvent("transfer_start", map[string]interface{}{
		"current_file":      fileName,
		"file_size":         fileSize,
		"file_index":        l.fileIndex + 1,
		"total_files":       l.totalFiles,
		"batch_total_bytes": l.totalBatchBytes,
	}))
}

func (l *daemonListener) OnProgress(stats engine.TransferStats) {
	now := time.Now()
	if now.Sub(l.lastUpdate) < 100*time.Millisecond && stats.ProgressPercent < 100 {
		return // Throttle WebSocket telemetry to 10 updates/second to avoid flooding UI
	}
	l.lastUpdate = now

	fileBytes := stats.BytesTransferred
	fileTotal := stats.TotalBytes
	if fileTotal <= 0 {
		fileTotal = l.fileSize
	}

	cumulativeBatchBytes := l.baseBatchBytes + fileBytes
	if cumulativeBatchBytes > l.totalBatchBytes {
		cumulativeBatchBytes = l.totalBatchBytes
	}

	batchPercent := 0.0
	if l.totalBatchBytes > 0 {
		batchPercent = (float64(cumulativeBatchBytes) / float64(l.totalBatchBytes)) * 100.0
		if batchPercent > 100.0 {
			batchPercent = 100.0
		}
	} else {
		batchPercent = stats.ProgressPercent
	}

	eta := 0
	if stats.SpeedMBps > 0 && l.totalBatchBytes > cumulativeBatchBytes {
		remainingBytes := l.totalBatchBytes - cumulativeBatchBytes
		eta = int(float64(remainingBytes) / (stats.SpeedMBps * 1024 * 1024))
	} else if stats.SpeedMBps > 0 && fileTotal > fileBytes {
		remainingBytes := fileTotal - fileBytes
		eta = int(float64(remainingBytes) / (stats.SpeedMBps * 1024 * 1024))
	}

	l.server.mu.RLock()
	isPaused := l.server.isPaused
	l.server.mu.RUnlock()

	speed := stats.SpeedMBps
	if isPaused {
		speed = 0
		eta = 0
	}

	l.server.RecordTransferSample(stats, batchPercent, l.currentFile, l.totalFiles, l.totalBatchBytes)

	l.server.Broadcast(NewEvent("transfer_progress", TransferProgressData{
		CurrentFile:        l.currentFile,
		FileIndex:          l.fileIndex + 1,
		TotalFiles:         l.totalFiles,
		FileBytes:          fileBytes,
		FileTotalBytes:     fileTotal,
		BatchBytes:         cumulativeBatchBytes,
		BatchTotalBytes:    l.totalBatchBytes,
		SpeedMBps:          speed,
		FilePercent:        stats.ProgressPercent,
		BatchPercent:       batchPercent,
		EtaSeconds:         eta,
		IsPaused:           isPaused,
		DiskWriteLatencyMs: stats.DiskWriteLatencyMs,
		NetReadLatencyMs:   stats.NetReadLatencyMs,
		Bottleneck:         stats.Bottleneck,
		BottleneckReason:   stats.BottleneckReason,
	}))
}

func (l *daemonListener) OnChunkFailed(chunkIndex uint32, retryCount int, err error) {}

func (l *daemonListener) OnComplete(savePath string, duration time.Duration) {
	l.server.Broadcast(NewEvent("file_complete", map[string]interface{}{
		"file":        l.currentFile,
		"save_path":   savePath,
		"duration_ms": duration.Milliseconds(),
	}))
}

func (l *daemonListener) OnError(err error) {
	l.server.Broadcast(NewEvent("transfer_error", map[string]string{
		"file":  l.currentFile,
		"error": err.Error(),
	}))
}

type HotspotStartPayload struct {
	Band     string `json:"band"`     // "5ghz", "2.4ghz", "auto"
	SSID     string `json:"ssid"`     // optional custom SSID
	Password string `json:"password"` // optional custom password
}

func (s *DaemonServer) handleHotspotStart(conn *websocket.Conn, req RequestMessage) {
	var payload HotspotStartPayload
	if len(req.Payload) > 0 {
		_ = json.Unmarshal(req.Payload, &payload)
	}

	band := hotspot.Band5GHz
	lowerBand := strings.ToLower(payload.Band)
	if lowerBand == "2.4ghz" || lowerBand == "2.4" {
		band = hotspot.Band2GHz
	} else if lowerBand == "auto" {
		band = hotspot.BandAuto
	}

	s.hotspotMu.Lock()
	if s.activeHotspot != nil {
		netInfo := s.activeHotspot
		s.hotspotMu.Unlock()

		qrWifi, _ := GenerateWiFiQRDataURI(netInfo.SSID, netInfo.Password, 256)
		localIP := netInfo.LocalIP.String()
		if localIP == "" || localIP == "<nil>" {
			localIP = "192.168.137.1"
		}
		port := s.currentHTTPPort()
		if port <= 0 {
			port = 18888
		}
		portalURL := fmt.Sprintf("http://%s:%d/share", localIP, port)
		qrPortal, _ := GenerateURLQRDataURI(portalURL, 256)

		s.sendTo(conn, NewEvent("hotspot_started", map[string]interface{}{
			"active":     true,
			"ssid":       netInfo.SSID,
			"password":   netInfo.Password,
			"ip":         localIP,
			"band":       netInfo.Band.String(),
			"channel":    netInfo.Channel,
			"portal_url": portalURL,
			"qr_wifi":    qrWifi,
			"qr_portal":  qrPortal,
		}, req.ID))
		return
	}

	if s.hotspotCtrl != nil {
		s.hotspotMu.Unlock()
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "Hotspot is already starting or running"}, req.ID))
		return
	}

	ctrl := hotspot.New()
	s.hotspotCtrl = ctrl
	s.hotspotMu.Unlock()

	ssid := payload.SSID
	password := payload.Password
	if ssid == "" || password == "" {
		s, p := hotspot.GenerateCredentials()
		if ssid == "" {
			ssid = s
		}
		if password == "" {
			password = p
		}
	}

	go func() {
		netInfo, err := ctrl.Start(hotspot.Config{
			SSID:     ssid,
			Password: password,
			Band:     band,
		})
		if err != nil {
			s.hotspotMu.Lock()
			s.hotspotCtrl = nil
			s.activeHotspot = nil
			s.hotspotMu.Unlock()
			s.Broadcast(NewEvent("action_error", map[string]string{"error": fmt.Sprintf("Failed to start hotspot: %v", err)}, req.ID))
			return
		}

		s.hotspotMu.Lock()
		s.activeHotspot = netInfo
		s.hotspotMu.Unlock()

		qrWifi, _ := GenerateWiFiQRDataURI(netInfo.SSID, netInfo.Password, 256)
		localIP := netInfo.LocalIP.String()
		if localIP == "" || localIP == "<nil>" {
			localIP = "192.168.137.1"
		}
		port := s.currentHTTPPort()
		if port <= 0 {
			port = 18888
		}
		portalURL := fmt.Sprintf("http://%s:%d/share", localIP, port)
		qrPortal, _ := GenerateURLQRDataURI(portalURL, 256)

		infoMap := map[string]interface{}{
			"active":     true,
			"ssid":       netInfo.SSID,
			"password":   netInfo.Password,
			"ip":         localIP,
			"band":       netInfo.Band.String(),
			"channel":    netInfo.Channel,
			"portal_url": portalURL,
			"qr_wifi":    qrWifi,
			"qr_portal":  qrPortal,
			"warning":    netInfo.Warning,
		}

		s.Broadcast(NewEvent("hotspot_started", infoMap, req.ID))
	}()
}

func (s *DaemonServer) handleHotspotStop(conn *websocket.Conn, req RequestMessage) {
	s.hotspotMu.Lock()
	ctrl := s.hotspotCtrl
	s.hotspotCtrl = nil
	s.activeHotspot = nil
	s.hotspotMu.Unlock()

	if ctrl != nil {
		_ = ctrl.Stop()
	}

	s.Broadcast(NewEvent("hotspot_stopped", map[string]string{"message": "Hotspot stopped"}, req.ID))
}

func (s *DaemonServer) handleHotspotStatus(conn *websocket.Conn, req RequestMessage) {
	s.hotspotMu.Lock()
	netInfo := s.activeHotspot
	s.hotspotMu.Unlock()

	if netInfo == nil {
		s.sendTo(conn, NewEvent("hotspot_status", map[string]interface{}{
			"active": false,
		}, req.ID))
		return
	}

	qrWifi, _ := GenerateWiFiQRDataURI(netInfo.SSID, netInfo.Password, 256)
	localIP := netInfo.LocalIP.String()
	if localIP == "" || localIP == "<nil>" {
		localIP = "192.168.137.1"
	}
	port := s.currentHTTPPort()
	if port <= 0 {
		port = 18888
	}
	portalURL := fmt.Sprintf("http://%s:%d/share", localIP, port)
	qrPortal, _ := GenerateURLQRDataURI(portalURL, 256)

	s.sendTo(conn, NewEvent("hotspot_status", map[string]interface{}{
		"active":     true,
		"ssid":       netInfo.SSID,
		"password":   netInfo.Password,
		"ip":         localIP,
		"band":       netInfo.Band.String(),
		"channel":    netInfo.Channel,
		"portal_url": portalURL,
		"qr_wifi":    qrWifi,
		"qr_portal":  qrPortal,
		"warning":    netInfo.Warning,
	}, req.ID))
}

func (s *DaemonServer) handleShareWebFiles(conn *websocket.Conn, req RequestMessage) {
	var payload struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(req.Payload, &payload); err != nil || len(payload.Paths) == 0 {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "no paths specified to share"}, req.ID))
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
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
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

func (s *DaemonServer) handleClearWebFiles(conn *websocket.Conn, req RequestMessage) {
	s.mu.Lock()
	s.webSharedManifest = nil
	s.webSharedFile = ""
	s.mu.Unlock()

	s.Broadcast(NewEvent("web_files_cleared", map[string]interface{}{
		"message": "Web portal files cleared",
	}, req.ID))
}

func (s *DaemonServer) handleToggleWebShare(conn *websocket.Conn, req RequestMessage) {
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

func (s *DaemonServer) handleRegenerateSharePIN(conn *websocket.Conn, req RequestMessage) {
	digits := 6
	var payload struct {
		Digits int `json:"digits"`
	}
	if err := json.Unmarshal(req.Payload, &payload); err == nil && payload.Digits > 0 {
		digits = payload.Digits
	}
	s.regeneratePINInternal(digits)
}

func (s *DaemonServer) handleSetWebShareAutoAccept(conn *websocket.Conn, req RequestMessage) {
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

func (s *DaemonServer) handleAcceptWebUpload(conn *websocket.Conn, req RequestMessage) {
	var payload struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(req.Payload, &payload); err != nil || payload.Ticket == "" {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "invalid ticket"}, req.ID))
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
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "ticket expired or not found"}, req.ID))
		return
	}

	s.Broadcast(NewEvent("web_share_upload_accepted", map[string]interface{}{
		"ticket": payload.Ticket,
	}, req.ID))
}

func (s *DaemonServer) handleRejectWebUpload(conn *websocket.Conn, req RequestMessage) {
	var payload struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(req.Payload, &payload); err != nil || payload.Ticket == "" {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "invalid ticket"}, req.ID))
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

func (s *DaemonServer) handleTestDisk(conn *websocket.Conn, req RequestMessage) {
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
			s.sendTo(conn, NewEvent("action_error", map[string]string{
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

		s.sendTo(conn, NewEvent("benchmark_disk_result", res, req.ID))
	}()
}

func (s *DaemonServer) handleTestNetwork(conn *websocket.Conn, req RequestMessage) {
	s.mu.RLock()
	sess := s.activeSession
	pairedName := s.pairedDeviceName
	s.mu.RUnlock()

	if sess == nil {
		s.sendTo(conn, NewEvent("action_error", map[string]string{
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

			select {
			case pong := <-s.benchPongChan:
				rttMs := float64(time.Now().UnixNano()-pong.BenchPingTS) / 1e6
				rtts = append(rtts, rttMs)
			case <-time.After(1 * time.Second):
				// timeout on ping
			}
			time.Sleep(20 * time.Millisecond)
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
				targetAddr := fmt.Sprintf("%s:%d", remoteIP, readyMsg.DataPort)
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

		s.sendTo(conn, NewEvent("benchmark_network_result", result, req.ID))
	}()
}

func (s *DaemonServer) handleGetTransferSummary(conn *websocket.Conn, req RequestMessage) {
	rep := s.GetLastSummaryReport()
	if rep == nil {
		s.sendTo(conn, NewEvent("last_transfer_summary", map[string]interface{}{
			"summary_report": nil,
		}, req.ID))
		return
	}
	s.sendTo(conn, NewEvent("last_transfer_summary", map[string]interface{}{
		"summary_report":   rep,
		"formatted_report": rep.FormattedReport,
	}, req.ID))
}
