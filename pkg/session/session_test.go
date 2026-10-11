package session

import (
	"net"
	"testing"
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
