package pairing

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/pake"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
)

// shown is what a PIN put on screen.
type shown struct {
	code    string
	expires time.Time
}

// newPIN makes a PIN whose screen is a channel.
func newPIN(t *testing.T, c PINConfig) (*PIN, <-chan shown) {
	t.Helper()
	screen := make(chan shown, 64)
	c.Show = func(code string, expires time.Time) { screen <- shown{code, expires} }
	p, err := NewPIN(c)
	if err != nil {
		t.Fatal(err)
	}
	return p, screen
}

func next(t *testing.T, screen <-chan shown) shown {
	t.Helper()
	select {
	case s := <-screen:
		return s
	case <-time.After(10 * time.Second):
		t.Fatal("nothing shown")
		return shown{}
	}
}

// servePIN serves a PIN on a loopback listener and returns its address and current code.
func servePIN(t *testing.T, responder Options, c PINConfig) (addr string, screen <-chan shown, first string, done <-chan served) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	p, screen := newPIN(t, c)
	responder.PIN = p
	return ln.Addr().String(), screen, next(t, screen).code, serve(ln, nil, responder)
}

// wrong is a PIN other than code, of the same length.
func wrong(code string) string {
	b := []byte(code)
	b[0] = '0' + (b[0]-'0'+1)%10
	return string(b)
}

// Bible §6.2: the PIN typed on the initiator pairs both devices, each trusting the other under
// the name sent after the confirmations.
func TestPINPairsBothWays(t *testing.T) {
	responder, initiator := device(t, "pc"), device(t, "phone")
	addr, _, code, done := servePIN(t, responder, PINConfig{})
	r, err := JoinPIN(context.Background(), addr, FormatPIN(code), initiator)
	if err != nil {
		t.Fatal(err)
	}
	if r.Fingerprint != responder.Identity.Fingerprint || r.Name != "pc" {
		t.Fatalf("initiator paired with %+v", r)
	}
	s := <-done
	if s.err != nil || s.r.Fingerprint != initiator.Identity.Fingerprint || s.r.Name != "phone" {
		t.Fatalf("responder: %+v, %v", s.r, s.err)
	}
	if p, ok := responder.Trust.Get(initiator.Identity.Fingerprint); !ok || p.Alias != "phone" {
		t.Fatal("the responder does not trust the initiator")
	}
	if p, ok := initiator.Trust.Get(responder.Identity.Fingerprint); !ok || p.Alias != "pc" {
		t.Fatal("the initiator does not trust the responder")
	}
}

// PAIR-07: a wrong PIN gets a neutral refusal, nothing is trusted, and the responder waits on.
func TestWrongPINIsRefusedAndWaitGoesOn(t *testing.T) {
	responder, initiator := device(t, "pc"), device(t, "phone")
	addr, _, code, done := servePIN(t, responder, PINConfig{})
	attacker := device(t, "attacker")
	if _, err := JoinPIN(context.Background(), addr, wrong(code), attacker); !errors.Is(err, ErrRefused) {
		t.Fatalf("wrong PIN: %v, want ErrRefused", err)
	}
	if responder.Trust.Contains(attacker.Identity.Fingerprint) || attacker.Trust.Contains(responder.Identity.Fingerprint) {
		t.Fatal("a wrong PIN left a trust behind")
	}
	if _, err := JoinPIN(context.Background(), addr, code, initiator); err != nil {
		t.Fatalf("right PIN after a wrong one: %v", err)
	}
	if s := <-done; s.err != nil || s.r.Fingerprint != initiator.Identity.Fingerprint {
		t.Fatalf("responder: %+v, %v", s.r, s.err)
	}
}

// PAIR-04: after 3 wrong PINs the PIN is destroyed and another is shown; the old one, even
// right, no longer pairs (the 4th attempt of the bible's test list).
func TestThreeFailuresDestroyThePIN(t *testing.T) {
	responder, initiator := device(t, "pc"), device(t, "phone")
	addr, screen, code, done := servePIN(t, responder, PINConfig{})
	for i := 0; i < PINAttempts; i++ {
		if _, err := JoinPIN(context.Background(), addr, wrong(code), device(t, "attacker")); !errors.Is(err, ErrRefused) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	fresh := next(t, screen)
	if fresh.code == "" || fresh.code == code {
		t.Fatalf("after %d failures the screen shows %q", PINAttempts, fresh.code)
	}
	if _, err := JoinPIN(context.Background(), addr, code, device(t, "late")); !errors.Is(err, ErrRefused) {
		t.Fatalf("the destroyed PIN: %v, want ErrRefused", err)
	}
	if _, err := JoinPIN(context.Background(), addr, fresh.code, initiator); err != nil {
		t.Fatalf("the new PIN: %v", err)
	}
	if s := <-done; s.err != nil {
		t.Fatal(s.err)
	}
}

// PAIR-07: a PIN with no attempt left answers like a wrong one. The peer sees pair_pin_reply,
// then pair_reject, whether its guess was wrong or could not be tested at all; no message
// carries a name.
func TestNoAttemptLeftLooksLikeAWrongPIN(t *testing.T) {
	responder := device(t, "pc")
	addr, screen, code, _ := servePIN(t, responder, PINConfig{Attempts: 1, Delay: time.Hour})
	seen := func(pin string) []string {
		var types []string
		for _, m := range rawPIN(t, addr, device(t, "attacker"), pin, true) {
			types = append(types, m.Type)
			if m.DeviceName != "" {
				t.Fatalf("%s carries the name %q", m.Type, m.DeviceName)
			}
		}
		return types
	}
	want := []string{TypePINReply, TypeReject}
	// Attempts is 1: each wrong PIN is destroyed; the third in a row starts the delay.
	for i := 0; i < pinDestroyedIn; i++ {
		if got := seen(wrong(code)); !slices.Equal(got, want) {
			t.Fatalf("wrong PIN %d: %v, want %v", i+1, got, want)
		}
		code = next(t, screen).code
	}
	if code != "" {
		t.Fatalf("a PIN %q is shown during the delay", code)
	}
	if got := seen("123456"); !slices.Equal(got, want) {
		t.Fatalf("no PIN valid: %v, want %v (the same as a wrong PIN)", got, want)
	}
}

// The attempt counts from the responder's reply: a peer that tests its guess and leaves
// without confirming has used it (PAIR-04).
func TestSilentAttemptCounts(t *testing.T) {
	responder := device(t, "pc")
	addr, screen, code, _ := servePIN(t, responder, PINConfig{Attempts: 1})
	if got := rawPIN(t, addr, device(t, "attacker"), wrong(code), false); len(got) != 1 || got[0].Type != TypePINReply {
		t.Fatalf("got %+v", got)
	}
	if s := next(t, screen); s.code == "" || s.code == code {
		t.Fatalf("after an attempt left unconfirmed the screen shows %q, want a new PIN", s.code)
	}
}

// PAIR-03, PAIR-05: a relay between the two devices, with its own key on each side, passes
// the PAKE messages unchanged; the PIN is right, yet neither side confirms, because the keys
// and the TLS sessions differ on each side (bible §6.2, "Pourquoi le MITM échoue").
func TestPINThroughARelayFails(t *testing.T) {
	responder, initiator, relay := device(t, "pc"), device(t, "phone"), device(t, "relay")
	addr, screen, code, _ := servePIN(t, responder, PINConfig{Attempts: 1})
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
	if _, err := JoinPIN(context.Background(), ln.Addr().String(), code, initiator); !errors.Is(err, ErrRefused) {
		t.Fatalf("through a relay: %v, want ErrRefused", err)
	}
	if s := next(t, screen); s.code == code {
		t.Fatal("the relayed attempt was not counted")
	}
	if initiator.Trust.Contains(relay.Identity.Fingerprint) || responder.Trust.Contains(relay.Identity.Fingerprint) {
		t.Fatal("the relay is trusted")
	}
}

// D9: a suite the responder does not know is refused, without using an attempt.
func TestUnknownSuiteIsRefused(t *testing.T) {
	responder := device(t, "pc")
	addr, screen, _, _ := servePIN(t, responder, PINConfig{Attempts: 1})
	initiator := device(t, "phone")
	_, ch, _, err := dial(context.Background(), addr, securechan.Config{Identity: initiator.Identity, AllowUnknown: true})
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	if err := ch.Send(session.Message{Type: TypePIN, Suite: "SPAKE2-OF-THE-FUTURE", Share: make([]byte, 32)}); err != nil {
		t.Fatal(err)
	}
	if m, err := ch.Read(); err != nil || m.Type != TypeReject {
		t.Fatalf("got %+v, %v; want pair_reject", m, err)
	}
	select {
	case s := <-screen:
		t.Fatalf("the PIN changed (%q): the refusal used an attempt", s.code)
	case <-time.After(200 * time.Millisecond):
	}
}

// PAIR-02: an unused PIN expires, and so does the wait.
func TestServePINExpires(t *testing.T) {
	responder := device(t, "pc")
	start := time.Now()
	_, _, _, done := servePIN(t, responder, PINConfig{TTL: 300 * time.Millisecond})
	if s := <-done; !errors.Is(s.err, ErrExpired) {
		t.Fatalf("Serve = %v, want ErrExpired", s.err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("expiry took too long")
	}
}

// D5: a delay after each series of 3 PINs destroyed, doubled at each series up to its cap,
// and reset by a pairing.
func TestPINDelayDoublesAndResets(t *testing.T) {
	const delay = 150 * time.Millisecond
	p, screen := newPIN(t, PINConfig{Attempts: 1, Delay: delay, MaxDelay: 2 * delay, MaxSeries: -1})
	defer p.Close()
	next(t, screen)
	fail := func() {
		t.Helper()
		if _, ok := p.begin(); !ok {
			t.Fatal("no attempt available")
		}
		p.end(false)
	}
	series := func(want time.Duration) {
		t.Helper()
		for i := 0; i < pinDestroyedIn-1; i++ {
			fail()
			if s := next(t, screen); s.code == "" {
				t.Fatal("a delay before the series ends")
			}
		}
		start := time.Now()
		fail()
		if s := next(t, screen); s.code != "" {
			t.Fatalf("no delay after %d PINs destroyed", pinDestroyedIn)
		}
		if _, ok := p.begin(); ok {
			t.Fatal("an attempt during the delay")
		}
		if s := next(t, screen); s.code == "" {
			t.Fatal("no PIN after the delay")
		}
		if d := time.Since(start); d < want {
			t.Fatalf("delay %v, want %v", d, want)
		}
	}
	series(delay)
	series(2 * delay)
	series(2 * delay) // capped
	if _, ok := p.begin(); !ok {
		t.Fatal("no attempt")
	}
	p.end(true)
	p.mu.Lock()
	reset := p.delay == delay && p.destroyed == 0
	p.mu.Unlock()
	if !reset {
		t.Fatal("a pairing does not reset the delay")
	}
	if _, ok := p.begin(); ok {
		t.Fatal("a PIN paired twice (PAIR-02)")
	}
}

// PAIR-04 with concurrent connections: no more attempts than allowed are begun on one PIN.
func TestPINAttemptsAreReserved(t *testing.T) {
	p, _ := newPIN(t, PINConfig{})
	defer p.Close()
	for i := 0; i < PINAttempts; i++ {
		if _, ok := p.begin(); !ok {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	if _, ok := p.begin(); ok {
		t.Fatal("a 4th attempt began while 3 are running")
	}
}

// PAIR-01: digits only, the requested length, leading zeros kept.
func TestDrawPIN(t *testing.T) {
	for _, n := range []int{6, 8} {
		code, err := drawPIN(bytes.NewReader(make([]byte, 64)), n)
		if err != nil || string(code) != strings.Repeat("0", n) {
			t.Fatalf("drawPIN(zeros, %d) = %q, %v", n, code, err)
		}
	}
	for i := 0; i < 1000; i++ {
		code, err := drawPIN(rand.Reader, PINDigits)
		if err != nil || len(code) != PINDigits || strings.Trim(string(code), "0123456789") != "" {
			t.Fatalf("drawPIN = %q, %v", code, err)
		}
	}
	if _, err := NewPIN(PINConfig{Digits: 4}); !errors.Is(err, ErrBadPIN) {
		t.Fatalf("a 4-digit PIN: %v", err)
	}
}

func TestNormalizeAndFormatPIN(t *testing.T) {
	for in, want := range map[string]string{"123 456": "123456", "123-456": "123456", "1234 5678": "12345678", "012345": "012345"} {
		if got, err := NormalizePIN(in); err != nil || string(got) != want {
			t.Fatalf("NormalizePIN(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"12345", "123456789", "12a456", "", "１２３４５６"} {
		if _, err := NormalizePIN(in); !errors.Is(err, ErrBadPIN) {
			t.Fatalf("NormalizePIN(%q) accepted", in)
		}
	}
	if got := FormatPIN("123456"); got != "123 456" {
		t.Fatal(got)
	}
	if got := FormatPIN("12345678"); got != "1234 5678" {
		t.Fatal(got)
	}
}

// rawPIN runs the initiator's side by hand and returns the messages the responder sent.
// confirm sends a confirmation (a wrong one when the reply does not verify).
func rawPIN(t *testing.T, addr string, initiator Options, pin string, confirm bool) []session.Message {
	t.Helper()
	conn, ch, _, err := dial(context.Background(), addr, securechan.Config{Identity: initiator.Identity, AllowUnknown: true})
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	ekm, err := conn.ChannelBinding()
	if err != nil {
		t.Fatal(err)
	}
	ex, err := pake.New(PINSuite, pake.Params{Password: []byte(pin), CI: pinCI(initiator.Identity.Fingerprint, conn.PeerFingerprint()),
		SID: ekm, Role: pake.Initiator})
	if err != nil {
		t.Fatal(err)
	}
	if err := ch.Send(session.Message{Type: TypePIN, Suite: PINSuite, Share: ex.Share()}); err != nil {
		t.Fatal(err)
	}
	var got []session.Message
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		m, err := ch.Read()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) && len(got) == 0 {
				t.Fatal(err)
			}
			return got
		}
		got = append(got, m)
		if m.Type != TypePINReply {
			return got
		}
		if !confirm {
			return got
		}
		tag := make([]byte, 64)
		if s, err := ex.Finish(m.Share); err == nil {
			tag = s.Tag()
		}
		if err := ch.Send(session.Message{Type: TypePINConfirm, Confirm: tag}); err != nil {
			return got
		}
	}
}

// #105: after MaxSeries series of PINs destroyed in a row, the PIN stops for good: the screen
// says so (code "" and a zero time), no attempt is begun any more, and none is drawn.
func TestPINStopsAfterMaxSeries(t *testing.T) {
	p, screen := newPIN(t, PINConfig{Attempts: 1, Delay: 50 * time.Millisecond})
	defer p.Close()
	next(t, screen)
	for series := 1; series <= PINMaxSeries; series++ {
		for i := 0; i < pinDestroyedIn; i++ {
			if _, ok := p.begin(); !ok {
				t.Fatalf("series %d, PIN %d: no attempt", series, i+1)
			}
			p.end(false)
			s := next(t, screen)
			last := i == pinDestroyedIn-1
			switch {
			case !last && s.code == "":
				t.Fatalf("series %d: no new PIN", series)
			case last && series < PINMaxSeries && (s.code != "" || s.expires.IsZero()):
				t.Fatalf("series %d: no delay (%+v)", series, s)
			case last && series < PINMaxSeries:
				if s := next(t, screen); s.code == "" {
					t.Fatal("no PIN after the delay")
				}
			case last && (s.code != "" || !s.expires.IsZero()):
				t.Fatalf("after %d series: %+v, want the stop", series, s)
			}
		}
	}
	select {
	case <-p.stopped:
	default:
		t.Fatal("the PIN is not stopped")
	}
	if _, ok := p.begin(); ok {
		t.Fatal("an attempt after the stop")
	}
	select {
	case s := <-screen:
		t.Fatalf("shown after the stop: %+v", s)
	case <-time.After(200 * time.Millisecond):
	}
}

// #105: Serve stops at once with ErrTooManyPINs, a QR code offered beside the PIN included.
func TestServeStopsAfterTooManyPINs(t *testing.T) {
	responder := device(t, "pc")
	ln, inv, _ := listen(t, responder, TTL)
	p, screen := newPIN(t, PINConfig{Attempts: 1, Delay: 20 * time.Millisecond, MaxSeries: 1})
	responder.PIN = p
	done := serve(ln, inv, responder)
	code := next(t, screen).code
	for i := 0; i < pinDestroyedIn; i++ {
		if _, err := JoinPIN(context.Background(), ln.Addr().String(), wrong(code), device(t, "attacker")); !errors.Is(err, ErrRefused) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
		code = next(t, screen).code
	}
	select {
	case s := <-done:
		if !errors.Is(s.err, ErrTooManyPINs) {
			t.Fatalf("Serve = %v, want ErrTooManyPINs", s.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not stop")
	}
}
