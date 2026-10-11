package securechan

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
)

type device struct {
	id    *identity.Identity
	trust *identity.TrustStore
}

func newDevice(t *testing.T) *device {
	t.Helper()
	dir := t.TempDir()
	id, err := identity.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := identity.OpenTrustStore(filepath.Join(dir, identity.TrustFile))
	if err != nil {
		t.Fatal(err)
	}
	return &device{id: id, trust: ts}
}

func (d *device) trustDevice(t *testing.T, other *device) {
	t.Helper()
	if err := d.trust.Add(identity.Peer{Fingerprint: other.id.Fingerprint}); err != nil {
		t.Fatal(err)
	}
}

type result struct {
	conn *Conn
	err  error
}

// serve accepts one connection on a loopback listener and runs the server handshake.
func serve(t *testing.T, cfg Config) (addr string, res <-chan result) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	ch := make(chan result, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			ch <- result{err: err}
			return
		}
		c, err := Server(context.Background(), raw, cfg)
		ch <- result{c, err}
	}()
	return ln.Addr().String(), ch
}

func wait(t *testing.T, ch <-chan result) result {
	t.Helper()
	select {
	case r := <-ch:
		if r.conn != nil {
			t.Cleanup(func() { r.conn.Close() })
		}
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("server handshake did not finish")
		return result{}
	}
}

func TestTrustedPeersAuthenticateBothWays(t *testing.T) {
	a, b := newDevice(t), newDevice(t)
	a.trustDevice(t, b)
	b.trustDevice(t, a)
	addr, srv := serve(t, Config{Identity: a.id, Trust: a.trust})
	c, err := Dial(context.Background(), addr, Config{Identity: b.id, Trust: b.trust})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := wait(t, srv)
	if s.err != nil {
		t.Fatal(s.err)
	}
	if c.PeerFingerprint() != a.id.Fingerprint || s.conn.PeerFingerprint() != b.id.Fingerprint {
		t.Fatal("each side must see the other's fingerprint")
	}
	if c.State() != Auth || s.conn.State() != Auth {
		t.Fatalf("states %v / %v, want AUTH / AUTH", c.State(), s.conn.State())
	}
	cs := c.ConnectionState()
	if cs.Version != tls.VersionTLS13 || cs.NegotiatedProtocol != ALPN || cs.DidResume {
		t.Fatalf("version %x, ALPN %q, resumed %v", cs.Version, cs.NegotiatedProtocol, cs.DidResume)
	}
	go func() { _, _ = c.Write([]byte("ping")) }()
	buf := make([]byte, 4)
	if _, err := io.ReadFull(s.conn, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("read %q, %v", buf, err)
	}
}

// QR code and reconnection: the client pins the key it expects (CHAN-03, ID-05).
func TestPinnedKeyMismatchIsRefused(t *testing.T) {
	a, b, mitm := newDevice(t), newDevice(t), newDevice(t)
	// The "server" is an attacker with its own key; b expects a's key.
	addr, srv := serve(t, Config{Identity: mitm.id, AllowUnknown: true})
	_, err := Dial(context.Background(), addr, Config{Identity: b.id, Expected: a.id.Fingerprint})
	if !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("client error = %v, want ErrFingerprintMismatch", err)
	}
	if r := wait(t, srv); r.err == nil {
		t.Fatal("the attacker's handshake succeeded")
	}
}

func TestUnknownPeerIsRefused(t *testing.T) {
	a, b := newDevice(t), newDevice(t)
	b.trustDevice(t, a) // a does not trust b
	addr, srv := serve(t, Config{Identity: a.id, Trust: a.trust})
	_, cerr := Dial(context.Background(), addr, Config{Identity: b.id, Trust: b.trust})
	if r := wait(t, srv); !errors.Is(r.err, ErrUnknownPeer) {
		t.Fatalf("server error = %v, want ErrUnknownPeer", r.err)
	}
	if cerr == nil {
		// TLS 1.3: the client may finish before reading the server's alert; its first read fails.
		t.Log("client handshake returned before the alert")
	}
}

// Unknown peers may be accepted for pairing only, in state Unauth with a read limit (CHAN-04).
func TestUnauthLimits(t *testing.T) {
	a, b := newDevice(t), newDevice(t)
	addr, srv := serve(t, Config{Identity: a.id, AllowUnknown: true})
	c, err := Dial(context.Background(), addr, Config{Identity: b.id, AllowUnknown: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := wait(t, srv)
	if s.err != nil {
		t.Fatal(s.err)
	}
	if s.conn.State() != Unauth || c.State() != Unauth {
		t.Fatalf("states %v / %v, want UNAUTH / UNAUTH", s.conn.State(), c.State())
	}
	go func() { _, _ = c.Write(make([]byte, UnauthReadLimit+512)) }()
	n, err := io.Copy(io.Discard, s.conn)
	if !errors.Is(err, ErrUnauthLimit) || n != UnauthReadLimit {
		t.Fatalf("read %d bytes, error %v; want %d then ErrUnauthLimit", n, err, UnauthReadLimit)
	}
}

func TestAuthenticateLiftsUnauthLimit(t *testing.T) {
	a, b := newDevice(t), newDevice(t)
	addr, srv := serve(t, Config{Identity: a.id, AllowUnknown: true})
	c, err := Dial(context.Background(), addr, Config{Identity: b.id, AllowUnknown: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := wait(t, srv)
	if s.err != nil {
		t.Fatal(s.err)
	}
	s.conn.Authenticate()
	const size = 3 * UnauthReadLimit
	go func() { _, _ = c.Write(make([]byte, size)); c.Close() }()
	n, err := io.Copy(io.Discard, s.conn)
	if n != size {
		t.Fatalf("read %d bytes after Authenticate (%v), want %d", n, err, size)
	}
}

// CHAN-02: TLS 1.2 is refused.
func TestTLS12IsRefused(t *testing.T) {
	a, b := newDevice(t), newDevice(t)
	addr, srv := serve(t, Config{Identity: a.id, AllowUnknown: true})
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c := tls.Client(raw, &tls.Config{
		MaxVersion: tls.VersionTLS12, InsecureSkipVerify: true, NextProtos: []string{ALPN},
		Certificates: []tls.Certificate{b.id.Cert},
	})
	defer c.Close()
	if err := c.Handshake(); err == nil {
		t.Fatal("a TLS 1.2 client completed the handshake")
	}
	if r := wait(t, srv); r.err == nil {
		t.Fatal("the server accepted TLS 1.2")
	}
}

// CHAN-02: a client that offers no ALPN is refused.
func TestMissingALPNIsRefused(t *testing.T) {
	a, b := newDevice(t), newDevice(t)
	addr, srv := serve(t, Config{Identity: a.id, AllowUnknown: true})
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c := tls.Client(raw, &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, Certificates: []tls.Certificate{b.id.Cert}})
	defer c.Close()
	_ = c.Handshake()
	if r := wait(t, srv); !errors.Is(r.err, ErrNoALPN) {
		t.Fatalf("server error = %v, want ErrNoALPN", r.err)
	}
}

// CHAN-01: a plaintext client never reaches the protocol.
func TestPlaintextClientIsRefused(t *testing.T) {
	a := newDevice(t)
	addr, srv := serve(t, Config{Identity: a.id, AllowUnknown: true, HandshakeTimeout: time.Second})
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	_, _ = raw.Write([]byte(`{"type":"pair_request","pairing_code":"123-456"}` + "\n"))
	if r := wait(t, srv); r.err == nil {
		t.Fatal("a plaintext client was accepted")
	}
}

// CHAN-05: a server that cuts the handshake gives an error; the client never falls back.
func TestCutHandshakeHasNoFallback(t *testing.T) {
	b := newDevice(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan []byte, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 512)
		n, _ := raw.Read(buf) // the ClientHello
		got <- buf[:n]
		raw.Close() // cut
		// A fallback would open a second connection: none must come.
		_ = ln.(*net.TCPListener).SetDeadline(time.Now().Add(500 * time.Millisecond))
		if extra, err := ln.Accept(); err == nil {
			extra.Close()
			got <- []byte("second connection")
		}
		close(got)
	}()
	if _, err := Dial(context.Background(), ln.Addr().String(), Config{Identity: b.id, AllowUnknown: true}); err == nil {
		t.Fatal("the handshake succeeded against a server that cut it")
	}
	first := <-got
	if len(first) == 0 || first[0] != 0x16 {
		t.Fatalf("first bytes are not a TLS handshake record: %q", first)
	}
	for extra := range got {
		t.Fatalf("unexpected: %s (plaintext fallback?)", extra)
	}
}

// LIM-02: a client that never finishes the handshake is cut at the timeout.
func TestHandshakeTimeout(t *testing.T) {
	a := newDevice(t)
	addr, srv := serve(t, Config{Identity: a.id, AllowUnknown: true, HandshakeTimeout: 300 * time.Millisecond})
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	start := time.Now()
	r := wait(t, srv)
	if r.err == nil || !strings.Contains(r.err.Error(), "handshake") {
		t.Fatalf("server error = %v, want a handshake timeout", r.err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("timeout took %v", d)
	}
}
