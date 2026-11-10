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
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/identity"
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
	mu                sync.RWMutex
	config            Config
	activeSession     *session.Channel
	outEvents         *daemonEvents // the transfer this device sends, while it runs
	pairedDeviceName  string
	pendingOffer      *session.Message   // a complete offer waiting for respond_offer
	incomingOffer     *session.Message   // an offer whose pages are still arriving (D2)
	lastOffered       *outgoingOffer     // what this device offered, until the peer answers
	transferCancel    context.CancelFunc // set while a transfer is active (LIM-08)
	activeTransferID  string
	endedTransferID   string             // the transfer endTransfer just ended: its peer's "complete" may come after (#129)
	activeReceiver    *transfer.Receiver // this side's scheduler, when it receives
	isPaused          bool
	currentBatchItems []BatchFileInfo // the queue the dashboard shows
	sessionGraceTimer *time.Timer
	// sentCompletedID is the last transfer this device sent that was completed (completeSent).
	sentCompletedID string
	// completedTransferID is the last transfer this device received whole (answerCheck).
	completedTransferID string
	isReconnecting      bool
	// redialTo is where this device dialed the active session: lost, it is dialed again there
	// (redial); redialCancel stops that.
	redialTo     string
	redialCancel context.CancelFunc
	httpPort     int
	webShare     webShareHost // the guest session of Web Share, if any (J7)

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

	// The path by BLE (ble.go, bible §6.6): its own lock, the platform calling it often.
	ble bleState

	benchPongChan     chan session.Message
	benchBurstAckChan chan session.Message

	trackerMu         sync.Mutex
	activeTracker     *TransferSessionTracker
	trackerID         string // the transfer of activeTracker
	closedTrackerID   string // the transfer whose measures were closed last (trackerFor)
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
		gate:              securechan.NewGate(),
		config:            cfg,
		pairingCode:       pCode,
		benchPongChan:     make(chan session.Message, 10),
		benchBurstAckChan: make(chan session.Message, 10),
		ctx:               ctx,
		cancel:            cancel,
		dataDir:           dataDir,
	}
	return srv
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

// serveHTTP serves the health probe. Web Share has its own listener, per guest session
// (pkg/webshare, WEB-01).
func (s *DaemonServer) serveHTTP(httpLn net.Listener) error {
	s.SetDiscoverable(true) // the daemon is up for discovery while it serves
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)

	httpSrv := newHTTPServer(mux)
	s.mu.Lock()
	s.httpSrv = httpSrv
	s.mu.Unlock()

	log.Printf("[*] medXfer Headless Daemon started on http://%s", httpLn.Addr().String())
	return httpSrv.Serve(httpLn)
}

// newHTTPServer is the daemon's server. The headers of a request and an idle connection are
// bounded: a slow client holds nothing open (§3.7, #156).
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
	s.stopWebShare()
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
	s.stopRedial()
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
			if !t.LocalIP.IsLoopback() && t.LocalIP.To4() != nil { // 169.254 too: a cable between two PCs (DISC-13)
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
		Status:          "idle",
		DeviceName:      s.config.DeviceName,
		DownloadDir:     s.config.DownloadDir,
		CollisionPolicy: s.config.CollisionPolicy,
		Paired:          s.activeSession != nil,
		ActiveTransfer:  s.transferCancel != nil,
		IsPaused:        s.isPaused,
		Version:         "1.0.0",
		LocalPort:       s.httpPort,
		PairingCode:     s.pairingCode,
		Discoverable:    s.discCancel != nil,
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
		case pairing.TypeBLE: // after BLE: the key it authenticated, once, within 2 minutes (BLE-10)
			if !s.ble.auth.Take(fp) {
				_ = ch.Send(session.Message{Type: "pair_reject", FileName: pairing.ErrRefused.Error()})
				s.Broadcast(NewEvent("pair_failed", map[string]string{}))
				ch.Close()
				return
			}
			// The PIN or the pair's key proved the user's authorisation, as for a PIN (ID-05, #217).
			s.addressTaken(ip, fp)
			mutual = s.mutual(fp, msg.TrustsYou)
			if err := s.touchTrust(fp, msg.DeviceName, ip); err != nil {
				s.Broadcast(NewEvent("log", map[string]string{"level": "WARN", "message": "could not save the trusted device: " + err.Error()}))
			}
			ch.Authenticate()
			_ = conn.SetDeadline(time.Time{})
			_ = ch.Send(session.Message{Type: "pair_accept", DeviceName: devName, TrustsYou: s.declares(fp)})
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
			// A QR code's session too may be between devices of mutual trust (J6c): then nothing to
			// offer.
			offer, mutual = trust, s.mutual(fp, msg.TrustsYou)
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
	silence := sessionSilence.get()
	for {
		_ = sess.SetReadDeadline(time.Now().Add(silence)) // the peer pings (pingSession)
		msg, err := sess.Read()
		if err != nil {
			sess.Close() // the peer sees it at once, and the side that dialed dials again
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
				// Nobody else brings it back (WP-B.8): the side that dialed dials again.
				if s.redialTo != "" && pairing.Declares(s.trust, s.trustPeer) {
					ctx, cancel := context.WithCancel(s.ctx)
					s.redialCancel = cancel
					go s.redial(ctx, s.redialTo, s.trustPeer)
				}
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
		s.stopRedial()
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
		s.stopRedial()
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

	case session.TypePing: // its coming is enough (listenToSession)

	case session.TypeError: // never answered, so that two peers cannot ping-pong errors
		if msg.Code == session.CodeUnsupported && msg.Error == session.Unsupported(session.TypePing).Error {
			return // a peer without pings: its answer shows it is there
		}
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

// RecordTransferSample records a live progress measurement of transfer id.
func (s *DaemonServer) RecordTransferSample(id string, stats engine.TransferStats, progressPercent float64, fallbackName string, totalFiles int, totalBytes int64) {
	tracker := s.trackerFor(id, fallbackName, totalFiles, totalBytes)
	if tracker != nil {
		tracker.RecordSample(stats, progressPercent)
	}
}

// trackerFor is the measures of transfer id, created on its first measure; nil once they were
// closed (FinishTransferTracker): a late progress of an ended transfer does not open new ones,
// whose report would replace the real one (WP-B.8).
func (s *DaemonServer) trackerFor(id, name string, totalFiles int, totalBytes int64) *TransferSessionTracker {
	s.trackerMu.Lock()
	defer s.trackerMu.Unlock()
	if id != "" && id == s.closedTrackerID {
		return nil
	}
	if s.activeTracker == nil {
		s.activeTracker = NewTransferSessionTracker(name, totalFiles, totalBytes)
		s.trackerID = id
	}
	return s.activeTracker
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
func (s *DaemonServer) RecordTransferStage(id, name string, totalFiles int, totalBytes int64, stage string, parallel int, d time.Duration, bytes int64) {
	if tracker := s.trackerFor(id, name, totalFiles, totalBytes); tracker != nil {
		tracker.OnStage(stage, parallel, d, bytes)
	}
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
	s.closedTrackerID = s.trackerID
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
