package api

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// startNodeListener starts a daemon and returns the loopback address of its control
// (pairing) listener once it is bound.
func startNodeListener(t *testing.T) (*DaemonServer, string) {
	t.Helper()
	server := NewDaemonServer(0, t.TempDir(), "SecureChanTest")
	ln, err := server.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(server.Stop)
	for deadline := time.Now().Add(2 * time.Second); ; {
		server.mu.RLock()
		ready := server.nodeLn != nil
		server.mu.RUnlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("node listener not started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	addr := server.GetNodeAddr()
	if strings.HasPrefix(addr, "0.0.0.0:") {
		addr = "127.0.0.1:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}
	return server, addr
}

// CHAN-01: the control listener speaks TLS from the first byte. A plaintext pair_request,
// even with the right pairing code, gets no answer: the connection is closed.
func TestDaemonRefusesPlaintextPairing(t *testing.T) {
	server, addr := startNodeListener(t)
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	code := server.GetStatus().PairingCode
	_, _ = raw.Write([]byte(`{"type":"pair_request","device_name":"plain","pairing_code":"` + code + `"}` + "\n"))
	_ = raw.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := bufio.NewReader(raw).ReadString('\n')
	if strings.Contains(line, "pair_accept") || strings.Contains(line, "pair_reject") {
		t.Fatalf("the daemon answered a plaintext pairing request: %q", line)
	}
	if err == nil {
		t.Fatalf("the connection stayed open after a plaintext request, read %q", line)
	}
}

// The daemon's pairing connection carries the peer's verified key; a successful pairing makes
// it AUTH (securechan state).
func TestDaemonPairingIsSecure(t *testing.T) {
	server, addr := startNodeListener(t)
	conn, err := dialSecure(t.Context(), addr, testPeerChan(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if conn.PeerFingerprint() != server.identity.Fingerprint {
		t.Fatalf("pinned %s, daemon identity is %s", conn.PeerFingerprint().Short(), server.identity.Fingerprint.Short())
	}
	if !conn.ConnectionState().HandshakeComplete || conn.ConnectionState().NegotiatedProtocol != "medxfer/1" {
		t.Fatal("handshake incomplete or wrong ALPN")
	}
}

// LIM-01: the control listener accepts at most 5 new connections per minute from one IP.
func TestDaemonLimitsNewConnectionsPerIP(t *testing.T) {
	_, addr := startNodeListener(t)
	peer := testPeerChan(t)
	for i := 1; i <= 5; i++ {
		conn, err := dialSecure(t.Context(), addr, peer)
		if err != nil {
			t.Fatalf("connection %d refused: %v", i, err)
		}
		conn.Close()
	}
	if conn, err := dialSecure(t.Context(), addr, peer); err == nil {
		// TLS 1.3: the client may finish its side first; the server has closed: reading fails.
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, rerr := conn.Read(make([]byte, 1)); rerr == nil {
			t.Fatal("6th connection in the minute was served")
		}
		conn.Close()
	}
}
