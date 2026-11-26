package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/testkit"
)

// PAIR-13: the user who connected to a device that transfers with another one learns it by the
// code busy, as an unreachable device has its own (XFER-08).
func TestBusyRefusalHasItsCode(t *testing.T) {
	e := pairFailed("192.0.2.7:18887", &rejectedError{reason: errBusyElsewhere.Error(), code: codeBusy}, "id")
	data, _ := e.Data.(map[string]string)
	if e.Event != "action_error" || data["code"] != codeBusy {
		t.Fatalf("%s %v; want an action_error with the code %q", e.Event, e.Data, codeBusy)
	}
}

// PAIR-13 and ID-05: during a transfer, another key at a trusted device's address is still
// refused as such and reported, not answered busy as the device it claims to be would be.
func TestChangedKeyIsReportedDuringATransfer(t *testing.T) {
	s, n := startOwnIdentity(t, "Daemon")
	known, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.trust.Add(identity.Peer{Fingerprint: known.Fingerprint, Alias: "phone", LastAddress: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.activeTransferID, s.trustPeer = "t1", identity.Fingerprint{1} // a transfer with a third device
	s.mu.Unlock()
	got := <-pairRequest(t, n.PeerAddr(), testPeerChan(t), session.Message{TrustsYou: true}, 5*time.Second)
	if got.Type != "pair_reject" || got.FileName != errKeyChanged.Error() {
		t.Fatalf("got %+v, want pair_reject (%s)", got, errKeyChanged)
	}
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		for _, e := range n.Client.Events() {
			if e.Event == "log" && strings.Contains(string(e.Data), "presents another key") {
				return
			}
		}
	}
	t.Fatal("the changed key was not reported")
}

// PAIR-13: a PIN on demand may be typed up to two minutes after the request, on the same
// connection. A transfer with another device that began meanwhile keeps its session: the PIN
// typed gets busy.
func TestPINTypedDuringAnotherTransferIsRefused(t *testing.T) {
	s, n := startOwnIdentity(t, "Laptop")
	dir := t.TempDir()
	id, err := identity.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := identity.OpenTrustStore(filepath.Join(dir, identity.TrustFile))
	if err != nil {
		t.Fatal(err)
	}
	third := identity.Fingerprint{1}
	typed := func() (string, error) {
		var asked struct {
			PIN string `json:"pin"`
		}
		if err := n.WaitEvent("pin_requested", 0).Decode(&asked); err != nil {
			return "", err
		}
		s.mu.Lock() // the user types; meanwhile a transfer with a third device begins
		s.activeTransferID, s.trustPeer = "t1", third
		s.mu.Unlock()
		return asked.PIN, nil
	}
	_, err = pairing.JoinPINOnDemand(context.Background(), n.PeerAddr(), typed,
		pairing.Options{Identity: id, Trust: ts, DeviceName: "Phone"})
	s.mu.RLock()
	peer := s.trustPeer
	s.mu.RUnlock()
	if err == nil || peer != third {
		t.Fatalf("pairing: %v; the session of the transfer was replaced: %v", err, peer != third)
	}
}

// PAIR-13: busy is told only to a device this user authorized (a trusted key, a QR code, a PIN,
// BLE). An unknown device gets the answer it gets without a transfer: it learns nothing of one.
func TestBusyIsToldOnlyToAnAuthorizedDevice(t *testing.T) {
	s, n := startOwnIdentity(t, "Laptop")
	s.mu.Lock()
	s.activeTransferID, s.trustPeer = "t1", identity.Fingerprint{1}
	s.mu.Unlock()
	got := <-pairRequest(t, n.PeerAddr(), testPeerChan(t), session.Message{}, 5*time.Second)
	if got.Type != "pair_reject" || got.Code != codePINRequired {
		t.Fatalf("an unknown device got %s with the code %q, want %q as without a transfer", got.Type, got.Code, codePINRequired)
	}
}

// PAIR-13: an automatic redial does not take the session another device has, transfer or not:
// it is refused with busy, which ends it.
func TestRedialDoesNotTakeAnotherSession(t *testing.T) {
	every := redialEvery
	redialEvery = time.Hour // b dials again below, once c has a's session
	t.Cleanup(func() { redialEvery = every })
	a, na, b, nb := trustedPair(t) // b dialed a
	cutSession(t, b)
	nb.WaitEvent("reconnecting", 0)
	c, nc := startOwnIdentity(t, "Tablet")
	pairByQR(t, na, nc, false)

	err := b.redialOnce(context.Background(), na.PeerAddr(), a.identity.Fingerprint)
	var rej *rejectedError
	if !errors.As(err, &rej) || rej.code != codeBusy {
		t.Fatalf("b dialed a again while c had its session: %v; want a refusal with the code %q", err, codeBusy)
	}
	a.mu.RLock()
	peer := a.trustPeer
	a.mu.RUnlock()
	if peer != c.identity.Fingerprint {
		t.Fatal("a's session is no longer c's")
	}
}

// PAIR-13: a session the user chose replaces another device's, which is told: it shows
// disconnected, and neither dials again (the devices took the session from each other every 3 s)
// nor says it reconnects (the app then waited for a session that never came back).
func TestReplacedDeviceIsTold(t *testing.T) {
	fastRedial(t)
	_, na, _, nb := trustedPair(t) // b dialed a
	_, nc := startOwnIdentity(t, "Tablet")
	pairByQR(t, na, nc, false) // c scans a's QR code: the user's choice
	nb.WaitEvent("disconnected", 5*time.Second)
	if e, ok := nb.TryWaitFor(func(e testkit.Event) bool { return e.Event == "paired" || e.Event == "reconnecting" }, 10*redialEvery); ok {
		t.Fatalf("the device replaced got %s after disconnected", e.Event)
	}
}
