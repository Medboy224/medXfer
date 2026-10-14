package pairing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/session"
)

func device(t testing.TB, name string) Options {
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
	return Options{Identity: id, Trust: ts, DeviceName: name}
}

func TestURIRoundTrip(t *testing.T) {
	o := device(t, "pc")
	inv, err := NewInvitation(TTL)
	if err != nil {
		t.Fatal(err)
	}
	u := inv.URI(o.Identity.Fingerprint, "192.168.1.10", 18887)
	s := u.String()
	if !strings.HasPrefix(s, "medxfer://v1/pair?fp="+o.Identity.Fingerprint.String()+"&a=192.168.1.10&p=18887&s=") {
		t.Fatalf("URI %q does not follow the bible's format", s)
	}
	got, err := Parse(s, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Fingerprint != u.Fingerprint || got.Host != u.Host || got.Port != u.Port || got.Secret != u.Secret || got.Expires.Unix() != u.Expires.Unix() {
		t.Fatalf("round trip: %+v, want %+v", got, u)
	}
}

func TestParseRejects(t *testing.T) {
	o := device(t, "pc")
	inv, _ := NewInvitation(TTL)
	good := inv.URI(o.Identity.Fingerprint, "10.0.0.2", 18887).String()
	for name, s := range map[string]string{
		"scheme":      strings.Replace(good, "medxfer://", "https://", 1),
		"version":     strings.Replace(good, "//v1/", "//v2/", 1),
		"fingerprint": strings.Replace(good, "fp="+o.Identity.Fingerprint.String(), "fp=ABCD", 1),
		"address":     strings.Replace(good, "a=10.0.0.2", "a=evil.example", 1),
		"port":        strings.Replace(good, "p=18887", "p=70000", 1),
		"secret":      good[:strings.Index(good, "&s=")] + "&s=c2hvcnQ" + good[strings.Index(good, "&exp="):],
	} {
		if _, err := Parse(s, time.Now()); !errors.Is(err, ErrNotPairingURI) {
			t.Errorf("%s: Parse(%q) = %v, want ErrNotPairingURI", name, s, err)
		}
	}
	if _, err := Parse(good, inv.Expires().Add(time.Second)); !errors.Is(err, ErrExpired) {
		t.Errorf("expired URI: %v, want ErrExpired", err)
	}
}

func TestInvitationSingleUse(t *testing.T) {
	inv, _ := NewInvitation(TTL)
	now := time.Now()
	if err := inv.Redeem(make([]byte, SecretSize), now); !errors.Is(err, ErrRefused) {
		t.Fatalf("wrong secret: %v", err)
	}
	if err := inv.Redeem(inv.secret[:], now); err != nil {
		t.Fatalf("right secret: %v", err)
	}
	if err := inv.Redeem(inv.secret[:], now); !errors.Is(err, ErrRefused) {
		t.Fatalf("second use: %v, want ErrRefused", err)
	}
	late, _ := NewInvitation(time.Second)
	if err := late.Redeem(late.secret[:], late.Expires()); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: %v, want ErrExpired", err)
	}
}

// listen returns a loopback listener and the URI of a fresh invitation on it.
func listen(t *testing.T, shower Options, ttl time.Duration) (net.Listener, *Invitation, URI) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	inv, err := NewInvitation(ttl)
	if err != nil {
		t.Fatal(err)
	}
	return ln, inv, inv.URI(shower.Identity.Fingerprint, "127.0.0.1", ln.Addr().(*net.TCPAddr).Port)
}

type served struct {
	r   Result
	err error
}

func serve(ln net.Listener, inv *Invitation, o Options) <-chan served {
	ch := make(chan served, 1)
	go func() {
		r, err := Serve(context.Background(), ln, inv, o)
		ch <- served{r, err}
	}()
	return ch
}

func TestPairingTrustsBothWays(t *testing.T) {
	shower, scanner := device(t, "pc"), device(t, "phone")
	ln, inv, u := listen(t, shower, TTL)
	done := serve(ln, inv, shower)

	r, err := Join(context.Background(), u, scanner)
	if err != nil {
		t.Fatal(err)
	}
	if r.Fingerprint != shower.Identity.Fingerprint || r.Name != "pc" {
		t.Fatalf("scanner paired with %+v", r)
	}
	s := <-done
	if s.err != nil || s.r.Fingerprint != scanner.Identity.Fingerprint || s.r.Name != "phone" {
		t.Fatalf("shower: %+v, %v", s.r, s.err)
	}
	if p, ok := shower.Trust.Get(scanner.Identity.Fingerprint); !ok || p.Alias != "phone" {
		t.Fatal("the shower does not trust the scanner")
	}
	if p, ok := scanner.Trust.Get(shower.Identity.Fingerprint); !ok || p.Alias != "pc" || p.LastAddress != "127.0.0.1" {
		t.Fatalf("the scanner does not trust the shower: %+v", p)
	}
	// The QR code is used: nobody can redeem it again.
	if _, err := Join(context.Background(), u, device(t, "late")); err == nil {
		t.Fatal("a used QR code paired a second device")
	}
}

// A wrong secret gets a neutral refusal (PAIR-07) and the shower keeps waiting.
func TestWrongSecretIsRefusedAndWaitGoesOn(t *testing.T) {
	shower, scanner := device(t, "pc"), device(t, "phone")
	ln, inv, u := listen(t, shower, TTL)
	done := serve(ln, inv, shower)

	bad := u
	bad.Secret[0] ^= 0xff
	_, err := Join(context.Background(), bad, device(t, "attacker"))
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("wrong secret: %v, want ErrRefused", err)
	}
	if _, err := Join(context.Background(), u, scanner); err != nil {
		t.Fatalf("right secret after a wrong one: %v", err)
	}
	if s := <-done; s.err != nil || s.r.Fingerprint != scanner.Identity.Fingerprint {
		t.Fatalf("shower: %+v, %v", s.r, s.err)
	}
}

// The scanner pins the QR code's key: another device at that address is refused.
func TestJoinPinsTheShowersKey(t *testing.T) {
	shower, impostor, scanner := device(t, "pc"), device(t, "impostor"), device(t, "phone")
	ln, inv, u := listen(t, impostor, TTL) // the listener belongs to the impostor
	go func() { _, _ = Serve(context.Background(), ln, inv, impostor) }()
	u.Fingerprint = shower.Identity.Fingerprint // the QR code shows the real shower's key
	if _, err := Join(context.Background(), u, scanner); err == nil {
		t.Fatal("the scanner paired with a device that does not hold the QR code's key")
	}
	if scanner.Trust.Contains(impostor.Identity.Fingerprint) {
		t.Fatal("the impostor was trusted")
	}
}

// PAIR-08: --once writes nothing on disk.
func TestOnceWritesNothing(t *testing.T) {
	dirs := map[string]string{"pc": t.TempDir(), "phone": t.TempDir()}
	opts := map[string]Options{}
	for name, dir := range dirs {
		id, err := identity.LoadOrCreate(dir)
		if err != nil {
			t.Fatal(err)
		}
		ts, err := identity.OpenTrustStore(filepath.Join(dir, identity.TrustFile))
		if err != nil {
			t.Fatal(err)
		}
		opts[name] = Options{Identity: id, Trust: ts, DeviceName: name, Once: true}
	}
	ln, inv, u := listen(t, opts["pc"], TTL)
	done := serve(ln, inv, opts["pc"])
	if _, err := Join(context.Background(), u, opts["phone"]); err != nil {
		t.Fatal(err)
	}
	if s := <-done; s.err != nil {
		t.Fatal(s.err)
	}
	for name, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, identity.TrustFile)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: a --once pairing wrote trusted.json (%v)", name, err)
		}
	}
}

func TestServeExpires(t *testing.T) {
	shower := device(t, "pc")
	ln, inv, _ := listen(t, shower, 300*time.Millisecond)
	start := time.Now()
	_, err := Serve(context.Background(), ln, inv, shower)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("Serve = %v, want ErrExpired", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("expiry took too long")
	}
}

// A one-shot send's offer reaches the scanner with pair_accept (WP-3.5b).
// pair_accept carries the one-shot send's offer; an offer too big for one CONTROL frame comes
// in pages (decision D2), which Join reads and checks before it returns.
func TestAcceptCarriesTheOffer(t *testing.T) {
	shower, scanner := device(t, "pc"), device(t, "phone")
	offer := protocol.Offer{TransferID: "t1", Root: "photos", ChunkSize: protocol.DefaultChunkSize}
	for i := 0; i < 30000; i++ {
		offer.Items = append(offer.Items, protocol.OfferItem{Item: i, Path: fmt.Sprintf("photos/%s/%06d.jpg", strings.Repeat("x", 80), i), Size: 7})
		offer.TotalBytes += 7
	}
	var follow []session.Message
	shower.Offer, follow = session.OfferMessages(session.Message{DataPort: 18888}, offer)
	if len(follow) == 0 {
		t.Fatal("the test offer fits in one message: it does not test the pages")
	}
	shower.Follow = follow
	ln, inv, u := listen(t, shower, TTL)
	done := serve(ln, inv, shower)
	r, err := Join(context.Background(), u, scanner)
	if err != nil {
		t.Fatal(err)
	}
	<-done
	o := r.Offer
	if o.Type != TypeAccept || o.DeviceName != "pc" || o.DataPort != 18888 || o.Offer == nil {
		t.Fatalf("offer received: %+v", o)
	}
	if got := o.Offer; len(got.Items) != len(offer.Items) || got.Count != 0 || got.Items[29999].Path != offer.Items[29999].Path {
		t.Fatalf("offer of %d items received, want %d", len(got.Items), len(offer.Items))
	}
}

// Without the QR code, the shower's user decides (D3): a no is neutral and the wait goes on,
// a yes pairs.
func TestHelloIsDecidedByTheShowersUser(t *testing.T) {
	shower, intruder, scanner := device(t, "pc"), device(t, "intruder"), device(t, "phone")
	var asked []string
	shower.Confirm = func(_ context.Context, r Result) bool {
		asked = append(asked, r.Name) // calls are serialised by Serve
		return r.Fingerprint == scanner.Identity.Fingerprint
	}
	ln, inv, u := listen(t, shower, TTL)
	done := serve(ln, inv, shower)

	if _, err := Ask(context.Background(), u.Addr(), accept(u.Fingerprint), time.Second, intruder); !errors.Is(err, ErrRefused) {
		t.Fatalf("refused hello: %v, want ErrRefused", err)
	}
	r, err := Ask(context.Background(), u.Addr(), accept(u.Fingerprint), time.Second, scanner)
	if err != nil || r.Fingerprint != shower.Identity.Fingerprint {
		t.Fatalf("accepted hello: %+v, %v", r, err)
	}
	s := <-done
	if s.err != nil || s.r.Fingerprint != scanner.Identity.Fingerprint {
		t.Fatalf("shower: %+v, %v", s.r, s.err)
	}
	if strings.Join(asked, ",") != "intruder,phone" {
		t.Fatalf("the shower's user was asked about %v", asked)
	}
}

// With no Confirm (xfer pair --show), a hello is refused: only the QR code pairs.
func TestHelloWithoutConfirmIsRefused(t *testing.T) {
	shower := device(t, "pc")
	ln, inv, u := listen(t, shower, TTL)
	_ = serve(ln, inv, shower)
	if _, err := Ask(context.Background(), u.Addr(), accept(u.Fingerprint), time.Second, device(t, "phone")); !errors.Is(err, ErrRefused) {
		t.Fatalf("hello without Confirm: %v, want ErrRefused", err)
	}
}

// accept is the scanner's user accepting exactly the key want.
func accept(want identity.Fingerprint) func(identity.Fingerprint) bool {
	return func(fp identity.Fingerprint) bool { return fp == want }
}

// Without a QR code the scanner learns the shower's key from TLS, not from discovery: its user
// sees that key, and a no sends nothing (the shower's user is never asked).
func TestAskShowsTheKeyFromTLS(t *testing.T) {
	shower, scanner := device(t, "pc"), device(t, "phone")
	asked := make(chan struct{}, 4)
	shower.Confirm = func(context.Context, Result) bool { asked <- struct{}{}; return true }
	ln, inv, u := listen(t, shower, TTL)
	done := serve(ln, inv, shower)

	var seen identity.Fingerprint
	_, err := Ask(context.Background(), u.Addr(), func(fp identity.Fingerprint) bool { seen = fp; return false }, time.Second, scanner)
	if !errors.Is(err, ErrDeclined) {
		t.Fatalf("declined: %v, want ErrDeclined", err)
	}
	if seen != shower.Identity.Fingerprint {
		t.Fatalf("the scanner's user saw %s, the shower is %s", seen.Short(), shower.Identity.Fingerprint.Short())
	}
	select {
	case <-asked:
		t.Fatal("the shower's user was asked although the scanner declined")
	case <-time.After(200 * time.Millisecond):
	}
	r, err := Ask(context.Background(), u.Addr(), accept(shower.Identity.Fingerprint), time.Second, scanner)
	if err != nil || r.Fingerprint != shower.Identity.Fingerprint {
		t.Fatalf("accepted: %+v, %v", r, err)
	}
	if s := <-done; s.err != nil || s.r.Fingerprint != scanner.Identity.Fingerprint {
		t.Fatalf("shower: %+v, %v", s.r, s.err)
	}
}
