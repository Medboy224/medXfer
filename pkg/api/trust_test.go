package api

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/testkit"
)

// startOwnIdentity starts a daemon with its own configuration folder, hence its own key and
// trusted devices (the other tests of this package share one).
func startOwnIdentity(t *testing.T, name string) (*DaemonServer, *testkit.Node) {
	t.Helper()
	configMu.RLock()
	previous := customConfigDir
	configMu.RUnlock()
	SetCustomConfigDir(t.TempDir())
	s := NewDaemonServer(0, t.TempDir(), name)
	n := testkit.StartNode(t, s) // Serve loads the identity before the control client connects
	SetCustomConfigDir(previous)
	return s, n
}

// pairRequest connects to the daemon's control listener as cfg's identity and sends a
// pair_request; it returns the daemon's answer, read within wait.
func pairRequest(t *testing.T, addr string, cfg securechan.Config, msg session.Message, wait time.Duration) <-chan session.Message {
	t.Helper()
	out := make(chan session.Message, 1)
	conn, err := dialSecure(context.Background(), addr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	ch := session.NewChannel(conn)
	msg.Type = "pair_request"
	if err := ch.Send(msg); err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = conn.SetReadDeadline(time.Now().Add(wait))
		resp, _ := ch.Read()
		out <- resp
	}()
	return out
}

type trustRequestEvent struct {
	Fingerprint string `json:"fingerprint"`
	Short       string `json:"short"`
	Self        string `json:"self"`
	DeviceName  string `json:"device_name"`
	Direction   string `json:"direction"`
}

// WP-3.6b: the pairing code gives no trust. An unknown key, even with the right code, waits
// for the user, who sees both fingerprints; "no" refuses, "once" admits without saving,
// "always" saves.
func TestUnknownDeviceIsDecidedByTheUser(t *testing.T) {
	s, n := startOwnIdentity(t, "Daemon")
	addr := n.PeerAddr()
	for _, c := range []struct {
		answer, want string
		saved        bool
	}{{"no", "pair_reject", false}, {"once", "pair_accept", false}, {"always", "pair_accept", true}} {
		peer := testPeerChan(t)
		resp := pairRequest(t, addr, peer, session.Message{DeviceName: "laptop", PairingCode: s.PairingCode()}, 10*time.Second)
		var req trustRequestEvent
		if err := n.WaitEvent("trust_request", 0).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Fingerprint != peer.Identity.Fingerprint.String() || req.Short != peer.Identity.Fingerprint.Short() ||
			req.Self != s.identity.Fingerprint.Short() || req.DeviceName != "laptop" || req.Direction != "incoming" {
			t.Fatalf("trust_request: %+v", req)
		}
		n.Request("trust_respond", map[string]string{"fingerprint": req.Fingerprint, "answer": c.answer})
		if got := <-resp; got.Type != c.want {
			t.Fatalf("answer %q: got %+v, want %s", c.answer, got, c.want)
		}
		if s.trust.Contains(peer.Identity.Fingerprint) != c.saved {
			t.Fatalf("answer %q: saved = %v", c.answer, !c.saved)
		}
	}
}

// No answer is a no (D3: never a silent acceptance).
func TestUnknownDeviceWithoutAnswerIsRefused(t *testing.T) {
	s, n := startOwnIdentity(t, "Daemon")
	s.mu.Lock()
	s.trustTimeout = 300 * time.Millisecond
	s.mu.Unlock()
	resp := pairRequest(t, n.PeerAddr(), testPeerChan(t), session.Message{PairingCode: s.PairingCode()}, 10*time.Second)
	if got := <-resp; got.Type != "pair_reject" {
		t.Fatalf("got %+v, want pair_reject", got)
	}
	n.WaitEvent("trust_expired", 0)
}

// A trusted device is admitted at once, without a code or a question.
func TestTrustedDeviceIsAdmittedAtOnce(t *testing.T) {
	s, n := startOwnIdentity(t, "Daemon")
	peer := testPeerChan(t)
	if err := s.TrustDevice(peer.Identity.Fingerprint.String(), "phone"); err != nil {
		t.Fatal(err)
	}
	if got := <-pairRequest(t, n.PeerAddr(), peer, session.Message{}, 5*time.Second); got.Type != "pair_accept" {
		t.Fatalf("got %+v, want pair_accept", got)
	}
	for _, e := range n.Client.Events() {
		if e.Event == "trust_request" {
			t.Fatal("the user was asked about a trusted device")
		}
	}
}

// pair_show returns a QR code URI; its secret admits one device, which becomes trusted. A
// wrong or reused secret is refused at once, without asking the user (PAIR-07).
func TestPairShowSecretAdmitsOnce(t *testing.T) {
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
	if u.Fingerprint != s.identity.Fingerprint {
		t.Fatal("the QR code does not carry the daemon's key")
	}
	addr := n.PeerAddr()
	pinned := func() securechan.Config {
		c := testPeerChan(t)
		c.Expected, c.AllowUnknown = u.Fingerprint, false
		return c
	}

	bad := u.Secret
	bad[0] ^= 0xff
	if got := <-pairRequest(t, addr, pinned(), session.Message{Secret: pairing.EncodeSecret(bad)}, 5*time.Second); got.Type != "pair_reject" {
		t.Fatalf("wrong secret: %+v", got)
	}
	scanner := pinned()
	if got := <-pairRequest(t, addr, scanner, session.Message{Secret: pairing.EncodeSecret(u.Secret), DeviceName: "phone"}, 5*time.Second); got.Type != "pair_accept" {
		t.Fatalf("right secret: %+v", got)
	}
	if p, ok := s.trust.Get(scanner.Identity.Fingerprint); !ok || p.Alias != "phone" {
		t.Fatal("the device that scanned the QR code is not trusted")
	}
	if got := <-pairRequest(t, addr, pinned(), session.Message{Secret: pairing.EncodeSecret(u.Secret)}, 5*time.Second); got.Type != "pair_reject" {
		t.Fatalf("reused secret: %+v", got)
	}
	for _, e := range n.Client.Events() {
		if e.Event == "trust_request" {
			t.Fatal("a QR code pairing asked the user")
		}
	}
}

// Two daemons: one shows a QR code, the other joins it with pair_uri; both end up paired and
// trusting each other.
func TestPairURIBetweenDaemons(t *testing.T) {
	a, na := startOwnIdentity(t, "Shower")
	b, nb := startOwnIdentity(t, "Scanner")
	na.Request("pair_show", nil)
	var shown struct {
		URI string `json:"uri"`
	}
	if err := na.WaitEvent("pair_show", 0).Decode(&shown); err != nil {
		t.Fatal(err)
	}
	// The QR code carries the LAN address; in the test the daemon listens on loopback too.
	u, err := pairing.Parse(shown.URI, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	u.Host = "127.0.0.1"
	nb.Request("pair_uri", map[string]string{"uri": u.String()})
	nb.WaitEvent("paired", 0)
	na.WaitEvent("paired", 0)
	if !a.trust.Contains(b.identity.Fingerprint) || !b.trust.Contains(a.identity.Fingerprint) {
		t.Fatal("the two daemons do not trust each other")
	}
}

// ID-05: a trusted device's address that presents another key is refused, without asking.
func TestDaemonRefusesChangedKey(t *testing.T) {
	s, n := startOwnIdentity(t, "Daemon")
	known, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.trust.Add(identity.Peer{Fingerprint: known.Fingerprint, Alias: "phone", LastAddress: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if got := <-pairRequest(t, n.PeerAddr(), testPeerChan(t), session.Message{}, 5*time.Second); got.Type != "pair_reject" || got.FileName != errKeyChanged.Error() {
		t.Fatalf("got %+v, want pair_reject (%s)", got, errKeyChanged)
	}
}

// Without a QR code, both users decide (D3): the daemon that connects asks its user about the
// other's key ("outgoing"), then the other asks its own ("incoming"). Nothing is saved.
func TestPairWithoutQRBothUsersDecide(t *testing.T) {
	a, na := startOwnIdentity(t, "Responder")
	b, nb := startOwnIdentity(t, "Initiator")
	nb.Request("pair", map[string]any{"ip": "127.0.0.1", "port": portNumber(t, na.PeerAddr()), "code": a.PairingCode()})
	for _, side := range []struct {
		n         *testkit.Node
		other     *DaemonServer
		direction string
	}{{nb, a, "outgoing"}, {na, b, "incoming"}} {
		var req trustRequestEvent
		if err := side.n.WaitEvent("trust_request", 0).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Fingerprint != side.other.identity.Fingerprint.String() || req.Direction != side.direction {
			t.Fatalf("trust_request: %+v", req)
		}
		side.n.Request("trust_respond", map[string]string{"fingerprint": req.Fingerprint, "answer": "once"})
	}
	nb.WaitEvent("paired", 0)
	na.WaitEvent("paired", 0)
	if a.trust.Contains(b.identity.Fingerprint) || b.trust.Contains(a.identity.Fingerprint) {
		t.Fatal(`"once" saved a trusted device`)
	}
}

func portNumber(t *testing.T, addr string) int {
	t.Helper()
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
