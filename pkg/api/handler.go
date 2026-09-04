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

	case "open_hotspot_settings":
		go func() {
			_ = exec.Command("cmd", "/c", "start", "ms-settings:network-mobilehotspot").Start()
		}()

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
	if err := json.Unmarshal(req.Payload, &payload); err != nil || payload.IP == "" {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "invalid pair payload"}, req.ID))
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

	s.mu.Lock()
	if s.sessionGraceTimer != nil {
		s.sessionGraceTimer.Stop()
		s.sessionGraceTimer = nil
	}
	s.isReconnecting = false
	if s.activeSession != nil {
		s.activeSession.Close()
	}
	s.activeSession = session.NewChannel(connPeer)
	devName := s.config.DeviceName
	sess := s.activeSession
	s.mu.Unlock()

	_ = sess.Send(session.Message{
		Type:       "pair_hello",
		DeviceName: devName,
	})

	s.Broadcast(NewEvent("paired", map[string]string{
		"ip":          payload.IP,
		"device_name": "Connecting...",
	}, req.ID))

	go s.listenToSession(sess)
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
		go s.connectAndSend(payload.TargetIP, payload.Paths, req.ID)
		return
	}

	// If we have an active session, send over it
	if sess != nil {
		go func() {
			err := s.sendOverSession(payload.Paths, req.ID)
			if err != nil && payload.TargetIP != "" {
				// Old session failed (stale/broken connection), immediately reconnect and retry!
				s.connectAndSend(payload.TargetIP, payload.Paths, req.ID)
			} else if err != nil {
				s.sendTo(conn, NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
			}
		}()
		return
	}

	if payload.TargetIP != "" {
		go s.connectAndSend(payload.TargetIP, payload.Paths, req.ID)
		return
	}

	s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "not paired with any device and no target_ip specified"}, req.ID))
}

func (s *DaemonServer) connectAndSend(targetIP string, paths []string, id string) {
	target := targetIP
	if !strings.Contains(target, ":") {
		target = fmt.Sprintf("%s:18887", targetIP)
	}
	connPeer, err := session.DialTLSPeer(target)
	if err != nil {
		s.Broadcast(NewEvent("action_error", map[string]string{"error": fmt.Sprintf("failed to connect to %s: %v", target, err)}, id))
		return
	}

	s.mu.Lock()
	if s.activeSession != nil {
		s.activeSession.Close()
	}
	s.activeSession = session.NewChannel(connPeer)
	devName := s.config.DeviceName
	sess := s.activeSession
	s.mu.Unlock()

	_ = sess.Send(session.Message{
		Type:       "pair_hello",
		DeviceName: devName,
	})

	cleanIP := strings.Split(targetIP, ":")[0]
	s.Broadcast(NewEvent("paired", map[string]string{
		"ip":          cleanIP,
		"device_name": "Connecting...",
	}))
	go s.listenToSession(sess)

	// Dispatch offer immediately
	_ = s.sendOverSession(paths, id)
}

func (s *DaemonServer) sendOverSession(paths []string, reqID string) error {
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

		s.mu.Lock()
		s.lastOfferedManifest = m
		s.currentBatchItems = batchItems
		s.skippedFiles = make(map[int]bool)
		s.batchCanceled = false
		s.isPaused = false
		s.lastOfferedIsStream = isFolder
		s.activePort++
		s.mu.Unlock()

		err = sess.Send(session.Message{
			Type:       "batch_offer",
			DeviceName: devName,
			Batch:      m,
			IsStream:   isFolder,
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

		s.Broadcast(NewEvent("transfer_complete", map[string]string{"message": "Folder batch transfer complete"}))
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

	l.server.Broadcast(NewEvent("transfer_progress", TransferProgressData{
		CurrentFile:     l.currentFile,
		FileIndex:       l.fileIndex + 1,
		TotalFiles:      l.totalFiles,
		FileBytes:       fileBytes,
		FileTotalBytes:  fileTotal,
		BatchBytes:      cumulativeBatchBytes,
		BatchTotalBytes: l.totalBatchBytes,
		SpeedMBps:       speed,
		FilePercent:     stats.ProgressPercent,
		BatchPercent:    batchPercent,
		EtaSeconds:      eta,
		IsPaused:        isPaused,
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
		port := s.httpPort
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
		port := s.httpPort
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
	port := s.httpPort
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
	if len(payload.Paths) == 1 {
		fi, err := os.Stat(payload.Paths[0])
		if err == nil && !fi.IsDir() {
			s.lastOfferedFile = payload.Paths[0]
			s.lastOfferedManifest = nil
			s.mu.Unlock()
			s.Broadcast(NewEvent("web_files_shared", map[string]interface{}{
				"count":       1,
				"name":        filepath.Base(payload.Paths[0]),
				"size":        fi.Size(),
				"total_bytes": fi.Size(),
			}, req.ID))
			return
		}
	}

	m, err := manifest.Build(payload.Paths)
	if err != nil {
		s.mu.Unlock()
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
		return
	}
	s.lastOfferedManifest = m
	s.lastOfferedFile = ""
	s.mu.Unlock()

	s.Broadcast(NewEvent("web_files_shared", map[string]interface{}{
		"count":       len(m.Items),
		"root_name":   m.RootName,
		"total_bytes": m.TotalBytes,
	}, req.ID))
}
