package transfer

import (
	"context"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/store"
)

func lastProgress(t *testing.T, r *recorder) Progress {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.progress) == 0 {
		t.Fatal("no progress reported")
	}
	return r.progress[len(r.progress)-1]
}

// Moved is what crossed the network in this session: a file already there counts in Bytes at
// once, but not in Moved. The average speed of a resumed transfer counted the bytes found on
// disk as if they had come in this session (#304).
func TestMovedLeavesOutWhatWasAlreadyThere(t *testing.T) {
	offer, paths, _ := singleFile(t, 8*testChunk)
	out := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	p := newOfferPair(t)
	p.serve(t, offer, paths)
	first := &recorder{}
	r := p.receiver(out)
	r.Events = first
	offer.TransferID = p.id.String()
	if _, err := r.Run(ctx, p.addr, offer); err != nil {
		t.Fatal(err)
	}
	if got := lastProgress(t, first); got.Moved < offer.TotalBytes {
		t.Fatalf("first session: moved %d of %d bytes", got.Moved, offer.TotalBytes)
	}

	q := p.again(t)
	q.serve(t, offer, paths)
	second := &recorder{}
	r = q.receiver(out)
	r.Policy = store.PolicySkip // the file received above is there: nothing comes again
	r.Events = second
	offer.TransferID = q.id.String()
	if _, err := r.Run(ctx, q.addr, offer); err != nil {
		t.Fatal(err)
	}
	if got := lastProgress(t, second); got.Bytes != offer.TotalBytes || got.Moved != 0 {
		t.Fatalf("second session: bytes %d, moved %d; want %d and 0", got.Bytes, got.Moved, offer.TotalBytes)
	}
}
