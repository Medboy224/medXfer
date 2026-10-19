package api

import (
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pairing"
)

// DISC-11 in the core: peers_list names a device of mutual trust by its tag, and announces this
// device's own tags; an address alone names no one.
func TestRecognizeByTag(t *testing.T) {
	s, _ := startOwnIdentity(t, "Laptop")
	other, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	key[1] = 9
	if err := s.trust.Add(identity.Peer{Fingerprint: other.Fingerprint, Alias: "Galaxy A57", LastAddress: "192.0.2.7", TagKey: key}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tag := pairing.Tag(key, other.Fingerprint, pairing.TagPeriodOf(now))
	got := s.recognize([]discovery.Peer{
		{DeviceName: "medXfer-1a2b", HostIP: "192.0.2.9", Tags: []string{tag}},
		{DeviceName: "medXfer-9f9f", HostIP: "192.0.2.7"},
	})
	if got[0].TrustedAlias != "Galaxy A57" || got[1].TrustedAlias != "" {
		t.Fatalf("recognized: %q, %q", got[0].TrustedAlias, got[1].TrustedAlias)
	}
	if tags := s.announceTags(); len(tags) != 1 || tags[0] != pairing.Tag(key, s.identity.Fingerprint, pairing.TagPeriodOf(time.Now())) {
		t.Fatalf("announced tags %v", tags)
	}
}
