package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/manifest"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/store"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

// IPAuthRecord tracks failed PIN authentication attempts for brute-force defense
type IPAuthRecord struct {
	FailCount   int
	LockedUntil time.Time
	LastFail    time.Time
}

// DaemonServer manages the headless background engine and WebSocket connections
type DaemonServer struct {
	mu                 sync.RWMutex
	config             Config
	activeSession      *session.Channel
	outEvents          *daemonEvents // the transfer this device sends, while it runs
	pairedDeviceName   string
	pendingOffer       *session.Message // a complete offer waiting for respond_offer
	incomingOffer      *session.Message // an offer whose pages are still arriving (D2)
	lastOffered        *outgoingOffer   // what this device offered, until the peer answers
	webSharedManifest  *manifest.Manifest
	webSharedFile      string
	webShareEnabled    bool
	webSharePIN        string
	webShareToken      string
	webShareAutoAccept bool
	pendingWebUploads  map[string]*WebUploadRequest
	transferCancel     context.CancelFunc // set while a transfer is active (LIM-08)
	activeTransferID   string
	endedTransferID    string             // the transfer endTransfer just ended: its peer's "complete" may come after (#129)
	activeReceiver     *transfer.Receiver // this side's scheduler, when it receives
	isPaused           bool
	currentBatchItems  []BatchFileInfo // the queue the dashboard shows
	sessionGraceTimer  *time.Timer
	// sentCompletedID is the last transfer this device sent that was completed (completeSent).
	sentCompletedID string
	// completedTransferID is the last transfer this device received whole (answerCheck).
	completedTransferID string
	isReconnecting      bool
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

	ctx     context.Context
	cancel  context.CancelFunc
	httpSrv *http.Server
	nodeLn  net.Listener

	// dataDir holds config.json, the identity, the trusted devices and the reports. It is set
	// at construction: two daemons in one process (pkg/core) keep apart (D2 of the J6a plan).
	dataDir   string
	startOnce sync.Once
	startErr  error

	// Subscribers to Broadcast (pkg/core's event queue, D3 of the J6a plan).
	sinkMu   sync.RWMutex
	sinks    map[int]func(EventMessage)
	nextSink int

	// This device's identity and trusted devices (WP-3.1), loaded by Serve.
	identity    *identity.Identity
	trust       *identity.TrustStore
	gate        *securechan.Gate // limits unauthenticated control connections (LIM-01)
	hub         *transfer.Hub    // the device's single port: control and data (CHAN-01)
	hubControl  *transfer.Listener
	discSrv     *discovery.DiscoveryServer
	discCancel  context.CancelFunc // set while this device announces itself (DISC-05)
	pairingCode string             // finds this device on the network; gives no trust (WP-3.6b)

	// Pairing and trust (trust.go, pin.go): the QR code shown by pair_show, the PIN shown by
	// pair_pin_show, the PINs drawn on demand (PAIR-11), and the trust negotiation of the
	// active session (PAIR-10). trust: offer mutual trust once paired.
	invite        *pairing.Invitation
	inviteTrust   bool
	pin           *pairing.PIN
	pinTrust      bool
	pinDemand     *pairing.PINOnDemand
	pinDemandFrom string               // the address of the last device that asked for a PIN
	trustTalk     *pairing.TrustTalk   // of the active session
	trustPeer     identity.Fingerprint // the key of the active session's device
	trustOffered  time.Time            // our trust_offer waiting for an answer

	benchPongChan     chan session.Message
	benchBurstAckChan chan session.Message

	trackerMu         sync.Mutex
	activeTracker     *TransferSessionTracker
	lastSummaryReport *TransferSummaryReport
}

// NewDaemonServer initializes a daemon with default configurations
func NewDaemonServer(port int, defaultOutDir, deviceName string) *DaemonServer {
	return NewDaemonServerAt(filepath.Dir(GetConfigFilePath()), port, defaultOutDir, deviceName)
}

// NewDaemonServerAt is NewDaemonServer with its own data folder (config.json, identity,
// trusted devices, reports) instead of the process-wide one (B6, D2 of the J6a plan).
func NewDaemonServerAt(dataDir string, port int, defaultOutDir, deviceName string) *DaemonServer {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := LoadConfigFrom(filepath.Join(dataDir, "config.json"), defaultOutDir, deviceName)

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

	pCode, _, _ := discovery.GeneratePairingCode("")
	registerSecrets(pin, token, pCode)

	srv := &DaemonServer{
		gate:               securechan.NewGate(),
		config:             cfg,
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
		dataDir:            dataDir,
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
	registerSecrets(pin, token, "")
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

	if err := s.StartNode(); err != nil {
		return err
	}
	return s.serveHTTP(httpLn)
}

// StartNode starts what makes this device a medXfer node, without any HTTP or WebSocket: its
// identity and trusted devices, the single port (pairing and data, CHAN-01) and discovery.
// Serve calls it; pkg/core calls it alone (API-01). Only the first call does anything.
func (s *DaemonServer) StartNode() error {
	s.startOnce.Do(func() { s.startErr = s.startNode() })
	return s.startErr
}

func (s *DaemonServer) startNode() error {
	if err := s.loadIdentity(); err != nil {
		return err
	}

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
		s.startHub(ln)
	}

	// 2. Start Discovery Broadcast Server
	discOffer := &discovery.TransferOffer{} // no name: DISC-06
	disc := discovery.NewDiscoveryServer("node", nodePort, discOffer)
	if s.pairingCode != "" {
		disc.SetPairingCode(s.pairingCode)
	}
	disc.SetTags(s.announceTags) // devices of mutual trust recognize this one (DISC-11)
	s.mu.Lock()
	s.discSrv = disc // silent until SetDiscoverable (DISC-05)
	s.mu.Unlock()
	return nil
}

// serveHTTP serves the Web Share portal and the health probe.
func (s *DaemonServer) serveHTTP(httpLn net.Listener) error {
	s.SetDiscoverable(true) // the dashboard's daemon is up for discovery while it serves
	// 3. Start HTTP/WebSocket Router
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/share", shareHeaders(s.handleSharePortal, true))
	mux.HandleFunc("/api/share/list", shareHeaders(s.handleShareList, true))
	mux.HandleFunc("/api/share/download", shareHeaders(s.handleShareDownload, false))
	mux.HandleFunc("/api/share/request_upload", shareHeaders(s.handleShareRequestUpload, false))
	mux.HandleFunc("/api/share/upload", shareHeaders(s.handleShareUpload, false))
	mux.HandleFunc("/api/share/upload_chunk", shareHeaders(s.handleShareUploadChunk, false))
	mux.HandleFunc("/api/share/pause", shareHeaders(s.handleSharePause, false))
	mux.HandleFunc("/api/share/resume", shareHeaders(s.handleShareResume, false))
	mux.HandleFunc("/api/share/cancel", shareHeaders(s.handleShareCancel, false))

	httpSrv := newHTTPServer(mux)
	s.mu.Lock()
	s.httpSrv = httpSrv
	s.mu.Unlock()

	log.Printf("[*] medXfer Headless Daemon started on http://%s", httpLn.Addr().String())
	return httpSrv.Serve(httpLn)
}

// newHTTPServer is the server of the dashboard and the portal. The headers of a request and an
// idle connection are bounded: a slow client holds nothing open (§3.7, #156). Reads and writes
// are not, for the WebSocket and the uploads; the portal's own limits come with J7.
func newHTTPServer(h http.Handler) *http.Server {
	return &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
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
	if s.sessionGraceTimer != nil { // it would fire on a stopped server (§3.7, #156)
		s.sessionGraceTimer.Stop()
		s.sessionGraceTimer = nil
	}
	s.isReconnecting = false
	nodeLn, httpSrv, control := s.nodeLn, s.httpSrv, s.hubControl
	s.mu.Unlock()
	if control != nil {
		control.Close()
	}

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
	// The web dashboard was removed at gate G7 (DEV-19-R): "/" leads to the Web Share portal.
	redirectURL := "/share"
	if r.URL.RawQuery != "" {
		redirectURL += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
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

// Broadcast sends a JSON event to all active Flutter clients
func (s *DaemonServer) Broadcast(evt EventMessage) {
	journalEvent(evt)
	s.sinkMu.RLock()
	for _, fn := range s.sinks {
		fn(evt) // must not block (pkg/core: a bounded queue that drops, B11)
	}
	s.sinkMu.RUnlock()
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
	registerSecrets(pin, "", "")
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

// statusAddress is the address status shows: it may enumerate the interfaces and read the
// ARP table (slow). Tests replace it.
var statusAddress = func() string {
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
	return localIP
}

func (s *DaemonServer) getStatus() DaemonStatus {
	// What the server holds is read under its lock; the slow part (interfaces, ARP table, QR
	// code) runs outside it, or every other action would wait meanwhile (audit 9, #156).
	s.mu.RLock()
	st := DaemonStatus{
		Status:             "idle",
		DeviceName:         s.config.DeviceName,
		DownloadDir:        s.config.DownloadDir,
		CollisionPolicy:    s.config.CollisionPolicy,
		Paired:             s.activeSession != nil,
		ActiveTransfer:     s.transferCancel != nil,
		IsPaused:           s.isPaused,
		Version:            "1.0.0",
		LocalPort:          s.httpPort,
		WebShareEnabled:    s.webShareEnabled,
		WebSharePIN:        s.webSharePIN,
		WebShareToken:      s.webShareToken,
		WebShareAutoAccept: s.webShareAutoAccept,
		PairingCode:        s.pairingCode,
		Discoverable:       s.discCancel != nil,
	}
	if st.Paired {
		st.Status = "paired"
		st.PairedIP = s.activeSession.RemoteIP()
		st.PairedDevice = s.pairedDeviceName
	}
	if st.ActiveTransfer {
		st.Status = "transferring"
		if st.IsPaused {
			st.Status = "paused"
		}
	}
	s.mu.RUnlock()

	if st.LocalPort <= 0 {
		st.LocalPort = 19999
	}
	st.LocalIP = statusAddress()
	if st.LocalIP != "" {
		switch {
		case st.WebShareToken != "" && st.WebSharePIN != "":
			st.PortalURL = fmt.Sprintf("http://%s:%d/share?token=%s&pin=%s", st.LocalIP, st.LocalPort, st.WebShareToken, st.WebSharePIN)
		case st.WebSharePIN != "":
			st.PortalURL = fmt.Sprintf("http://%s:%d/share?pin=%s", st.LocalIP, st.LocalPort, st.WebSharePIN)
		default:
			st.PortalURL = fmt.Sprintf("http://%s:%d/share", st.LocalIP, st.LocalPort)
		}
		st.PortalQR, _ = GenerateURLQRDataURI(st.PortalURL, 220)
	}
	st.Instance = s.instance()
	return st
}

// hostAllowed accepts 127.0.0.1, localhost or [::1] with this listener's port.
func (s *DaemonServer) hostAllowed(h string) bool {
	host, port, err := net.SplitHostPort(h)
	if err != nil || port != strconv.Itoa(s.currentHTTPPort()) {
		return false
	}
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
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

// NodeListenAddr returns the address of the peer (pairing) listener, or "" until Serve has
// opened it. Unlike GetNodeAddr it never falls back to the default port.
func (s *DaemonServer) NodeListenAddr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.nodeLn != nil {
		return s.nodeLn.Addr().String()
	}
	return ""
}

// PairingCode returns the code a peer must present to pair with this daemon.
func (s *DaemonServer) PairingCode() string { return s.GetStatus().PairingCode }

// Subscribe calls fn with every broadcast event, from then on, until cancel. fn must not block.
func (s *DaemonServer) Subscribe(fn func(EventMessage)) (cancel func()) {
	s.sinkMu.Lock()
	if s.sinks == nil {
		s.sinks = map[int]func(EventMessage){}
	}
	id := s.nextSink
	s.nextSink++
	s.sinks[id] = fn
	s.sinkMu.Unlock()
	return func() {
		s.sinkMu.Lock()
		delete(s.sinks, id)
		s.sinkMu.Unlock()
	}
}

// Dispatch runs one request: its replies go to rs, its other effects to Broadcast. It is the
// entry point of every transport (the dashboard's WebSocket, pkg/core).
func (s *DaemonServer) Dispatch(rs Responder, req RequestMessage) { s.dispatch(rs, req) }

// DataDir is the folder of this daemon's configuration, identity and reports.
func (s *DaemonServer) DataDir() string { return s.dataDir }

// loadIdentity reads, or creates on first use, this device's identity and its trusted devices,
// next to config.json (WP-3.1).
func (s *DaemonServer) loadIdentity() error {
	dir := s.dataDir
	id, err := identity.LoadOrCreate(dir)
	if err != nil {
		return fmt.Errorf("device identity: %w", err)
	}
	trust, err := identity.OpenTrustStore(filepath.Join(dir, identity.TrustFile))
	if err != nil {
		return fmt.Errorf("trusted devices: %w", err)
	}
	s.mu.Lock()
	s.identity, s.trust = id, trust
	s.mu.Unlock()
	return nil
}

// pairingChanConfig is the secure channel of a control connection. An unknown key is
// accepted in state UNAUTH; the pairing check decides whether it becomes AUTH.
func (s *DaemonServer) pairingChanConfig() securechan.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return securechan.Config{Identity: s.identity, Trust: s.trust, AllowUnknown: true, Gate: s.gate}
}

// streamChanConfig pins a data connection to the key of the paired device: any other key,
// or no paired device, is refused.
func (s *DaemonServer) streamChanConfig() securechan.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var peer identity.Fingerprint
	if s.activeSession != nil {
		peer = s.activeSession.PeerFingerprint()
	}
	return securechan.Config{Identity: s.identity, Expected: peer}
}

// devicePort is the port of this device's single listener: control and data (CHAN-01).
func (s *DaemonServer) devicePort() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.nodeLn != nil {
		if a, ok := s.nodeLn.Addr().(*net.TCPAddr); ok {
			return a.Port
		}
	}
	return 18887
}

// registerTransfer makes a new transfer joinable on the device's port by the session's peer,
// and returns its id and the listener of its data connections. Closing the listener (or the
// end of Sender.Serve) unregisters it.
func (s *DaemonServer) registerTransfer(sess *session.Channel) (transfer.ID, *transfer.Listener, error) {
	s.mu.RLock()
	hub := s.hub
	s.mu.RUnlock()
	if hub == nil {
		return transfer.ID{}, nil, errors.New("the device listener is not started")
	}
	peer := sess.PeerFingerprint()
	if peer.IsZero() {
		return transfer.ID{}, nil, errors.New("the session has no verified peer key")
	}
	id, err := transfer.NewID()
	if err != nil {
		return transfer.ID{}, nil, err
	}
	l, err := hub.Register(id, peer)
	return id, l, err
}

// startHub serves the device's single port (CHAN-01): pairing and session connections go to
// acceptPairing, data connections to the transfers this daemon has registered.
func (s *DaemonServer) startHub(ln net.Listener) {
	hub := transfer.NewHub(s.pairingChanConfig())
	hub.Wrap = engine.WrapAccepted // diag builds inject their faults on the network bytes
	control := hub.Control()
	s.mu.Lock()
	s.hub, s.hubControl = hub, control
	s.mu.Unlock()
	go func() { _ = hub.Serve(ln) }()
	go func() {
		for {
			c, err := control.Accept()
			if err != nil {
				return
			}
			go s.acceptPairing(c.(*securechan.Conn))
		}
	}()
}

// fingerprint is this device's key fingerprint (full form), or "" before Serve.
func (s *DaemonServer) fingerprint() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.identity == nil {
		return ""
	}
	return s.identity.Fingerprint.String()
}

// dialSecure opens a TCP connection with the radio wake-up strategy of session.DialPeer, then
// the secure channel. There is no plaintext fallback (CHAN-05).
func dialSecure(ctx context.Context, addr string, cfg securechan.Config) (*securechan.Conn, error) {
	raw, err := session.DialPeer(addr)
	if err != nil {
		return nil, err
	}
	return securechan.Client(ctx, raw, cfg)
}

// acceptPairing runs the secure handshake of an incoming control connection, then the pairing
// check, which moves the connection from UNAUTH to AUTH.
func (s *DaemonServer) acceptPairing(conn *securechan.Conn) {
	{
		ch := session.NewChannel(conn)

		// Read incoming authentication / handshake message with 5s deadline
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		msg, err := ch.Read()
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil {
			ch.Close()
			return
		}

		s.mu.RLock()
		devName := s.config.DeviceName
		s.mu.RUnlock()

		ip, fp := ch.RemoteIP(), conn.PeerFingerprint()
		var offer, mutual bool
		switch msg.Type {
		case pairing.TypePIN, pairing.TypePINWanted: // path P2: the PIN shown, or one on demand
			name, trust, ok := s.acceptPIN(conn, ch, msg, devName)
			if !ok {
				ch.Close()
				return
			}
			msg.DeviceName, offer = name, trust // the name is sent only after the confirmations (PAIR-07)
		case "pair_request": // a QR code's secret (P1), or a device of mutual trust (P3)
			// The pairing code, if any, is ignored: trust comes from the key (WP-3.6b).
			trust, err := s.admitIncoming(conn, msg, ip)
			if err != nil {
				reject := session.Message{Type: "pair_reject", FileName: err.Error()}
				if errors.Is(err, errPINRequired) {
					reject.Code = codePINRequired
				}
				_ = ch.Send(reject)
				ch.Close()
				return
			}
			offer, mutual = trust, msg.Secret == ""
			if err := s.touchTrust(fp, msg.DeviceName, ip); err != nil {
				s.Broadcast(NewEvent("log", map[string]string{"level": "WARN", "message": "could not save the trusted device: " + err.Error()}))
			}
			ch.Authenticate()
			_ = conn.SetDeadline(time.Time{})
			_ = ch.Send(session.Message{Type: "pair_accept", DeviceName: devName, TrustsYou: s.declares(fp)})
		default:
			_ = ch.Send(session.Message{Type: "pair_reject", FileName: "pair_request expected"})
			ch.Close()
			return
		}

		peerName := msg.DeviceName
		if peerName == "" {
			peerName = ip
		}
		s.startSession(ch, fp, peerName, ip, mutual, offer, "")
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
	if pairing.IsTrustMessage(msg.Type) { // PAIR-10
		s.handleTrustMessage(msg)
		return
	}
	switch msg.Type {
	case "pair_request":
		s.mu.Lock()
		s.pairedDeviceName = msg.DeviceName
		devName := s.config.DeviceName
		sess := s.activeSession
		s.mu.Unlock()

		// Inside a session the peer's key was already admitted: nothing more to check.
		if sess != nil {
			_ = sess.Send(session.Message{
				Type:       "pair_accept",
				DeviceName: devName,
			})
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
		s.dropTransfers()
		s.mu.Unlock()
		s.Broadcast(NewEvent("disconnected", map[string]string{"reason": "peer requested disconnect"}))

	case "bench_ping", "bench_burst_req":
		s.mu.RLock()
		sess := s.activeSession
		s.mu.RUnlock()
		if sess != nil {
			AnswerBench(sess, msg)
		}

	case session.TypeProgress: // the receiver's progress, which this sender shows
		s.mu.RLock()
		ev := s.outEvents
		s.mu.RUnlock()
		if ev != nil && msg.Progress != nil && msg.TransferID == ev.offer.TransferID {
			ev.takePeer(msg.Progress)
		}

	case "bench_pong":
		select {
		case s.benchPongChan <- msg:
		default:
		}

	case "bench_burst_ready", "bench_burst_ack":
		select {
		case s.benchBurstAckChan <- msg:
		default:
		}

	case "offer", session.TypeOfferItems: // one offer, in pages when large (D2)
		s.takeOffer(msg)

	case "pause", "resume": // the whole transfer, or one item of it (D6)
		if msg.Item != nil {
			s.itemAction(msg.Type, *msg.Item, true, "")
		} else {
			s.pauseAll(msg.Type == "pause", true, "")
		}

	case "skip": // D6
		if msg.Item != nil {
			s.itemAction(itemSkip, *msg.Item, true, "")
		}

	case "cancel":
		s.cancelTransfer(true, "")

	case "reject":
		s.mu.Lock()
		if s.lastOffered != nil && (msg.TransferID == "" || msg.TransferID == s.lastOffered.offer.TransferID) {
			s.lastOffered.close() // the offer is over: nobody may join it any more
			s.lastOffered = nil
		}
		s.mu.Unlock()
		message := "Peer rejected transfer"
		if msg.Code == "busy" {
			message = "Peer is busy with another transfer"
		}
		s.Broadcast(NewEvent("transfer_rejected", map[string]string{"message": message}))

	case "accept":
		s.serveAccepted(msg)

	case "complete":
		s.peerCompleted(msg)

	case session.TypeFailed:
		s.peerFailed(msg)

	case session.TypeTransferCheck:
		s.answerCheck(msg)

	case session.TypeError: // never answered, so that two peers cannot ping-pong errors
		s.Broadcast(NewEvent("log", map[string]string{
			"level":   "WARN",
			"message": fmt.Sprintf("peer error %s: %s", msg.Code, msg.Error),
		}))

	default: // bible §8.2: an unknown type gets error{code: unsupported}
		s.mu.RLock()
		sess := s.activeSession
		s.mu.RUnlock()
		if sess != nil {
			_ = sess.Send(session.Unsupported(msg.Type))
		}
	}
}

func (s *DaemonServer) parseCollisionPolicy(p string) store.Policy {
	switch p {
	case "overwrite":
		return store.PolicyOverwrite
	case "skip":
		return store.PolicySkip
	default:
		return store.PolicyAutoRename
	}
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

// liveStages is the active transfer's stage loads and limiting stage, if it has measures.
func (s *DaemonServer) liveStages() (map[string]float64, diag.Bottleneck) {
	s.trackerMu.Lock()
	tracker := s.activeTracker
	s.trackerMu.Unlock()
	if tracker == nil {
		return nil, diag.Bottleneck{}
	}
	return tracker.Live()
}

// RecordTransferStage records the time of one stage of one chunk in the active telemetry
// tracker (DEV-03), creating it on the first measure.
func (s *DaemonServer) RecordTransferStage(name string, totalFiles int, totalBytes int64, stage string, parallel int, d time.Duration, bytes int64) {
	s.trackerMu.Lock()
	if s.activeTracker == nil {
		s.activeTracker = NewTransferSessionTracker(name, totalFiles, totalBytes)
	}
	tracker := s.activeTracker
	s.trackerMu.Unlock()
	tracker.OnStage(stage, parallel, d, bytes)
}

// RecordTransferRetry counts a chunk retry in the active telemetry tracker, if any.
func (s *DaemonServer) RecordTransferRetry() {
	s.trackerMu.Lock()
	tracker := s.activeTracker
	s.trackerMu.Unlock()
	if tracker != nil {
		tracker.RecordRetry()
	}
}

// maxSavedReports is how many transfer reports ReportsDir keeps (oldest removed first).
const maxSavedReports = 20

// ReportsDir is where the daemon saves transfer reports, next to config.json.
func ReportsDir() string {
	return filepath.Join(filepath.Dir(GetConfigFilePath()), "reports")
}

// FinishTransferTracker compiles the final diagnostic summary report and saves it in
// ReportsDir. The report carries no file name, IP or host name (DEV-05).
func (s *DaemonServer) FinishTransferTracker(outcome string) *TransferSummaryReport {
	s.trackerMu.Lock()
	if s.activeTracker == nil {
		defer s.trackerMu.Unlock()
		return s.lastSummaryReport
	}
	s.activeTracker.Finish(outcome)
	report := s.activeTracker.GenerateReport(diag.ReportOptions{})
	s.lastSummaryReport = report
	s.activeTracker = nil
	s.trackerMu.Unlock()

	if _, err := diag.SaveReport(filepath.Join(s.dataDir, "reports"), report, maxSavedReports); err != nil {
		log.Printf("[diag] could not save the transfer report: %v", err)
	}
	return report
}

// GetLastSummaryReport returns the latest completed transfer report
func (s *DaemonServer) GetLastSummaryReport() *TransferSummaryReport {
	s.trackerMu.Lock()
	last := s.lastSummaryReport
	s.trackerMu.Unlock()
	if last != nil {
		return last
	}
	// None since the start: the newest one saved (the developer panel after a restart).
	files, _ := diag.ListReports(filepath.Join(s.dataDir, "reports"))
	if len(files) == 0 {
		return nil
	}
	rep, err := diag.LoadReport(files[len(files)-1])
	if err != nil {
		return nil
	}
	if rep.FormattedReport == "" {
		rep.FormattedReport = diag.FormatText(rep)
	}
	return rep
}

// broadcastTransferComplete completes telemetry tracker and broadcasts transfer_complete with report
func (s *DaemonServer) broadcastTransferComplete(extra map[string]interface{}) {
	rep := s.FinishTransferTracker(diag.OutcomeCompleted)
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
