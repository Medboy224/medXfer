package api

import (
	"testing"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/identity"
)

// A trusted device is recognized in a scan by its last address, before any pairing; the alias
// stays on this device (it is in the local peers_list only).
func TestScanRecognizesTrustedDevices(t *testing.T) {
	s, _ := startOwnIdentity(t, "Scanner")
	known, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.trust.Add(identity.Peer{Fingerprint: known.Fingerprint, Alias: "phone", LastAddress: "192.0.2.7"}); err != nil {
		t.Fatal(err)
	}
	got := s.recognize([]discovery.Peer{{HostIP: "192.0.2.7", Port: 18888}, {HostIP: "192.0.2.8", Port: 18888}})
	if len(got) != 2 || got[0].TrustedAlias != "phone" || got[1].TrustedAlias != "" {
		t.Fatalf("recognize: %+v", got)
	}
}
