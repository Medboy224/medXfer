package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/hotspot"
	"github.com/Medboy224/medXfer/pkg/manifest"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow localhost and local LAN connections from Flutter UI
	},
}

// DaemonServer manages the headless background engine and WebSocket connections
type DaemonServer struct {
	mu                  sync.RWMutex
	config              Config
	clients             map[*websocket.Conn]bool
	clientsMu           sync.Mutex
	activeSession       *session.Channel
	pairedDeviceName    string
	pendingOffer        *session.Message
	lastOfferedManifest *manifest.Manifest
	lastOfferedFile     string
	lastOfferedPort     int
	transferCancel      context.CancelFunc
	itemCancel          context.CancelFunc
	activeReceiver      *engine.Receiver
	isPaused            bool
	batchCanceled       bool
	currentBatchIndex   int
	skippedFiles        map[int]bool
	pausedFiles         map[int]bool
	batchResumeChan     chan struct{}
	currentBatchItems   []BatchFileInfo
	activePort          int
	itemDoneChan        chan bool
	batchTotalFiles     int
	batchTotalBytes     int64
	batchBaseBytes      int64
	sessionGraceTimer   *time.Timer
	isReconnecting      bool
	lastOfferedIsStream bool
	hotspotCtrl         hotspot.Controller
	activeHotspot       *hotspot.NetworkInfo
	hotspotMu           sync.Mutex
	httpPort            int

	ctx     context.Context
	cancel  context.CancelFunc
	httpSrv *http.Server
	nodeLn  net.Listener
	discSrv *discovery.DiscoveryServer
}

// NewDaemonServer initializes a daemon with default configurations
func NewDaemonServer(port int, defaultOutDir, deviceName string) *DaemonServer {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := LoadConfig(defaultOutDir, deviceName)

	return &DaemonServer{
		config:          cfg,
		clients:         make(map[*websocket.Conn]bool),
		activePort:      18888,
		itemDoneChan:    make(chan bool, 1),
		skippedFiles:    make(map[int]bool),
		pausedFiles:     make(map[int]bool),
		batchResumeChan: make(chan struct{}, 1),
		ctx:             ctx,
		cancel:          cancel,
	}
}

func (s *DaemonServer) calculateCompletedBatchBytes() int64 {
	var total int64 = 0
	for _, item := range s.currentBatchItems {
		if item.Status == "completed" {
			total += item.Size
		}
	}
	return total
}

// Listen binds the HTTP listener synchronously
func (s *DaemonServer) Listen(port int) (net.Listener, error) {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	return net.Listen("tcp4", addr)
}

// Serve runs the daemon on an existing listener
func (s *DaemonServer) Serve(httpLn net.Listener) error {
	if tcpAddr, ok := httpLn.Addr().(*net.TCPAddr); ok {
		s.httpPort = tcpAddr.Port
	} else {
		s.httpPort = 18888
	}

	// 1. Start Node Pairing TCP Listener (port 18887)
	ln, err := net.Listen("tcp4", "0.0.0.0:18887")
	if err == nil {
		s.nodeLn = ln
		go s.listenForIncomingPairings(ln)
	}

	// 2. Start Discovery Broadcast Server
	discOffer := &discovery.TransferOffer{FileName: s.config.DeviceName, FileSize: 0}
	s.discSrv = discovery.NewDiscoveryServer("node", 18887, discOffer, s.config.DeviceName)
	s.discSrv.Start(s.ctx)

	// 3. Start HTTP/WebSocket Router
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/status", s.handleHTTPStatus)
	mux.HandleFunc("/api/browse", s.handleBrowse)
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/api/fs/list", s.handleFSList)
	mux.HandleFunc("/api/fs/mkdir", s.handleFSMkdir)
	mux.HandleFunc("/share", s.handleSharePortal)
	mux.HandleFunc("/api/share/list", s.handleShareList)
	mux.HandleFunc("/api/share/download", s.handleShareDownload)
	mux.HandleFunc("/api/share/upload", s.handleShareUpload)
	mux.HandleFunc("/ws", s.handleWebSocket)

	s.httpSrv = &http.Server{
		Handler: mux,
	}

	log.Printf("[*] medXfer Headless Daemon started on http://%s (ws://%s/ws)", httpLn.Addr().String(), httpLn.Addr().String())
	return s.httpSrv.Serve(httpLn)
}

// Start boots the daemon, node listener, discovery server, and HTTP/WebSocket server
func (s *DaemonServer) Start(port int) error {
	ln, err := s.Listen(port)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// Stop shuts down the daemon gracefully
func (s *DaemonServer) Stop() {
	s.cancel()
	s.mu.Lock()
	if s.transferCancel != nil {
		s.transferCancel()
	}
	sess := s.activeSession
	s.activeSession = nil
	s.mu.Unlock()

	if sess != nil {
		sess.Send(session.Message{Type: "disconnect"})
		sess.Close()
	}
	if s.nodeLn != nil {
		_ = s.nodeLn.Close()
	}
	if s.httpSrv != nil {
		_ = s.httpSrv.Close()
	}
	s.hotspotMu.Lock()
	if s.hotspotCtrl != nil {
		_ = s.hotspotCtrl.Stop()
		s.hotspotCtrl = nil
		s.activeHotspot = nil
	}
	s.hotspotMu.Unlock()
}

func (s *DaemonServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(IndexHTML))
}

func (s *DaemonServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "1.0.0"})
}

func (s *DaemonServer) handleHTTPStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.getStatus())
}

func (s *DaemonServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	s.clientsMu.Lock()
	s.clients[conn] = true
	s.clientsMu.Unlock()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, conn)
		s.clientsMu.Unlock()
		conn.Close()
	}()

	// Send initial status immediately upon connection
	s.sendTo(conn, NewEvent("status", s.getStatus()))

	for {
		var req RequestMessage
		if err := conn.ReadJSON(&req); err != nil {
			break
		}
		s.dispatch(conn, req)
	}
}

// Broadcast sends a JSON event to all active Flutter clients
func (s *DaemonServer) Broadcast(evt EventMessage) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	for client := range s.clients {
		_ = client.WriteJSON(evt)
	}
}

// SendTo sends a JSON event to a specific client connection
func (s *DaemonServer) sendTo(conn *websocket.Conn, evt EventMessage) {
	_ = conn.WriteJSON(evt)
}

func (s *DaemonServer) getStatus() DaemonStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := "idle"
	paired := s.activeSession != nil
	pairedIP := ""
	pairedDevice := ""
	if paired {
		status = "paired"
		pairedIP = s.activeSession.RemoteIP()
		pairedDevice = s.pairedDeviceName
	}
	if s.transferCancel != nil || s.itemCancel != nil {
		status = "transferring"
		if s.isPaused {
			status = "paused"
		}
	}

	targets := discovery.GetActiveNetworkTargets()
	localIP := ""
	for _, t := range targets {
		if !t.LocalIP.IsLoopback() && t.LocalIP.To4() != nil && !strings.HasPrefix(t.LocalIP.String(), "169.254.") {
			localIP = t.LocalIP.String()
			break
		}
	}
	port := s.httpPort
	if port <= 0 {
		port = 19999
	}
	portalURL := ""
	portalQR := ""
	if localIP != "" {
		portalURL = fmt.Sprintf("http://%s:%d/share", localIP, port)
		portalQR, _ = GenerateURLQRDataURI(portalURL, 220)
	}

	return DaemonStatus{
		Status:          status,
		DeviceName:      s.config.DeviceName,
		DownloadDir:     s.config.DownloadDir,
		CollisionPolicy: s.config.CollisionPolicy,
		Paired:          paired,
		PairedIP:        pairedIP,
		PairedDevice:    pairedDevice,
		ActiveTransfer:  s.transferCancel != nil || s.itemCancel != nil,
		IsPaused:        s.isPaused,
		Version:         "1.0.0",
		LocalIP:         localIP,
		LocalPort:       port,
		PortalURL:       portalURL,
		PortalQR:        portalQR,
	}
}

func (s *DaemonServer) listenForIncomingPairings(ln net.Listener) {
	srvTLSConfig, _ := session.ServerTLSConfig()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}

		if srvTLSConfig != nil {
			if upConn, _, err := session.UpgradeToTLSIfClientHello(conn, srvTLSConfig); err == nil {
				conn = upConn
			}
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
		s.activeSession = session.NewChannel(conn)
		remoteIP := s.activeSession.RemoteIP()
		devName := s.config.DeviceName
		sess := s.activeSession
		s.mu.Unlock()

		_ = sess.Send(session.Message{
			Type:       "pair_hello",
			DeviceName: devName,
		})

		s.Broadcast(NewEvent("paired", map[string]string{
			"ip":          remoteIP,
			"device_name": "Connecting...",
		}))

		go s.listenToSession(sess)
	}
}

func (s *DaemonServer) listenToSession(sess *session.Channel) {
	for {
		msg, err := sess.Read()
		if err != nil {
			s.mu.Lock()
			if s.activeSession == sess {
				s.activeSession = nil
				s.isReconnecting = true
				if s.sessionGraceTimer != nil {
					s.sessionGraceTimer.Stop()
				}
				s.sessionGraceTimer = time.AfterFunc(15*time.Second, func() {
					s.mu.Lock()
					if s.isReconnecting {
						s.isReconnecting = false
						s.pairedDeviceName = ""
						s.mu.Unlock()
						s.Broadcast(NewEvent("disconnected", map[string]string{"reason": "connection timed out"}))
					} else {
						s.mu.Unlock()
					}
				})
			}
			s.mu.Unlock()
			s.Broadcast(NewEvent("reconnecting", map[string]string{"message": "Connection lost. Waiting for peer to reconnect (15s grace period)..."}))
			return
		}

		s.handleSessionMessage(msg)
	}
}

func (s *DaemonServer) handleSessionMessage(msg session.Message) {
	switch msg.Type {
	case "pair_hello":
		s.mu.Lock()
		s.pairedDeviceName = msg.DeviceName
		devName := s.config.DeviceName
		sess := s.activeSession
		s.mu.Unlock()

		if sess != nil {
			_ = sess.Send(session.Message{
				Type:       "pair_hello_ack",
				DeviceName: devName,
			})
			s.Broadcast(NewEvent("paired", map[string]string{
				"ip":          sess.RemoteIP(),
				"device_name": msg.DeviceName,
			}))
		}

	case "pair_hello_ack":
		s.mu.Lock()
		s.pairedDeviceName = msg.DeviceName
		sess := s.activeSession
		s.mu.Unlock()

		if sess != nil {
			s.Broadcast(NewEvent("paired", map[string]string{
				"ip":          sess.RemoteIP(),
				"device_name": msg.DeviceName,
			}))
		}

	case "disconnect":
		s.mu.Lock()
		if s.sessionGraceTimer != nil {
			s.sessionGraceTimer.Stop()
			s.sessionGraceTimer = nil
		}
		s.isReconnecting = false
		if s.activeSession != nil {
			s.activeSession.Close()
			s.activeSession = nil
		}
		s.pairedDeviceName = ""
		if s.transferCancel != nil {
			s.transferCancel()
			s.transferCancel = nil
		}
		s.mu.Unlock()
		s.Broadcast(NewEvent("disconnected", map[string]string{"reason": "peer requested disconnect"}))

	case "offer":
		s.mu.Lock()
		s.pendingOffer = &msg
		devName := msg.DeviceName
		if devName == "" {
			devName = s.pairedDeviceName
		}
		if devName == "" {
			devName = "Peer"
		}
		s.mu.Unlock()

		s.Broadcast(NewEvent("incoming_offer", IncomingOfferData{
			SenderIP:   s.activeSession.RemoteIP(),
			DeviceName: devName,
			IsBatch:    false,
			FileName:   msg.FileName,
			FileSize:   msg.FileSize,
			TotalFiles: 1,
		}))

	case "batch_offer":
		totalFiles := 0
		totalBytes := int64(0)
		batchID := ""
		var batchItems []BatchFileInfo
		if msg.Batch != nil {
			totalFiles = msg.Batch.TotalFiles
			totalBytes = msg.Batch.TotalBytes
			batchID = msg.Batch.BatchID
			for i, it := range msg.Batch.Items {
				batchItems = append(batchItems, BatchFileInfo{
					Index:   i,
					RelPath: it.RelPath,
					Size:    it.Size,
					Status:  "pending",
				})
			}
		}

		s.mu.Lock()
		s.pendingOffer = &msg
		s.batchTotalFiles = totalFiles
		s.batchTotalBytes = totalBytes
		s.batchBaseBytes = 0
		s.currentBatchIndex = 0
		s.skippedFiles = make(map[int]bool)
		s.currentBatchItems = batchItems
		s.batchCanceled = false
		s.isPaused = false
		s.lastOfferedIsStream = msg.IsStream
		devName := msg.DeviceName
		if devName == "" {
			devName = s.pairedDeviceName
		}
		if devName == "" {
			devName = "Peer"
		}
		s.mu.Unlock()

		s.Broadcast(NewEvent("incoming_offer", IncomingOfferData{
			SenderIP:   s.activeSession.RemoteIP(),
			DeviceName: devName,
			IsBatch:    true,
			FileName:   msg.Batch.SummaryString(),
			FileSize:   totalBytes,
			TotalFiles: totalFiles,
			BatchID:    batchID,
			Items:      batchItems,
		}))

	case "pause":
		s.mu.Lock()
		s.isPaused = true
		if s.activeReceiver != nil {
			s.activeReceiver.Pause()
		}
		s.mu.Unlock()
		s.Broadcast(NewEvent("transfer_paused", map[string]string{"message": "Transfer paused by peer"}))

	case "resume":
		s.mu.Lock()
		s.isPaused = false
		if s.activeReceiver != nil {
			s.activeReceiver.Resume()
		}
		s.mu.Unlock()
		s.Broadcast(NewEvent("transfer_resumed", map[string]string{"message": "Transfer resumed by peer"}))

	case "cancel":
		s.mu.Lock()
		s.isPaused = false
		s.batchCanceled = true
		s.pausedFiles = make(map[int]bool)
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
		s.mu.Unlock()
		select {
		case s.batchResumeChan <- struct{}{}:
		default:
		}
		s.Broadcast(NewEvent("transfer_canceled", map[string]string{"message": "Transfer canceled by peer"}))

	case "skip_file":
		s.mu.Lock()
		skipIdx := msg.ItemIndex
		s.skippedFiles[skipIdx] = true
		for i := range s.currentBatchItems {
			if s.currentBatchItems[i].Index == skipIdx {
				s.currentBatchItems[i].Status = "skipped"
			}
		}
		currIdx := s.currentBatchIndex
		itemCancel := s.itemCancel
		items := s.currentBatchItems
		s.mu.Unlock()

		s.Broadcast(NewEvent("file_skipped", map[string]interface{}{
			"item_index": skipIdx,
			"items":      items,
		}))

		if skipIdx == currIdx && itemCancel != nil {
			itemCancel()
		}

	case "pause_file":
		s.mu.Lock()
		pauseIdx := msg.ItemIndex
		s.pausedFiles[pauseIdx] = true
		for i := range s.currentBatchItems {
			if s.currentBatchItems[i].Index == pauseIdx {
				s.currentBatchItems[i].Status = "paused"
			}
		}
		currIdx := s.currentBatchIndex
		itemCancel := s.itemCancel
		items := s.currentBatchItems
		s.mu.Unlock()

		s.Broadcast(NewEvent("file_paused", map[string]interface{}{
			"item_index": pauseIdx,
			"items":      items,
		}))

		if pauseIdx == currIdx && itemCancel != nil {
			itemCancel()
		}

	case "resume_file":
		s.mu.Lock()
		resumeIdx := msg.ItemIndex
		delete(s.pausedFiles, resumeIdx)
		for i := range s.currentBatchItems {
			if s.currentBatchItems[i].Index == resumeIdx && s.currentBatchItems[i].Status == "paused" {
				s.currentBatchItems[i].Status = "pending"
			}
		}
		items := s.currentBatchItems
		s.mu.Unlock()

		s.Broadcast(NewEvent("file_resumed", map[string]interface{}{
			"item_index": resumeIdx,
			"items":      items,
		}))

		select {
		case s.batchResumeChan <- struct{}{}:
		default:
		}

	case "reject":
		s.Broadcast(NewEvent("transfer_rejected", map[string]string{"message": "Peer rejected transfer"}))

	case "batch_accept":
		s.mu.Lock()
		m := s.lastOfferedManifest
		isStream := s.lastOfferedIsStream
		s.batchCanceled = false
		s.isPaused = false
		s.pausedFiles = make(map[int]bool)
		items := s.currentBatchItems
		s.mu.Unlock()

		if m == nil {
			return
		}

		s.Broadcast(NewEvent("batch_accepted", map[string]interface{}{
			"summary": m.SummaryString(),
			"items":   items,
		}))

		if isStream {
			s.mu.Lock()
			s.activePort++
			streamPort := s.activePort
			s.mu.Unlock()

			ln, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", streamPort))
			if err == nil {
				streamCtx, streamCancel := context.WithCancel(s.ctx)
				s.mu.Lock()
				s.transferCancel = streamCancel
				s.mu.Unlock()

				go func(port int, m *manifest.Manifest) {
					defer ln.Close()
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					defer conn.Close()

					srvTLS, _ := session.ServerTLSConfig()
					if srvTLS != nil {
						if upConn, _, err := session.UpgradeToTLSIfClientHello(conn, srvTLS); err == nil {
							conn = upConn
						}
					}

					listener := newDaemonListener(s, m.RootName, m.TotalBytes, 0, m.TotalFiles, 0, m.TotalBytes)
					_ = engine.StreamTar(streamCtx, conn, m, listener)

					if s.activeSession != nil {
						s.activeSession.Send(session.Message{Type: "batch_complete"})
					}
					s.Broadcast(NewEvent("transfer_complete", map[string]interface{}{
						"message": "Folder stream transfer complete",
					}))
				}(streamPort, m)

				if s.activeSession != nil {
					s.activeSession.Send(session.Message{
						Type:      "batch_stream",
						FileName:  m.RootName,
						FileSize:  m.TotalBytes,
						ItemIndex: m.TotalFiles,
						DataPort:  streamPort,
						IsStream:  true,
					})
				}
				return
			}
		}

		go func(m *manifest.Manifest) {
			s.mu.Lock()
			s.activePort++
			batchPort := s.activePort
			s.mu.Unlock()

			for {
				s.mu.Lock()
				if s.batchCanceled {
					s.mu.Unlock()
					return
				}

				nextIdx := -1
				hasPaused := false

				for i := range s.currentBatchItems {
					idx := s.currentBatchItems[i].Index
					status := s.currentBatchItems[i].Status
					if status == "completed" || status == "skipped" {
						continue
					}
					if s.pausedFiles[idx] || status == "paused" {
						hasPaused = true
						continue
					}
					if nextIdx == -1 {
						nextIdx = idx
					}
				}

				if nextIdx == -1 {
					if hasPaused {
						s.mu.Unlock()
						s.Broadcast(NewEvent("batch_paused_waiting", map[string]interface{}{
							"message": "Remaining files are paused. Click Resume on any file to continue.",
							"items":   s.currentBatchItems,
						}))
						select {
						case <-s.ctx.Done():
							return
						case <-s.batchResumeChan:
							continue
						}
					} else {
						s.mu.Unlock()
						break
					}
				}

				// Drain any residual signal before starting an item
				select {
				case <-s.itemDoneChan:
				default:
				}

				idx := nextIdx
				item := m.Items[idx]
				s.currentBatchIndex = idx
				for i := range s.currentBatchItems {
					if s.currentBatchItems[i].Index == idx {
						s.currentBatchItems[i].Status = "transferring"
					}
				}
				port := batchPort
				baseBytes := s.calculateCompletedBatchBytes()
				items := s.currentBatchItems
				s.mu.Unlock()

				s.Broadcast(NewEvent("batch_item_transferring", map[string]interface{}{
					"item_index": idx,
					"items":      items,
				}))

				itemCtx, itemCancel := context.WithCancel(s.ctx)
				s.mu.Lock()
				s.itemCancel = itemCancel
				s.transferCancel = itemCancel
				s.mu.Unlock()

				go func(fPath, rPath string, p int, bBase int64) {
					sender := engine.NewSender(s.config.Workers, uint32(s.config.ChunkSizeMB*1024*1024))
					listener := newDaemonListener(s, rPath, item.Size, idx, m.TotalFiles, bBase, m.TotalBytes)
					bindAddr := fmt.Sprintf("0.0.0.0:%d", p)
					_ = sender.ServeAndSendWithRelPath(itemCtx, bindAddr, fPath, rPath, listener, 0)
				}(item.FullPath, item.RelPath, port, baseBytes)

				time.Sleep(30 * time.Millisecond)

				if s.activeSession != nil {
					s.activeSession.Send(session.Message{
						Type:      "batch_item",
						FileName:  item.RelPath,
						FileSize:  item.Size,
						FileID:    item.FileID,
						DataPort:  port,
						ItemIndex: idx,
					})
				}

				select {
				case <-itemCtx.Done():
					s.mu.Lock()
					isCanceled := s.batchCanceled
					s.mu.Unlock()
					if isCanceled {
						return
					}
				case <-s.itemDoneChan:
					itemCancel()
					s.mu.Lock()
					for i := range s.currentBatchItems {
						if s.currentBatchItems[i].Index == idx && s.currentBatchItems[i].Status != "skipped" && s.currentBatchItems[i].Status != "paused" {
							s.currentBatchItems[i].Status = "completed"
						}
					}
					completedItems := s.currentBatchItems
					s.mu.Unlock()

					s.Broadcast(NewEvent("item_completed", map[string]interface{}{
						"item_index": idx,
						"items":      completedItems,
					}))
				}
			}

			s.mu.Lock()
			s.itemCancel = nil
			s.transferCancel = nil
			finalItems := s.currentBatchItems
			s.mu.Unlock()

			if s.activeSession != nil {
				s.activeSession.Send(session.Message{Type: "batch_complete"})
			}
			s.Broadcast(NewEvent("transfer_complete", map[string]interface{}{
				"message": "All batch files sent successfully",
				"items":   finalItems,
			}))
		}(m)

	case "batch_stream":
		s.mu.RLock()
		outDir := s.config.DownloadDir
		s.mu.RUnlock()

		streamCtx, streamCancel := context.WithCancel(s.ctx)
		s.mu.Lock()
		s.transferCancel = streamCancel
		s.mu.Unlock()

		s.Broadcast(NewEvent("transfer_start", map[string]interface{}{
			"current_file": msg.FileName,
			"file_index":   1,
			"total_files":  msg.ItemIndex,
			"total_bytes":  msg.FileSize,
		}))

		go func(msg session.Message) {
			defer streamCancel()
			targetAddr := fmt.Sprintf("%s:%d", s.activeSession.RemoteIP(), msg.DataPort)
			conn, err := session.DialTLSPeer(targetAddr)
			if err != nil {
				s.Broadcast(NewEvent("action_error", map[string]string{"error": err.Error()}))
				return
			}
			defer conn.Close()

			listener := newDaemonListener(s, msg.FileName, msg.FileSize, 0, msg.ItemIndex, 0, msg.FileSize)
			err = engine.ExtractTar(streamCtx, conn, outDir, msg.FileSize, msg.ItemIndex, listener)
			if err == nil {
				s.Broadcast(NewEvent("transfer_complete", map[string]interface{}{
					"message": "Directory stream transfer complete",
				}))
			}
		}(msg)

	case "batch_item":
		s.mu.RLock()
		outDir := s.config.DownloadDir
		policyStr := s.config.CollisionPolicy
		bTotalFiles := s.batchTotalFiles
		bTotalBytes := s.batchTotalBytes
		s.mu.RUnlock()

		bBaseBytes := s.calculateCompletedBatchBytes()

		s.mu.Lock()
		if s.batchCanceled || s.skippedFiles[msg.ItemIndex] {
			s.mu.Unlock()
			return
		}
		// Sender is sending this item: unpause on receiver
		delete(s.pausedFiles, msg.ItemIndex)
		s.currentBatchIndex = msg.ItemIndex
		for i := range s.currentBatchItems {
			if s.currentBatchItems[i].Index == msg.ItemIndex {
				s.currentBatchItems[i].Status = "transferring"
			}
		}
		itemTransferringList := s.currentBatchItems
		s.mu.Unlock()

		s.Broadcast(NewEvent("batch_item_transferring", map[string]interface{}{
			"item_index": msg.ItemIndex,
			"items":      itemTransferringList,
		}))

		policy := s.parseCollisionPolicy(policyStr)

		var ctx context.Context
		s.mu.Lock()
		ctx, s.itemCancel = context.WithCancel(s.ctx)
		s.transferCancel = s.itemCancel
		receiver := engine.NewReceiver(outDir, s.config.Workers)
		receiver.SetCollisionPolicy(policy)
		s.activeReceiver = receiver
		if s.isPaused {
			receiver.Pause()
		}
		s.mu.Unlock()

		go func(msg session.Message, bBase, bTotal int64, bCount int) {
			listener := newDaemonListener(s, msg.FileName, msg.FileSize, msg.ItemIndex, bCount, bBase, bTotal)
			targetAddr := fmt.Sprintf("%s:%d", s.activeSession.RemoteIP(), msg.DataPort)

			err := receiver.Pull(ctx, targetAddr, listener, msg.FileID)
			s.mu.Lock()
			isSkipped := s.skippedFiles[msg.ItemIndex]
			isPaused := s.pausedFiles[msg.ItemIndex]
			isCanceled := s.batchCanceled
			s.mu.Unlock()

			if err == nil {
				s.mu.Lock()
				s.batchBaseBytes += msg.FileSize
				for i := range s.currentBatchItems {
					if s.currentBatchItems[i].Index == msg.ItemIndex {
						s.currentBatchItems[i].Status = "completed"
					}
				}
				completedItems := s.currentBatchItems
				s.mu.Unlock()

				s.Broadcast(NewEvent("item_completed", map[string]interface{}{
					"item_index": msg.ItemIndex,
					"items":      completedItems,
				}))

				if s.activeSession != nil {
					s.activeSession.Send(session.Message{Type: "item_complete", ItemIndex: msg.ItemIndex})
				}
			} else if !isSkipped && !isPaused && !isCanceled && ctx.Err() == nil {
				s.Broadcast(NewEvent("transfer_error", map[string]string{
					"file":  msg.FileName,
					"error": err.Error(),
				}))
			}
		}(msg, bBaseBytes, bTotalBytes, bTotalFiles)

	case "item_complete":
		s.mu.Lock()
		for i := range s.currentBatchItems {
			if s.currentBatchItems[i].Index == msg.ItemIndex {
				s.currentBatchItems[i].Status = "completed"
			}
		}
		completedItems := s.currentBatchItems
		s.mu.Unlock()

		s.Broadcast(NewEvent("item_completed", map[string]interface{}{
			"item_index": msg.ItemIndex,
			"items":      completedItems,
		}))

		select {
		case s.itemDoneChan <- true:
		default:
		}

	case "batch_complete":
		s.mu.Lock()
		if s.itemCancel != nil {
			s.itemCancel()
			s.itemCancel = nil
		}
		if s.transferCancel != nil {
			s.transferCancel()
			s.transferCancel = nil
		}
		finalItems := s.currentBatchItems
		s.mu.Unlock()
		s.Broadcast(NewEvent("transfer_complete", map[string]interface{}{
			"message": "All batch files mirrored successfully",
			"items":   finalItems,
		}))

	case "accept":
		s.mu.Lock()
		offeredFile := s.lastOfferedFile
		offeredPort := s.lastOfferedPort
		s.mu.Unlock()

		s.Broadcast(NewEvent("transfer_accepted", map[string]interface{}{
			"resume_bytes": msg.ResumeBytes,
		}))

		if offeredFile != "" {
			go func(filePath string, port int, resume int64) {
				var ctx context.Context
				s.mu.Lock()
				ctx, s.transferCancel = context.WithCancel(s.ctx)
				s.mu.Unlock()

				sender := engine.NewSender(s.config.Workers, uint32(s.config.ChunkSizeMB*1024*1024))
				fi, _ := os.Stat(filePath)
				size := int64(0)
				if fi != nil {
					size = fi.Size()
				}
				listener := newDaemonListener(s, filepath.Base(filePath), size, 0, 1, 0, size)
				bindAddr := fmt.Sprintf("0.0.0.0:%d", port)
				_ = sender.ServeAndSend(ctx, bindAddr, filePath, listener, resume)
			}(offeredFile, offeredPort, msg.ResumeBytes)
		}

	case "complete":
		s.mu.Lock()
		if s.transferCancel != nil {
			s.transferCancel()
			s.transferCancel = nil
		}
		s.mu.Unlock()
		s.Broadcast(NewEvent("transfer_complete", map[string]string{"message": "Receiver confirmed transfer complete"}))
	}
}

func (s *DaemonServer) parseCollisionPolicy(p string) engine.CollisionPolicy {
	switch p {
	case "overwrite":
		return engine.PolicyOverwrite
	case "skip":
		return engine.PolicySkip
	default:
		return engine.PolicyAutoRename
	}
}

func (s *DaemonServer) handleBrowse(w http.ResponseWriter, r *http.Request) {
	browseType := r.URL.Query().Get("type")
	w.Header().Set("Content-Type", "application/json")

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		if browseType == "folder" {
			psScript := `Add-Type -AssemblyName System.Windows.Forms; $f = New-Object System.Windows.Forms.FolderBrowserDialog; $f.Description = 'Select a folder to send'; if($f.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK){ Write-Output $f.SelectedPath }`
			cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
		} else {
			psScript := `Add-Type -AssemblyName System.Windows.Forms; $f = New-Object System.Windows.Forms.OpenFileDialog; $f.Title = 'Select files to send'; $f.Multiselect = $true; if($f.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK){ Write-Output ($f.FileNames -join "` + "`" + `n") }`
			cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
		}
	case "darwin":
		if browseType == "folder" {
			cmd = exec.Command("osascript", "-e", `POSIX path of (choose folder with prompt "Select a folder to send")`)
		} else {
			cmd = exec.Command("osascript", "-e", `set f to choose file with prompt "Select files to send" with multiple selections allowed
set p to ""
repeat with i in f
    set p to p & (POSIX path of i) & linefeed
end repeat
return p`)
		}
	case "linux":
		if browseType == "folder" {
			if _, err := exec.LookPath("zenity"); err == nil {
				cmd = exec.Command("zenity", "--file-selection", "--directory", "--title=Select a folder to send")
			} else if _, err := exec.LookPath("kdialog"); err == nil {
				cmd = exec.Command("kdialog", "--getexistingdirectory")
			}
		} else {
			if _, err := exec.LookPath("zenity"); err == nil {
				cmd = exec.Command("zenity", "--file-selection", "--multiple", "--separator=\n", "--title=Select files to send")
			} else if _, err := exec.LookPath("kdialog"); err == nil {
				cmd = exec.Command("kdialog", "--getopenfilename", "--multiple", "--separate-output")
			}
		}
	}

	var paths []string
	if cmd != nil {
		out, err := cmd.Output()
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" {
					paths = append(paths, line)
				}
			}
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"paths": paths,
	})
}

func (s *DaemonServer) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	reader, err := r.MultipartReader()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	stageID := fmt.Sprintf("stage_%d", time.Now().UnixNano())
	stageDir := filepath.Join(os.TempDir(), "medxfer_staged", stageID)
	_ = os.MkdirAll(stageDir, 0755)

	var savedPaths []string

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		fileName := part.FileName()
		if fileName == "" {
			fileName = part.FormName()
		}
		if fileName == "" {
			continue
		}

		cleanRel := strings.ReplaceAll(fileName, "\\", "/")
		cleanRel = strings.TrimPrefix(cleanRel, "/")
		fullDest := filepath.Join(stageDir, filepath.FromSlash(cleanRel))

		_ = os.MkdirAll(filepath.Dir(fullDest), 0755)
		outFile, err := os.Create(fullDest)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_, copyErr := io.Copy(outFile, part)
		outFile.Close()
		if copyErr != nil {
			http.Error(w, copyErr.Error(), http.StatusInternalServerError)
			return
		}

		savedPaths = append(savedPaths, fullDest)
	}

	w.Header().Set("Content-Type", "application/json")
	if len(savedPaths) > 0 {
		pathsToSend := savedPaths
		if len(savedPaths) > 1 {
			// Check if all files share a single common top-level folder inside stageDir
			// (e.g. when user selected a folder using webkitdirectory)
			firstRel, _ := filepath.Rel(stageDir, savedPaths[0])
			firstParts := strings.Split(filepath.ToSlash(firstRel), "/")
			if len(firstParts) > 1 {
				commonFolder := firstParts[0]
				allShare := true
				for _, sp := range savedPaths[1:] {
					rel, _ := filepath.Rel(stageDir, sp)
					parts := strings.Split(filepath.ToSlash(rel), "/")
					if len(parts) <= 1 || parts[0] != commonFolder {
						allShare = false
						break
					}
				}
				if allShare {
					// Send user's actual folder, so receiver creates user's folder, NOT stage_...
					pathsToSend = []string{filepath.Join(stageDir, commonFolder)}
				}
			}
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "ok",
			"stage_dir":   stageDir,
			"paths":       pathsToSend,
			"total_files": len(savedPaths),
		})
	} else {
		http.Error(w, "no files received", http.StatusBadRequest)
	}
}

type FSQuickDir struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type FSDirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type FSFileEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type FSListResponse struct {
	CurrentDir string        `json:"current_dir"`
	ParentDir  string        `json:"parent_dir"`
	QuickDirs  []FSQuickDir  `json:"quick_dirs"`
	Dirs       []FSDirEntry  `json:"dirs"`
	Files      []FSFileEntry `json:"files"`
}

func getQuickDirs() []FSQuickDir {
	var quick []FSQuickDir
	home, _ := os.UserHomeDir()

	// 1. Android shared storage directories
	androidDirs := []struct {
		name string
		path string
	}{
		{"📥 Downloads", "/storage/emulated/0/Download"},
		{"📁 Documents", "/storage/emulated/0/Documents"},
		{"📷 DCIM", "/storage/emulated/0/DCIM"},
		{"🎬 Movies", "/storage/emulated/0/Movies"},
		{"🎵 Music", "/storage/emulated/0/Music"},
		{"📱 SD Card", "/sdcard"},
	}
	for _, ad := range androidDirs {
		if fi, err := os.Stat(ad.path); err == nil && fi.IsDir() {
			quick = append(quick, FSQuickDir{Name: ad.name, Path: ad.path})
		}
	}

	// 2. User Home / Downloads / Documents
	if home != "" {
		dl := filepath.Join(home, "Downloads")
		if fi, err := os.Stat(dl); err == nil && fi.IsDir() {
			quick = append(quick, FSQuickDir{Name: "📥 Downloads", Path: dl})
		}
		docs := filepath.Join(home, "Documents")
		if fi, err := os.Stat(docs); err == nil && fi.IsDir() {
			quick = append(quick, FSQuickDir{Name: "📁 Documents", Path: docs})
		}
		quick = append(quick, FSQuickDir{Name: "🏠 Home", Path: home})
	}

	// 3. Windows Drives
	if runtime.GOOS == "windows" {
		for _, drive := range "CDEFGHIJKLMNOPQRSTUVWXYZ" {
			dPath := string(drive) + ":\\"
			if fi, err := os.Stat(dPath); err == nil && fi.IsDir() {
				quick = append(quick, FSQuickDir{Name: "💾 " + string(drive) + ":", Path: dPath})
			}
		}
	}

	return quick
}

func (s *DaemonServer) handleFSList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	targetDir := r.URL.Query().Get("dir")

	if targetDir == "" {
		s.mu.RLock()
		targetDir = s.config.DownloadDir
		s.mu.RUnlock()
	}

	if targetDir == "" || targetDir == "." {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			dl := filepath.Join(home, "Downloads")
			if fi, err := os.Stat(dl); err == nil && fi.IsDir() {
				targetDir = dl
			} else {
				targetDir = home
			}
		} else {
			targetDir, _ = os.Getwd()
		}
	}

	absDir, err := filepath.Abs(targetDir)
	if err == nil {
		targetDir = absDir
	}

	entries, err := os.ReadDir(targetDir)
	var dirs []FSDirEntry
	var files []FSFileEntry
	if err == nil {
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") && len(name) > 1 {
				continue
			}
			if e.IsDir() {
				dirs = append(dirs, FSDirEntry{
					Name: name,
					Path: filepath.Join(targetDir, name),
				})
			} else {
				fi, err := e.Info()
				size := int64(0)
				if err == nil {
					size = fi.Size()
				}
				files = append(files, FSFileEntry{
					Name: name,
					Path: filepath.Join(targetDir, name),
					Size: size,
				})
			}
		}
	}

	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})

	parent := filepath.Dir(targetDir)
	if parent == targetDir {
		parent = ""
	}

	_ = json.NewEncoder(w).Encode(FSListResponse{
		CurrentDir: targetDir,
		ParentDir:  parent,
		QuickDirs:  getQuickDirs(),
		Dirs:       dirs,
		Files:      files,
	})
}

func (s *DaemonServer) handleFSMkdir(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	parentDir := r.URL.Query().Get("dir")
	folderName := r.URL.Query().Get("name")

	if parentDir == "" || folderName == "" {
		http.Error(w, "missing dir or name", http.StatusBadRequest)
		return
	}

	target := filepath.Join(parentDir, folderName)
	if err := os.MkdirAll(target, 0755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"path":   target,
	})
}
