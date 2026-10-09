package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/hotspot"
	"github.com/Medboy224/medXfer/pkg/manifest"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/gorilla/websocket"
)

// controlTokenPlaceholder is replaced by the real control token when the dashboard is served.
const controlTokenPlaceholder = "__MEDXFER_CONTROL_TOKEN__"

// IPAuthRecord tracks failed PIN authentication attempts for brute-force defense
type IPAuthRecord struct {
	FailCount   int
	LockedUntil time.Time
	LastFail    time.Time
}

// DaemonServer manages the headless background engine and WebSocket connections
type DaemonServer struct {
	controlToken    string // 256-bit secret required on every control route (API-04)
	controlDisabled bool   // share-only mode: no dashboard, /ws, /status or /api/fs/*
	mu                  sync.RWMutex
	config              Config
	clients             map[*websocket.Conn]bool
	clientsMu           sync.Mutex
	activeSession       *session.Channel
	pairedDeviceName    string
	pendingOffer        *session.Message
	lastOfferedManifest *manifest.Manifest
	lastOfferedFile     string
	webSharedManifest   *manifest.Manifest
	webSharedFile       string
	webShareEnabled     bool
	webSharePIN         string
	webShareToken       string
	webShareAutoAccept  bool
	pendingWebUploads   map[string]*WebUploadRequest
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

	authFailMu   sync.Mutex
	lastAuthFail map[string]time.Time
	ipLockouts   map[string]*IPAuthRecord

	webShareTransferMu        sync.Mutex
	webShareTransferCancel    context.CancelFunc
	webShareTransferPaused    bool
	webShareTransferPauseCond *sync.Cond
	webShareTransferDirection string // "upload" | "download"
	webShareTransferFile      string
	webShareTransferClientIP  string
	webShareTransferDestPath  string
	webShareTransferLastBytes int64
	webShareTransferLastTime  time.Time
	webShareTransferSpeed     float64

	ctx         context.Context
	cancel      context.CancelFunc
	httpSrv     *http.Server
	nodeLn      net.Listener
	discSrv     *discovery.DiscoveryServer
	pairingCode string

	benchPongChan     chan session.Message
	benchBurstAckChan chan session.Message

	trackerMu         sync.Mutex
	activeTracker     *TransferSessionTracker
	lastSummaryReport *TransferSummaryReport
}

// NewDaemonServer initializes a daemon with default configurations
func NewDaemonServer(port int, defaultOutDir, deviceName string) *DaemonServer {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := LoadConfig(defaultOutDir, deviceName)

	// 6-digit PIN (1,000,000 combinations) and 128-bit token from crypto/rand (WEB-02, WEB-03).
	// Since Go 1.24 crypto/rand cannot fail on supported platforms; refuse to start if it does.
	pin, err := randomPIN(6)
	if err != nil {
		panic("medxfer: crypto/rand unavailable: " + err.Error())
	}
	token, err := randomHex(16)
	if err != nil {
		panic("medxfer: crypto/rand unavailable: " + err.Error())
	}

	// Control token (API-04, decision D0-2): MEDXFER_CONTROL_TOKEN if long enough, else random.
	controlToken := os.Getenv("MEDXFER_CONTROL_TOKEN")
	if len(controlToken) < 32 {
		if controlToken, err = randomHex(32); err != nil {
			panic("medxfer: crypto/rand unavailable: " + err.Error())
		}
	}

	pCode, _, _ := discovery.GeneratePairingCode("")

	srv := &DaemonServer{
		controlToken:       controlToken,
		config:             cfg,
		clients:            make(map[*websocket.Conn]bool),
		activePort:         18888,
		itemDoneChan:       make(chan bool, 1),
		skippedFiles:       make(map[int]bool),
		pausedFiles:        make(map[int]bool),
		batchResumeChan:    make(chan struct{}, 1),
		webSharePIN:        pin,
		webShareToken:      token,
		webShareEnabled:    false,
		webShareAutoAccept: false,
		pairingCode:        pCode,
		pendingWebUploads:  make(map[string]*WebUploadRequest),
		lastAuthFail:       make(map[string]time.Time),
		ipLockouts:         make(map[string]*IPAuthRecord),
		benchPongChan:      make(chan session.Message, 10),
		benchBurstAckChan:  make(chan session.Message, 10),
		ctx:                ctx,
		cancel:             cancel,
	}
	srv.webShareTransferPauseCond = sync.NewCond(&srv.webShareTransferMu)
	return srv
}

// CheckLockout returns true if the client IP is currently locked out from brute-force protection
func (s *DaemonServer) CheckLockout(clientIP string) (bool, int) {
	s.authFailMu.Lock()
	defer s.authFailMu.Unlock()
	if s.ipLockouts == nil {
		return false, 0
	}
	rec, ok := s.ipLockouts[clientIP]
	if !ok || rec == nil {
		return false, 0
	}
	now := time.Now()
	if now.Before(rec.LockedUntil) {
		remaining := int(rec.LockedUntil.Sub(now).Seconds())
		if remaining < 1 {
			remaining = 1
		}
		return true, remaining
	}
	return false, 0
}

// RecordAuthFailure registers a failed PIN attempt, enforces progressive lockout,
// and triggers emergency PIN rotation on 10 consecutive failures (Panic Mode)
func (s *DaemonServer) RecordAuthFailure(clientIP string) (locked bool, remainingSec int, pinRegenerated bool) {
	s.authFailMu.Lock()
	if s.ipLockouts == nil {
		s.ipLockouts = make(map[string]*IPAuthRecord)
	}
	rec, ok := s.ipLockouts[clientIP]
	if !ok || rec == nil {
		rec = &IPAuthRecord{}
		s.ipLockouts[clientIP] = rec
	}

	now := time.Now()
	if !rec.LastFail.IsZero() && now.Sub(rec.LastFail) > 15*time.Minute {
		rec.FailCount = 0
	}
	rec.LastFail = now
	rec.FailCount++

	if rec.FailCount >= 10 {
		rec.LockedUntil = now.Add(5 * time.Minute)
		locked = true
		remainingSec = 300
		pinRegenerated = true
	} else if rec.FailCount >= 5 {
		rec.LockedUntil = now.Add(1 * time.Minute)
		locked = true
		remainingSec = 60
	}
	s.authFailMu.Unlock()

	log.Printf("[Security] Web Share failed attempt #%d from %s", rec.FailCount, clientIP)

	if locked {
		log.Printf("[Security] 🚨 Brute force lockout applied to IP %s for %d seconds", clientIP, remainingSec)
		s.Broadcast(NewEvent("web_share_brute_force_blocked", map[string]interface{}{
			"client_ip":       clientIP,
			"fail_count":      rec.FailCount,
			"locked_seconds":  remainingSec,
			"pin_regenerated": pinRegenerated,
			"timestamp":       now.Format("15:04:05"),
		}))
	} else {
		s.notifyAuthFailure(clientIP)
	}

	if pinRegenerated {
		s.regeneratePINInternal(6)
	}

	return locked, remainingSec, pinRegenerated
}

// RecordAuthSuccess resets the failed attempt counter for a client IP upon successful authentication
func (s *DaemonServer) RecordAuthSuccess(clientIP string) {
	s.authFailMu.Lock()
	if s.ipLockouts != nil {
		delete(s.ipLockouts, clientIP)
	}
	s.authFailMu.Unlock()
}

// regeneratePINInternal generates a new random PIN and crypto token, then broadcasts status
func (s *DaemonServer) regeneratePINInternal(digits int) {
	if digits != 4 {
		digits = 6
	}
	pin, err := randomPIN(digits)
	if err != nil {
		log.Printf("[Security] Web Share credential rotation failed: %v", err)
		return
	}
	token, err := randomHex(16)
	if err != nil {
		log.Printf("[Security] Web Share credential rotation failed: %v", err)
		return
	}
	s.mu.Lock()
	s.webSharePIN = pin
	s.webShareToken = token
	s.mu.Unlock()

	log.Printf("[Security] Web Share credentials rotated") // never log the PIN or token (WEB-04)
	s.Broadcast(NewEvent("status", s.getStatus()))
}

// notifyAuthFailure logs and broadcasts an authentication failure event, rate-limited per client IP
func (s *DaemonServer) notifyAuthFailure(clientIP string) {
	s.authFailMu.Lock()
	if s.lastAuthFail == nil {
		s.lastAuthFail = make(map[string]time.Time)
	}
	last, ok := s.lastAuthFail[clientIP]
	now := time.Now()
	if ok && now.Sub(last) < 3*time.Second {
		s.authFailMu.Unlock()
		return
	}
	s.lastAuthFail[clientIP] = now
	s.authFailMu.Unlock()

	log.Printf("[Security] Web Share invalid PIN attempt from %s", clientIP)
	s.Broadcast(NewEvent("web_share_auth_failed", map[string]interface{}{
		"client_ip": clientIP,
		"timestamp": now.Format("15:04:05"),
	}))
}

// randomHex returns n bytes from crypto/rand, hex-encoded.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// randomPIN returns a PIN uniform in [0, 10^digits), without modulo bias (PAIR-01, WEB-03).
func randomPIN(digits int) (string, error) {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", digits, n), nil
}

// secureEq compares secrets in constant time.
func secureEq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
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

func (s *DaemonServer) RegisterWebShareTransfer(direction, clientIP, fileName string, cancelFunc context.CancelFunc, destPath string) {
	s.webShareTransferMu.Lock()
	defer s.webShareTransferMu.Unlock()
	s.webShareTransferDirection = direction
	s.webShareTransferClientIP = clientIP
	s.webShareTransferFile = fileName
	s.webShareTransferCancel = cancelFunc
	s.webShareTransferDestPath = destPath
	s.webShareTransferPaused = false
	s.webShareTransferLastBytes = 0
	s.webShareTransferLastTime = time.Now()
	s.webShareTransferSpeed = 0
}

func (s *DaemonServer) UnregisterWebShareTransfer() {
	s.webShareTransferMu.Lock()
	defer s.webShareTransferMu.Unlock()
	s.webShareTransferDirection = ""
	s.webShareTransferClientIP = ""
	s.webShareTransferFile = ""
	s.webShareTransferCancel = nil
	s.webShareTransferDestPath = ""
	s.webShareTransferPaused = false
	s.webShareTransferLastBytes = 0
	s.webShareTransferSpeed = 0
	if s.webShareTransferPauseCond != nil {
		s.webShareTransferPauseCond.Broadcast()
	}
}

func (s *DaemonServer) PauseWebShareTransfer() bool {
	s.webShareTransferMu.Lock()
	defer s.webShareTransferMu.Unlock()
	if s.webShareTransferDirection == "" {
		return false
	}
	s.webShareTransferPaused = true
	dir := s.webShareTransferDirection
	file := s.webShareTransferFile
	clientIP := s.webShareTransferClientIP

	s.Broadcast(NewEvent("web_share_paused", map[string]interface{}{
		"direction": dir,
		"file":      file,
		"client_ip": clientIP,
		"message":   "Web Share transfer paused by host",
	}))
	return true
}

func (s *DaemonServer) ResumeWebShareTransfer() bool {
	s.webShareTransferMu.Lock()
	defer s.webShareTransferMu.Unlock()
	if s.webShareTransferDirection == "" {
		return false
	}
	s.webShareTransferPaused = false
	if s.webShareTransferPauseCond != nil {
		s.webShareTransferPauseCond.Broadcast()
	}
	dir := s.webShareTransferDirection
	file := s.webShareTransferFile
	clientIP := s.webShareTransferClientIP

	s.Broadcast(NewEvent("web_share_resumed", map[string]interface{}{
		"direction": dir,
		"file":      file,
		"client_ip": clientIP,
		"message":   "Web Share transfer resumed by host",
	}))
	return true
}

func (s *DaemonServer) CancelWebShareTransfer() bool {
	s.webShareTransferMu.Lock()
	cancel := s.webShareTransferCancel
	s.webShareTransferCancel = nil
	destPath := s.webShareTransferDestPath
	s.webShareTransferDestPath = ""
	dir := s.webShareTransferDirection
	file := s.webShareTransferFile
	clientIP := s.webShareTransferClientIP
	s.webShareTransferDirection = ""
	s.webShareTransferFile = ""
	s.webShareTransferClientIP = ""
	s.webShareTransferPaused = false
	s.webShareTransferLastBytes = 0
	s.webShareTransferSpeed = 0
	if s.webShareTransferPauseCond != nil {
		s.webShareTransferPauseCond.Broadcast()
	}
	s.webShareTransferMu.Unlock()

	if cancel != nil {
		cancel()
	}
	if destPath != "" {
		_ = os.Remove(destPath)
	}

	s.Broadcast(NewEvent("web_share_canceled", map[string]interface{}{
		"direction": dir,
		"file":      file,
		"client_ip": clientIP,
		"message":   "Web Share transfer canceled",
	}))
	return true
}

func (s *DaemonServer) CheckWebSharePause(ctx context.Context) error {
	s.webShareTransferMu.Lock()
	defer s.webShareTransferMu.Unlock()
	for s.webShareTransferPaused {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.webShareTransferPauseCond.Wait()
	}
	return ctx.Err()
}

func (s *DaemonServer) IsWebShareTransferPaused() bool {
	s.webShareTransferMu.Lock()
	defer s.webShareTransferMu.Unlock()
	return s.webShareTransferPaused
}

// Listen binds the HTTP listener synchronously, falling back to next available port if busy
func (s *DaemonServer) Listen(port int) (net.Listener, error) {
	for p := port; p < port+20; p++ {
		addr := fmt.Sprintf("0.0.0.0:%d", p)
		ln, err := net.Listen("tcp4", addr)
		if err == nil {
			s.mu.Lock()
			s.httpPort = p
			s.mu.Unlock()
			return ln, nil
		}
	}
	return nil, fmt.Errorf("could not bind to any port in range %d-%d", port, port+19)
}

// Serve runs the daemon on an existing listener
func (s *DaemonServer) Serve(httpLn net.Listener) error {
	httpPort := 18888
	if tcpAddr, ok := httpLn.Addr().(*net.TCPAddr); ok {
		httpPort = tcpAddr.Port
	}
	s.mu.Lock()
	s.httpPort = httpPort
	s.mu.Unlock()

	// 1. Start Node Pairing TCP Listener (port 18887 or fallback)
	nodePort := 18887
	ln, err := net.Listen("tcp4", "0.0.0.0:18887")
	if err != nil {
		ln, err = net.Listen("tcp4", "0.0.0.0:0")
	}
	if err == nil {
		s.mu.Lock()
		s.nodeLn = ln
		s.mu.Unlock()
		if tcpAddr, ok := ln.Addr().(*net.TCPAddr); ok {
			nodePort = tcpAddr.Port
		}
		go s.listenForIncomingPairings(ln)
	}

	// 2. Start Discovery Broadcast Server
	discOffer := &discovery.TransferOffer{FileName: s.config.DeviceName, FileSize: 0}
	s.discSrv = discovery.NewDiscoveryServer("node", nodePort, discOffer, s.config.DeviceName)
	if s.pairingCode != "" {
		s.discSrv.SetPairingCode(s.pairingCode)
	}
	s.discSrv.Start(s.ctx)

	// 3. Start HTTP/WebSocket Router
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/health", s.handleHealth)
	if !s.controlDisabled {
		// Control surface (API-04): loopback + Host + Origin + control token on every route.
		mux.HandleFunc("/status", s.control(s.handleHTTPStatus))
		mux.HandleFunc("/api/browse", s.control(s.handleBrowse))
		mux.HandleFunc("/api/upload", s.control(s.handleUpload))
		mux.HandleFunc("/api/fs/list", s.control(s.handleFSList))
		mux.HandleFunc("/api/fs/mkdir", s.control(s.handleFSMkdir))
		mux.HandleFunc("/ws", s.control(s.handleWebSocket))
	}
	mux.HandleFunc("/share", shareHeaders(s.handleSharePortal, true))
	mux.HandleFunc("/api/share/list", shareHeaders(s.handleShareList, true))
	mux.HandleFunc("/api/share/download", shareHeaders(s.handleShareDownload, false))
	mux.HandleFunc("/api/share/request_upload", shareHeaders(s.handleShareRequestUpload, false))
	mux.HandleFunc("/api/share/upload", shareHeaders(s.handleShareUpload, false))
	mux.HandleFunc("/api/share/upload_chunk", shareHeaders(s.handleShareUploadChunk, false))
	mux.HandleFunc("/api/share/pause", shareHeaders(s.handleSharePause, false))
	mux.HandleFunc("/api/share/resume", shareHeaders(s.handleShareResume, false))
	mux.HandleFunc("/api/share/cancel", shareHeaders(s.handleShareCancel, false))

	httpSrv := &http.Server{
		Handler: mux,
	}
	s.mu.Lock()
	s.httpSrv = httpSrv
	s.mu.Unlock()

	log.Printf("[*] medXfer Headless Daemon started on http://%s (ws://%s/ws)", httpLn.Addr().String(), httpLn.Addr().String())
	return httpSrv.Serve(httpLn)
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
	nodeLn, httpSrv := s.nodeLn, s.httpSrv
	s.mu.Unlock()

	if sess != nil {
		sess.Send(session.Message{Type: "disconnect"})
		sess.Close()
	}
	if nodeLn != nil {
		_ = nodeLn.Close()
	}
	if httpSrv != nil {
		_ = httpSrv.Close()
	}
	s.hotspotMu.Lock()
	if s.hotspotCtrl != nil {
		_ = s.hotspotCtrl.Stop()
		s.hotspotCtrl = nil
		s.activeHotspot = nil
	}
	s.hotspotMu.Unlock()
}

// isLoopbackRequest checks whether an incoming HTTP request originated from localhost
func isLoopbackRequest(r *http.Request) bool {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

func (s *DaemonServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Security check: External network devices MUST NOT access the full host dashboard!
	// Redirect any non-loopback device (or everyone in share-only mode) to the Web Share portal.
	if s.controlDisabled || !isLoopbackRequest(r) {
		redirectURL := "/share"
		if r.URL.RawQuery != "" {
			redirectURL += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
		return
	}
	// The page receives the control token: never serve it under a foreign Host (DNS rebinding).
	// No token is required here, it is the page that obtains it (API-04, control 7).
	if !s.hostAllowed(r.Host) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write([]byte(strings.Replace(IndexHTML, controlTokenPlaceholder, s.controlToken, 1)))
}

func (s *DaemonServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Liveness probe without token (decision D0-4), but loopback and Host only.
	if !isLoopbackRequest(r) || !s.hostAllowed(r.Host) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "1.0.0"})
}

func (s *DaemonServer) handleHTTPStatus(w http.ResponseWriter, r *http.Request) {
	// The status carries the Web Share PIN/token and the pairing code: never serve it to the LAN.
	if !isLoopbackRequest(r) {
		http.Error(w, "Forbidden: status is restricted to localhost", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.getStatus())
}

func (s *DaemonServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		http.Error(w, "Forbidden: WebSocket control channel is restricted to localhost", http.StatusForbidden)
		return
	}

	upgrader := websocket.Upgrader{
		// control() already rejected foreign origins; checked again as defense in depth.
		CheckOrigin:  func(r *http.Request) bool { o := r.Header.Get("Origin"); return o == "" || s.originAllowed(o) },
		Subprotocols: []string{"medxfer.v1"}, // never echo the "token.<hex>" protocol
	}
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
		_ = client.SetWriteDeadline(time.Now().Add(2 * time.Second))
		err := client.WriteJSON(evt)
		_ = client.SetWriteDeadline(time.Time{})
		if err != nil {
			_ = client.Close()
			delete(s.clients, client)
		}
	}
}

// SendTo sends a JSON event to a specific client connection.
// gorilla/websocket allows one concurrent writer per connection: every write,
// here and in Broadcast, holds clientsMu.
func (s *DaemonServer) sendTo(conn *websocket.Conn, evt EventMessage) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_ = conn.WriteJSON(evt)
	_ = conn.SetWriteDeadline(time.Time{})
}

// SharePaths configures files or directories to be immediately available on the Web Share portal
func (s *DaemonServer) SharePaths(paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("no paths provided")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(paths) == 1 {
		fi, err := os.Stat(paths[0])
		if err == nil && !fi.IsDir() {
			s.webSharedFile = paths[0]
			s.webSharedManifest = nil
			return nil
		}
	}

	m, err := manifest.Build(paths)
	if err != nil {
		return err
	}
	s.webSharedManifest = m
	s.webSharedFile = ""
	return nil
}

// SetPIN configures the 4-digit security PIN for Web Share
func (s *DaemonServer) SetPIN(pin string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webSharePIN = pin
}

// SetAutoAccept toggles automatic acceptance of Web Share incoming uploads
func (s *DaemonServer) SetAutoAccept(autoAccept bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webShareAutoAccept = autoAccept
}

// SetWebShareEnabled enables or disables Web Share access and broadcasts status
func (s *DaemonServer) SetWebShareEnabled(enabled bool) {
	s.mu.Lock()
	s.webShareEnabled = enabled
	s.mu.Unlock()
	s.Broadcast(NewEvent("status", s.getStatus(), ""))
}

// GetStatus returns the current daemon status
func (s *DaemonServer) GetStatus() DaemonStatus {
	return s.getStatus()
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

	localIP := discovery.GetPrimaryLocalIP()
	if localIP == "" || localIP == "127.0.0.1" {
		targets := discovery.GetActiveNetworkTargets()
		for _, t := range targets {
			if !t.LocalIP.IsLoopback() && t.LocalIP.To4() != nil && !strings.HasPrefix(t.LocalIP.String(), "169.254.") {
				localIP = t.LocalIP.String()
				break
			}
		}
	}
	port := s.httpPort
	if port <= 0 {
		port = 19999
	}
	portalURL := ""
	portalQR := ""
	if localIP != "" {
		if s.webShareToken != "" && s.webSharePIN != "" {
			portalURL = fmt.Sprintf("http://%s:%d/share?token=%s&pin=%s", localIP, port, s.webShareToken, s.webSharePIN)
		} else if s.webSharePIN != "" {
			portalURL = fmt.Sprintf("http://%s:%d/share?pin=%s", localIP, port, s.webSharePIN)
		} else {
			portalURL = fmt.Sprintf("http://%s:%d/share", localIP, port)
		}
		portalQR, _ = GenerateURLQRDataURI(portalURL, 220)
	}

	return DaemonStatus{
		Status:             status,
		DeviceName:         s.config.DeviceName,
		DownloadDir:        s.config.DownloadDir,
		CollisionPolicy:    s.config.CollisionPolicy,
		Paired:             paired,
		PairedIP:           pairedIP,
		PairedDevice:       pairedDevice,
		ActiveTransfer:     s.transferCancel != nil || s.itemCancel != nil,
		IsPaused:           s.isPaused,
		Version:            "1.0.0",
		LocalIP:            localIP,
		LocalPort:          port,
		PortalURL:          portalURL,
		PortalQR:           portalQR,
		WebShareEnabled:    s.webShareEnabled,
		WebSharePIN:        s.webSharePIN,
		WebShareToken:      s.webShareToken,
		WebShareAutoAccept: s.webShareAutoAccept,
		PairingCode:        s.pairingCode,
	}
}

// ControlToken returns the secret required on control routes. Never log it.
func (s *DaemonServer) ControlToken() string { return s.controlToken }

// DisableControlSurface turns the server into a Web Share portal only (xfer share):
// no dashboard, /ws, /status or /api/* control routes. Call before Serve.
func (s *DaemonServer) DisableControlSurface() { s.controlDisabled = true }

// control guards a control route (API-04). In order: loopback peer, Host header naming this
// listener (DNS rebinding), Origin if present (cross-site WebSocket hijacking), then the
// control token, compared in constant time.
func (s *DaemonServer) control(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackRequest(r) || !s.hostAllowed(r.Host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !s.originAllowed(o) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !secureEq(extractControlToken(r), s.controlToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// hostAllowed accepts 127.0.0.1, localhost or [::1] with this listener's port.
func (s *DaemonServer) hostAllowed(h string) bool {
	host, port, err := net.SplitHostPort(h)
	if err != nil || port != strconv.Itoa(s.currentHTTPPort()) {
		return false
	}
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// originAllowed accepts only the dashboard's own origin.
func (s *DaemonServer) originAllowed(o string) bool {
	u, err := url.Parse(o)
	return err == nil && u.Scheme == "http" && u.Path == "" && s.hostAllowed(u.Host)
}

// extractControlToken reads "Authorization: Bearer <token>" (native clients) or the WebSocket
// subprotocol "token.<token>" (browsers cannot set headers on a WebSocket). Never the URL.
func extractControlToken(r *http.Request) string {
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
		return strings.TrimPrefix(a, "Bearer ")
	}
	for _, p := range websocket.Subprotocols(r) {
		if strings.HasPrefix(p, "token.") {
			return strings.TrimPrefix(p, "token.")
		}
	}
	return ""
}

// currentHTTPPort reads the HTTP port set by Listen/Serve. Callers must not hold s.mu.
func (s *DaemonServer) currentHTTPPort() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.httpPort
}

// GetNodeAddr returns the network address the daemon's node listener is bound to
func (s *DaemonServer) GetNodeAddr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.nodeLn != nil {
		return s.nodeLn.Addr().String()
	}
	return "127.0.0.1:18887"
}

func (s *DaemonServer) listenForIncomingPairings(ln net.Listener) {
	srvTLSConfig, _ := session.ServerTLSConfig()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		engine.TuneConn(conn)

		if srvTLSConfig != nil {
			if upConn, _, err := session.UpgradeToTLSIfClientHello(conn, srvTLSConfig); err == nil {
				conn = upConn
			}
		}

		ch := session.NewChannel(conn)

		// Read incoming authentication / handshake message with 5s deadline
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		msg, err := ch.Read()
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil {
			ch.Close()
			continue
		}

		s.mu.RLock()
		expectedCode := s.pairingCode
		devName := s.config.DeviceName
		s.mu.RUnlock()

		if msg.Type == "pair_request" {
			normExpected, _ := discovery.NormalizePairingCode(expectedCode)
			normProvided, _ := discovery.NormalizePairingCode(msg.PairingCode)
			if normExpected == "" || normProvided == "" || normExpected != normProvided {
				_ = ch.Send(session.Message{Type: "pair_reject", FileName: "invalid pairing code"})
				ch.Close()
				continue
			}
			_ = ch.Send(session.Message{
				Type:       "pair_accept",
				DeviceName: devName,
			})
		} else {
			_ = ch.Send(session.Message{Type: "pair_reject", FileName: "pairing code required"})
			ch.Close()
			continue
		}

		peerName := msg.DeviceName
		if peerName == "" {
			peerName = ch.RemoteIP()
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
		s.pairedDeviceName = peerName
		remoteIP := ch.RemoteIP()
		s.mu.Unlock()

		s.Broadcast(NewEvent("paired", map[string]string{
			"ip":          remoteIP,
			"device_name": peerName,
		}))

		go s.listenToSession(ch)
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
	case "pair_request":
		s.mu.Lock()
		s.pairedDeviceName = msg.DeviceName
		devName := s.config.DeviceName
		sess := s.activeSession
		expectedCode := s.pairingCode
		s.mu.Unlock()

		if sess != nil {
			normExpected, _ := discovery.NormalizePairingCode(expectedCode)
			normProvided, _ := discovery.NormalizePairingCode(msg.PairingCode)
			if normExpected == "" || normProvided == "" || normExpected != normProvided {
				_ = sess.Send(session.Message{Type: "pair_reject", FileName: "invalid pairing code"})
				sess.Close()
				s.mu.Lock()
				if s.activeSession == sess {
					s.activeSession = nil
				}
				s.mu.Unlock()
				return
			}
			_ = sess.Send(session.Message{
				Type:       "pair_accept",
				DeviceName: devName,
			})
			s.Broadcast(NewEvent("paired", map[string]string{
				"ip":          sess.RemoteIP(),
				"device_name": msg.DeviceName,
			}))
		}

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

	case "pair_accept":
		s.mu.Lock()
		if msg.DeviceName != "" {
			s.pairedDeviceName = msg.DeviceName
		}
		sess := s.activeSession
		s.mu.Unlock()

		if sess != nil {
			s.Broadcast(NewEvent("paired", map[string]string{
				"ip":          sess.RemoteIP(),
				"device_name": s.pairedDeviceName,
			}))
		}

	case "pair_reject":
		s.mu.Lock()
		if s.activeSession != nil {
			s.activeSession.Close()
			s.activeSession = nil
		}
		s.pairedDeviceName = ""
		s.mu.Unlock()
		reason := "pairing rejected by peer"
		if msg.FileName != "" {
			reason = msg.FileName
		}
		s.Broadcast(NewEvent("disconnected", map[string]string{"reason": reason}))

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

	case "bench_ping":
		s.mu.RLock()
		sess := s.activeSession
		s.mu.RUnlock()
		if sess != nil {
			_ = sess.Send(session.Message{
				Type:        "bench_pong",
				BenchPingTS: msg.BenchPingTS,
			})
		}

	case "bench_pong":
		select {
		case s.benchPongChan <- msg:
		default:
		}

	case "bench_burst_req":
		s.mu.RLock()
		sess := s.activeSession
		s.mu.RUnlock()
		if sess != nil {
			benchSize := msg.BenchSize
			if benchSize <= 0 {
				benchSize = 8 * 1024 * 1024
			}
			port, waitFn, err := engine.RunNetworkBurstServer(0, benchSize, 4*time.Second)
			if err == nil {
				_ = sess.Send(session.Message{
					Type:      "bench_burst_ready",
					DataPort:  port,
					BenchSize: benchSize,
				})
				go func() {
					speed, _ := waitFn()
					s.mu.RLock()
					sCur := s.activeSession
					s.mu.RUnlock()
					if sCur != nil {
						_ = sCur.Send(session.Message{
							Type:      "bench_burst_ack",
							FileSize:  int64(speed * 100), // Speed encoded in hundredths of MB/s
							BenchSize: benchSize,
						})
					}
				}()
			}
		}

	case "bench_burst_ready", "bench_burst_ack":
		select {
		case s.benchBurstAckChan <- msg:
		default:
		}

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
					engine.TuneConn(conn)

					srvTLS, _ := session.ServerTLSConfig()
					if srvTLS != nil {
						if upConn, _, err := session.UpgradeToTLSIfClientHello(conn, srvTLS); err == nil {
							conn = upConn
						}
					}

					listener := newDaemonListener(s, m.RootName, m.TotalBytes, 0, 1, 0, m.TotalBytes)
					_ = engine.StreamTar(streamCtx, conn, m, listener)

					if s.activeSession != nil {
						s.activeSession.Send(session.Message{Type: "batch_complete"})
					}
					s.broadcastTransferComplete(map[string]interface{}{
						"message": "Folder stream transfer complete",
					})
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
				s.activePort++
				port := s.activePort
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

				readyChan := make(chan error, 1)
				go func(fPath, rPath string, p int, bBase int64) {
					sender := engine.NewSender(s.config.Workers, uint32(s.config.ChunkSizeMB*1024*1024))
					listener := newDaemonListener(s, rPath, item.Size, idx, m.TotalFiles, bBase, m.TotalBytes)
					bindAddr := fmt.Sprintf("0.0.0.0:%d", p)
					_ = sender.ServeAndSendWithRelPathReady(itemCtx, bindAddr, fPath, rPath, listener, 0, readyChan)
				}(item.FullPath, item.RelPath, port, baseBytes)

				var bindErr error
				select {
				case <-itemCtx.Done():
					return
				case bindErr = <-readyChan:
				case <-time.After(5 * time.Second):
					bindErr = fmt.Errorf("sender port bind timeout after 5s")
				}

				if bindErr != nil {
					itemCancel()
					s.mu.Lock()
					for i := range s.currentBatchItems {
						if s.currentBatchItems[i].Index == idx {
							s.currentBatchItems[i].Status = "failed"
						}
					}
					failedItems := s.currentBatchItems
					s.mu.Unlock()

					s.Broadcast(NewEvent("item_failed", map[string]interface{}{
						"item_index": idx,
						"items":      failedItems,
						"error":      bindErr.Error(),
					}))
					continue
				}

				if s.activeSession != nil {
					s.activeSession.Send(session.Message{
						Type:      "batch_item",
						FileName:  item.RelPath,
						FileSize:  item.Size,
						FileID:    item.FileID,
						DataPort:  port,
						ItemIndex: idx,
						ChunkSize: uint32(s.config.ChunkSizeMB * 1024 * 1024),
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
				case success := <-s.itemDoneChan:
					itemCancel()
					if success {
						s.mu.Lock()
						for i := range s.currentBatchItems {
							if s.currentBatchItems[i].Index == idx && s.currentBatchItems[i].Status != "skipped" && s.currentBatchItems[i].Status != "paused" && s.currentBatchItems[i].Status != "failed" {
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
				case <-time.After(60 * time.Second):
					// Watchdog: If an item transfer is completely silent for 60s without progress/complete, advance safely
					itemCancel()
					s.mu.Lock()
					for i := range s.currentBatchItems {
						if s.currentBatchItems[i].Index == idx {
							s.currentBatchItems[i].Status = "failed"
						}
					}
					failedItems := s.currentBatchItems
					s.mu.Unlock()

					s.Broadcast(NewEvent("item_failed", map[string]interface{}{
						"item_index": idx,
						"items":      failedItems,
						"error":      "item transfer timed out after 60s with no response from peer",
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
			s.broadcastTransferComplete(map[string]interface{}{
				"message": "All batch files sent successfully",
				"items":   finalItems,
			})
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
			"total_files":  1,
			"total_bytes":  msg.FileSize,
			"is_stream":    true,
			"is_folder":    true,
		}))

		go func(msg session.Message) {
			defer streamCancel()
			targetAddr := net.JoinHostPort(s.activeSession.RemoteIP(), strconv.Itoa(int(msg.DataPort)))
			conn, err := session.DialTLSPeer(targetAddr)
			if err != nil {
				s.Broadcast(NewEvent("action_error", map[string]string{"error": err.Error()}))
				return
			}
			defer conn.Close()

			listener := newDaemonListener(s, msg.FileName, msg.FileSize, 0, 1, 0, msg.FileSize)
			err = engine.ExtractTar(streamCtx, conn, outDir, msg.FileSize, msg.ItemIndex, listener)
			if err == nil {
				s.broadcastTransferComplete(map[string]interface{}{
					"message": "Directory stream transfer complete",
				})
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
		if s.batchCanceled {
			s.mu.Unlock()
			return
		}
		if s.skippedFiles[msg.ItemIndex] {
			s.mu.Unlock()
			if s.activeSession != nil {
				s.activeSession.Send(session.Message{Type: "item_skipped", ItemIndex: msg.ItemIndex})
			}
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
			targetAddr := net.JoinHostPort(s.activeSession.RemoteIP(), strconv.Itoa(int(msg.DataPort)))

			var err error
			if msg.ChunkSize > 0 {
				meta := protocol.FileMetadata{
					FileName:  msg.FileName,
					FileSize:  msg.FileSize,
					FileID:    msg.FileID,
					ChunkSize: msg.ChunkSize,
				}
				err = receiver.PullWithMetadata(ctx, targetAddr, listener, meta, msg.FileID)
			} else {
				err = receiver.Pull(ctx, targetAddr, listener, msg.FileID)
			}
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
				s.mu.Lock()
				for i := range s.currentBatchItems {
					if s.currentBatchItems[i].Index == msg.ItemIndex {
						s.currentBatchItems[i].Status = "failed"
					}
				}
				failedItems := s.currentBatchItems
				s.mu.Unlock()

				s.Broadcast(NewEvent("transfer_error", map[string]string{
					"file":  msg.FileName,
					"error": err.Error(),
				}))
				s.Broadcast(NewEvent("item_failed", map[string]interface{}{
					"item_index": msg.ItemIndex,
					"items":      failedItems,
					"error":      err.Error(),
				}))

				if s.activeSession != nil {
					s.activeSession.Send(session.Message{
						Type:      "item_failed",
						ItemIndex: msg.ItemIndex,
						FileName:  msg.FileName,
						Error:     err.Error(),
					})
				}
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

	case "item_failed":
		s.mu.Lock()
		for i := range s.currentBatchItems {
			if s.currentBatchItems[i].Index == msg.ItemIndex {
				s.currentBatchItems[i].Status = "failed"
			}
		}
		failedItems := s.currentBatchItems
		s.mu.Unlock()

		s.Broadcast(NewEvent("item_failed", map[string]interface{}{
			"item_index": msg.ItemIndex,
			"items":      failedItems,
			"error":      msg.Error,
		}))

		select {
		case s.itemDoneChan <- false:
		default:
		}

	case "item_skipped":
		s.mu.Lock()
		for i := range s.currentBatchItems {
			if s.currentBatchItems[i].Index == msg.ItemIndex {
				s.currentBatchItems[i].Status = "skipped"
			}
		}
		skippedItems := s.currentBatchItems
		s.mu.Unlock()

		s.Broadcast(NewEvent("file_skipped", map[string]interface{}{
			"item_index": msg.ItemIndex,
			"items":      skippedItems,
		}))

		select {
		case s.itemDoneChan <- false:
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
		s.broadcastTransferComplete(map[string]interface{}{
			"message": "All batch files mirrored successfully",
			"items":   finalItems,
		})

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
		s.broadcastTransferComplete(map[string]interface{}{
			"message": "Receiver confirmed transfer complete",
		})
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
	if !isLoopbackRequest(r) {
		http.Error(w, "Forbidden: Native file dialog is restricted to localhost", http.StatusForbidden)
		return
	}

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
	if !isLoopbackRequest(r) {
		http.Error(w, "Forbidden: Staging upload is restricted to localhost", http.StatusForbidden)
		return
	}

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

func isSystemMount(p string) bool {
	p = filepath.ToSlash(filepath.Clean(p))
	if p == "/" {
		return false
	}
	systemPrefixes := []string{
		"/apex", "/bootstrap-apex", "/data", "/dev", "/proc", "/sys",
		"/system", "/vendor", "/product", "/omr", "/efs", "/cache",
		"/etc", "/bin", "/sbin", "/lib", "/lib64", "/usr", "/var", "/tmp",
		"/run/user", "/run/lock", "/run/systemd", "/snap",
		"/mnt/androidwritable", "/mnt/appfuse", "/mnt/asec", "/mnt/installer",
		"/mnt/knox", "/mnt/obb", "/mnt/runtime", "/mnt/secure", "/mnt/shell", "/mnt/user",
	}
	for _, sp := range systemPrefixes {
		if p == sp || strings.HasPrefix(p, sp+"/") {
			return true
		}
	}
	return false
}

func getLinuxMounts() []FSQuickDir {
	var mounts []FSQuickDir
	seenPaths := make(map[string]bool)

	addMount := func(name, path string) {
		path = filepath.Clean(path)
		if seenPaths[path] || isSystemMount(path) {
			return
		}
		if fi, err := os.Stat(path); err == nil && fi.IsDir() {
			seenPaths[path] = true
			mounts = append(mounts, FSQuickDir{Name: name, Path: path})
		}
	}

	// 1. Inspect /proc/mounts and /etc/mtab for active storage mounts
	mountFiles := []string{"/proc/mounts", "/etc/mtab"}
	for _, mf := range mountFiles {
		data, err := os.ReadFile(mf)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 3 {
				continue
			}
			device := fields[0]
			mountPoint := fields[1]
			fstype := fields[2]

			if isSystemMount(mountPoint) {
				continue
			}

			isBlockDev := strings.HasPrefix(device, "/dev/sd") ||
				strings.HasPrefix(device, "/dev/nvme") ||
				strings.HasPrefix(device, "/dev/mmcblk") ||
				strings.HasPrefix(device, "/dev/vd") ||
				strings.HasPrefix(device, "/dev/mapper/") ||
				strings.HasPrefix(device, "/dev/disk/") ||
				strings.HasPrefix(device, "/dev/block/") ||
				device == "fuseblk" || strings.HasPrefix(fstype, "fuse")

			isMediaMount := strings.HasPrefix(mountPoint, "/media/") ||
				strings.HasPrefix(mountPoint, "/run/media/") ||
				strings.HasPrefix(mountPoint, "/mnt/") ||
				(strings.HasPrefix(mountPoint, "/storage/") && !strings.HasPrefix(mountPoint, "/storage/emulated"))

			isStorageFS := fstype == "ext4" || fstype == "ext3" || fstype == "ext2" ||
				fstype == "vfat" || fstype == "fat" || fstype == "exfat" ||
				fstype == "ntfs" || fstype == "ntfs3" || fstype == "fuseblk" ||
				fstype == "btrfs" || fstype == "xfs" || fstype == "f2fs" ||
				fstype == "iso9660" || fstype == "udf" || fstype == "cifs" || fstype == "nfs"

			if (isMediaMount || isBlockDev) && isStorageFS {
				baseName := filepath.Base(mountPoint)
				label := fmt.Sprintf("💾 %s", baseName)
				if strings.HasPrefix(mountPoint, "/media/") || strings.HasPrefix(mountPoint, "/run/media/") {
					label = fmt.Sprintf("🔌 %s", baseName)
				} else if strings.HasPrefix(mountPoint, "/mnt/") {
					label = fmt.Sprintf("💾 %s (mnt)", baseName)
				}
				addMount(label, mountPoint)
			}
		}
	}

	// 2. Direct directory scan of /media, /run/media, and /mnt
	scanDir := func(baseDir string, icon string) {
		entries, err := os.ReadDir(baseDir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(baseDir, e.Name())
			if isSystemMount(p) {
				continue
			}

			// If it's a user directory (like /media/username or /run/media/username), inspect its subdirectories
			subEntries, subErr := os.ReadDir(p)
			if subErr == nil && len(subEntries) > 0 {
				hasSubDirs := false
				for _, se := range subEntries {
					if se.IsDir() && !strings.HasPrefix(se.Name(), ".") {
						hasSubDirs = true
						subPath := filepath.Join(p, se.Name())
						addMount(fmt.Sprintf("%s %s", icon, se.Name()), subPath)
					}
				}
				if hasSubDirs {
					continue
				}
			}

			addMount(fmt.Sprintf("%s %s", icon, e.Name()), p)
		}
	}

	scanDir("/media", "🔌")
	scanDir("/run/media", "🔌")
	if user := os.Getenv("USER"); user != "" {
		scanDir(filepath.Join("/media", user), "🔌")
		scanDir(filepath.Join("/run/media", user), "🔌")
	}
	scanDir("/mnt", "💾")

	// 3. Android external SD card or USB OTG storage (/storage/XXXX-XXXX)
	if entries, err := os.ReadDir("/storage"); err == nil {
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() && name != "self" && name != "emulated" && !strings.HasPrefix(name, ".") {
				p := filepath.Join("/storage", name)
				addMount(fmt.Sprintf("💾 SD/USB: %s", name), p)
			}
		}
	}

	// 4. Always add Root (/) for easy navigation on Unix
	addMount("🖥️ Racine (/)", "/")

	return mounts
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

	// 4. Linux & Android External Drives and Mounted Partitions
	if runtime.GOOS == "linux" || runtime.GOOS == "android" {
		quick = append(quick, getLinuxMounts()...)
	}

	return quick
}

func (s *DaemonServer) handleFSList(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		http.Error(w, "Forbidden: Filesystem browsing is restricted to localhost", http.StatusForbidden)
		return
	}

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
	if !isLoopbackRequest(r) {
		http.Error(w, "Forbidden: Directory creation is restricted to localhost", http.StatusForbidden)
		return
	}

	if r.Method != http.MethodPost { // no side effect on GET (API-04, control 6)
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

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

// StartTransferTracker initializes an active telemetry tracker
func (s *DaemonServer) StartTransferTracker(name string, totalFiles int, totalBytes int64) *TransferSessionTracker {
	s.trackerMu.Lock()
	defer s.trackerMu.Unlock()
	tracker := NewTransferSessionTracker(name, totalFiles, totalBytes)
	s.activeTracker = tracker
	return tracker
}

// RecordTransferSample records a live progress measurement
func (s *DaemonServer) RecordTransferSample(stats engine.TransferStats, progressPercent float64, fallbackName string, totalFiles int, totalBytes int64) {
	s.trackerMu.Lock()
	if s.activeTracker == nil {
		s.activeTracker = NewTransferSessionTracker(fallbackName, totalFiles, totalBytes)
	}
	tracker := s.activeTracker
	s.trackerMu.Unlock()

	tracker.RecordSample(stats, progressPercent)
}

// FinishTransferTracker compiles the final diagnostic summary report
func (s *DaemonServer) FinishTransferTracker() *TransferSummaryReport {
	s.trackerMu.Lock()
	defer s.trackerMu.Unlock()
	if s.activeTracker == nil {
		return s.lastSummaryReport
	}
	report := s.activeTracker.GenerateReport()
	s.lastSummaryReport = report
	s.activeTracker = nil
	return report
}

// GetLastSummaryReport returns the latest completed transfer report
func (s *DaemonServer) GetLastSummaryReport() *TransferSummaryReport {
	s.trackerMu.Lock()
	defer s.trackerMu.Unlock()
	return s.lastSummaryReport
}

// broadcastTransferComplete completes telemetry tracker and broadcasts transfer_complete with report
func (s *DaemonServer) broadcastTransferComplete(extra map[string]interface{}) {
	rep := s.FinishTransferTracker()
	payload := make(map[string]interface{})
	for k, v := range extra {
		payload[k] = v
	}
	if rep != nil {
		payload["summary_report"] = rep
		payload["formatted_report"] = rep.FormattedReport
	}
	s.Broadcast(NewEvent("transfer_complete", payload))
}
