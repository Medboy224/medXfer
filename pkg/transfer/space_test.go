package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Medboy224/medXfer/pkg/store"
)

// STO-07: an offer needs what is still to receive, plus max(1 %, 64 MiB). What a resume finds
// in its partial file is not counted again, nor a folder the policy skips.
func TestCheckSpaceCountsWhatIsLeft(t *testing.T) {
	offer, paths, _ := singleFile(t, 8*testChunk)
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	out := t.TempDir()
	offer.TransferID = p.id.String()

	s, err := p.receiver(out).CheckSpace(offer)
	if err != nil {
		t.Fatal(err)
	}
	if want := offer.TotalBytes + spaceMargin; s.Need != want || s.Free <= 0 {
		t.Fatalf("fresh offer: %+v; want need %d and the free space", s, want)
	}

	first := p.receiver(out)
	first.Workers = 1
	v := newValve(3 * testChunk)
	first.dial = v.dial
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-v.reached; cancel() }()
	_, _ = first.Run(ctx, p.addr, offer)
	fi, err := os.Stat(filepath.Join(out, "f.bin"+store.PartSuffix))
	if err != nil {
		t.Fatal(err)
	}
	s, _ = p.receiver(out).CheckSpace(offer)
	if want := offer.TotalBytes - fi.Size() + spaceMargin; fi.Size() == 0 || s.Need != want {
		t.Fatalf("resume of %d bytes: need %d; want %d", fi.Size(), s.Need, want)
	}
}

func TestCheckSpaceSkippedFolderNeedsNothing(t *testing.T) {
	offer, _, _ := tree(t, []int{10, 20})
	out := t.TempDir()
	writeUserFile(t, filepath.Join(out, "folder", "x.txt"), "the user's")
	r := NewReceiver(out, nil)
	r.Policy = store.PolicySkip
	if s, err := r.CheckSpace(offer); err != nil || s.Need != 0 || !s.Enough() {
		t.Fatalf("skipped folder: %+v, %v; want nothing needed", s, err)
	}
}

func TestSpaceErr(t *testing.T) {
	if err := (Space{Free: 10 << 20, Need: 80 << 20}).Err(); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Err() = %v; want ErrNoSpace", err)
	}
	for _, s := range []Space{{Free: 80 << 20, Need: 80 << 20}, {Free: -1, Need: 80 << 20}} {
		if err := s.Err(); err != nil {
			t.Fatalf("%+v: Err() = %v; want nil", s, err)
		}
	}
}
