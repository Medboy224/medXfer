package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/engine"
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
	mu             sync.RWMutex
	config         Config
	clients        map[*websocket.Conn]bool
	clientsMu      sync.Mutex
	activeSession  *session.Channel
	pendingOffer   *session.Message
	transferCancel context.CancelFunc
	activePort     int
	itemDoneChan   chan bool

	ctx     context.Context
	cancel  context.CancelFunc
	httpSrv *http.Server
	nodeLn  net.Listener
	discSrv *discovery.DiscoveryServer
}

// NewDaemonServer initializes a daemon with default configurations
func NewDaemonServer(port int, defaultOutDir, deviceName string) *DaemonServer {
	if defaultOutDir == "" {
		defaultOutDir = "."
	}
	if deviceName == "" {
		h, _ := os.Hostname()
		if h != "" {
			deviceName = h
		} else {
			deviceName = "medXfer-Node"
		}
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &DaemonServer{
		config: Config{
			DeviceName:      deviceName,
			DownloadDir:     defaultOutDir,
			CollisionPolicy: "auto_rename",
			Workers:         4,
			ChunkSizeMB:     2,
		},
		clients:      make(map[*websocket.Conn]bool),
		activePort:   18888,
		itemDoneChan: make(chan bool, 1),
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Listen binds the HTTP listener synchronously
func (s *DaemonServer) Listen(port int) (net.Listener, error) {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	return net.Listen("tcp4", addr)
}

// Serve runs the daemon on an existing listener
func (s *DaemonServer) Serve(httpLn net.Listener) error {
	// 1. Start Node Pairing TCP Listener (port 18887)
	ln, err := net.Listen("tcp4", "0.0.0.0:18887")
	if err == nil {
		s.nodeLn = ln
		go s.listenForIncomingPairings(ln)
	}

	// 2. Start Discovery Broadcast Server
	discOffer := &discovery.TransferOffer{FileName: s.config.DeviceName, FileSize: 0}
	s.discSrv = discovery.NewDiscoveryServer("node", 18887, discOffer)
	s.discSrv.Start(s.ctx)

	// 3. Start HTTP/WebSocket Router
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/status", s.handleHTTPStatus)
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
	if s.transferCancel != nil {
		s.transferCancel()
	}
	if s.activeSession != nil {
		s.activeSession.Send(session.Message{Type: "disconnect"})
		s.activeSession.Close()
	}
	if s.nodeLn != nil {
		_ = s.nodeLn.Close()
	}
	if s.httpSrv != nil {
		_ = s.httpSrv.Close()
	}
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
	if paired {
		status = "paired"
		pairedIP = s.activeSession.RemoteIP()
	}
	if s.transferCancel != nil {
		status = "transferring"
	}

	return DaemonStatus{
		Status:          status,
		DeviceName:      s.config.DeviceName,
		DownloadDir:     s.config.DownloadDir,
		CollisionPolicy: s.config.CollisionPolicy,
		Paired:          paired,
		PairedIP:        pairedIP,
		ActiveTransfer:  s.transferCancel != nil,
		Version:         "1.0.0",
	}
}

func (s *DaemonServer) listenForIncomingPairings(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.activeSession == nil {
			s.activeSession = session.NewChannel(conn.(*net.TCPConn))
			remoteIP := s.activeSession.RemoteIP()
			s.mu.Unlock()

			s.Broadcast(NewEvent("paired", map[string]string{
				"ip": remoteIP,
			}))

			go s.listenToSession(s.activeSession)
		} else {
			s.mu.Unlock()
			conn.Close()
		}
	}
}

func (s *DaemonServer) listenToSession(sess *session.Channel) {
	for {
		msg, err := sess.Read()
		if err != nil {
			s.mu.Lock()
			if s.activeSession == sess {
				s.activeSession = nil
			}
			s.mu.Unlock()
			s.Broadcast(NewEvent("disconnected", map[string]string{"reason": "peer closed connection"}))
			return
		}

		s.handleSessionMessage(msg)
	}
}

func (s *DaemonServer) handleSessionMessage(msg session.Message) {
	switch msg.Type {
	case "disconnect":
		s.mu.Lock()
		if s.activeSession != nil {
			s.activeSession.Close()
			s.activeSession = nil
		}
		if s.transferCancel != nil {
			s.transferCancel()
			s.transferCancel = nil
		}
		s.mu.Unlock()
		s.Broadcast(NewEvent("disconnected", map[string]string{"reason": "peer requested disconnect"}))

	case "offer":
		s.mu.Lock()
		s.pendingOffer = &msg
		s.mu.Unlock()

		s.Broadcast(NewEvent("incoming_offer", IncomingOfferData{
			SenderIP:   s.activeSession.RemoteIP(),
			DeviceName: "Peer",
			IsBatch:    false,
			FileName:   msg.FileName,
			FileSize:   msg.FileSize,
			TotalFiles: 1,
		}))

	case "batch_offer":
		s.mu.Lock()
		s.pendingOffer = &msg
		s.mu.Unlock()

		totalFiles := 0
		totalBytes := int64(0)
		batchID := ""
		if msg.Batch != nil {
			totalFiles = msg.Batch.TotalFiles
			totalBytes = msg.Batch.TotalBytes
			batchID = msg.Batch.BatchID
		}

		s.Broadcast(NewEvent("incoming_offer", IncomingOfferData{
			SenderIP:   s.activeSession.RemoteIP(),
			DeviceName: "Peer",
			IsBatch:    true,
			FileName:   msg.Batch.SummaryString(),
			FileSize:   totalBytes,
			TotalFiles: totalFiles,
			BatchID:    batchID,
		}))

	case "reject":
		s.Broadcast(NewEvent("transfer_rejected", map[string]string{"message": "Peer rejected transfer"}))

	case "batch_item":
		s.mu.RLock()
		outDir := s.config.DownloadDir
		policyStr := s.config.CollisionPolicy
		s.mu.RUnlock()

		policy := s.parseCollisionPolicy(policyStr)

		var ctx context.Context
		s.mu.Lock()
		ctx, s.transferCancel = context.WithCancel(s.ctx)
		s.mu.Unlock()

		go func(msg session.Message) {
			receiver := engine.NewReceiver(outDir, 4)
			receiver.SetCollisionPolicy(policy)

			listener := newDaemonListener(s, msg.FileName, msg.FileSize, msg.ItemIndex, 0, 0)
			targetAddr := fmt.Sprintf("%s:%d", s.activeSession.RemoteIP(), msg.DataPort)

			err := receiver.Pull(ctx, targetAddr, listener, msg.FileID)
			if err == nil {
				if s.activeSession != nil {
					s.activeSession.Send(session.Message{Type: "item_complete", ItemIndex: msg.ItemIndex})
				}
			} else if ctx.Err() == nil {
				s.Broadcast(NewEvent("transfer_error", map[string]string{
					"file":  msg.FileName,
					"error": err.Error(),
				}))
			}
		}(msg)

	case "item_complete":
		select {
		case s.itemDoneChan <- true:
		default:
		}

	case "batch_complete":
		s.mu.Lock()
		if s.transferCancel != nil {
			s.transferCancel()
			s.transferCancel = nil
		}
		s.mu.Unlock()
		s.Broadcast(NewEvent("transfer_complete", map[string]interface{}{
			"message": "All batch files mirrored successfully",
		}))

	case "accept":
		s.Broadcast(NewEvent("transfer_accepted", map[string]interface{}{
			"resume_bytes": msg.ResumeBytes,
		}))

	case "cancel":
		s.mu.Lock()
		if s.transferCancel != nil {
			s.transferCancel()
			s.transferCancel = nil
		}
		s.mu.Unlock()
		s.Broadcast(NewEvent("transfer_canceled", map[string]string{"message": "Peer cancelled transfer"}))

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
