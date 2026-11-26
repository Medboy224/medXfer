package api

import (
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/session"
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
