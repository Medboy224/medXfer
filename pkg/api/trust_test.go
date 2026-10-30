package api

import (
	"context"
	"net"
	"strconv"
	"strings"
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

// noEvent fails if n received an event named name.
func noEvent(t *testing.T, n *testkit.Node, name string) {
	t.Helper()
	for _, e := range n.Client.Events() {
		if e.Event == name {
			t.Fatalf("unexpected event %s", name)
		}
	}
}

type incompleteEvent struct {
	Fingerprint string `json:"fingerprint"`
	Side        string `json:"side"`
}

// PAIR-12: a device seen for the first time is never admitted on a pair_request, even with
// the pairing code: it needs a PIN or a QR code (pair_reject, code pin_required). No one is
// asked to compare fingerprints.
func TestUnknownDeviceNeedsAPINOrAQRCode(t *testing.T) {
	s, n := startOwnIdentity(t, "Daemon")
	got := <-pairRequest(t, n.PeerAddr(), testPeerChan(t), session.Message{DeviceName: "laptop", PairingCode: s.PairingCode()}, 10*time.Second)
	if got.Type != "pair_reject" || got.Code != codePINRequired {
		t.Fatalf("got %+v, want pair_reject (%s)", got, codePINRequired)
	}
	noEvent(t, n, "paired")
}

// PAIR-09: a device of mutual trust is admitted at once (P3). A trust on one side only gives
// nothing, and the user hears of it (trust_incomplete), whichever side lacks it.
func TestOnlyMutualTrustIsAdmittedAtOnce(t *testing.T) {
	s, n := startOwnIdentity(t, "Daemon")
	peer := testPeerChan(t)
	fp := peer.Identity.Fingerprint

	// It trusts us, we do not trust it.
	if got := <-pairRequest(t, n.PeerAddr(), peer, session.Message{TrustsYou: true}, 5*time.Second); got.Type != "pair_reject" || got.Code != codePINRequired {
		t.Fatalf("one-sided (remote): %+v", got)
	}
	var inc incompleteEvent
	if err := n.WaitEvent("trust_incomplete", 0).Decode(&inc); err != nil || inc.Side != "remote" || inc.Fingerprint != fp.Short() {
		t.Fatalf("trust_incomplete: %+v, %v", inc, err)
	}

	// We trust it, it does not trust us.
	if err := s.TrustDevice(fp.String(), "phone"); err != nil {
		t.Fatal(err)
	}
	if got := <-pairRequest(t, n.PeerAddr(), peer, session.Message{}, 5*time.Second); got.Type != "pair_reject" || got.Code != codePINRequired {
		t.Fatalf("one-sided (local): %+v", got)
	}
	if err := n.WaitEvent("trust_incomplete", 0).Decode(&inc); err != nil || inc.Side != "local" {
		t.Fatalf("trust_incomplete: %+v, %v", inc, err)
	}

	// Both: admitted at once, and the answer declares our side.
	got := <-pairRequest(t, n.PeerAddr(), peer, session.Message{TrustsYou: true}, 5*time.Second)
	if got.Type != "pair_accept" || !got.TrustsYou {
		t.Fatalf("mutual: %+v, want pair_accept declaring the trust", got)
	}
	var paired struct {
		Mutual bool `json:"mutual"`
	}
	if err := n.WaitEvent("paired", 0).Decode(&paired); err != nil || !paired.Mutual {
		t.Fatalf("paired: %+v, %v", paired, err)
	}
}

// pair_show returns a QR code URI; its secret admits one device, for this session only
// (PAIR-08). A wrong or reused secret is refused at once (PAIR-07).
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
	if s.trust.Contains(scanner.Identity.Fingerprint) {
		t.Fatal("a QR code pairing wrote the device (PAIR-08)")
	}
	if got := <-pairRequest(t, addr, pinned(), session.Message{Secret: pairing.EncodeSecret(u.Secret)}, 5*time.Second); got.Type != "pair_reject" {
		t.Fatalf("reused secret: %+v", got)
	}
}

// pairByQR pairs b with a by a's QR code; trust asks b to offer mutual trust once paired.
func pairByQR(t *testing.T, na, nb *testkit.Node, trust bool) {
	t.Helper()
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
	nb.Request("pair_uri", map[string]any{"uri": u.String(), "trust": trust})
	nb.WaitEvent("paired", 0)
	na.WaitEvent("paired", 0)
}

// Two daemons pair by QR code: a session, nothing written on either side (PAIR-08).
func TestPairURIBetweenDaemons(t *testing.T) {
	a, na := startOwnIdentity(t, "Shower")
	b, nb := startOwnIdentity(t, "Scanner")
	pairByQR(t, na, nb, false)
	if a.trust.Contains(b.identity.Fingerprint) || b.trust.Contains(a.identity.Fingerprint) {
		t.Fatal("a session wrote a trusted device")
	}
}

// PAIR-10, PAIR-09 end to end: b offers mutual trust, a's user accepts, both write the other.
// They then reconnect at once (P3, mutual). Once a forgets b, b's next pairing needs a PIN
// again, and b hears that its own entry is incomplete.
func TestMutualTrustBetweenDaemons(t *testing.T) {
	a, na := startOwnIdentity(t, "Laptop")
	b, nb := startOwnIdentity(t, "Phone")
	pairByQR(t, na, nb, true)
	var offered struct {
		DeviceName string `json:"device_name"`
		TimeoutS   int    `json:"timeout_s"`
	}
	if err := na.WaitEvent("trust_offered", 0).Decode(&offered); err != nil || offered.TimeoutS != 60 {
		t.Fatalf("trust_offered: %+v, %v", offered, err)
	}
	na.Request("trust_answer", map[string]bool{"accept": true})
	na.WaitEvent("trust_established", 0)
	nb.WaitEvent("trust_established", 0)
	if !a.trust.Contains(b.identity.Fingerprint) || !b.trust.Contains(a.identity.Fingerprint) {
		t.Fatal("mutual trust not written on both sides")
	}

	port := portNumber(t, na.PeerAddr())
	nb.Request("disconnect", nil)
	nb.Request("pair", map[string]any{"ip": "127.0.0.1", "port": port})
	var paired struct {
		Mutual bool `json:"mutual"`
	}
	if err := nb.WaitEvent("paired", 0).Decode(&paired); err != nil || !paired.Mutual {
		t.Fatalf("reconnection: %+v, %v", paired, err)
	}

	if _, err := a.trust.Forget(b.identity.Fingerprint); err != nil {
		t.Fatal(err)
	}
	nb.Request("disconnect", nil)
	nb.Request("pair", map[string]any{"ip": "127.0.0.1", "port": port})
	var inc incompleteEvent
	if err := nb.WaitEvent("trust_incomplete", 0).Decode(&inc); err != nil || inc.Side != "local" {
		t.Fatalf("trust_incomplete: %+v, %v", inc, err)
	}
	nb.WaitEvent("pin_needed", 0)
	na.WaitEvent("pin_requested", 0)
}

// J6c, found on the real devices: two devices of mutual trust that meet again by a QR code (the
// phone scanning the PC's, the PC's network created for it) are of mutual trust: the session
// says so, and trust is not offered again. Before, a QR code's session was never mutual: each
// scan, with "remember this device" on, offered trust again.
func TestQRCodeBetweenTrustedDevicesIsMutual(t *testing.T) {
	a, na := startOwnIdentity(t, "Laptop")
	b, nb := startOwnIdentity(t, "Phone")
	scan := func() {
		t.Helper()
		na.Request("pair_show", map[string]any{"trust": true}) // "remember this device", as the app does
		var shown struct {
			URI string `json:"uri"`
		}
		if err := na.WaitEvent("pair_show", 0).Decode(&shown); err != nil {
			t.Fatal(err)
		}
		u, err := pairing.Parse(shown.URI, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		u.Host = "127.0.0.1"
		nb.Request("pair_uri", map[string]any{"uri": u.String(), "trust": true})
	}
	scan()
	na.WaitEvent("paired", 0)
	nb.WaitEvent("paired", 0)
	// Both offered (the code's shower, the scanner): both commit, nobody has to answer.
	na.WaitEvent("trust_established", 0)
	nb.WaitEvent("trust_established", 0)
	if !a.trust.Contains(b.identity.Fingerprint) || !b.trust.Contains(a.identity.Fingerprint) {
		t.Fatal("mutual trust not written on both sides")
	}

	nb.Request("disconnect", nil)
	na.WaitEvent("disconnected", 0)
	scan()
	var paired struct {
		Mutual bool `json:"mutual"`
	}
	for _, n := range []*testkit.Node{na, nb} {
		if err := n.WaitEvent("paired", 0).Decode(&paired); err != nil || !paired.Mutual {
			t.Fatalf("the QR code's session between trusted devices: %+v, %v; want mutual", paired, err)
		}
	}
	offered := func(e testkit.Event) bool { return e.Event == "trust_offered" }
	for _, n := range []*testkit.Node{na, nb} {
		if _, ok := n.TryWaitFor(offered, 2*time.Second); ok {
			t.Fatal("trust offered again between devices of mutual trust")
		}
	}
}

// A no writes nothing, and the offerer hears it.
func TestTrustOfferDeclined(t *testing.T) {
	a, na := startOwnIdentity(t, "Laptop")
	b, nb := startOwnIdentity(t, "Phone")
	pairByQR(t, na, nb, true)
	na.WaitEvent("trust_offered", 0)
	na.Request("trust_answer", map[string]bool{"accept": false})
	var declined struct {
		Reason string `json:"reason"`
	}
	if err := nb.WaitEvent("trust_declined", 0).Decode(&declined); err != nil || declined.Reason != "declined" {
		t.Fatalf("trust_declined: %+v, %v", declined, err)
	}
	if a.trust.Contains(b.identity.Fingerprint) || b.trust.Contains(a.identity.Fingerprint) {
		t.Fatal("a declined offer wrote a trusted device")
	}
	na.Request("trust_answer", map[string]bool{"accept": true}) // nothing left to answer
	na.WaitEvent("action_error", 0)
}

// PAIR-11 between daemons: b picks a (pair, by address); a shows a PIN on demand
// (pin_requested), b is asked to type it (pin_needed), then pair_pin pairs them. The first
// connection, closed before the PIN is typed, is no failure on a's side.
func TestPairByPINOnDemand(t *testing.T) {
	a, na := startOwnIdentity(t, "Laptop")
	b, nb := startOwnIdentity(t, "Phone")
	nb.Request("pair", map[string]any{"ip": "127.0.0.1", "port": portNumber(t, na.PeerAddr())})
	var asked struct {
		PIN  string `json:"pin"`
		From string `json:"from"`
	}
	if err := na.WaitEvent("pin_requested", 0).Decode(&asked); err != nil || len(strings.ReplaceAll(asked.PIN, " ", "")) != 6 || asked.From != "127.0.0.1" {
		t.Fatalf("pin_requested: %+v, %v", asked, err)
	}
	var needed struct {
		Target string `json:"target"`
	}
	if err := nb.WaitEvent("pin_needed", 0).Decode(&needed); err != nil || needed.Target == "" {
		t.Fatalf("pin_needed: %+v, %v", needed, err)
	}
	nb.Request("pair_pin", map[string]any{"target": needed.Target, "pin": asked.PIN})
	nb.WaitEvent("paired", 0)
	na.WaitEvent("paired", 0)
	noEvent(t, na, "pair_failed")
	if a.trust.Contains(b.identity.Fingerprint) || b.trust.Contains(a.identity.Fingerprint) {
		t.Fatal("a PIN pairing wrote a trusted device (PAIR-08)")
	}
}

// ID-05: a trusted device's address that presents another key is refused.
func TestDaemonRefusesChangedKey(t *testing.T) {
	s, n := startOwnIdentity(t, "Daemon")
	known, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.trust.Add(identity.Peer{Fingerprint: known.Fingerprint, Alias: "phone", LastAddress: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if got := <-pairRequest(t, n.PeerAddr(), testPeerChan(t), session.Message{TrustsYou: true}, 5*time.Second); got.Type != "pair_reject" || got.FileName != errKeyChanged.Error() {
		t.Fatalf("got %+v, want pair_reject (%s)", got, errKeyChanged)
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

// Bible §8.2: on an established session, a message type the daemon does not know is answered
// with error{code: unsupported}; the session stays open.
func TestUnknownMessageIsUnsupported(t *testing.T) {
	s, n := startOwnIdentity(t, "Daemon")
	peer := testPeerChan(t)
	if err := s.TrustDevice(peer.Identity.Fingerprint.String(), "phone"); err != nil {
		t.Fatal(err)
	}
	conn, err := dialSecure(context.Background(), n.PeerAddr(), peer)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch := session.NewChannel(conn)
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := ch.Send(session.Message{Type: "pair_request", DeviceName: "phone", TrustsYou: true}); err != nil {
		t.Fatal(err)
	}
	if m, err := ch.Read(); err != nil || m.Type != "pair_accept" {
		t.Fatalf("pairing: %+v, %v", m, err)
	}
	n.WaitEvent("paired", 0)
	if err := ch.Send(session.Message{Type: "from_the_future"}); err != nil {
		t.Fatal(err)
	}
	m, err := ch.Read()
	if err != nil || m.Type != session.TypeError || m.Code != session.CodeUnsupported {
		t.Fatalf("reply to an unknown type: %+v, %v", m, err)
	}
	// An error message itself is not answered: the next message is the reply to a known one.
	if err := ch.Send(session.Message{Type: session.TypeError, Code: "whatever"}); err != nil {
		t.Fatal(err)
	}
	if err := ch.Send(session.Message{Type: "from_the_future_too"}); err != nil {
		t.Fatal(err)
	}
	if m, err := ch.Read(); err != nil || m.Code != session.CodeUnsupported || !strings.Contains(m.Error, "from_the_future_too") {
		t.Fatalf("after an error message: %+v, %v", m, err)
	}
}

// PAIR-11, refused: a shows a PIN on demand, its user refuses (pin_refuse); the PIN read out
// no longer pairs, and b gets the neutral pair_failed (PAIR-07).
func TestRefusedPINOnDemand(t *testing.T) {
	_, na := startOwnIdentity(t, "Laptop")
	_, nb := startOwnIdentity(t, "Phone")
	nb.Request("pair", map[string]any{"ip": "127.0.0.1", "port": portNumber(t, na.PeerAddr())})
	var asked struct {
		PIN string `json:"pin"`
	}
	if err := na.WaitEvent("pin_requested", 0).Decode(&asked); err != nil {
		t.Fatal(err)
	}
	var needed struct {
		Target string `json:"target"`
	}
	if err := nb.WaitEvent("pin_needed", 0).Decode(&needed); err != nil {
		t.Fatal(err)
	}
	na.Request("pin_refuse", nil)
	na.WaitEvent("pin_refused", 0)
	nb.Request("pair_pin", map[string]any{"target": needed.Target, "pin": asked.PIN})
	nb.WaitEvent("pair_failed", 0)
	noEvent(t, na, "paired")
}

// staleTrusted trusts on s a device of another key, last seen at 127.0.0.1: the address the
// devices of these tests come from.
func staleTrusted(t *testing.T, s *DaemonServer) {
	t.Helper()
	old, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.trust.Add(identity.Peer{Fingerprint: old.Fingerprint, Alias: "old phone", LastAddress: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
}

// waitTaken waits for the log line saying a device paired by the user took a trusted address,
// among all the events n received: it may come before an event waited for already.
func waitTaken(t *testing.T, n *testkit.Node) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		for _, e := range n.Client.Events() {
			if e.Event == "log" && strings.Contains(string(e.Data), "takes it") {
				return
			}
		}
	}
	t.Fatal("no log line of an address taken")
}

// ID-05, #217: an address is no identity. A device that proves the user's authorisation, the QR
// code's secret, takes the address a trusted device of another key had, and the log says so.
// Without such a proof, it is still refused (TestDaemonRefusesChangedKey).
func TestQRSecretTakesAKnownAddress(t *testing.T) {
	s, n := startOwnIdentity(t, "Daemon")
	staleTrusted(t, s)
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
	scanner := testPeerChan(t)
	scanner.Expected, scanner.AllowUnknown = u.Fingerprint, false
	got := <-pairRequest(t, n.PeerAddr(), scanner, session.Message{Secret: pairing.EncodeSecret(u.Secret), DeviceName: "phone"}, 5*time.Second)
	if got.Type != "pair_accept" {
		t.Fatalf("got %+v, want pair_accept", got)
	}
	waitTaken(t, n)
}

// ID-05, #217: the same with a PIN checked by the PAKE, on both sides at once: the stale trusted
// devices of the typist and of the shower at that address no longer stop the pairing. A wrong
// PIN still does.
func TestPINTakesAKnownAddress(t *testing.T) {
	a, na := startOwnIdentity(t, "Shower")
	b, nb := startOwnIdentity(t, "Typist")
	staleTrusted(t, a)
	staleTrusted(t, b)
	na.Request("pair_pin_show", nil)
	var shown pinShownEvent
	if err := na.WaitEvent("pair_pin_shown", 0).Decode(&shown); err != nil {
		t.Fatal(err)
	}
	bad := []byte(strings.ReplaceAll(shown.PIN, " ", ""))
	bad[0] = '0' + (bad[0]-'0'+1)%10
	nb.Request("pair_pin", map[string]any{"target": na.PeerAddr(), "pin": string(bad)})
	nb.WaitEvent("pair_failed", 0)

	nb.Request("pair_pin", map[string]any{"target": na.PeerAddr(), "pin": shown.PIN})
	nb.WaitEvent("paired", 0)
	na.WaitEvent("paired", 0)
	waitTaken(t, na)
	waitTaken(t, nb)
}
