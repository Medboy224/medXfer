package transfer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// serveBench serves a bench sender on the pair's transfer.
func (p *offerPair) serveBench(t *testing.T, s *Sender) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Serve(ctx, p.tl) }()
	t.Cleanup(func() { cancel(); <-done })
}

// DEV-08: a bench offer goes through the same connections, frames, hashes and digest as a
// transfer; with the null sink nothing reaches the disk.
func TestBenchOfferToNullSink(t *testing.T) {
	src, err := RandomSource()
	if err != nil {
		t.Fatal(err)
	}
	p := newOfferPair(t)
	offer := BenchOffer(10*testChunk+3, testChunk)
	offer.TransferID = p.id.String()
	s, err := NewSourceSender(offer, src)
	if err != nil {
		t.Fatal(err)
	}
	p.serveBench(t, s)
	out := t.TempDir()
	r := p.receiver(out)
	r.Sink = SinkNull
	res := runOffer(t, r, p.addr, offer)
	if res[0].State != ItemDone {
		t.Fatalf("result %+v", res[0])
	}
	if got := s.Progress().Bytes; got != offer.TotalBytes {
		t.Fatalf("sender served %d bytes, want %d", got, offer.TotalBytes)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("the null sink wrote %v", entries)
	}
}

// Bench ladder R4: the disk sink without fsync writes the file whole.
func TestBenchOfferToDiskWithoutSync(t *testing.T) {
	p := newOfferPair(t)
	offer := BenchOffer(3*testChunk+7, testChunk)
	offer.TransferID = p.id.String()
	s, err := NewSourceSender(offer, ZeroSource())
	if err != nil {
		t.Fatal(err)
	}
	p.serveBench(t, s)
	out := t.TempDir()
	r := p.receiver(out)
	r.NoSync = true
	if res := runOffer(t, r, p.addr, offer); res[0].State != ItemDone {
		t.Fatalf("result %+v", res[0])
	}
	got, err := os.ReadFile(filepath.Join(out, "bench.bin"))
	if err != nil || !bytes.Equal(got, make([]byte, offer.TotalBytes)) {
		t.Fatalf("received %d bytes (%v)", len(got), err)
	}
}

// The random source repeats its block seamlessly: a read across the block's end matches two
// reads, and two chunks of the same transfer differ.
func TestRandomSource(t *testing.T) {
	src, err := RandomSource()
	if err != nil {
		t.Fatal(err)
	}
	const block = 4 << 20
	across := make([]byte, 100)
	_, _ = src.ReadAt(across, block-50)
	end, start := make([]byte, 50), make([]byte, 50)
	_, _ = src.ReadAt(end, block-50)
	_, _ = src.ReadAt(start, 0)
	if !bytes.Equal(across, append(end, start...)) {
		t.Fatal("a read across the end of the block does not wrap around")
	}
	a, b := make([]byte, testChunk), make([]byte, testChunk)
	_, _ = src.ReadAt(a, 0)
	_, _ = src.ReadAt(b, testChunk)
	if bytes.Equal(a, b) {
		t.Fatal("two chunks of the random source are equal")
	}
}

func TestSourceSenderNeedsOneItem(t *testing.T) {
	offer, _, _ := tree(t, []int{1, 2})
	if _, err := NewSourceSender(offer, ZeroSource()); err == nil {
		t.Fatal("a source sender accepted two items")
	}
}
