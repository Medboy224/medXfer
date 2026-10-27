package api

import (
	"sync"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

// WP-B.8, found on the real devices: the session lost while the data goes on, the receiver's
// reports no longer come. The sender showed the last one until the end, its bar still. Then,
// showing what it served alone, its bar went back and forth after a resume, whose serving starts
// at 0, whenever a report was late. It now adds what it served since the last report.
func TestSenderBarGoesOnWithoutTheReports(t *testing.T) {
	s := NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Solo")
	offer := protocol.Offer{TransferID: "t1", TotalBytes: 1000, Items: []protocol.OfferItem{{Item: 0, Path: "a.bin", Size: 1000}}}
	ev := newDaemonEvents(s, offer, true, nil)
	var mu sync.Mutex
	var shown int64
	defer s.Subscribe(func(e EventMessage) {
		if d, ok := e.Data.(TransferProgressData); ok {
			mu.Lock()
			shown = d.BatchBytes
			mu.Unlock()
		}
	})()
	progress := func(served int64) int64 {
		ev.mu.Lock()
		ev.lastUpdate = time.Time{} // not held back by the 10 updates a second
		ev.mu.Unlock()
		ev.OnProgress(transfer.Progress{Bytes: served, Total: 1000, Streams: 1, Items: 1})
		mu.Lock()
		defer mu.Unlock()
		return shown
	}
	late := func() {
		ev.mu.Lock()
		ev.peerAt = time.Now().Add(-peerStale)
		ev.mu.Unlock()
	}

	// A resume: the receiver has 600 already, this sender serves from 0.
	ev.takePeer(&session.Progress{Bytes: 600})
	if got := progress(100); got != 600 {
		t.Fatalf("with a fresh report the sender shows %d, want the receiver's 600", got)
	}
	late()
	if got := progress(250); got != 750 {
		t.Fatalf("with no report for %v the sender shows %d, want 600 and the 150 served since", peerStale, got)
	}
	if got := progress(300); got != 800 {
		t.Fatalf("still without a report the sender shows %d, want 600 and the 200 served since", got)
	}
	ev.takePeer(&session.Progress{Bytes: 790})
	if got := progress(320); got != 790 {
		t.Fatalf("with the reports back the sender shows %d, want the receiver's 790", got)
	}
}
