package securechan

import (
	"bytes"
	"context"
	"testing"
)

// connect runs one handshake on loopback and returns the client's and the server's ends.
func connect(t *testing.T, server, client Config) (*Conn, *Conn) {
	t.Helper()
	addr, srv := serve(t, server)
	c, err := Dial(context.Background(), addr, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	s := wait(t, srv)
	if s.err != nil {
		t.Fatal(s.err)
	}
	return c, s.conn
}

func binding(t *testing.T, c *Conn) []byte {
	t.Helper()
	b, err := c.ChannelBinding()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != ChannelBindingLen {
		t.Fatalf("binding of %d bytes, want %d", len(b), ChannelBindingLen)
	}
	return b
}

// PAIR-03: both ends of one connection get the same binding, in state Unauth too (pairing
// runs there), and it is the exporter of RFC 8446 §7.5 under the pairing's label.
func TestChannelBindingIsSharedByBothEnds(t *testing.T) {
	a, b := newDevice(t), newDevice(t)
	c, s := connect(t, Config{Identity: a.id, AllowUnknown: true}, Config{Identity: b.id, AllowUnknown: true})
	if c.State() != Unauth || s.State() != Unauth {
		t.Fatalf("states %v / %v, want UNAUTH / UNAUTH", c.State(), s.State())
	}
	bc, bs := binding(t, c), binding(t, s)
	if !bytes.Equal(bc, bs) {
		t.Fatalf("the two ends disagree:\n%x\n%x", bc, bs)
	}
	if bytes.Equal(bc, make([]byte, ChannelBindingLen)) {
		t.Fatal("zero binding")
	}
	cs := c.ConnectionState()
	other, err := cs.ExportKeyingMaterial("EXPORTER-medxfer-v1-other", nil, ChannelBindingLen)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(other, bc) {
		t.Fatal("the binding does not depend on its label")
	}
	if again := binding(t, c); !bytes.Equal(again, bc) {
		t.Fatal("the binding changed within a connection")
	}
}

// Two connections between the same devices get two bindings: a value from an old session
// never fits a new one.
func TestChannelBindingDiffersPerConnection(t *testing.T) {
	a, b := newDevice(t), newDevice(t)
	a.trustDevice(t, b)
	b.trustDevice(t, a)
	c1, _ := connect(t, Config{Identity: a.id, Trust: a.trust}, Config{Identity: b.id, Trust: b.trust})
	c2, _ := connect(t, Config{Identity: a.id, Trust: a.trust}, Config{Identity: b.id, Trust: b.trust})
	if bytes.Equal(binding(t, c1), binding(t, c2)) {
		t.Fatal("two connections share a binding")
	}
}

// A relay between a and b holds two TLS sessions, one with each: a and b see two different
// bindings, which is what makes a pairing through it fail (bible §6.2, "Pourquoi le MITM
// échoue").
func TestChannelBindingBreaksThroughARelay(t *testing.T) {
	a, b, m := newDevice(t), newDevice(t), newDevice(t)
	// a → m: m poses as the responder.
	aSide, mFacingA := connect(t, Config{Identity: m.id, AllowUnknown: true}, Config{Identity: a.id, AllowUnknown: true})
	// m → b: m poses as the initiator.
	mFacingB, bSide := connect(t, Config{Identity: b.id, AllowUnknown: true}, Config{Identity: m.id, AllowUnknown: true})
	ba, bb := binding(t, aSide), binding(t, bSide)
	if bytes.Equal(ba, bb) {
		t.Fatal("a and b share a binding through a relay")
	}
	// The relay knows both values, but can make neither equal to the other: each is fixed by
	// a session it shares with one honest side only.
	if !bytes.Equal(binding(t, mFacingA), ba) || !bytes.Equal(binding(t, mFacingB), bb) {
		t.Fatal("each session's ends must agree")
	}
}
