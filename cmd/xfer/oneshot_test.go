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
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/transfer"
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

// startTestHub serves a one-shot hub for sender on the loopback; it returns the hub and its
// address.
func startTestHub(t *testing.T, sender *identity.Identity) (*transfer.Hub, string) {
	t.Helper()
	localID = sender
	hub, ln, err := startOneShotHub("127.0.0.1", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return hub, ln.Addr().String()
}

// joinData opens a data connection to addr as client and joins transfer id; it reports
// whether the hub kept it (false: closed by the hub).
func joinData(t *testing.T, addr string, client, sender *identity.Identity, id transfer.ID) bool {
	t.Helper()
	raw, err := net.Dial("tcp4", addr)
	if err != nil {
		t.Fatal(err)
	}
	c, err := transfer.JoinFunc(securechan.Config{Identity: client, Expected: sender.Fingerprint}, id)(context.Background(), raw)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, err = c.Read(make([]byte, 1))
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout() // still open: nobody wrote, nobody closed
}

// CHAN-06 for a one-shot send: before the pairing nobody may join the transfer; after it, the
// paired receiver only, and only one receiver is ever registered.
func TestOneShotSendAdmitsOnlyThePairedReceiver(t *testing.T) {
	sender, _ := testDevice(t)
	receiver, _ := testDevice(t)
	other, _ := testDevice(t)
	hub, addr := startTestHub(t, sender)
	base, _ := transfer.NewID()
	send := newOneShotSend(hub, base)

	if joinData(t, addr, receiver, sender, base) {
		t.Fatal("a data connection was kept before any receiver was paired")
	}
	if err := send.register(pairing.Result{Fingerprint: receiver.Fingerprint}); err != nil {
		t.Fatal(err)
	}
	if !joinData(t, addr, receiver, sender, base) {
		t.Fatal("the paired receiver could not join")
	}
	if joinData(t, addr, other, sender, base) {
		t.Fatal("another key joined the transfer")
	}
	if err := send.register(pairing.Result{Fingerprint: other.Fingerprint}); err == nil {
		t.Fatal("a second receiver was registered")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if send.listener(ctx) == nil {
		t.Fatal("no listener after the pairing")
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

// The QR code of xfer send is renewed when it expires, with a new secret on the same port;
// the latest one pairs, registers the receiver before pair_accept, and carries the offer.
func TestServeOneShotPairingRenewsTheQRCode(t *testing.T) {
	sender, _ := testDevice(t)
	receiver, _ := testDevice(t)
	hub, addr := startTestHub(t, sender)
	port := portNum(t, addr)
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
	var registered identity.Fingerprint
	before := func(r pairing.Result) error { registered = r.Fingerprint; return nil }
	type result struct {
		r   pairing.Result
		err error
	}
	done := make(chan result, 1)
	offer := protocol.Offer{Root: "a.bin", Items: []protocol.OfferItem{{Path: "a.bin", Size: 1}}, TotalBytes: 1, ChunkSize: protocol.DefaultChunkSize}
	msg, follow := session.OfferMessages(session.Message{DataPort: port}, offer)
	go func() {
		r, err := serveOneShotPairing(context.Background(), hub, sender, "127.0.0.1", port, 300*time.Millisecond,
			msg, follow, nil, before, shown, nil)
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
	if first.Secret == u.Secret || first.Port != u.Port || u.Port != port {
		t.Fatal("a renewed QR code must keep the device's port and change its secret")
	}
	var r pairing.Result
	var err error
	for i := 0; i < 5; i++ { // retry if the code expired meanwhile
		u, _ = latest()
		if r, err = pairing.Join(context.Background(), u, pairing.Options{Identity: receiver, Once: true}); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if r.Offer.Offer == nil || r.Offer.Offer.Root != "a.bin" || r.Offer.DataPort != port {
		t.Fatalf("offer: %+v", r.Offer)
	}
	if s := <-done; s.err != nil || s.r.Fingerprint != receiver.Fingerprint || registered != receiver.Fingerprint {
		t.Fatalf("sender: %+v, %v (registered %s)", s.r, s.err, registered.Short())
	}
}

// xfer send --pin: a PIN instead of the QR code, on the device's single port. The receiver that
// types it pairs, is registered before pair_accept, and gets the offer; nothing else is shown.
func TestServeOneShotPairingByPIN(t *testing.T) {
	sender, _ := testDevice(t)
	receiver, _ := testDevice(t)
	hub, addr := startTestHub(t, sender)
	port := portNum(t, addr)
	pins := make(chan string, 8)
	var registered identity.Fingerprint
	before := func(r pairing.Result) error { registered = r.Fingerprint; return nil }
	noQR := func(pairing.URI) { t.Error("a QR code was shown for xfer send --pin") }
	type result struct {
		r   pairing.Result
		err error
	}
	done := make(chan result, 1)
	offer := protocol.Offer{Root: "a.bin", Items: []protocol.OfferItem{{Path: "a.bin", Size: 1}}, TotalBytes: 1, ChunkSize: protocol.DefaultChunkSize}
	msg, follow := session.OfferMessages(session.Message{DataPort: port}, offer)
	go func() {
		r, err := serveOneShotPairing(context.Background(), hub, sender, "127.0.0.1", port, time.Minute,
			msg, follow, nil, before, noQR, func(code string, _ time.Time) { pins <- code })
		done <- result{r, err}
	}()
	var code string
	select {
	case code = <-pins:
	case <-time.After(5 * time.Second):
		t.Fatal("no PIN shown")
	}
	r, err := pairing.JoinPIN(context.Background(), addr, code, pairing.Options{Identity: receiver, Once: true})
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if r.Offer.Offer == nil || r.Offer.Offer.Root != "a.bin" || r.Offer.DataPort != port {
		t.Fatalf("offer: %+v", r.Offer)
	}
	if s := <-done; s.err != nil || s.r.Fingerprint != receiver.Fingerprint || registered != receiver.Fingerprint {
		t.Fatalf("sender: %+v, %v (registered %s)", s.r, s.err, registered.Short())
	}
}

// #133, CHAN-01: xfer send takes the device's port; when another medXfer holds it, a free
// port, unless the user chose the port.
func TestStartSendHubFallsBackWhenTaken(t *testing.T) {
	busy, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	if _, _, err := startSendHub("127.0.0.1", port, false, nil); err == nil {
		t.Fatal("a port chosen by the user was replaced")
	}
	_, ln, err := startSendHub("127.0.0.1", port, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if got := ln.Addr().(*net.TCPAddr).Port; got == port || got == 0 {
		t.Fatalf("listening on %d", got)
	}
	if msg := unreachableSender("10.0.0.2:18887"); !strings.Contains(msg, "10.0.0.2:18887") || !strings.Contains(msg, "TCP port 18887") {
		t.Fatal(msg)
	}
}
