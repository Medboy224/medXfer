//go:build diag

package transfer

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag/faultconn"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/identity"
)

// Audit 14 (#156): a long transfer on a network that cuts now and then goes on. Only
// reconnections without any chunk received in between count towards maxReconnects: here every
// connection is cut after about two chunks, far more than maxReconnects times in all.
func TestCutsWithProgressDoNotEndTheTransfer(t *testing.T) {
	device := newID(t)
	peer, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, addr := startHub(t, device)
	env := &chaosEnv{device: device, peer: peer, h: h, addr: addr}

	folder := filepath.Join(t.TempDir(), "long")
	data := bytes.Repeat([]byte("medXfer!"), 48*testChunk/8) // 48 chunks
	writeUserFile(t, filepath.Join(folder, "long.bin"), string(data))
	offer, files, err := BuildOffer([]string{folder}, testChunk)
	if err != nil {
		t.Fatal(err)
	}
	b := chaosBatch{offer: offer, files: files}

	t.Cleanup(engine.SetConnWrapper(func(role engine.ConnRole, c net.Conn) net.Conn {
		if role != engine.ConnDialed {
			return c
		}
		return faultconn.Wrap(c, faultconn.Plan{CutAfter: 5 * testChunk / 2})
	}))
	offer = env.serve(t, b)
	out := filepath.Join(t.TempDir(), "out")
	r := env.receiver(out, offer)
	r.Workers = 1
	if _, err := runFor(r, addr, offer, time.Minute); err != nil {
		t.Fatalf("transfer ended by cuts that let chunks through: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(out, "long", "long.bin"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("file received with %d bytes (%v), want %d", len(got), err, len(data))
	}
}
