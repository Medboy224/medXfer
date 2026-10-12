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
// receiver's side.
func limitingStage(t *testing.T) diag.Bottleneck {
	t.Helper()
	offer, paths, contents := tree(t, []int{4 * testChunk, 4 * testChunk, 4 * testChunk, 4 * testChunk,
		4 * testChunk, 4 * testChunk, 4 * testChunk, 4 * testChunk})
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	out := t.TempDir()
	offer.TransferID = p.id.String()
	r := p.receiver(out)
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
		slow       func(t *testing.T)
	}{
		{"slow disk", StageDiskWrite, func(t *testing.T) {
			t.Cleanup(engine.SetStorageWrapper(func(f engine.StorageFile, path string) engine.StorageFile {
				if strings.HasSuffix(path, ".medxfer") {
					return f
				}
				return faultfs.Wrap(f, faultfs.Plan{WriteDelay: 25 * time.Millisecond})
			}))
		}},
		{"throttled network", StageNetWait, func(t *testing.T) {
			lim := faultconn.NewLimiter(4 << 20)
			t.Cleanup(engine.SetConnWrapper(func(_ engine.ConnRole, c net.Conn) net.Conn {
				return faultconn.Wrap(c, faultconn.Plan{Limiter: lim})
			}))
		}},
		{"slow hash", StageHash, func(t *testing.T) {
			hashDelay = 25 * time.Millisecond
			t.Cleanup(func() { hashDelay = 0 })
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.slow(t)
			for i := 0; i < 3; i++ {
				b := limitingStage(t)
				t.Logf("run %d: %+v", i+1, b)
				if b.Stage != c.want {
					t.Fatalf("run %d: limiting stage %+v; want %s", i+1, b, c.want)
				}
			}
		})
	}
}
