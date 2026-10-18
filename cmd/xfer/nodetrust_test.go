package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pairing"
)

// ID-05: the address of a trusted device presenting another key is reported; the same key,
// or an address nobody trusted, is not.
func TestKeyChanged(t *testing.T) {
	dir := t.TempDir()
	ts, err := identity.OpenTrustStore(filepath.Join(dir, identity.TrustFile))
	if err != nil {
		t.Fatal(err)
	}
	known, other := newTestIdentity(t), newTestIdentity(t)
	if err := ts.Add(identity.Peer{Fingerprint: known.Fingerprint, Alias: "phone", LastAddress: "192.168.1.20"}); err != nil {
		t.Fatal(err)
	}
	if _, bad := keyChanged(ts, "192.168.1.20", known.Fingerprint); bad {
		t.Error("the trusted key itself was reported")
	}
	p, bad := keyChanged(ts, "192.168.1.20", other.Fingerprint)
	if !bad || p.Fingerprint != known.Fingerprint {
		t.Fatalf("another key at a trusted address was not reported: %+v %v", p, bad)
	}
	msg := keyChangedAlert(p, "192.168.1.20", other.Fingerprint)
	if !strings.Contains(msg, "REFUSED") || !strings.Contains(msg, "xfer trust forget") {
		t.Fatalf("alert:\n%s", msg)
	}
	if _, bad := keyChanged(ts, "192.168.1.99", other.Fingerprint); bad {
		t.Error("an unknown address was reported")
	}
}

// PAIR-08: touchPeer never adds a device; it updates where a trusted one was seen.
func TestTouchPeer(t *testing.T) {
	ts, err := identity.OpenTrustStore(filepath.Join(t.TempDir(), identity.TrustFile))
	if err != nil {
		t.Fatal(err)
	}
	a := newTestIdentity(t)
	if err := touchPeer(ts, a.Fingerprint, "phone", "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	if ts.Contains(a.Fingerprint) {
		t.Fatal("an unknown device was saved")
	}
	if err := ts.Add(identity.Peer{Fingerprint: a.Fingerprint, Alias: "phone", LastAddress: "10.0.0.2"}); err != nil {
		t.Fatal(err)
	}
	if err := touchPeer(ts, a.Fingerprint, "", "10.0.0.7"); err != nil {
		t.Fatal(err)
	}
	if p, _ := ts.Get(a.Fingerprint); p.LastAddress != "10.0.0.7" || p.Alias != "phone" {
		t.Fatalf("after a new address: %+v", p)
	}
}

func newTestIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// xfer node's QR code: its secret admits one device; with --trust, mutual trust is offered.
func TestNodeInvitation(t *testing.T) {
	id, _ := testDevice(t)
	var n nodeInvitation
	if _, ok := n.redeem("anything"); ok {
		t.Fatal("a secret was accepted with no QR code shown")
	}
	u, err := n.renew(id.Fingerprint, "10.0.0.2", 18887, false)
	if err != nil {
		t.Fatal(err)
	}
	if u.Fingerprint != id.Fingerprint || u.Port != 18887 {
		t.Fatalf("URI: %+v", u)
	}
	bad := u.Secret
	bad[0] ^= 0xff
	if _, ok := n.redeem(pairing.EncodeSecret(bad)); ok {
		t.Fatal("a wrong secret was accepted")
	}
	if offer, ok := n.redeem(pairing.EncodeSecret(u.Secret)); !ok || offer {
		t.Fatalf("right secret: %v, %v", offer, ok)
	}
	if _, ok := n.redeem(pairing.EncodeSecret(u.Secret)); ok {
		t.Fatal("the secret was accepted twice")
	}
	u, _ = n.renew(id.Fingerprint, "10.0.0.2", 18887, true)
	if offer, ok := n.redeem(pairing.EncodeSecret(u.Secret)); !ok || !offer {
		t.Fatalf("--trust: %v, %v", offer, ok)
	}
}

// A trusted device is recognized in a discovery list by its last address (WP-6.5b).
func TestTrustedAt(t *testing.T) {
	ts, err := identity.OpenTrustStore(filepath.Join(t.TempDir(), identity.TrustFile))
	if err != nil {
		t.Fatal(err)
	}
	other, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.Add(identity.Peer{Fingerprint: other.Fingerprint, Alias: "phone", LastAddress: "192.0.2.7"}); err != nil {
		t.Fatal(err)
	}
	if got := trustedAt(ts, "192.0.2.7"); got != " - trusted 'phone'" {
		t.Fatalf("trustedAt = %q", got)
	}
	if trustedAt(ts, "192.0.2.8") != "" || trustedAt(nil, "192.0.2.7") != "" {
		t.Fatal("an unknown address, or no trust store, gave a label")
	}
}
