package diag

import (
	"os/exec"
	"strings"
	"testing"
)

// DEV-01: diag observes the core, the core never depends on diag.
func TestCoreDoesNotImportDiag(t *testing.T) {
	core := []string{
		"github.com/Medboy224/medXfer/pkg/engine",
		"github.com/Medboy224/medXfer/pkg/protocol",
		"github.com/Medboy224/medXfer/pkg/session",
		"github.com/Medboy224/medXfer/pkg/manifest",
	}
	args := append([]string{"list", "-deps"}, core...)
	out, err := exec.Command("go", args...).CombinedOutput()
	if err != nil {
		t.Skipf("go list unavailable: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.HasPrefix(dep, "github.com/Medboy224/medXfer/pkg/diag") {
			t.Fatalf("a core package depends on %s", dep)
		}
	}
}
