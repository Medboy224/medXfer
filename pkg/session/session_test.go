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

func TestTLS13Encryption(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer ln.Close()

	srvTLSConfig, err := ServerTLSConfig()
	if err != nil {
		t.Fatalf("Failed to get ServerTLSConfig: %v", err)
	}

	serverChan := make(chan *Channel, 1)
	go func() {
		rawConn, err := ln.Accept()
		if err != nil {
			return
		}
		upConn, isTLS, err := UpgradeToTLSIfClientHello(rawConn, srvTLSConfig)
		if err != nil {
			rawConn.Close()
			return
		}
		if !isTLS {
			t.Errorf("Expected isTLS to be true")
		}
		serverChan <- NewChannel(upConn)
	}()

	clientConn, err := DialTLSPeer(ln.Addr().String())
	if err != nil {
		t.Fatalf("DialTLSPeer failed: %v", err)
	}
	defer clientConn.Close()

	clientCh := NewChannel(clientConn)
	if !clientCh.IsEncrypted() {
		t.Fatalf("Expected client channel to be encrypted")
	}

	serverCh := <-serverChan
	defer serverCh.Close()

	if !serverCh.IsEncrypted() {
		t.Fatalf("Expected server channel to be encrypted")
	}

	// Send encrypted message
	if err := clientCh.Send(Message{Type: "encrypted_hello", DeviceName: "SecureNode"}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	msg, err := serverCh.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if msg.Type != "encrypted_hello" || msg.DeviceName != "SecureNode" {
		t.Fatalf("Unexpected msg: %+v", msg)
	}
}
