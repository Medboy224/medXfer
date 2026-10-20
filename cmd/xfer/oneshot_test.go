package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// PAIR-10: the other device's offer of mutual trust is accepted on an explicit yes only; no
// answer in time is a no.
func TestAskTrustFrom(t *testing.T) {
	r := pairing.Result{Name: "phone"}
	for answer, want := range map[string]bool{"y\n": true, "oui\n": true, "\n": false, "n\n": false, "always\n": false} {
		lines := make(chan string, 1)
		lines <- answer
		var out bytes.Buffer
		if got := askTrustFrom(&out, lines)(context.Background(), r); got != want {
			t.Errorf("answer %q: %v, want %v", answer, got, want)
		}
		if !strings.Contains(out.String(), "phone") || !strings.Contains(out.String(), "[y/N]") {
			t.Fatalf("question: %q", out.String())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	if askTrustFrom(&out, make(chan string))(ctx, r) {
		t.Fatal("no answer was taken for a yes")
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
		p := oneShotPairing{hub: hub, id: sender, host: "127.0.0.1", port: port, ttl: 300 * time.Millisecond,
			offer: msg, follow: follow, before: before, shown: shown}
		r, err := p.serve(context.Background())
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
		if r, err = pairing.Join(context.Background(), u, pairing.Options{Identity: receiver}); err == nil {
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

// xfer send --pin: the PIN alone, on the device's single port. The receiver that types it pairs,
// is registered before pair_accept, and gets the offer; no QR code is shown.
func TestServeOneShotPairingByPIN(t *testing.T) {
	sender, _ := testDevice(t)
	receiver, _ := testDevice(t)
	hub, addr := startTestHub(t, sender)
	port := portNum(t, addr)
	pins := make(chan string, 8)
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
		p := oneShotPairing{hub: hub, id: sender, host: "127.0.0.1", port: port, ttl: time.Minute,
			offer: msg, follow: follow, before: before, pin: func(code string, _ time.Time) { pins <- code }}
		r, err := p.serve(context.Background())
		done <- result{r, err}
	}()
	var code string
	select {
	case code = <-pins:
	case <-time.After(5 * time.Second):
		t.Fatal("no PIN shown")
	}
	r, err := pairing.JoinPIN(context.Background(), addr, code, pairing.Options{Identity: receiver})
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

// D2, PAIR-09: by default xfer send shows its QR code and its PIN; a receiver that picks it in
// its list gets in by the PIN on demand, and a device of mutual trust gets in with neither.
func TestServeOneShotPairingShowsBothAndAdmitsMutualTrust(t *testing.T) {
	sender, senderTrust := testDevice(t)
	stranger, _ := testDevice(t)
	friend, friendTrust := testDevice(t)
	for _, x := range []struct {
		ts *identity.TrustStore
		fp identity.Fingerprint
	}{{senderTrust, friend.Fingerprint}, {friendTrust, sender.Fingerprint}} {
		if err := x.ts.Add(identity.Peer{Fingerprint: x.fp}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name string
		join func(addr string, pins <-chan string) (pairing.Result, error)
		who  identity.Fingerprint
	}{
		{"PIN on demand", func(addr string, pins <-chan string) (pairing.Result, error) {
			return pairing.JoinPINOnDemand(context.Background(), addr, func() (string, error) { return <-pins, nil },
				pairing.Options{Identity: stranger})
		}, stranger.Fingerprint},
		{"mutual trust", func(addr string, _ <-chan string) (pairing.Result, error) {
			return pairing.JoinTrusted(context.Background(), addr, pairing.Options{Identity: friend, Trust: friendTrust})
		}, friend.Fingerprint},
	} {
		t.Run(c.name, func(t *testing.T) {
			hub, addr := startTestHub(t, sender)
			port := portNum(t, addr)
			pins := make(chan string, 8)
			qr := make(chan pairing.URI, 8)
			offer := protocol.Offer{Root: "a.bin", Items: []protocol.OfferItem{{Path: "a.bin", Size: 1}}, TotalBytes: 1, ChunkSize: protocol.DefaultChunkSize}
			msg, follow := session.OfferMessages(session.Message{DataPort: port}, offer)
			p := oneShotPairing{hub: hub, id: sender, trust: senderTrust, host: "127.0.0.1", port: port, ttl: time.Minute,
				offer: msg, follow: follow, shown: func(u pairing.URI) { qr <- u }, pin: func(code string, _ time.Time) { pins <- code }}
			done := make(chan pairing.Result, 1)
			go func() { r, _ := p.serve(context.Background()); done <- r }()
			select {
			case <-qr:
			case <-time.After(5 * time.Second):
				t.Fatal("no QR code shown")
			}
			r, err := c.join(addr, pins)
			if err != nil || r.Offer.Offer == nil {
				t.Fatalf("join: %+v, %v", r, err)
			}
			if got := <-done; got.Fingerprint != c.who {
				t.Fatalf("sender paired with %s", got.Fingerprint.Short())
			}
			if senderTrust.Contains(stranger.Fingerprint) {
				t.Fatal("a PIN pairing wrote the receiver (PAIR-08)")
			}
		})
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

// CHAN-01: a node whose port is held by another medXfer (xfer node on the same phone) takes
// a free one and says so, instead of running without a port.
func TestNodePortFallsBackWhenTaken(t *testing.T) {
	busy, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", defaultPort)) // the loopback: no firewall prompt
	if err == nil {
		defer busy.Close()
	} // else: already held, which is the case under test
	l, err := listenNodePort("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if p := l.Addr().(*net.TCPAddr).Port; p == defaultPort || p == 0 {
		t.Fatalf("node listening on %d", p)
	}
}
