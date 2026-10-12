//go:build diag

package transfer

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/diag/faultconn"
	"github.com/Medboy224/medXfer/pkg/diag/faultfs"
	"github.com/Medboy224/medXfer/pkg/engine"
)

// stageRecorder is a receiver's events with its stages measured by diag.
type stageRecorder struct {
	recorder
	*diag.Stages
}

// limitingStage receives a small folder and returns the limiting stage diag finds on the
// receiver's side. noSync takes the disk out of a scenario that slows another stage: a fsync
// may hold concurrent writes, and a busy disk would compete with the stage under test (#87).
func limitingStage(t *testing.T, noSync bool) diag.Bottleneck {
	t.Helper()
	offer, paths, contents := tree(t, []int{4 * testChunk, 4 * testChunk, 4 * testChunk, 4 * testChunk,
		4 * testChunk, 4 * testChunk, 4 * testChunk, 4 * testChunk})
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	out := t.TempDir()
	offer.TransferID = p.id.String()
	r := p.receiver(out)
	r.NoSync = noSync
	ev := &stageRecorder{Stages: diag.NewStages()}
	r.Events = ev
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := r.Run(ctx, p.addr, offer); err != nil {
		t.Fatal(err)
	}
	checkFiles(t, out, offer, contents, nil)
	return ev.Bottleneck()
}

// DEV-04: in synthetic scenarios where one stage is made slow, the heuristic must name that
// stage every time (bible: "dans 100 % des scénarios").
func TestLimitingStageIsFound(t *testing.T) {
	for _, c := range []struct {
		name, want string
		noSync     bool // the disk is not a factor of this scenario (#87)
		slow       func(t *testing.T)
	}{
		{"slow disk", StageDiskWrite, false, func(t *testing.T) {
			t.Cleanup(engine.SetStorageWrapper(func(f engine.StorageFile, path string) engine.StorageFile {
				if strings.HasSuffix(path, ".part.state") {
					return f
				}
				return faultfs.Wrap(f, faultfs.Plan{WriteDelay: 25 * time.Millisecond})
			}))
		}},
		{"throttled network", StageNetWait, true, func(t *testing.T) {
			lim := faultconn.NewLimiter(4 << 20)
			t.Cleanup(engine.SetConnWrapper(func(_ engine.ConnRole, c net.Conn) net.Conn {
				return faultconn.Wrap(c, faultconn.Plan{Limiter: lim})
			}))
		}},
		{"slow hash", StageHash, true, func(t *testing.T) {
			hashDelay = 25 * time.Millisecond
			t.Cleanup(func() { hashDelay = 0 })
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.slow(t)
			for i := 0; i < 3; i++ {
				b := limitingStage(t, c.noSync)
				t.Logf("run %d: %+v", i+1, b)
				if b.Stage != c.want {
					t.Fatalf("run %d: limiting stage %+v; want %s", i+1, b, c.want)
				}
			}
		})
	}
}
