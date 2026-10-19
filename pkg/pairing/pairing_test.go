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

// PAIR-08: a QR code pairs both devices for the session; nothing is written.
func TestQRPairsForTheSession(t *testing.T) {
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
	if shower.Trust.Contains(scanner.Identity.Fingerprint) || scanner.Trust.Contains(shower.Identity.Fingerprint) || r.Trusted || s.r.Trusted {
		t.Fatal("a pairing without a trust offer wrote a trusted device")
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

// PAIR-08: by default, a pairing writes nothing on disk, not even an empty list.
func TestSessionWritesNothing(t *testing.T) {
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
		opts[name] = Options{Identity: id, Trust: ts, DeviceName: name}
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
			t.Fatalf("%s: a session wrote trusted.json (%v)", name, err)
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

// trustBoth makes a and b devices of mutual trust.
func trustBoth(t *testing.T, a, b Options) {
	t.Helper()
	for _, x := range []struct{ self, other Options }{{a, b}, {b, a}} {
		if err := x.self.Trust.Add(identity.Peer{Fingerprint: x.other.Identity.Fingerprint, Alias: x.other.DeviceName}); err != nil {
			t.Fatal(err)
		}
	}
}

// PAIR-09, PAIR-12: without a code, only a device of mutual trust gets in (P3). An unknown one
// is told to use a PIN or a QR code (nothing sent, the key seen returned for ID-05), and so is
// one trusted on one side only.
func TestJoinTrustedNeedsMutualTrust(t *testing.T) {
	shower, scanner := device(t, "pc"), device(t, "phone")
	ln, inv, u := listen(t, shower, TTL)
	done := serve(ln, inv, shower)

	r, err := JoinTrusted(context.Background(), u.Addr(), scanner)
	if !errors.Is(err, ErrPINRequired) || r.Fingerprint != shower.Identity.Fingerprint {
		t.Fatalf("unknown: %+v, %v; want ErrPINRequired with the key seen", r, err)
	}
	if err := scanner.Trust.Add(identity.Peer{Fingerprint: shower.Identity.Fingerprint, Alias: "pc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := JoinTrusted(context.Background(), u.Addr(), scanner); !errors.Is(err, ErrPINRequired) {
		t.Fatalf("trusted on the scanner's side only: %v, want ErrPINRequired", err)
	}
	trustBoth(t, shower, scanner)
	r, err = JoinTrusted(context.Background(), u.Addr(), scanner)
	if err != nil || !r.Mutual || r.Name != "pc" {
		t.Fatalf("mutual: %+v, %v", r, err)
	}
	if s := <-done; s.err != nil || !s.r.Mutual || s.r.Fingerprint != scanner.Identity.Fingerprint {
		t.Fatalf("shower: %+v, %v", s.r, s.err)
	}
}

// PAIR-10 in a one-shot pairing: the side that offers says so in its pairing message, and the
// negotiation follows pair_accept. Both write only on two yes; a no writes nothing; two offers
// need no question.
func TestOneShotTrustNegotiation(t *testing.T) {
	for _, c := range []struct {
		name                     string
		scannerOffers, showerOff bool
		showerSays, scannerSays  bool
		want                     bool
	}{
		{"scanner offers, shower accepts", true, false, true, false, true},
		{"scanner offers, shower declines", true, false, false, false, false},
		{"shower offers, scanner accepts", false, true, false, true, true},
		{"both offer", true, true, false, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			shower, scanner := device(t, "pc"), device(t, "phone")
			shower.OfferTrust, scanner.OfferTrust = c.showerOff, c.scannerOffers
			var askedShower, askedScanner bool
			shower.AskTrust = func(_ context.Context, r Result) bool {
				askedShower = r.Name == "phone"
				return c.showerSays
			}
			scanner.AskTrust = func(_ context.Context, r Result) bool {
				askedScanner = r.Name == "pc"
				return c.scannerSays
			}
			ln, inv, u := listen(t, shower, TTL)
			done := serve(ln, inv, shower)
			r, err := Join(context.Background(), u, scanner)
			if err != nil {
				t.Fatal(err)
			}
			s := <-done
			if s.err != nil {
				t.Fatal(s.err)
			}
			if r.Trusted != c.want || s.r.Trusted != c.want {
				t.Fatalf("trusted: scanner %v, shower %v; want %v", r.Trusted, s.r.Trusted, c.want)
			}
			both := shower.Trust.Contains(scanner.Identity.Fingerprint) && scanner.Trust.Contains(shower.Identity.Fingerprint)
			none := !shower.Trust.Contains(scanner.Identity.Fingerprint) && !scanner.Trust.Contains(shower.Identity.Fingerprint)
			if (c.want && !both) || (!c.want && !none) {
				t.Fatal("the lists do not match the answers")
			}
			if c.scannerOffers && c.showerOff && (askedShower || askedScanner) {
				t.Fatal("a user was asked although both offered")
			}
			// Both offered: both commit their own key, and both must end with the same one, or
			// neither recognizes the other's announcements (DISC-11, #170).
			if c.want {
				ka, kb := keyOf(t, shower, scanner.Identity.Fingerprint), keyOf(t, scanner, shower.Identity.Fingerprint)
				if len(ka) != tagKeySize || string(ka) != string(kb) {
					t.Fatal("the two sides keep different recognition keys")
				}
			}
			if c.scannerOffers && !c.showerOff && !askedShower {
				t.Fatal("the shower's user was not asked")
			}
			if c.showerOff && !c.scannerOffers && !askedScanner {
				t.Fatal("the scanner's user was not asked")
			}
		})
	}
}

// Audit 10 (#156): once pair_accept is sent, the QR code expiring does not cut the trust
// question: the user keeps the time to answer, and Serve returns the pairing.
func TestTrustQuestionOutlivesTheQRCode(t *testing.T) {
	shower, scanner := device(t, "pc"), device(t, "phone")
	scanner.OfferTrust = true
	cut := make(chan bool, 1)
	shower.AskTrust = func(ctx context.Context, _ Result) bool {
		select {
		case <-ctx.Done(): // the question was cut
			cut <- true
			return false
		case <-time.After(900 * time.Millisecond): // answered after the QR code expired
			cut <- false
			return true
		}
	}
	ln, inv, u := listen(t, shower, 400*time.Millisecond)
	done := serve(ln, inv, shower)
	r, err := Join(context.Background(), u, scanner)
	if err != nil {
		t.Fatal(err)
	}
	s := <-done
	if <-cut {
		t.Fatal("the QR code expiring cut the user's answer")
	}
	if s.err != nil {
		t.Fatalf("Serve = %v after a pairing accepted", s.err)
	}
	if !r.Trusted || !s.r.Trusted {
		t.Fatalf("trusted: scanner %v, shower %v; want both", r.Trusted, s.r.Trusted)
	}
}

// Audit 10 (#156): cancelling Join also ends its trust question.
func TestJoinCancelEndsTheTrustQuestion(t *testing.T) {
	shower, scanner := device(t, "pc"), device(t, "phone")
	shower.OfferTrust = true
	scanner.AskTrust = func(ctx context.Context, _ Result) bool {
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
		return false
	}
	ln, inv, u := listen(t, shower, TTL)
	done := serve(ln, inv, shower)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	r, err := Join(ctx, u, scanner)
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Join ended %v after its context, want at once", d)
	}
	if err == nil && r.Trusted {
		t.Fatal("trust established without an answer")
	}
	<-done
}

// Audit §3.5 (#156): the address of a QR code is IPv4, as every connection of medXfer: an IPv6
// one is refused when read, not left to fail when dialled.
func TestParseRefusesIPv6(t *testing.T) {
	inv, err := NewInvitation(TTL)
	if err != nil {
		t.Fatal(err)
	}
	shower := device(t, "pc")
	u := inv.URI(shower.Identity.Fingerprint, "fe80::1", 18887)
	if _, err := Parse(u.String(), time.Now()); !errors.Is(err, ErrNotPairingURI) {
		t.Fatalf("Parse of an IPv6 address = %v, want ErrNotPairingURI", err)
	}
}
