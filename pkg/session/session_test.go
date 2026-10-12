package session

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/Medboy224/medXfer/pkg/protocol"
)

func TestDialPeer(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer ln.Close()

	acceptedChan := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			acceptedChan <- c
		}
	}()

	conn, err := DialPeer(ln.Addr().String())
	if err != nil {
		t.Fatalf("DialPeer failed: %v", err)
	}
	defer conn.Close()

	accepted := <-acceptedChan
	defer accepted.Close()

	if conn.RemoteAddr().String() != ln.Addr().String() {
		t.Fatalf("Expected remote addr %s, got %s", ln.Addr().String(), conn.RemoteAddr().String())
	}
}

func pipe(t *testing.T) (*Channel, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	return NewChannel(a), b
}

func TestControlFramesRoundTrip(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ca, cb := NewChannel(a), NewChannel(b)
	go func() { _ = ca.Send(Message{Type: "offer", FileName: "a.txt", FileSize: 3}) }()
	m, err := cb.Read()
	if err != nil || m.Type != "offer" || m.FileName != "a.txt" || m.FileSize != 3 {
		t.Fatalf("got %+v, %v", m, err)
	}
}

// LIM-04: a message larger than the limit is neither sent nor read.
func TestControlMessageLimit(t *testing.T) {
	ch, peer := pipe(t)
	ch.SetMaxControl(64)
	if err := ch.Send(Message{Type: "offer", FileName: strings.Repeat("x", 100)}); !errors.Is(err, protocol.ErrFrameTooLarge) {
		t.Fatalf("send: %v, want ErrFrameTooLarge", err)
	}
	go func() {
		_, _ = peer.Write(protocol.AppendFrame(nil, protocol.TypeControl, []byte(strings.Repeat(" ", 65))))
	}()
	if _, err := ch.Read(); !errors.Is(err, protocol.ErrFrameTooLarge) {
		t.Fatalf("read: %v, want ErrFrameTooLarge", err)
	}
}

// A peer that still speaks the JSON stream of earlier builds is refused, not misread.
func TestJSONStreamPeerIsRefused(t *testing.T) {
	ch, peer := pipe(t)
	go func() { _, _ = peer.Write([]byte(`{"type":"pair_request","device_name":"old"}` + "\n")) }()
	if _, err := ch.Read(); !errors.Is(err, protocol.ErrBadMagic) {
		t.Fatalf("read: %v, want ErrBadMagic", err)
	}
}

// Unknown JSON fields are ignored (bible §8.2); invalid JSON is an error.
func TestControlMessageDecoding(t *testing.T) {
	ch, peer := pipe(t)
	go func() {
		_, _ = peer.Write(protocol.AppendFrame(nil, protocol.TypeControl, []byte(`{"type":"pause","future_field":42}`)))
		_, _ = peer.Write(protocol.AppendFrame(nil, protocol.TypeControl, []byte(`{"type":`)))
	}()
	if m, err := ch.Read(); err != nil || m.Type != "pause" {
		t.Fatalf("first: %+v, %v", m, err)
	}
	if _, err := ch.Read(); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
}

func bigOffer(n int) protocol.Offer {
	o := protocol.Offer{TransferID: "t1", Root: "photos", ChunkSize: protocol.DefaultChunkSize}
	for i := 0; i < n; i++ {
		path := fmt.Sprintf("photos/%s/IMG_%06d.jpg", strings.Repeat("album", 15), i)
		o.Items = append(o.Items, protocol.OfferItem{Item: i, Path: path, Size: int64(i % 5000), FileID: "0123456789abcdef0123456789abcdef"})
		o.TotalBytes += int64(i % 5000)
	}
	return o
}

// D2: an offer whose manifest is far above 1 MiB travels in pages that each fit in a CONTROL
// frame, and comes back whole.
func TestPagedOffer(t *testing.T) {
	offer := bigOffer(30_000)
	first, follow := OfferMessages(Message{Type: "offer"}, offer)
	if len(follow) < 2 {
		t.Fatalf("%d pages for a manifest of several MiB", len(follow))
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ca, cb := NewChannel(a), NewChannel(b)
	go func() {
		for _, m := range append([]Message{first}, follow...) {
			if err := ca.Send(m); err != nil { // Send refuses anything past MaxControl
				t.Error(err)
				return
			}
		}
	}()
	m, err := cb.Read()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadOffer(cb, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != len(offer.Items) || got.Items[29_999] != offer.Items[29_999] || got.TotalBytes != offer.TotalBytes || got.Count != 0 {
		t.Fatalf("offer came back with %d items", len(got.Items))
	}
}

func TestPagedOfferRefusesBadPages(t *testing.T) {
	offer := bigOffer(30_000)
	first, follow := OfferMessages(Message{Type: "offer"}, offer)
	follow[0].Items[0].Item = 7 // numbering broken
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ca, cb := NewChannel(a), NewChannel(b)
	go func() {
		for _, m := range append([]Message{first}, follow...) {
			if ca.Send(m) != nil {
				return
			}
		}
	}()
	m, _ := cb.Read()
	if _, err := ReadOffer(cb, m); !errors.Is(err, protocol.ErrBadOffer) {
		t.Fatalf("ReadOffer = %v, want ErrBadOffer", err)
	}
}
