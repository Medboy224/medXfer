package pairing

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
)

// serveOnDemand answers PIN demands on a loopback listener with PINs from d, as a node or the
// core does for a device picked in a list, and returns its address.
func serveOnDemand(t *testing.T, responder Options, d *PINOnDemand) string {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				conn, err := securechan.Server(context.Background(), raw, securechan.Config{Identity: responder.Identity, AllowUnknown: true})
				if err != nil {
					return
				}
				ch := session.NewChannel(conn)
				defer ch.Close()
				m, err := ch.Read()
				if err != nil || m.Type != TypePINWanted {
					return
				}
				name, err := AnswerPINWanted(ch, conn, d.Get, responder.Identity.Fingerprint)
				if err != nil {
					_ = ch.Send(session.Message{Type: TypeReject, FileName: ErrRefused.Error()})
					return
				}
				_ = ch.Send(session.Message{Type: TypeAccept, DeviceName: responder.DeviceName + " for " + name})
			}()
		}
	}()
	return ln.Addr().String()
}

// typedFrom types the PIN that the responder's screen shows after the demand, or code when set.
func typedFrom(t *testing.T, screen <-chan shown, code string) func() (string, error) {
	return func() (string, error) {
		s := next(t, screen)
		if code != "" {
			return code, nil
		}
		return s.code, nil
	}
}

func onDemand(t *testing.T, c PINConfig) (*PINOnDemand, <-chan shown) {
	t.Helper()
	screen := make(chan shown, 64)
	c.Show = func(code string, expires time.Time) { screen <- shown{code, expires} }
	return NewPINOnDemand(c), screen
}

// PAIR-11: a device picked in a list shows a PIN when asked; the one that picked it types it
// and the pairing goes on as the PIN path. No PIN is shown before the demand.
func TestPINOnDemandPairs(t *testing.T) {
	responder, initiator := device(t, "pc"), device(t, "phone")
	d, screen := onDemand(t, PINConfig{})
	addr := serveOnDemand(t, responder, d)
	select {
	case s := <-screen:
		t.Fatalf("a PIN %q was shown before any demand", s.code)
	default:
	}
	r, err := JoinPINOnDemand(context.Background(), addr, typedFrom(t, screen, ""), initiator)
	if err != nil {
		t.Fatal(err)
	}
	if r.Fingerprint != responder.Identity.Fingerprint || r.Name != "pc for phone" {
		t.Fatalf("paired with %+v", r)
	}
}

// PAIR-04: asking again does not draw a new PIN. A wrong PIN costs an attempt on the PIN on
// screen, and the next demand gets that same PIN, so a guesser gains nothing by asking.
func TestPINOnDemandKeepsItsPINAndAttempts(t *testing.T) {
	responder, initiator := device(t, "pc"), device(t, "phone")
	d, screen := onDemand(t, PINConfig{Attempts: 2})
	addr := serveOnDemand(t, responder, d)
	var first string
	typed := func() (string, error) {
		select {
		case s := <-screen:
			first = s.code
		case <-time.After(100 * time.Millisecond): // already on screen: no new PIN shown
		}
		return wrong(first), nil
	}
	if _, err := JoinPINOnDemand(context.Background(), addr, typed, initiator); !errors.Is(err, ErrRefused) {
		t.Fatalf("wrong PIN: %v, want ErrRefused", err)
	}
	if _, err := JoinPINOnDemand(context.Background(), addr, typed, initiator); !errors.Is(err, ErrRefused) {
		t.Fatalf("wrong PIN again: %v, want ErrRefused", err)
	}
	// Two attempts were allowed: the PIN is destroyed, and the next one is shown.
	if s := next(t, screen); s.code == first || s.code == "" {
		t.Fatalf("after its attempts, the PIN shown is %q (was %q)", s.code, first)
	}
}

// PAIR-03, PAIR-05: through a relay, the right PIN still fails: the TLS sessions differ.
func TestPINOnDemandThroughARelayFails(t *testing.T) {
	responder, initiator, relay := device(t, "pc"), device(t, "phone"), device(t, "relay")
	d, screen := onDemand(t, PINConfig{})
	addr := serveOnDemand(t, responder, d)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		in, err := securechan.Server(context.Background(), raw, securechan.Config{Identity: relay.Identity, AllowUnknown: true})
		if err != nil {
			return
		}
		inCh := session.NewChannel(in)
		defer inCh.Close()
		_, outCh, _, err := dial(context.Background(), addr, securechan.Config{Identity: relay.Identity, AllowUnknown: true})
		if err != nil {
			return
		}
		defer outCh.Close()
		go func() {
			for {
				m, err := outCh.Read()
				if err != nil || inCh.Send(m) != nil {
					inCh.Close()
					return
				}
			}
		}()
		for {
			m, err := inCh.Read()
			if err != nil || outCh.Send(m) != nil {
				return
			}
		}
	}()
	if _, err := JoinPINOnDemand(context.Background(), ln.Addr().String(), typedFrom(t, screen, ""), initiator); !errors.Is(err, ErrRefused) {
		t.Fatalf("through a relay: %v, want ErrRefused", err)
	}
	if initiator.Trust.Contains(relay.Identity.Fingerprint) || responder.Trust.Contains(relay.Identity.Fingerprint) {
		t.Fatal("the relay is trusted")
	}
}

// #105: once too many PINs were destroyed, demands draw no more PINs until the user starts
// over; an expired PIN gives way to a new one.
func TestPINOnDemandStopsAndRenews(t *testing.T) {
	d, _ := onDemand(t, PINConfig{Attempts: 1, MaxSeries: 1})
	for i := 0; i < pinDestroyedIn; i++ {
		p, err := d.Get()
		if err != nil {
			t.Fatalf("PIN %d: %v", i, err)
		}
		if _, ok := p.begin(); !ok {
			t.Fatalf("PIN %d: no attempt", i)
		}
		p.end(false)
	}
	for i := 0; i < 2; i++ {
		if _, err := d.Get(); !errors.Is(err, ErrTooManyPINs) {
			t.Fatalf("after the series: %v, want ErrTooManyPINs", err)
		}
	}
	d.Reset()
	if _, err := d.Get(); err != nil {
		t.Fatalf("after Reset: %v", err)
	}

	short, _ := onDemand(t, PINConfig{TTL: 50 * time.Millisecond})
	p1, err := short.Get()
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := short.Get(); again != p1 {
		t.Fatal("a valid PIN was drawn again")
	}
	time.Sleep(80 * time.Millisecond)
	if p2, err := short.Get(); err != nil || p2 == p1 {
		t.Fatalf("expired PIN: %v, same: %v", err, p2 == p1)
	}
}

// A one-shot send shows its PIN at once (D2): a device that picks it in a list and asks for a
// PIN gets that one.
func TestServeAnswersADemandWithThePINShown(t *testing.T) {
	responder, initiator := device(t, "pc"), device(t, "phone")
	addr, _, code, done := servePIN(t, responder, PINConfig{})
	r, err := JoinPINOnDemand(context.Background(), addr, func() (string, error) { return code, nil }, initiator)
	if err != nil {
		t.Fatal(err)
	}
	if r.Fingerprint != responder.Identity.Fingerprint {
		t.Fatalf("paired with %s", r.Fingerprint.Short())
	}
	if s := <-done; s.err != nil || s.r.Fingerprint != initiator.Identity.Fingerprint {
		t.Fatalf("responder: %+v, %v", s.r, s.err)
	}
}

// Audit §3.5 (#156): a PIN that is no longer live (used, expired, closed) is not announced: the
// device that asked is refused at once, without being told to type anything.
func TestDemandForADeadPINIsRefused(t *testing.T) {
	responder, initiator := device(t, "pc"), device(t, "phone")
	pin, err := NewPIN(PINConfig{})
	if err != nil {
		t.Fatal(err)
	}
	pin.Close()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	responder.PIN = pin
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, _ = Serve(ctx, ln, nil, responder) }()
	typed := false
	_, err = JoinPINOnDemand(context.Background(), ln.Addr().String(), func() (string, error) {
		typed = true
		return "000000", nil
	}, initiator)
	if typed {
		t.Fatal("the initiator was asked to type a PIN that is not live")
	}
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("JoinPINOnDemand = %v, want ErrRefused", err)
	}
}
