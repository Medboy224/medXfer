// Package webshare is Web Share v2 (bible chapter 11): a phone or a PC without medXfer sends
// and receives files with its browser. The host starts a guest session, shows its QR code
// (https://<ip>:<port>/s#t=<token>) and its PIN; the guest's browser talks to this session's
// own HTTPS listener only, which exists as long as the session (WEB-01, WEB-10).
package webshare

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
)

// DefaultPort is the port tried first: a fixed one, so that Windows needs a single firewall
// rule (decision D5 of the J7 plan). Another is taken when it is busy.
const DefaultPort = 19999

// DefaultTTL is how long a session lasts without a transfer (WEB-10).
const DefaultTTL = 15 * time.Minute

// MaxPINTries is how many wrong PINs end the session (WEB-03).
const MaxPINTries = 5

// Why a session ended, as Event "session_closed" says it.
const (
	ClosedStopped     = "stopped"      // the host left its screen
	ClosedExpired     = "expired"      // no transfer for TTL
	ClosedPINAttempts = "pin_attempts" // MaxPINTries wrong PINs: the host is told
)

// Options are what the host gives to start a session.
type Options struct {
	// Addr is the address of the interface the guest reaches: the listener binds it only, and
	// the certificate names it (WEB-01).
	Addr string
	// Port to try first: DefaultPort when 0. Another, free, when it is busy; a negative one
	// takes any free port at once (tests).
	Port int
	// Name is the host's generic name (DISC-07), shown on the guest's page.
	Name string
	// Paths are the files and folders offered to the guest.
	Paths []string
	// TTL: DefaultTTL when 0.
	TTL time.Duration
	// PinIP: once the guest is authenticated, requests from another address are refused
	// (WEB-09). Off by default: an address changes, and a NAT shares one.
	PinIP bool
	// ReceiveDir is where the guest's files go; "" refuses uploads.
	ReceiveDir string
	// AutoAccept: the guest's uploads are accepted without asking the host (WEB-06).
	AutoAccept bool
	// MaxBytes and MaxFiles bound what a session receives (WEB-06): DefaultMaxBytes and
	// DefaultMaxFiles when 0.
	MaxBytes int64
	MaxFiles int
	// Events receives what the host is told; it must not block.
	Events func(Event)
	// Logf receives the server's own errors (TLS handshakes refused by the guest's browser
	// among others): never a token or a PIN (WEB-04). log.Printf when nil.
	Logf func(format string, args ...any)
}

// Event is what the session tells the host.
type Event struct {
	Kind string         // "session_closed", "download_started", "download_done"
	Data map[string]any // never the token nor the PIN (WEB-04)
}

// Session is one guest session: its listener, certificate, token and PIN live and die with it.
type Session struct {
	opts        Options
	url         string
	token       string
	pin         string
	fingerprint string
	items       []Item

	srv *http.Server
	ln  net.Listener

	mu        sync.Mutex
	expires   time.Time
	tries     int    // wrong PINs so far
	guestIP   string // the authenticated guest's address, when PinIP
	busy      int    // transfers running: the session does not expire meanwhile
	closed    string // why it ended; "" while it runs
	uploads   map[string]*upload
	active    *upload // the one upload pending or coming (WEB-06)
	usedFiles int
	usedBytes int64
	timer     *time.Timer
	done      chan struct{}
	now       func() time.Time
	failWait  time.Duration // the wait after a wrong PIN (WEB-03)
	recheck   time.Duration // how soon an expiry put off by a transfer is looked at again
}

// Start opens a guest session: its certificate, token, PIN, listener and server.
func Start(opts Options) (*Session, error) {
	return start(opts, time.Now, 500*time.Millisecond)
}

func start(opts Options, now func() time.Time, failWait time.Duration) (*Session, error) {
	ip := net.ParseIP(opts.Addr)
	if ip == nil || ip.IsUnspecified() {
		return nil, fmt.Errorf("webshare: give the address of the interface to serve on, not %q", opts.Addr)
	}
	if opts.TTL <= 0 {
		opts.TTL = DefaultTTL
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	items, err := itemsOf(opts.Paths)
	if err != nil {
		return nil, err
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	pin, err := randomPIN(6)
	if err != nil {
		return nil, err
	}
	// Masked wherever the process writes, whatever a future line would print (WEB-04).
	diag.Secrets.Add("share token", token)
	diag.Secrets.Add("share PIN", pin)
	cert, fp, err := newCertificate(ip, now())
	if err != nil {
		return nil, err
	}
	ln, err := listen(ip, opts.Port)
	if err != nil {
		return nil, err
	}
	s := &Session{
		opts:        opts,
		token:       token,
		pin:         pin,
		fingerprint: fp,
		items:       items,
		ln:          ln,
		expires:     now().Add(opts.TTL),
		uploads:     map[string]*upload{},
		done:        make(chan struct{}),
		now:         now,
		failWait:    failWait,
		recheck:     30 * time.Second,
	}
	port := ln.Addr().(*net.TCPAddr).Port
	s.url = "https://" + net.JoinHostPort(ip.String(), strconv.Itoa(port)) + "/s#t=" + token
	s.srv = &http.Server{
		Handler: s.handler(),
		// WEB-08: a slow or oversized client holds nothing open.
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    16 << 10,
		IdleTimeout:       2 * time.Minute,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		ErrorLog:          log.New(logWriter(opts.Logf), "", 0),
	}
	s.mu.Lock()
	s.timer = time.AfterFunc(opts.TTL, s.checkExpiry)
	s.mu.Unlock()
	go func() { _ = s.srv.ServeTLS(ln, "", "") }()
	return s, nil
}

// listen binds the address, at port when free, else at any free port.
func listen(ip net.IP, port int) (net.Listener, error) {
	if port == 0 {
		port = DefaultPort
	}
	if port > 0 {
		if ln, err := net.Listen("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port))); err == nil {
			return ln, nil
		}
	}
	return net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
}

// URL is what the QR code carries: the token in the fragment, which the browser never sends
// (WEB-02).
func (s *Session) URL() string { return s.url }

// Token and PIN go to the host's own screen only (WEB-04).
func (s *Session) Token() string { return s.token }
func (s *Session) PIN() string   { return s.pin }

// Fingerprint is the SHA-256 of the session's certificate, as browsers show it (decision D6).
func (s *Session) Fingerprint() string { return s.fingerprint }

// Port is the one the listener got.
func (s *Session) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// Expires is when the session ends if no transfer runs then.
func (s *Session) Expires() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expires
}

// Done is closed when the session ends; Closed says why.
func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) Closed() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Close ends the session: its listener, connections and server go (WEB-10).
func (s *Session) Close() { s.end(ClosedStopped) }

func (s *Session) end(why string) {
	s.mu.Lock()
	if s.closed != "" {
		s.mu.Unlock()
		return
	}
	s.closed = why
	if s.timer != nil {
		s.timer.Stop()
	}
	close(s.done)
	s.mu.Unlock()
	s.dropUploads() // no part file left behind
	// Off the request that may have ended it: Shutdown waits for it.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if s.srv.Shutdown(ctx) != nil {
			_ = s.srv.Close()
		}
	}()
	s.emit(Event{Kind: "session_closed", Data: map[string]any{"reason": why}})
}

// checkExpiry ends the session at its expiry, unless a transfer runs: then it looks again a
// little later (WEB-10).
func (s *Session) checkExpiry() {
	s.mu.Lock()
	if s.closed != "" {
		s.mu.Unlock()
		return
	}
	left := s.expires.Sub(s.now())
	if s.busy > 0 || left > 0 {
		if left < s.recheck {
			left = s.recheck
		}
		if s.busy > 0 {
			s.expires = s.now().Add(left)
		}
		s.timer.Reset(left)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.end(ClosedExpired)
}

// begin and finish frame a transfer: the session does not expire while one runs. At its end,
// the guest has the session's full TTL again.
func (s *Session) begin() {
	s.mu.Lock()
	s.busy++
	s.mu.Unlock()
}

func (s *Session) finish() {
	s.mu.Lock()
	s.busy--
	if s.busy == 0 {
		if later := s.now().Add(s.opts.TTL); later.After(s.expires) {
			s.expires = later
			if s.timer != nil {
				s.timer.Reset(s.opts.TTL)
			}
		}
	}
	s.mu.Unlock()
}

func (s *Session) emit(e Event) {
	if s.opts.Events != nil {
		s.opts.Events(e)
	}
}

// secureEq compares secrets in constant time (WEB-03).
func secureEq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// randomToken is 128 bits from crypto/rand (WEB-02), in hexadecimal.
func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// randomPIN is uniform, without bias, through crypto/rand (bible §11.2).
func randomPIN(digits int) (string, error) {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", digits, n), nil
}

// logWriter turns the server's error log into Logf lines.
type logWriter func(format string, args ...any)

func (f logWriter) Write(p []byte) (int, error) {
	f("webshare: %s", strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
