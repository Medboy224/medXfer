package transfer

import (
	"context"
	"testing"
	"time"
)

// waitFinished waits for the sender to learn the end of the transfer (DONE).
func waitFinished(s *Sender) Progress {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if p := s.Progress(); p.Finished {
			return p
		}
	}
	return s.Progress()
}

// D8: the receiver tells the sender that every item ended, and whether all were received.
func TestSenderLearnsTheEnd(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		offer, paths, _ := tree(t, []int{2*testChunk + 1, 10})
		p := newOfferPair(t)
		s := p.serve(t, offer, paths)
		offer.TransferID = p.id.String()
		runOffer(t, p.receiver(t.TempDir()), p.addr, offer)
		if got := waitFinished(s); !got.Finished || got.Partial {
			t.Fatalf("sender progress %+v; want finished, complete", got)
		}
	})
	t.Run("a skipped item", func(t *testing.T) {
		offer, paths, _ := tree(t, []int{testChunk, 10})
		p := newOfferPair(t)
		s := p.serve(t, offer, paths)
		offer.TransferID = p.id.String()
		r := p.receiver(t.TempDir())
		r.SkipItem(1)
		runOffer(t, r, p.addr, offer)
		if got := waitFinished(s); !got.Finished || !got.Partial {
			t.Fatalf("sender progress %+v; want finished, partial", got)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		offer, paths, _ := tree(t, []int{24 * testChunk})
		p := newOfferPair(t)
		s := p.serve(t, offer, paths)
		offer.TransferID = p.id.String()
		r := p.receiver(t.TempDir())
		v := newValve(2 * testChunk) // never opened: the transfer cannot end before the cancel
		r.dial = v.dial
		ctx, cancel := context.WithCancel(context.Background())
		go func() { <-v.reached; cancel() }()
		_, _ = r.Run(ctx, p.addr, offer)
		time.Sleep(300 * time.Millisecond)
		if got := s.Progress(); got.Finished {
			t.Fatalf("a cancelled receiver sent DONE: %+v", got)
		}
	})
}

// #33: after a resume the sender serves only what was missing; it still learns the end.
func TestSenderLearnsTheEndAfterAResume(t *testing.T) {
	offer, paths, _ := tree(t, []int{16 * testChunk})
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	out := t.TempDir()
	offer.TransferID = p.id.String()
	first := p.receiver(out)
	first.Workers = 1
	v := newValve(4 * testChunk)
	first.dial = v.dial
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-v.reached; cancel() }()
	_, _ = first.Run(ctx, p.addr, offer)

	q := p.again(t)
	again := q.serve(t, offer, paths)
	offer.TransferID = q.id.String()
	runOffer(t, q.receiver(out), q.addr, offer)
	got := waitFinished(again)
	if !got.Finished || got.Partial || got.Bytes >= got.Total {
		t.Fatalf("second sender %+v; want finished and complete, having served less than the total", got)
	}
}
