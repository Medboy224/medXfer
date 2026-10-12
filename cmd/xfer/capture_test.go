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

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/protocol"
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

// joinFunc secures a data connection and joins a transfer (transfer.JoinFunc); the plaintext
// control uses the raw connection.
type joinFunc = func(context.Context, net.Conn) (net.Conn, error)

func plaintext(_ context.Context, c net.Conn) (net.Conn, error) { return c, nil }

// fileOffer is the offer xfer send makes for path, in 512 KiB chunks.
func fileOffer(t *testing.T, path string, id transfer.ID) (protocol.Offer, []string) {
	t.Helper()
	offer, files, err := buildOffer([]string{path}, protocol.MinChunkSize*2)
	if err != nil {
		t.Fatal(err)
	}
	offer.TransferID = id.String()
	return offer, files
}

// startDataSender serves offer on loopback without TLS: what a capture of an unprotected
// transfer would see.
func startDataSender(t *testing.T, offer protocol.Offer, files []string) string {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s, err := transfer.NewSender(offer, files)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Serve(ctx, l)
	}()
	t.Cleanup(func() { cancel(); <-done })
	return l.Addr().String()
}

func pull(t *testing.T, addr string, offer protocol.Offer, join joinFunc) string {
	t.Helper()
	out := t.TempDir()
	r := transfer.NewReceiver(out, join)
	r.Workers = 2
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := r.Run(ctx, addr, offer); err != nil {
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
	send := newOneShotSend(hub, base)
	offer, files := fileOffer(t, path, base)
	msg, follow := session.OfferMessages(session.Message{DataPort: port, TransferID: base.String()}, offer)
	uris := make(chan pairing.URI, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_, _ = serveOneShotPairing(ctx, hub, sender, "127.0.0.1", port, time.Minute, msg, follow, confirm, send.register,
			func(u pairing.URI) { uris <- u })
	}()
	go func() {
		if tl := send.listener(ctx); tl != nil {
			if s, err := transfer.NewSender(offer, files); err == nil {
				_ = s.Serve(ctx, tl)
			}
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
	if r.Offer.Offer == nil || r.Offer.Offer.Root != name {
		t.Fatalf("offer: %+v", r.Offer)
	}
	id, err := transfer.ParseID(r.Offer.Offer.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	out := pull(t, relay.addr, *r.Offer.Offer, transfer.JoinFunc(securechan.Config{Identity: receiverID, Expected: u.Fingerprint}, id))
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

// The control: the same capture of a transfer without TLS does show the marker (the name now
// travels only in the offer, on the control connection), so the test above would see a leak.
func TestCaptureSeesAPlaintextTransfer(t *testing.T) {
	path, _, marker := markedFile(t, 1<<20)
	id, _ := transfer.NewID()
	offer, files := fileOffer(t, path, id)
	c := startCapture(t, startDataSender(t, offer, files))
	pull(t, c.addr, offer, plaintext)
	captured := c.bytes()
	if !bytes.Contains(captured, marker) {
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
	if r.Offer.Offer == nil {
		t.Fatalf("no offer with pair_accept: %+v", r.Offer)
	}
	id, err := transfer.ParseID(r.Offer.Offer.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	out := pull(t, relay.addr, *r.Offer.Offer, transfer.JoinFunc(securechan.Config{Identity: receiverID, Expected: r.Fingerprint}, id))
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
