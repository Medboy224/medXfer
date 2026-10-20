package api

import (
	"os"
	"testing"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/securechan"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if testConfigDir != "" {
		_ = os.RemoveAll(testConfigDir)
	}
	os.Exit(code)
}

// testPeerChan is the secure channel of a test peer: a fresh identity, which the daemon
// does not know, so the connection starts UNAUTH and the pairing code decides.
func testPeerChan(t *testing.T) securechan.Config {
	t.Helper()
	id, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return securechan.Config{Identity: id, AllowUnknown: true}
}
