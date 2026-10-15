package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pairing"
)

func TestParseTrustAnswer(t *testing.T) {
	for in, want := range map[string]trustAnswer{
		"y": trustOnce, "Yes": trustOnce, "o": trustOnce, " oui ": trustOnce,
		"a": trustAlways, "always": trustAlways, "toujours": trustAlways,
		"": trustRefused, "n": trustRefused, "no": trustRefused, "yolo": trustRefused, "ok": trustRefused,
	} {
		if got := parseTrustAnswer(in); got != want {
			t.Errorf("parseTrustAnswer(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestTrustQuestionShowsBothFingerprints(t *testing.T) {
	a, b := newTestIdentity(t), newTestIdentity(t)
	q := trustQuestion("phone", a.Fingerprint, b.Fingerprint)
	if !strings.Contains(q, a.Fingerprint.Short()) || !strings.Contains(q, b.Fingerprint.Short()) || !strings.Contains(q, "[N]o") {
		t.Fatalf("question:\n%s", q)
	}
}

// ID-05: the address of a trusted device presenting another key is reported; the same key,
// or an address nobody trusted, is not.
func TestKeyChanged(t *testing.T) {
	dir := t.TempDir()
	ts, err := identity.OpenTrustStore(filepath.Join(dir, identity.TrustFile))
	if err != nil {
		t.Fatal(err)
	}
	known, other := newTestIdentity(t), newTestIdentity(t)
	if err := rememberPeer(ts, known.Fingerprint, "phone", "192.168.1.20", trustAlways); err != nil {
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

func TestRememberPeer(t *testing.T) {
	ts, err := identity.OpenTrustStore(filepath.Join(t.TempDir(), identity.TrustFile))
	if err != nil {
		t.Fatal(err)
	}
	a := newTestIdentity(t)
	if err := rememberPeer(ts, a.Fingerprint, "phone", "10.0.0.2", trustOnce); err != nil {
		t.Fatal(err)
	}
	if ts.Contains(a.Fingerprint) {
		t.Fatal("a once-only decision was saved")
	}
	if err := rememberPeer(ts, a.Fingerprint, "phone", "10.0.0.2", trustAlways); err != nil {
		t.Fatal(err)
	}
	if p, ok := ts.Get(a.Fingerprint); !ok || p.LastAddress != "10.0.0.2" || p.Alias != "phone" {
		t.Fatalf("saved peer = %+v, %v", p, ok)
	}
	// A trusted device seen at a new address: the address is updated.
	if err := rememberPeer(ts, a.Fingerprint, "", "10.0.0.7", trustRefused); err != nil {
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

// xfer node's QR code: its secret admits one device, saved unless --once (WP-3.6b).
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
	if a, ok := n.redeem(pairing.EncodeSecret(u.Secret)); !ok || a != trustAlways {
		t.Fatalf("right secret: %v, %v", a, ok)
	}
	if _, ok := n.redeem(pairing.EncodeSecret(u.Secret)); ok {
		t.Fatal("the secret was accepted twice")
	}
	u, _ = n.renew(id.Fingerprint, "10.0.0.2", 18887, true)
	if a, ok := n.redeem(pairing.EncodeSecret(u.Secret)); !ok || a != trustOnce {
		t.Fatalf("--once: %v, %v", a, ok)
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
