package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/api"
	"github.com/Medboy224/medXfer/pkg/discovery"
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

// DISC-07, DISC-11: a device of mutual trust shows in a discovery list under the name recorded
// here, recognized by its tag; a device at its last address without the tag stays anonymous.
func TestListedName(t *testing.T) {
	ts, err := identity.OpenTrustStore(filepath.Join(t.TempDir(), identity.TrustFile))
	if err != nil {
		t.Fatal(err)
	}
	other := newTestIdentity(t)
	key := make([]byte, 32)
	key[0] = 7
	if err := ts.Add(identity.Peer{Fingerprint: other.Fingerprint, Alias: "Naruto", LastAddress: "192.0.2.7", TagKey: key}); err != nil {
		t.Fatal(err)
	}
	tag := pairing.Tag(key, other.Fingerprint, pairing.TagPeriodOf(time.Now()))
	if got := listedName(ts, discovery.Peer{DeviceName: "medXfer-1a2b", HostIP: "192.0.2.9", Tags: []string{"zzzzzzzzzzzzz", tag}}); got != "Naruto (trusted)" {
		t.Fatalf("listedName = %q", got)
	}
	// Its address, taken by another device without the tag: not recognized.
	if got := listedName(ts, discovery.Peer{DeviceName: "medXfer-9f9f", HostIP: "192.0.2.7"}); got != "medXfer-9f9f" {
		t.Fatalf("recognized by its address: %q", got)
	}
	if got := listedName(nil, discovery.Peer{DeviceName: "medXfer-1a2b", Tags: []string{tag}}); got != "medXfer-1a2b" {
		t.Fatalf("no trust store: %q", got)
	}
}

// N6: a name the user gave a trusted device (xfer trust rename) stays, whatever name the device
// sends when it pairs again; xfer name sets this device's own name.
func TestRenameAndName(t *testing.T) {
	dir := t.TempDir()
	ts, err := identity.OpenTrustStore(filepath.Join(dir, identity.TrustFile))
	if err != nil {
		t.Fatal(err)
	}
	a := newTestIdentity(t)
	if err := ts.Add(identity.Peer{Fingerprint: a.Fingerprint, Alias: "localhost"}); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runTrust(&out, dir, []string{"rename", a.Fingerprint.Short(), "Téléphone", "de", "Fatou"}); err != nil {
		t.Fatal(err)
	}
	ts, _ = identity.OpenTrustStore(filepath.Join(dir, identity.TrustFile))
	if p, _ := ts.Get(a.Fingerprint); p.Alias != "Téléphone de Fatou" || !p.Renamed {
		t.Fatalf("after rename: %+v", p)
	}
	if err := touchPeer(ts, a.Fingerprint, "Samsung SM-A576B", "10.0.0.9"); err != nil {
		t.Fatal(err)
	}
	if p, _ := ts.Get(a.Fingerprint); p.Alias != "Téléphone de Fatou" || p.LastAddress != "10.0.0.9" {
		t.Fatalf("the name the device sent replaced the user's: %+v", p)
	}
	if err := runTrust(&out, dir, []string{"rename", a.Fingerprint.Short(), " \t "}); err == nil {
		t.Fatal("an empty name was accepted")
	}

	api.SetCustomConfigDir(t.TempDir())
	t.Cleanup(func() { api.SetCustomConfigDir("") })
	if err := runName(&out, []string{"PC", "d'Amadou"}); err != nil {
		t.Fatal(err)
	}
	if got := api.DeviceName(); got != "PC d'Amadou" {
		t.Fatalf("device name %q", got)
	}
}
