package api

import (
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/testkit"
)

// fastRedial shortens the wait between two attempts to dial a lost session again.
func fastRedial(t *testing.T) {
	every := redialEvery
	redialEvery = 100 * time.Millisecond
	t.Cleanup(func() { redialEvery = every })
}

// trustedPair pairs b with a by a's QR code (b dials) and makes them devices of mutual trust.
func trustedPair(t *testing.T) (a *DaemonServer, na *testkit.Node, b *DaemonServer, nb *testkit.Node) {
	t.Helper()
	a, na = startOwnIdentity(t, "Laptop")
	b, nb = startOwnIdentity(t, "Phone")
	pairByQR(t, na, nb, true)
	na.WaitEvent("trust_offered", 0)
	na.Request("trust_answer", map[string]bool{"accept": true})
	na.WaitEvent("trust_established", 0)
	nb.WaitEvent("trust_established", 0)
	return a, na, b, nb
}

// cutSession closes s's session as a network gone would: no "disconnect" is sent.
func cutSession(t *testing.T, s *DaemonServer) {
	t.Helper()
	s.mu.RLock()
	sess := s.activeSession
	s.mu.RUnlock()
	if sess == nil {
		t.Fatal("no session to cut")
	}
	sess.Close()
}

// WP-B.8, found on the real devices: the network gone a while, the session came back only when
// the user pressed Connect again. The side that dialed it dials it again, the other side needs
// nothing; both are paired again.
func TestLostSessionComesBack(t *testing.T) {
	fastRedial(t)
	a, na, b, nb := trustedPair(t)
	cutSession(t, b)
	nb.WaitEvent("reconnecting", 0)
	nb.WaitEvent("paired", 0)
	na.WaitEvent("paired", 0)

	// The side that did not dial waits for it: cut there, it comes back too.
	cutSession(t, a)
	na.WaitEvent("reconnecting", 0)
	na.WaitEvent("paired", 0)
	nb.WaitEvent("paired", 0)
	b.mu.RLock()
	back := b.activeSession != nil
	b.mu.RUnlock()
	if !back {
		t.Fatal("the dialing side has no session")
	}
}

// A disconnect, asked by the user, is not dialed again.
func TestDisconnectIsNotDialedAgain(t *testing.T) {
	fastRedial(t)
	_, na, _, nb := trustedPair(t)
	nb.Request("disconnect", nil)
	na.WaitEvent("disconnected", 0)
	if _, ok := nb.TryWaitFor(func(e testkit.Event) bool { return e.Event == "paired" }, 10*redialEvery); ok {
		t.Fatal("a disconnect asked by the user was dialed again")
	}
}

// A session without mutual trust (a QR code, a PIN) is not dialed again: that would take a code.
func TestUntrustedSessionIsNotDialedAgain(t *testing.T) {
	fastRedial(t)
	_, na := startOwnIdentity(t, "Laptop")
	b, nb := startOwnIdentity(t, "Phone")
	pairByQR(t, na, nb, false)
	cutSession(t, b)
	nb.WaitEvent("reconnecting", 0)
	if _, ok := nb.TryWaitFor(func(e testkit.Event) bool { return e.Event == "paired" }, 10*redialEvery); ok {
		t.Fatal("a session without mutual trust was dialed again")
	}
	if _, ok := na.TryWaitFor(func(e testkit.Event) bool { return e.Event == "pin_requested" }, redialEvery); ok {
		t.Fatal("the other device was asked for a PIN")
	}
}
