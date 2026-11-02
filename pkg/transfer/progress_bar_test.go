package transfer

import (
	"context"
	"testing"
	"time"
)

// onBar: the bytes on their way move the bar; below the total until everything is counted;
// never back, unless the bytes counted go back (an item starting over).
func TestOnBar(t *testing.T) {
	var shown, seen int64
	steps := []struct {
		counted, onTheWay, total, want int64
	}{
		{0, 300, 1000, 300},   // the first chunk on its way
		{0, 600, 1000, 600},   //
		{500, 100, 1000, 600}, // a chunk counted, the next one just begun: no step back
		{500, 450, 1000, 950},
		{500, 900, 1000, 999}, // more on the way than is left: below 100 % until counted
		{500, 0, 1000, 999},   // a connection lost, its chunk to come again: no step back
		{200, 0, 1000, 200},   // an item starting over: the bar goes back with it
		{1000, 0, 1000, 1000}, // everything counted: 100 %
	}
	for i, s := range steps {
		if got := onBar(&shown, &seen, s.counted, s.onTheWay, s.total); got != s.want {
			t.Fatalf("step %d: onBar(%d, %d, %d) = %d, want %d", i, s.counted, s.onTheWay, s.total, got, s.want)
		}
	}
}

// WP-E.6 of the cable plan: on a slow link the bar moved a chunk at a time, four chunks at once
// every few seconds (real test of 1 November). A transfer held in the middle of its second
// chunk shows what of that chunk has arrived, not the first chunk alone; the bar never goes
// back, and ends on the whole.
func TestProgressCountsTheChunkOnItsWay(t *testing.T) {
	offer, paths, contents := tree(t, []int{4 * testChunk})
	p := newOfferPair(t)
	offer.TransferID = p.id.String()
	p.serve(t, offer, paths)
	out := t.TempDir()
	r := p.receiver(out)
	r.Workers = 1
	rec := &recorder{}
	r.Events = rec
	v := newValve(testChunk + testChunk/2)
	r.dial = v.dial
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err := r.Run(ctx, p.addr, offer)
		done <- err
	}()

	v.wait(t)
	var held int64
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end) && held <= testChunk; {
		time.Sleep(50 * time.Millisecond)
		rec.mu.Lock()
		if n := len(rec.progress); n > 0 {
			held = rec.progress[n-1].Bytes
		}
		rec.mu.Unlock()
	}
	if held <= testChunk || held >= 2*testChunk {
		t.Fatalf("held in the second chunk, the bar shows %d bytes: want more than the first chunk (%d), less than two", held, testChunk)
	}

	v.open()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	checkFiles(t, out, offer, contents, nil)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var last int64
	for _, pr := range rec.progress {
		if pr.Bytes < last {
			t.Fatalf("the bar went back: %d after %d", pr.Bytes, last)
		}
		last = pr.Bytes
	}
	if last != offer.TotalBytes {
		t.Fatalf("the bar ends at %d of %d", last, offer.TotalBytes)
	}
}
