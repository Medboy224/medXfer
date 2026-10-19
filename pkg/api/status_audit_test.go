package api

import (
	"net"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/session"
)

// Audit 9 (#156): status does its slow work (interfaces, ARP table, QR code) outside the
// server's lock: a slow arp must not block every other action meanwhile.
func TestStatusDoesNotHoldTheLockWhileSlow(t *testing.T) {
	s := NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Solo")
	release := make(chan struct{})
	old := statusAddress
	statusAddress = func() string { <-release; return "192.0.2.1" }
	t.Cleanup(func() { statusAddress = old })
	done := make(chan DaemonStatus, 1)
	go func() { done <- s.getStatus() }()
	time.Sleep(50 * time.Millisecond) // status is now in the slow part
	locked := make(chan struct{})
	go func() { s.mu.Lock(); s.mu.Unlock(); close(locked) }()
	select {
	case <-locked:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("status held the server's lock while it looked for the address")
	}
	close(release)
	if st := <-done; st.LocalIP != "192.0.2.1" {
		t.Fatalf("status shows %q", st.LocalIP)
	}
}

// §3.7 (#156): instance reads the discovery server under the lock that its start writes it
// with (go test -race).
func TestInstanceWhileDiscoveryStarts(t *testing.T) {
	s := NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Solo")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			_ = s.instance()
		}
	}()
	for range 100 {
		d := discovery.NewDiscoveryServer("node", 0, &discovery.TransferOffer{})
		s.mu.Lock()
		s.discSrv = d
		s.mu.Unlock()
	}
	<-done
}

// §3.7 (#156): Stop stops the grace timer of a lost session: it does not fire on a stopped
// server.
func TestStopStopsTheGraceTimer(t *testing.T) {
	s := NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Solo")
	fired := make(chan struct{})
	s.mu.Lock()
	s.isReconnecting = true
	s.sessionGraceTimer = time.AfterFunc(200*time.Millisecond, func() { close(fired) })
	s.mu.Unlock()
	s.Stop()
	select {
	case <-fired:
		t.Fatal("the grace timer fired after Stop")
	case <-time.After(500 * time.Millisecond):
	}
}

// §3.7 (#156): the HTTP server bounds the headers of a request and idle connections (a slow
// client keeps no connection open); reads and writes are not bounded, for the WebSocket.
func TestHTTPServerTimeouts(t *testing.T) {
	srv := newHTTPServer(nil)
	if srv.ReadHeaderTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Fatalf("ReadHeaderTimeout %v, IdleTimeout %v: both must be set", srv.ReadHeaderTimeout, srv.IdleTimeout)
	}
	if srv.ReadTimeout != 0 || srv.WriteTimeout != 0 {
		t.Fatal("ReadTimeout or WriteTimeout set: they would cut the WebSocket")
	}
}

// §3.7 (#156): a session with another device replaces the active one and ends its transfers;
// the same device coming back keeps its transfer.
func TestNewPeerEndsTheOldTransfers(t *testing.T) {
	for _, c := range []struct {
		name     string
		samePeer bool
	}{{"another device", false}, {"the same device", true}} {
		t.Run(c.name, func(t *testing.T) {
			s := NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Solo")
			if err := s.loadIdentity(); err != nil {
				t.Fatal(err)
			}
			old, oldPeer := net.Pipe()
			defer oldPeer.Close()
			cancelled := false
			a, b := identity.Fingerprint{1}, identity.Fingerprint{2}
			s.mu.Lock()
			s.activeSession = session.NewChannel(old)
			s.trustPeer = a
			s.transferCancel = func() { cancelled = true }
			s.mu.Unlock()
			next, nextPeer := net.Pipe()
			defer nextPeer.Close()
			peer := b
			if c.samePeer {
				peer = a
			}
			s.startSession(session.NewChannel(next), peer, "phone", "192.0.2.2", false, false, "")
			if cancelled == c.samePeer {
				t.Fatalf("transfer cancelled: %v, want %v", cancelled, !c.samePeer)
			}
		})
	}
}
