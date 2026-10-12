package session

import (
	"errors"
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
