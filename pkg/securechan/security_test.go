package securechan

// Security tests of bible §14.1 for the secure channel (WP-3.7). The other tests of the table
// live next to the code they check: TLS 1.2 and ALPN probes, cut handshake and plaintext
// client in securechan_test.go; data connections from another key in pkg/engine
// (secure_test.go); changed key (ID-05) in cmd/xfer and pkg/api; the network capture of a
// whole transfer in cmd/xfer (capture_test.go).

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// mitm is an active man in the middle: it terminates the client's TLS with its own key and
// opens its own TLS connection to the real server, then would relay between the two.
type mitm struct {
	addr      string
	toServer  chan error // result of its handshake with the real server
	forClient chan error // result of its handshake with the client
}

func startMITM(t *testing.T, self *device, server string) *mitm {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	m := &mitm{addr: ln.Addr().String(), toServer: make(chan error, 1), forClient: make(chan error, 1)}
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			m.forClient <- err
			return
		}
		c, err := Server(context.Background(), raw, Config{Identity: self.id, AllowUnknown: true})
		m.forClient <- err
		if err == nil {
			defer c.Close()
		}
		up, err := Dial(context.Background(), server, Config{Identity: self.id, AllowUnknown: true})
		m.toServer <- err
		if err == nil {
			up.Close()
		}
	}()
	return m
}

// CHAN-03: a client that pins the server's key (QR code, reconnection, data connection) is
// refused by a proxy that answers with its own certificate.
func TestMITMIsRefusedByPinnedClient(t *testing.T) {
	server, client, attacker := newDevice(t), newDevice(t), newDevice(t)
	server.trustDevice(t, client)
	addr, _ := serve(t, Config{Identity: server.id, Trust: server.trust})
	m := startMITM(t, attacker, addr)

	_, err := Dial(context.Background(), m.addr, Config{Identity: client.id, Expected: server.id.Fingerprint})
	if !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("client through the proxy: %v, want ErrFingerprintMismatch", err)
	}
}

// CHAN-03: a client that only trusts known devices refuses the proxy's unknown key.
func TestMITMIsRefusedByTrustingClient(t *testing.T) {
	server, client, attacker := newDevice(t), newDevice(t), newDevice(t)
	client.trustDevice(t, server)
	server.trustDevice(t, client)
	addr, _ := serve(t, Config{Identity: server.id, Trust: server.trust})
	m := startMITM(t, attacker, addr)

	_, err := Dial(context.Background(), m.addr, Config{Identity: client.id, Trust: client.trust})
	if !errors.Is(err, ErrUnknownPeer) {
		t.Fatalf("client through the proxy: %v, want ErrUnknownPeer", err)
	}
}

// The other half: the proxy's own connection to a server that admits only trusted keys is
// refused too, so it cannot impersonate the client either.
func TestMITMIsRefusedByServer(t *testing.T) {
	server, attacker := newDevice(t), newDevice(t)
	addr, res := serve(t, Config{Identity: server.id, Trust: server.trust})

	c, err := Dial(context.Background(), addr, Config{Identity: attacker.id, AllowUnknown: true})
	if err == nil {
		// TLS 1.3: the client may finish first; the server's refusal shows when reading.
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, err = c.Read(make([]byte, 1))
		c.Close()
	}
	if err == nil {
		t.Fatal("the proxy's own key was served")
	}
	if r := wait(t, res); !errors.Is(r.err, ErrUnknownPeer) {
		t.Fatalf("server: %v, want ErrUnknownPeer", r.err)
	}
}
