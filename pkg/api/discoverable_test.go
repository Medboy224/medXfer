package api

import (
	"testing"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/identity"
)

// N1: a trusted device's last address is no longer enough to name a device in a scan: another
// device may take it. Recognition is by tag (TestRecognizeByTag).
func TestScanDoesNotRecognizeByAddress(t *testing.T) {
	s, _ := startOwnIdentity(t, "Scanner")
	known, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.trust.Add(identity.Peer{Fingerprint: known.Fingerprint, Alias: "phone", LastAddress: "192.0.2.7"}); err != nil {
		t.Fatal(err)
	}
	got := s.recognize([]discovery.Peer{{HostIP: "192.0.2.7", Port: 18888}, {HostIP: "192.0.2.8", Port: 18888}})
	if len(got) != 2 || got[0].TrustedAlias != "" || got[1].TrustedAlias != "" {
		t.Fatalf("recognize: %+v", got)
	}
}
