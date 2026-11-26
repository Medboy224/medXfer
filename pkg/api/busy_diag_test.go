//go:build diag

package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/testkit"
)

// #298, decision R1: a device of mutual trust that dials again while this one transfers with
// another device is refused with the code busy, which ends its attempts. The transfer goes on to
// its end, with the session it runs on.
func TestRedialDuringAnotherTransferIsRefused(t *testing.T) {
	every := redialEvery
	redialEvery = time.Hour // b dials again below, once a transfers with c
	t.Cleanup(func() { redialEvery = every })
	testkit.Throttle(t, 1<<20)

	a, na, b, nb := trustedPair(t) // b dialed a
	cutSession(t, b)               // b loses the network
	nb.WaitEvent("reconnecting", 0)

	c, nc := startOwnIdentity(t, "Tablet")
	pairByQR(t, na, nc, false)
	src := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(src, make([]byte, 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	nc.Request("send", map[string]any{"paths": []string{src}, "target_ip": na.PeerAddr()})
	na.WaitEvent("incoming_offer", 0)
	na.Request("respond_offer", map[string]any{"accept": true})
	deadline := time.Now().Add(10 * time.Second)
	for {
		a.mu.RLock()
		running := a.activeTransferID != ""
		a.mu.RUnlock()
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("c's transfer did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}

	err := b.redialOnce(context.Background(), na.PeerAddr(), a.identity.Fingerprint)
	var rej *rejectedError
	if !errors.As(err, &rej) || rej.code != codeBusy {
		t.Fatalf("b dialed a again during c's transfer: %v; want a refusal with the code %q", err, codeBusy)
	}
	if e := na.WaitEventAny(60*time.Second, "transfer_complete", "transfer_failed"); e.Event != "transfer_complete" {
		t.Fatalf("c's transfer: %s %s", e.Event, e.Data)
	}
	a.mu.RLock()
	peer := a.trustPeer
	a.mu.RUnlock()
	if peer != c.identity.Fingerprint {
		t.Fatal("a's session is no longer c's")
	}
}
