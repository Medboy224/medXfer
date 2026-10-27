package api

import (
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/testkit"
)

// fastHeartbeat shortens the pings and the silence that loses a session.
func fastHeartbeat(t *testing.T) {
	every, silence := pingEvery, sessionSilence
	pingEvery, sessionSilence = 100*time.Millisecond, 600*time.Millisecond
	t.Cleanup(func() { pingEvery, sessionSilence = every, silence })
}

// WP-B.8, found on the real devices: a device gone without a word (its network off) was seen
// about 2.5 min later, by TCP's keepalive. A session silent for sessionSilence is now lost.
func TestSilentPeerIsLost(t *testing.T) {
	fastHeartbeat(t)
	s, n := startOwnIdentity(t, "Daemon")
	n.Request("pair_show", nil)
	var shown struct {
		URI string `json:"uri"`
	}
	if err := n.WaitEvent("pair_show", 0).Decode(&shown); err != nil {
		t.Fatal(err)
	}
	u, err := pairing.Parse(shown.URI, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cfg := testPeerChan(t)
	cfg.Expected, cfg.AllowUnknown = s.identity.Fingerprint, false
	// A peer that pairs, then says nothing more and keeps its connection open.
	if got := <-pairRequest(t, n.PeerAddr(), cfg, session.Message{Secret: pairing.EncodeSecret(u.Secret), DeviceName: "phone"}, 5*time.Second); got.Type != "pair_accept" {
		t.Fatalf("pairing: %+v", got)
	}
	n.WaitEvent("paired", 0)
	n.WaitEvent("reconnecting", 5*time.Second)
}

// Two devices that ping each other keep a session with nothing else to say.
func TestQuietSessionStaysUp(t *testing.T) {
	fastHeartbeat(t)
	_, na := startOwnIdentity(t, "Laptop")
	_, nb := startOwnIdentity(t, "Phone")
	pairByQR(t, na, nb, false)
	lost := func(e testkit.Event) bool { return e.Event == "reconnecting" }
	if _, ok := na.TryWaitFor(lost, 5*sessionSilence); ok {
		t.Fatal("a quiet session was lost on the side that showed the code")
	}
	if _, ok := nb.TryWaitFor(lost, sessionSilence); ok {
		t.Fatal("a quiet session was lost on the side that scanned it")
	}
}
