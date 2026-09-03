package api

import (
	"context"
	"encoding/json"
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
	s.mu.Unlock()

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
	tcpConn, err := net.DialTimeout("tcp4", target, 3*time.Second)
	if err != nil {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": fmt.Sprintf("failed to pair with %s: %v", target, err)}, req.ID))
		return
	}

	s.mu.Lock()
	if s.activeSession != nil {
		s.activeSession.Close()
	}
	s.activeSession = session.NewChannel(tcpConn.(*net.TCPConn))
	s.mu.Unlock()

	s.Broadcast(NewEvent("paired", map[string]string{
		"ip": payload.IP,
	}, req.ID))

	go s.listenToSession(s.activeSession)
}

func (s *DaemonServer) handleDisconnect(conn *websocket.Conn, req RequestMessage) {
	s.mu.Lock()
	if s.activeSession != nil {
		s.activeSession.Send(session.Message{Type: "disconnect"})
		s.activeSession.Close()
		s.activeSession = nil
	}
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

	s.mu.RLock()
	hasSession := s.activeSession != nil
	s.mu.RUnlock()

	if hasSession {
		go s.sendOverSession(payload.Paths, req.ID)
	} else if payload.TargetIP != "" {
		go s.sendOneShot(payload.Paths, payload.TargetIP, req.ID)
	} else {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "node not paired and no target_ip provided"}, req.ID))
	}
}

func (s *DaemonServer) sendOverSession(paths []string, reqID string) {
	isFolderOrMulti := len(paths) > 1
	if len(paths) == 1 {
		if fi, err := os.Stat(paths[0]); err == nil && fi.IsDir() {
			isFolderOrMulti = true
		}
	}

	if isFolderOrMulti {
		m, err := manifest.Build(paths)
		if err != nil {
			s.Broadcast(NewEvent("action_error", map[string]string{"error": err.Error()}, reqID))
			return
		}

		s.mu.Lock()
		s.activePort++
		s.mu.Unlock()

		s.activeSession.Send(session.Message{
			Type:  "batch_offer",
			Batch: m,
		})

		s.Broadcast(NewEvent("batch_offered", map[string]interface{}{
			"summary":     m.SummaryString(),
			"total_files": m.TotalFiles,
			"total_bytes": m.TotalBytes,
		}, reqID))

		// Stream items sequentially when accepted
		go func(m *manifest.Manifest) {
			for idx, item := range m.Items {
				s.mu.Lock()
				s.activePort++
				port := s.activePort
				s.mu.Unlock()

				itemCtx, itemCancel := context.WithCancel(s.ctx)
				s.mu.Lock()
				s.transferCancel = itemCancel
				s.mu.Unlock()

				go func(fPath, rPath string, p int) {
					sender := engine.NewSender(s.config.Workers, uint32(s.config.ChunkSizeMB*1024*1024))
					listener := newDaemonListener(s, rPath, item.Size, idx, m.TotalFiles, m.TotalBytes)
					bindAddr := fmt.Sprintf("0.0.0.0:%d", p)
					_ = sender.ServeAndSendWithRelPath(itemCtx, bindAddr, fPath, rPath, listener, 0)
				}(item.FullPath, item.RelPath, port)

				time.Sleep(30 * time.Millisecond)

				s.activeSession.Send(session.Message{
					Type:      "batch_item",
					FileName:  item.RelPath,
					FileSize:  item.Size,
					FileID:    item.FileID,
					DataPort:  port,
					ItemIndex: idx,
				})

				select {
				case <-itemCtx.Done():
					return
				case <-s.itemDoneChan:
					itemCancel()
				}
			}

			s.activeSession.Send(session.Message{Type: "batch_complete"})
			s.Broadcast(NewEvent("transfer_complete", map[string]string{"message": "All batch files sent successfully"}))
		}(m)

	} else {
		filePath := paths[0]
		fi, err := os.Stat(filePath)
		if err != nil {
			s.Broadcast(NewEvent("action_error", map[string]string{"error": err.Error()}, reqID))
			return
		}

		s.mu.Lock()
		s.activePort++
		port := s.activePort
		s.mu.Unlock()

		fileID := engine.GenerateFileID(filePath)
		s.activeSession.Send(session.Message{
			Type:     "offer",
			FileName: filepath.Base(filePath),
			FileSize: fi.Size(),
			FileID:   fileID,
			DataPort: port,
		})

		s.Broadcast(NewEvent("file_offered", map[string]interface{}{
			"file_name": filepath.Base(filePath),
			"file_size": fi.Size(),
		}, reqID))

		go func() {
			var ctx context.Context
			s.mu.Lock()
			ctx, s.transferCancel = context.WithCancel(s.ctx)
			s.mu.Unlock()

			sender := engine.NewSender(s.config.Workers, uint32(s.config.ChunkSizeMB*1024*1024))
			listener := newDaemonListener(s, filepath.Base(filePath), fi.Size(), 0, 1, fi.Size())
			bindAddr := fmt.Sprintf("0.0.0.0:%d", port)
			_ = sender.ServeAndSend(ctx, bindAddr, filePath, listener, 0)
		}()
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

		for idx, item := range m.Items {
			itemPort := port + idx
			itemCtx, itemCancel := context.WithCancel(s.ctx)
			s.mu.Lock()
			s.transferCancel = itemCancel
			s.mu.Unlock()

			sender := engine.NewSender(s.config.Workers, uint32(s.config.ChunkSizeMB*1024*1024))
			listener := newDaemonListener(s, item.RelPath, item.Size, idx, m.TotalFiles, m.TotalBytes)
			bindAddr := fmt.Sprintf("0.0.0.0:%d", itemPort)
			_ = sender.ServeAndSendWithRelPath(itemCtx, bindAddr, item.FullPath, item.RelPath, listener, 0)
			itemCancel()
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
		listener := newDaemonListener(s, filepath.Base(filePath), fi.Size(), 0, 1, fi.Size())
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

	saveDir := payload.SaveDir
	if saveDir == "" {
		s.mu.RLock()
		saveDir = s.config.DownloadDir
		s.mu.RUnlock()
	}

	policyStr := payload.CollisionPolicy
	if policyStr == "" {
		s.mu.RLock()
		policyStr = s.config.CollisionPolicy
		s.mu.RUnlock()
	}
	policy := s.parseCollisionPolicy(policyStr)

	if offer.Type == "batch_offer" {
		sess.Send(session.Message{Type: "batch_accept"})
		s.Broadcast(NewEvent("batch_accepted", map[string]interface{}{
			"save_dir": saveDir,
		}, req.ID))
	} else {
		resumeBytes, _ := engine.PeekResumeOffset(saveDir, offer.FileName, offer.FileID, offer.FileSize, 2*1024*1024)
		sess.Send(session.Message{Type: "accept", ResumeBytes: resumeBytes})

		var ctx context.Context
		s.mu.Lock()
		ctx, s.transferCancel = context.WithCancel(s.ctx)
		s.mu.Unlock()

		go func(offer *session.Message, resume int64) {
			receiver := engine.NewReceiver(saveDir, s.config.Workers)
			receiver.SetCollisionPolicy(policy)

			listener := newDaemonListener(s, offer.FileName, offer.FileSize, 0, 1, offer.FileSize)
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

func (s *DaemonServer) handleCancel(conn *websocket.Conn, req RequestMessage) {
	s.mu.Lock()
	if s.transferCancel != nil {
		s.transferCancel()
		s.transferCancel = nil
	}
	if s.activeSession != nil {
		s.activeSession.Send(session.Message{Type: "cancel"})
	}
	s.mu.Unlock()

	s.Broadcast(NewEvent("transfer_canceled", map[string]string{"message": "Transfer canceled by user"}, req.ID))
}

// daemonListener bridges engine.TransferListener callbacks into JSON WebSocket broadcasts
type daemonListener struct {
	server      *DaemonServer
	currentFile string
	fileSize    int64
	fileIndex   int
	totalFiles  int
	batchBytes  int64
	lastUpdate  time.Time
}

func newDaemonListener(server *DaemonServer, fileName string, fileSize int64, fileIdx, totalFiles int, batchBytes int64) *daemonListener {
	if totalFiles <= 0 {
		totalFiles = 1
	}
	return &daemonListener{
		server:      server,
		currentFile: fileName,
		fileSize:    fileSize,
		fileIndex:   fileIdx,
		totalFiles:  totalFiles,
		batchBytes:  batchBytes,
		lastUpdate:  time.Now(),
	}
}

func (l *daemonListener) OnStart(fileName string, fileSize int64, chunkCount uint32) {
	l.server.Broadcast(NewEvent("transfer_start", map[string]interface{}{
		"current_file": fileName,
		"file_size":    fileSize,
		"file_index":   l.fileIndex + 1,
		"total_files":  l.totalFiles,
	}))
}

func (l *daemonListener) OnProgress(stats engine.TransferStats) {
	now := time.Now()
	if now.Sub(l.lastUpdate) < 100*time.Millisecond && stats.ProgressPercent < 100 {
		return // Throttle WebSocket telemetry to 10 updates/second to avoid flooding UI
	}
	l.lastUpdate = now

	eta := 0
	if stats.SpeedMBps > 0 && stats.TotalBytes > stats.BytesTransferred {
		remainingBytes := stats.TotalBytes - stats.BytesTransferred
		eta = int(float64(remainingBytes) / (stats.SpeedMBps * 1024 * 1024))
	}

	l.server.Broadcast(NewEvent("transfer_progress", TransferProgressData{
		CurrentFile:     l.currentFile,
		FileIndex:       l.fileIndex + 1,
		TotalFiles:      l.totalFiles,
		FileBytes:       stats.BytesTransferred,
		FileTotalBytes:  stats.TotalBytes,
		BatchBytes:      stats.BytesTransferred, // or cumulative
		BatchTotalBytes: l.batchBytes,
		SpeedMBps:       stats.SpeedMBps,
		FilePercent:     stats.ProgressPercent,
		BatchPercent:    stats.ProgressPercent,
		EtaSeconds:      eta,
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
