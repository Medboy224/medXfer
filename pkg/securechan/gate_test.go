package securechan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

// LIM-01: at most 5 new connections per minute from one IP; other IPs are not affected; the
// window slides.
func TestGateRateLimitPerIP(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := &Gate{MaxUnauth: 100, PerIPOneMinute: 5, now: func() time.Time { return now }}
	for i := 0; i < 5; i++ {
		adm, err := g.Admit(fakeAddr("10.0.0.1:4000"))
		if err != nil {
			t.Fatalf("connection %d refused: %v", i+1, err)
		}
		adm.Charge()
		adm.Charge() // counted once
		adm.Release()
	}
	if _, err := g.Admit(fakeAddr("10.0.0.1:4001")); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("6th connection in the minute: %v, want ErrRateLimited", err)
	}
	if _, err := g.Admit(fakeAddr("10.0.0.2:4000")); err != nil {
		t.Fatalf("another IP was limited: %v", err)
	}
	now = now.Add(61 * time.Second)
	if _, err := g.Admit(fakeAddr("10.0.0.1:4002")); err != nil {
		t.Fatalf("still limited after a minute: %v", err)
	}
}

// LIM-01: at most MaxUnauth places; a released place can be taken again, once.
func TestGateMaxUnauth(t *testing.T) {
	g := &Gate{MaxUnauth: 2, PerIPOneMinute: 100}
	r1, err := g.Admit(fakeAddr("10.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Admit(fakeAddr("10.0.0.2:1")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Admit(fakeAddr("10.0.0.3:1")); !errors.Is(err, ErrTooManyUnauth) {
		t.Fatalf("3rd place: %v, want ErrTooManyUnauth", err)
	}
	r1.Release()
	r1.Release() // a second release must not free another place
	if _, err := g.Admit(fakeAddr("10.0.0.3:1")); err != nil {
		t.Fatalf("released place not reusable: %v", err)
	}
	if _, err := g.Admit(fakeAddr("10.0.0.4:1")); !errors.Is(err, ErrTooManyUnauth) {
		t.Fatalf("double release freed a place: %v", err)
	}
}

// Through the server: unauthenticated connections hold their place until Authenticate or
// Close; a trusted peer never holds one.
func TestServerGate(t *testing.T) {
	a, b, c := newDevice(t), newDevice(t), newDevice(t)
	a.trustDevice(t, c)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	gate := &Gate{MaxUnauth: 1, PerIPOneMinute: 100}
	results := make(chan result, 8)
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				conn, err := Server(context.Background(), raw, Config{Identity: a.id, Trust: a.trust, AllowUnknown: true, Gate: gate})
				results <- result{conn, err}
			}()
		}
	}()
	dial := func(d *device) {
		t.Helper()
		if c, err := Dial(context.Background(), ln.Addr().String(), Config{Identity: d.id, AllowUnknown: true}); err == nil {
			t.Cleanup(func() { c.Close() })
		}
	}

	dial(b) // unknown: takes the only place
	first := wait(t, results)
	if first.err != nil || first.conn.State() != Unauth {
		t.Fatalf("first connection: %v", first.err)
	}
	dial(b)
	if r := wait(t, results); !errors.Is(r.err, ErrTooManyUnauth) {
		t.Fatalf("second unauthenticated connection: %v, want ErrTooManyUnauth", r.err)
	}
	dial(c) // trusted peers are refused too while the gate is full: they are not known yet
	if r := wait(t, results); !errors.Is(r.err, ErrTooManyUnauth) {
		t.Fatalf("connection while full: %v, want ErrTooManyUnauth", r.err)
	}
	first.conn.Authenticate() // place given back
	dial(c)
	r := wait(t, results)
	if r.err != nil || r.conn.State() != Auth {
		t.Fatalf("trusted peer after Authenticate: %v", r.err)
	}
	dial(b) // the trusted peer did not keep a place
	if r := wait(t, results); r.err != nil {
		t.Fatalf("place not free after a trusted handshake: %v", r.err)
	}
}

// LIM-01 limits unauthenticated connections: an admission that is never charged (its peer was
// authenticated by its key during the handshake) does not use the address's quota.
func TestGateDoesNotChargeAuthenticatedConnections(t *testing.T) {
	g := &Gate{MaxUnauth: 100, PerIPOneMinute: 5}
	for i := 0; i < 50; i++ {
		adm, err := g.Admit(fakeAddr("10.0.0.1:4000"))
		if err != nil {
			t.Fatalf("authenticated connection %d refused: %v", i+1, err)
		}
		adm.Release()
	}
}

// Through the server: a trusted peer opens more connections than the per-IP quota (the data
// connections of a transfer on the single port); unknown keys are still limited.
func TestServerGateQuotaIsForUnauthenticated(t *testing.T) {
	a, trusted, stranger := newDevice(t), newDevice(t), newDevice(t)
	a.trustDevice(t, trusted)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	gate := &Gate{MaxUnauth: 100, PerIPOneMinute: 2}
	results := make(chan result, 32)
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				conn, err := Server(context.Background(), raw, Config{Identity: a.id, Trust: a.trust, AllowUnknown: true, Gate: gate})
				if conn != nil {
					defer conn.Close()
				}
				results <- result{conn, err}
			}()
		}
	}()
	for i := 0; i < 8; i++ {
		c, err := Dial(context.Background(), ln.Addr().String(), Config{Identity: trusted.id, AllowUnknown: true})
		if err != nil {
			t.Fatalf("trusted connection %d: %v", i+1, err)
		}
		c.Close()
		if r := wait(t, results); r.err != nil || r.conn.State() != Auth {
			t.Fatalf("trusted connection %d on the server: %v", i+1, r.err)
		}
	}
	for i := 0; i < 2; i++ {
		if c, err := Dial(context.Background(), ln.Addr().String(), Config{Identity: stranger.id, AllowUnknown: true}); err == nil {
			c.Close()
		}
		if r := wait(t, results); r.err != nil {
			t.Fatalf("unknown connection %d: %v", i+1, r.err)
		}
	}
	if c, err := Dial(context.Background(), ln.Addr().String(), Config{Identity: stranger.id, AllowUnknown: true}); err == nil {
		c.Close()
	}
	if r := wait(t, results); !errors.Is(r.err, ErrRateLimited) {
		t.Fatalf("3rd unknown connection in the minute: %v, want ErrRateLimited", r.err)
	}
}

// Unread gives bytes back before the connection's own.
func TestUnread(t *testing.T) {
	a, b := newDevice(t), newDevice(t)
	addr, srv := serve(t, Config{Identity: a.id, Expected: b.id.Fingerprint})
	c, err := Dial(context.Background(), addr, Config{Identity: b.id, Expected: a.id.Fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := wait(t, srv)
	if s.err != nil {
		t.Fatal(s.err)
	}
	go func() { _, _ = c.Write([]byte("world")) }()
	s.conn.Unread([]byte("hello "))
	got := make([]byte, 11)
	if _, err := io.ReadFull(s.conn, got); err != nil || string(got) != "hello world" {
		t.Fatalf("read %q, %v", got, err)
	}
}

// #93: an address that authenticated recently gets the larger quota for KnownFor; other
// addresses keep the small one; the larger quota still ends.
func TestGateKnownAddressGetsLargerQuota(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := &Gate{MaxUnauth: 1000, PerIPOneMinute: 5, KnownPerIPOneMinute: 60, KnownFor: 10 * time.Minute,
		now: func() time.Time { return now }}
	adm, err := g.Admit(fakeAddr("10.0.0.1:4000"))
	if err != nil {
		t.Fatal(err)
	}
	adm.Authenticated()
	adm.Release()
	fail := func(addr string, n int) error {
		for i := 0; i < n; i++ {
			adm, err := g.Admit(fakeAddr(addr))
			if err != nil {
				return err
			}
			adm.Charge() // a failed handshake
			adm.Release()
		}
		return nil
	}
	if err := fail("10.0.0.1:4001", 60); err != nil {
		t.Fatalf("known address within its quota: %v", err)
	}
	if err := fail("10.0.0.1:4002", 1); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("61st failed handshake in the minute: %v, want ErrRateLimited", err)
	}
	if err := fail("10.0.0.2:4000", 5); err != nil {
		t.Fatal(err)
	}
	if err := fail("10.0.0.2:4001", 1); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("an unknown address got the larger quota: %v", err)
	}
	now = now.Add(11 * time.Minute) // KnownFor is over: back to the small quota
	if err := fail("10.0.0.1:4003", 5); err != nil {
		t.Fatal(err)
	}
	if err := fail("10.0.0.1:4004", 1); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("the larger quota did not end: %v", err)
	}
}

// #93 through the server: a device that paired (Authenticate) is not locked out by the
// handshakes that fail after it, from its address.
func TestServerGateRemembersPairedAddress(t *testing.T) {
	a, b := newDevice(t), newDevice(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	gate := &Gate{MaxUnauth: 100, PerIPOneMinute: 2, KnownPerIPOneMinute: 20, KnownFor: time.Minute}
	results := make(chan result, 32)
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				conn, err := Server(context.Background(), raw, Config{Identity: a.id, AllowUnknown: true, Gate: gate,
					HandshakeTimeout: 300 * time.Millisecond})
				results <- result{conn, err}
			}()
		}
	}()
	c, err := Dial(context.Background(), ln.Addr().String(), Config{Identity: b.id, AllowUnknown: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := wait(t, results)
	if s.err != nil || s.conn.State() != Unauth {
		t.Fatalf("first connection: %v", s.err)
	}
	s.conn.Authenticate()    // the pairing succeeded
	for i := 0; i < 5; i++ { // handshakes cut before they start: the Wi-Fi drops, the app restarts
		raw, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		raw.Close()
		if r := wait(t, results); r.err == nil || errors.Is(r.err, ErrRateLimited) {
			t.Fatalf("cut handshake %d: %v, want a handshake error", i+1, r.err)
		}
	}
	c2, err := Dial(context.Background(), ln.Addr().String(), Config{Identity: b.id, AllowUnknown: true})
	if err != nil {
		t.Fatalf("the paired device is locked out: %v", err)
	}
	defer c2.Close()
	if r := wait(t, results); r.err != nil {
		t.Fatalf("the paired device is locked out: %v", r.err)
	}
}

// Audit 5a (#156): handshakes still running count in their address's quota, or one address
// could take every place of the gate before any of them is charged.
func TestGateCountsHandshakesInProgress(t *testing.T) {
	g := &Gate{MaxUnauth: 16, PerIPOneMinute: 5}
	var held []*Admission
	for i := 0; i < 5; i++ {
		adm, err := g.Admit(fakeAddr("10.0.0.1:4000"))
		if err != nil {
			t.Fatalf("handshake %d refused: %v", i+1, err)
		}
		held = append(held, adm)
	}
	if _, err := g.Admit(fakeAddr("10.0.0.1:4001")); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("6th handshake at the same time from one address: %v, want ErrRateLimited", err)
	}
	if _, err := g.Admit(fakeAddr("10.0.0.2:4000")); err != nil {
		t.Fatalf("another address found no place: %v", err)
	}
	// An authenticated handshake gives its place back without using the quota.
	held[0].Release()
	if _, err := g.Admit(fakeAddr("10.0.0.1:4002")); err != nil {
		t.Fatalf("place of an authenticated handshake not given back: %v", err)
	}
	// A charged handshake moves from "in progress" to the minute's count: no double count.
	held[1].Charge()
	held[1].Release()
	if _, err := g.Admit(fakeAddr("10.0.0.1:4003")); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("charged handshake: %v, want ErrRateLimited (4 in progress + 1 this minute)", err)
	}
}

// Audit 5b (#156): an address whose minute is over leaves the gate's tables.
func TestGateForgetsQuietAddresses(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := &Gate{MaxUnauth: 100, PerIPOneMinute: 5, now: func() time.Time { return now }}
	for i := 0; i < 3; i++ {
		adm, err := g.Admit(fakeAddr(fmt.Sprintf("10.0.0.%d:4000", i+1)))
		if err != nil {
			t.Fatal(err)
		}
		adm.Charge()
		adm.Release()
	}
	now = now.Add(61 * time.Second)
	adm, err := g.Admit(fakeAddr("10.0.0.9:4000"))
	if err != nil {
		t.Fatal(err)
	}
	adm.Release()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.recent) != 0 || len(g.handshakes) != 0 {
		t.Fatalf("tables still hold %d and %d addresses", len(g.recent), len(g.handshakes))
	}
}
