package engine

import (
	"net"
	"testing"
)

func TestTuneConn(t *testing.T) {
	// Create a local TCP listener to generate a net.Conn
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer l.Close()

	go func() {
		conn, _ := l.Accept()
		if conn != nil {
			_ = conn.Close()
		}
	}()

	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()

	// Ensure TuneConn applies settings without panicking
	TuneConn(conn)
}
