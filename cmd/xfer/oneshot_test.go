package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
)

func testDevice(t *testing.T) (*identity.Identity, *identity.TrustStore) {
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
	return id, ts
}

// dataDial runs the receiver's side of a data connection against secure, the sender's side.
func dataDial(t *testing.T, secure func(context.Context, net.Conn) (net.Conn, error), client, sender *identity.Identity) error {
	t.Helper()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	done := make(chan error, 1)
	go func() {
		c, err := secure(context.Background(), b)
		if err == nil {
			_, err = c.Write([]byte("x")) // finishes the handshake on the client's side
		}
		done <- err
	}()
	c, err := securechan.Client(context.Background(), a, securechan.Config{Identity: client, Expected: sender.Fingerprint})
	if err == nil {
		_, err = c.Read(make([]byte, 1))
	}
	if serr := <-done; serr != nil {
		return serr
	}
	return err
}

// The data listener lets nobody in before pairing, then only the paired receiver.
func TestOneShotPeerSecure(t *testing.T) {
	sender, _ := testDevice(t)
	receiver, _ := testDevice(t)
	other, _ := testDevice(t)
	var peer oneShotPeer
	secure := peer.secure(sender)
	if err := dataDial(t, secure, receiver, sender); !errors.Is(err, errNoReceiver) {
		t.Fatalf("before pairing: %v, want errNoReceiver", err)
	}
	peer.set(receiver.Fingerprint)
	if err := dataDial(t, secure, receiver, sender); err != nil {
		t.Fatalf("paired receiver: %v", err)
	}
	if err := dataDial(t, secure, other, sender); err == nil {
		t.Fatal("another key got in on the data connections")
	}
}

func TestReceiverConfirm(t *testing.T) {
	self, trust := testDevice(t)
	localID = self
	stranger, _ := testDevice(t)
	friend, _ := testDevice(t)
	if err := trust.Add(identity.Peer{Fingerprint: friend.Fingerprint, Alias: "phone", LastAddress: "10.0.0.7"}); err != nil {
		t.Fatal(err)
	}
	answer := func(s string) func(context.Context) string {
		return func(context.Context) string { return s }
	}
	r := pairing.Result{Fingerprint: stranger.Fingerprint, Name: "laptop", Address: "10.0.0.9"}
	var out bytes.Buffer

	for _, no := range []string{"", "n\n", "whatever\n"} {
		if receiverConfirm(&out, answer(no), trust)(context.Background(), r) {
			t.Fatalf("answer %q let a stranger in", no)
		}
	}
	if !strings.Contains(out.String(), stranger.Fingerprint.Short()) || !strings.Contains(out.String(), self.Fingerprint.Short()) {
		t.Fatalf("both fingerprints must be shown:\n%s", out.String())
	}
	if !receiverConfirm(&out, answer("y\n"), trust)(context.Background(), r) || trust.Contains(stranger.Fingerprint) {
		t.Fatal(`"y" must let the receiver in for this send only`)
	}
	if !receiverConfirm(&out, answer("a\n"), trust)(context.Background(), r) || !trust.Contains(stranger.Fingerprint) {
		t.Fatal(`"a" must let the receiver in and remember it`)
	}

	// A trusted device needs no question.
	asked := false
	ask := func(context.Context) string { asked = true; return "" }
	if !receiverConfirm(&out, ask, trust)(context.Background(), pairing.Result{Fingerprint: friend.Fingerprint, Address: "10.0.0.7"}) || asked {
		t.Fatal("a trusted device was not let in at once")
	}

	// ID-05: the trusted device's address with another key is refused, without a question.
	out.Reset()
	impostor, _ := testDevice(t)
	if receiverConfirm(&out, answer("a\n"), trust)(context.Background(), pairing.Result{Fingerprint: impostor.Fingerprint, Address: "10.0.0.7"}) {
		t.Fatal("a changed key was let in")
	}
	if !strings.Contains(out.String(), "REFUSED") {
		t.Fatalf("no alert for a changed key:\n%s", out.String())
	}
}

// The QR code of xfer send is renewed when it expires, on a new port; the latest one pairs
// and carries the offer.
func TestServeOneShotPairingRenewsTheQRCode(t *testing.T) {
	sender, _ := testDevice(t)
	receiver, _ := testDevice(t)
	var mu sync.Mutex
	var uris []pairing.URI
	shown := func(u pairing.URI) {
		mu.Lock()
		defer mu.Unlock()
		uris = append(uris, u)
	}
	latest := func() (pairing.URI, int) {
		mu.Lock()
		defer mu.Unlock()
		if len(uris) == 0 {
			return pairing.URI{}, 0
		}
		return uris[len(uris)-1], len(uris)
	}
	type result struct {
		r   pairing.Result
		err error
	}
	done := make(chan result, 1)
	go func() {
		r, err := serveOneShotPairing(context.Background(), sender, "127.0.0.1", 300*time.Millisecond,
			session.Message{FileName: "a.bin", DataPort: 18888}, nil, shown)
		done <- result{r, err}
	}()
	var u pairing.URI
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var n int
		if u, n = latest(); n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the QR code was not renewed")
		}
	}
	mu.Lock()
	first := uris[0]
	mu.Unlock()
	if first.Secret == u.Secret {
		t.Fatal("a renewed QR code kept the old secret")
	}
	// Join with the URI shown last; retry if it expired meanwhile.
	var r pairing.Result
	var err error
	for i := 0; i < 5; i++ {
		u, _ = latest()
		if r, err = pairing.Join(context.Background(), u, pairing.Options{Identity: receiver, Once: true}); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if r.Offer.FileName != "a.bin" || r.Offer.DataPort != 18888 {
		t.Fatalf("offer: %+v", r.Offer)
	}
	if s := <-done; s.err != nil || s.r.Fingerprint != receiver.Fingerprint {
		t.Fatalf("sender: %+v, %v", s.r, s.err)
	}
}
