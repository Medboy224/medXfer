// Package securechan is the secure channel between two devices (bible chapter 7): TLS 1.3 from
// the first byte, mutual authentication, and trust decided by the peer's key fingerprint, never
// by a certificate authority. There is no fallback to plaintext (CHAN-05).
package securechan

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
)

// ALPN is the only application protocol spoken on the channel (CHAN-02).
const ALPN = "medxfer/1"

// Defaults (bible chapter 12; all are parameters, rule R4).
const (
	DefaultHandshakeTimeout = 5 * time.Second  // LIM-02, CHAN-07
	UnauthReadLimit         = 4 << 10          // LIM-03: bytes read in state Unauth
	UnauthTimeout           = 30 * time.Second // CHAN-04: life of an Unauth connection
)

var (
	ErrFingerprintMismatch = errors.New("securechan: peer key is not the expected one")
	ErrUnknownPeer         = errors.New("securechan: peer is not trusted")
	ErrNoALPN              = errors.New("securechan: peer did not negotiate " + ALPN)
	ErrNoPeerCertificate   = errors.New("securechan: peer sent no certificate")
	ErrUnauthLimit         = errors.New("securechan: too much data before authentication")
)

// State of a connection (CHAN-04).
type State int

const (
	// Unauth: the peer's key is unknown; only pairing messages may pass, within
	// UnauthReadLimit bytes and UnauthTimeout.
	Unauth State = iota
	// Auth: the peer's key is trusted, or pairing succeeded.
	Auth
)

func (s State) String() string {
	if s == Auth {
		return "AUTH"
	}
	return "UNAUTH"
}

// Config says who we are and which peers we accept.
type Config struct {
	Identity *identity.Identity
	// Trust holds the trusted devices; nil means none.
	Trust *identity.TrustStore
	// Known, when set, accepts more keys as authenticated: the peers a transfer expects on
	// the device's single port (pkg/transfer.Hub), which need not be trusted devices.
	Known func(identity.Fingerprint) bool
	// Expected pins the peer's key (QR code, reconnection, data connection of a transfer).
	// When set, nothing else is accepted.
	Expected identity.Fingerprint
	// AllowUnknown accepts an untrusted key; the connection then starts in state Unauth.
	AllowUnknown bool
	// HandshakeTimeout defaults to DefaultHandshakeTimeout.
	HandshakeTimeout time.Duration
	// Gate, on the server side, limits connections that are not authenticated yet (LIM-01).
	Gate *Gate
}

// Conn is an established secure channel.
type Conn struct {
	*tls.Conn
	peer identity.Fingerprint

	mu         sync.Mutex
	state      State
	unauthRead int
	release    func() // gives the Gate place back (LIM-01)
	pending    []byte // given back by Unread, read before the connection
}

// Unread gives bytes back to the connection: the next reads return them first. A reader
// that looked at the first frame to route the connection hands it over unchanged.
func (c *Conn) Unread(p []byte) {
	c.mu.Lock()
	c.pending = append(append([]byte(nil), p...), c.pending...)
	c.mu.Unlock()
}

// PeerFingerprint is the fingerprint of the peer's key, verified during the handshake.
func (c *Conn) PeerFingerprint() identity.Fingerprint { return c.peer }

// The TLS exporter of the pairing (bible §6.2, PAIR-03). The version is in the label: another
// binding gets another label, never a reinterpretation of this one (D9 of the J5 plan).
const (
	ChannelBindingLabel = "EXPORTER-medxfer-v1-pair"
	ChannelBindingLen   = 32
)

// ChannelBinding is the exporter value of this TLS session (RFC 8446 §7.5): both ends of one
// connection get the same bytes, while a relay between two TLS sessions gets two different
// values, so a pairing bound to it fails through a relay. It is a secret of the session: it
// is never logged (bible chapter 13). The error is never to be ignored: an empty binding
// would bind nothing.
func (c *Conn) ChannelBinding() ([]byte, error) {
	cs := c.Conn.ConnectionState()
	b, err := cs.ExportKeyingMaterial(ChannelBindingLabel, nil, ChannelBindingLen)
	if err != nil {
		return nil, fmt.Errorf("securechan: channel binding: %w", err)
	}
	return b, nil
}

// State returns Unauth or Auth.
func (c *Conn) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Authenticate moves an Unauth connection to Auth once pairing has succeeded, lifting the
// Unauth limits.
func (c *Conn) Authenticate() {
	c.mu.Lock()
	wasUnauth := c.state == Unauth
	c.state = Auth
	c.mu.Unlock()
	if wasUnauth {
		_ = c.Conn.SetDeadline(time.Time{})
	}
	c.releaseGate()
}

// Close closes the connection and gives its Gate place back.
func (c *Conn) Close() error {
	c.releaseGate()
	return c.Conn.Close()
}

func (c *Conn) releaseGate() {
	if c.release != nil {
		c.release()
	}
}

// Read enforces the Unauth read limit (LIM-03): past it, the connection is closed.
func (c *Conn) Read(p []byte) (int, error) {
	c.mu.Lock()
	if len(c.pending) > 0 { // already counted when first read
		n := copy(p, c.pending)
		c.pending = c.pending[n:]
		c.mu.Unlock()
		return n, nil
	}
	if c.state == Unauth {
		left := UnauthReadLimit - c.unauthRead
		if left <= 0 {
			c.mu.Unlock()
			_ = c.Close()
			return 0, ErrUnauthLimit
		}
		if len(p) > left {
			p = p[:left]
		}
	}
	c.mu.Unlock()
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	if c.state == Unauth {
		c.unauthRead += n
	}
	c.mu.Unlock()
	return n, err
}

// Server runs the TLS handshake on an accepted connection. On error raw is closed.
func Server(ctx context.Context, raw net.Conn, cfg Config) (*Conn, error) {
	return handshake(ctx, raw, cfg, true)
}

// Client runs the TLS handshake on a dialled connection. On error raw is closed: a failed
// handshake is never retried in plaintext (CHAN-05).
func Client(ctx context.Context, raw net.Conn, cfg Config) (*Conn, error) {
	return handshake(ctx, raw, cfg, false)
}

// Dial connects to addr and runs the client handshake (CHAN-07: connect timeout, keep-alive).
func Dial(ctx context.Context, addr string, cfg Config) (*Conn, error) {
	d := net.Dialer{Timeout: cfg.timeout(), KeepAlive: 15 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return Client(ctx, raw, cfg)
}

func (cfg Config) timeout() time.Duration {
	if cfg.HandshakeTimeout > 0 {
		return cfg.HandshakeTimeout
	}
	return DefaultHandshakeTimeout
}

func handshake(ctx context.Context, raw net.Conn, cfg Config, server bool) (*Conn, error) {
	if cfg.Identity == nil {
		raw.Close()
		return nil, errors.New("securechan: no identity")
	}
	release, charge := func() {}, func() {}
	if server && cfg.Gate != nil {
		adm, err := cfg.Gate.Admit(raw.RemoteAddr())
		if err != nil {
			raw.Close()
			return nil, err
		}
		release, charge = adm.Release, adm.Charge
	}
	var (
		peer  identity.Fingerprint
		state State
	)
	tc := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cfg.Identity.Cert},
		ClientAuth:   tls.RequireAnyClientCert,
		// There is no certificate authority: InsecureSkipVerify only turns off CA checks.
		// VerifyConnection below does the real check, on both sides, and is mandatory.
		InsecureSkipVerify:     true,
		NextProtos:             []string{ALPN},
		SessionTicketsDisabled: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if cs.NegotiatedProtocol != ALPN {
				return ErrNoALPN
			}
			if len(cs.PeerCertificates) == 0 {
				return ErrNoPeerCertificate
			}
			fp := identity.FingerprintOf(cs.PeerCertificates[0])
			switch {
			case !cfg.Expected.IsZero():
				if fp != cfg.Expected {
					return ErrFingerprintMismatch
				}
				state = Auth
			case cfg.Trust != nil && cfg.Trust.Contains(fp):
				state = Auth
			case cfg.Known != nil && cfg.Known(fp):
				state = Auth
			case cfg.AllowUnknown:
				state = Unauth
			default:
				return ErrUnknownPeer
			}
			peer = fp
			return nil
		},
	}
	var tconn *tls.Conn
	if server {
		tconn = tls.Server(raw, tc)
	} else {
		tconn = tls.Client(raw, tc)
	}
	hctx, cancel := context.WithTimeout(ctx, cfg.timeout())
	defer cancel()
	if err := tconn.HandshakeContext(hctx); err != nil {
		raw.Close()
		charge() // a failed handshake was an unauthenticated connection too (LIM-01)
		release()
		return nil, fmt.Errorf("securechan: handshake with %s: %w", raw.RemoteAddr(), err)
	}
	c := &Conn{Conn: tconn, peer: peer, state: state, release: release}
	if state == Unauth {
		charge()
		_ = tconn.SetDeadline(time.Now().Add(UnauthTimeout))
	} else {
		c.releaseGate() // an authenticated peer neither holds a place nor uses the quota
	}
	return c, nil
}
