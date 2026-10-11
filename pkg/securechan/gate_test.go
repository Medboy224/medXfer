package securechan

import (
	"context"
	"errors"
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
		release, err := g.Admit(fakeAddr("10.0.0.1:4000"))
		if err != nil {
			t.Fatalf("connection %d refused: %v", i+1, err)
		}
		release()
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
	r1()
	r1() // a second release must not free another place
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
