package main

// "Network capture" of bible §14.1 (WP-3.7), the testable form of J3's exit criterion: a whole
// one-shot transfer, QR code pairing then data, goes through relays that record every byte.
// The file is a 64-byte random marker repeated; neither the marker, nor the file name, nor the
// QR code's secret may appear in what the relays saw.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

// capture is a TCP relay to target that records the bytes of every connection, both ways.
type capture struct {
	addr string
	mu   sync.Mutex
	buf  bytes.Buffer
}

func startCapture(t *testing.T, target string) *capture {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	c := &capture{addr: ln.Addr().String()}
	go func() {
		for {
			in, err := ln.Accept()
			if err != nil {
				return
			}
			out, err := net.Dial("tcp4", target)
			if err != nil {
				in.Close()
				continue
			}
			go c.pipe(in, out)
			go c.pipe(out, in)
		}
	}()
	return c
}

func (c *capture) pipe(from, to net.Conn) {
	defer from.Close()
	defer to.Close()
	b := make([]byte, 64<<10)
	for {
		n, err := from.Read(b)
		if n > 0 {
			c.mu.Lock()
			c.buf.Write(b[:n])
			c.mu.Unlock()
			if _, werr := to.Write(b[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (c *capture) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf.Bytes()...)
}

// markedFile writes size bytes made of one random 64-byte marker repeated, under a random
// file name; it returns the path, the name and the marker.
func markedFile(t *testing.T, size int) (string, string, []byte) {
	t.Helper()
	marker := make([]byte, 64)
	_, _ = rand.Read(marker)
	name := "capture-" + hex.EncodeToString(marker[:6]) + ".bin"
	data := bytes.Repeat(marker, size/len(marker))
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, name, marker
}

// startDataSender serves path on loopback with secure (nil: plaintext), as xfer send does.
func startDataSender(t *testing.T, path string, secure engine.SecureFunc) string {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	s := engine.NewSender(2, 512<<10)
	if secure != nil {
		s.SetSecure(secure)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(done)
		_ = s.ServeAndSendWithRelPathReady(ctx, addr, path, "", nil, 0, ready)
	}()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); <-done })
	return addr
}

func pull(t *testing.T, addr string, secure engine.SecureFunc) string {
	t.Helper()
	out := t.TempDir()
	r := engine.NewReceiver(out, 2)
	if secure != nil {
		r.SetSecure(secure)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.Pull(ctx, addr, nil, ""); err != nil {
		t.Fatal(err)
	}
	return out
}

// oneShotThroughCapture starts the sender's side of xfer send for path on a loopback hub,
// behind a recording relay: everything the receiver sends or receives, pairing and data, goes
// through the device's single port, hence through the relay. It returns the relay and the
// first URI shown (whose address is the relay's).
func oneShotThroughCapture(t *testing.T, sender *identity.Identity, path, name string, size int,
	confirm func(context.Context, pairing.Result) bool) (*capture, pairing.URI, transfer.ID) {
	t.Helper()
	hub, addr := startTestHub(t, sender)
	relay := startCapture(t, addr)
	port := portNum(t, relay.addr)
	base, _ := transfer.NewID()
	send := newOneShotSend(hub, base, 0)
	offer := session.Message{FileName: name, FileSize: int64(size), DataPort: port, TransferID: base.String()}
	uris := make(chan pairing.URI, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_, _ = serveOneShotPairing(ctx, hub, sender, "127.0.0.1", port, time.Minute, offer, confirm, send.register,
			func(u pairing.URI) { uris <- u })
	}()
	go func() {
		if tl := send.listener(ctx, 0); tl != nil {
			_ = engine.NewSender(2, 512<<10).ServeOn(ctx, tl, path, "", nil, 0)
		}
	}()
	return relay, <-uris, base
}

func TestCaptureShowsNothingOfTheTransfer(t *testing.T) {
	const size = 4 << 20
	path, name, marker := markedFile(t, size)
	senderID, _ := testDevice(t)
	receiverID, _ := testDevice(t)
	relay, u, _ := oneShotThroughCapture(t, senderID, path, name, size, nil)

	// The receiver: xfer recv 'medxfer://...', through the relay for pairing and data alike.
	r, err := pairing.Join(context.Background(), u, pairing.Options{Identity: receiverID, Once: true})
	if err != nil {
		t.Fatal(err)
	}
	id, err := transfer.ParseID(r.Offer.TransferID)
	if err != nil || r.Offer.FileName != name {
		t.Fatalf("offer: %+v (%v)", r.Offer, err)
	}
	out := pull(t, relay.addr, transfer.JoinFunc(securechan.Config{Identity: receiverID, Expected: u.Fingerprint}, id))
	got, err := os.ReadFile(filepath.Join(out, name))
	if err != nil || len(got) != size || !bytes.Equal(got[:64], marker) {
		t.Fatalf("received file differs (%v)", err)
	}

	captured := relay.bytes()
	if len(captured) < size {
		t.Fatalf("the relay saw %d bytes, less than the file: the transfer did not go through it", len(captured))
	}
	for what, secret := range map[string][]byte{
		"the marker":            marker,
		"the marker's half":     marker[:32],
		"the file name":         []byte(name),
		"the QR secret":         u.Secret[:],
		"the QR secret, base64": []byte(pairing.EncodeSecret(u.Secret)),
	} {
		if bytes.Contains(captured, secret) {
			t.Errorf("%s appears in the captured bytes", what)
		}
	}
}

// The control: the same capture of a transfer without TLS does show the marker and the name,
// so the test above would see a leak.
func TestCaptureSeesAPlaintextTransfer(t *testing.T) {
	path, name, marker := markedFile(t, 1<<20)
	c := startCapture(t, startDataSender(t, path, nil))
	pull(t, c.addr, nil)
	captured := c.bytes()
	if !bytes.Contains(captured, marker) || !bytes.Contains(captured, []byte(name)) {
		t.Fatal("the capture misses a plaintext transfer: it proves nothing")
	}
}

func portNum(t *testing.T, addr string) int {
	t.Helper()
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// The same capture for the path without a QR code (xfer recv <code>): the receiver reads the
// sender's key in TLS, both users accept, and the offer comes with pair_accept. The discovery
// reply that found the sender carries nothing of the transfer either (pkg/discovery,
// TestAnswerRevealsNothingWithTheRightCode).
func TestCaptureShowsNothingOfTheCodePath(t *testing.T) {
	const size = 2 << 20
	path, name, marker := markedFile(t, size)
	senderID, _ := testDevice(t)
	receiverID, _ := testDevice(t)
	confirm := func(_ context.Context, r pairing.Result) bool { return r.Fingerprint == receiverID.Fingerprint }
	relay, _, _ := oneShotThroughCapture(t, senderID, path, name, size, confirm)

	accept := func(fp identity.Fingerprint) bool { return fp == senderID.Fingerprint }
	r, err := pairing.Ask(context.Background(), relay.addr, accept, 10*time.Second, pairing.Options{Identity: receiverID, Once: true})
	if err != nil {
		t.Fatal(err)
	}
	id, err := transfer.ParseID(r.Offer.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	out := pull(t, relay.addr, transfer.JoinFunc(securechan.Config{Identity: receiverID, Expected: r.Fingerprint}, id))
	if got, err := os.ReadFile(filepath.Join(out, name)); err != nil || len(got) != size {
		t.Fatalf("received file differs (%v)", err)
	}
	captured := relay.bytes()
	for what, secret := range map[string][]byte{"the marker": marker, "the file name": []byte(name)} {
		if bytes.Contains(captured, secret) {
			t.Errorf("%s appears in the captured bytes", what)
		}
	}
}
