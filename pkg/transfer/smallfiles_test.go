package transfer

// Where the time of a small file goes (WP-4.11): a folder of small files received in loopback,
// as it is, without fsync, and into the null sink. A measure, not a test: it runs only with
// MEDXFER_SMALL_PROFILE=1; MEDXFER_SMALL_N sets the number of files (default 2000).

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/protocol"
)

// WP-4.11: an offer of small files gets SmallFileWorkers data connections, unless the
// receiver sets its number; any other offer gets DefaultWorkers.
func TestChooseWorkers(t *testing.T) {
	offer := func(items int, each int64) protocol.Offer {
		o := protocol.Offer{TotalBytes: int64(items) * each}
		o.Items = make([]protocol.OfferItem, items)
		return o
	}
	small, big, few := offer(2000, 8<<10), offer(3, 256<<20), offer(5, 1<<10)
	for _, c := range []struct {
		name  string
		n     int
		offer protocol.Offer
		want  int
	}{
		{"small files, automatic", 0, small, SmallFileWorkers},
		{"big files, automatic", 0, big, DefaultWorkers},
		{"few small files, automatic", 0, few, DefaultWorkers},
		{"small files, 4 asked", 4, small, 4},
		{"too many asked", 20, big, MaxWorkers},
	} {
		if got := chooseWorkers(c.n, c.offer); got != c.want {
			t.Errorf("%s: %d workers; want %d", c.name, got, c.want)
		}
	}
}

func TestSmallFilesProfile(t *testing.T) {
	if os.Getenv("MEDXFER_SMALL_PROFILE") == "" {
		t.Skip("a measure, not a test: MEDXFER_SMALL_PROFILE=1 runs it (WP-4.11)")
	}
	n := 2000
	if s := os.Getenv("MEDXFER_SMALL_N"); s != "" {
		n, _ = strconv.Atoi(s)
	}
	folder := filepath.Join(t.TempDir(), "folder")
	rng := rand.New(rand.NewPCG(4011, 1))
	buf := make([]byte, 16<<10)
	for i := 0; i < n; i++ {
		size := 1024 + rng.IntN(15*1024+1)
		for k := range buf[:size] {
			buf[k] = byte(rng.Uint32())
		}
		writeUserFile(t, filepath.Join(folder, fmt.Sprintf("d%03d", i/1000), fmt.Sprintf("f%06d.bin", i)), string(buf[:size]))
	}
	offer, paths, err := BuildOffer([]string{folder}, testChunk)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name    string
		workers int
		noSync  bool
		sink    Sink
	}{
		{"as it is, 4 workers", 4, false, SinkDisk},
		{"as it is, 8 workers", 8, false, SinkDisk},
		{"as it is, automatic", 0, false, SinkDisk},
		{"no fsync, 4 workers", 4, true, SinkDisk},
		{"null sink, 4 workers", 4, false, SinkNull},
	} {
		p := newOfferPair(t)
		p.serve(t, offer, paths)
		o := offer
		o.TransferID = p.id.String()
		r := p.receiver(t.TempDir())
		r.Workers, r.NoSync, r.Sink = c.workers, c.noSync, c.sink
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		start := time.Now()
		res, err := r.Run(ctx, p.addr, o)
		d := time.Since(start)
		cancel()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		for _, x := range res {
			if x.State != ItemDone {
				t.Fatalf("%s: item %d %v: %v", c.name, x.Item, x.State, x.Err)
			}
		}
		t.Logf("%-22s %6d files in %8.2f s: %6.2f ms per file, %7.0f files/s, %6.1f MB/s",
			c.name, n, d.Seconds(), d.Seconds()*1000/float64(n), float64(n)/d.Seconds(), float64(offer.TotalBytes)/(1<<20)/d.Seconds())
	}
}
