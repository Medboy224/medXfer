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
// reports no longer come. The sender showed the last one until the end, its bar still; it now
// shows what it served once the report is older than peerStale.
func TestSenderShowsWhatItServedOnceTheReportsStop(t *testing.T) {
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

	ev.takePeer(&session.Progress{Bytes: 300})
	if got := progress(400); got != 300 {
		t.Fatalf("with a fresh report the sender shows %d, want the receiver's 300", got)
	}
	ev.mu.Lock()
	ev.peerAt = time.Now().Add(-peerStale)
	ev.mu.Unlock()
	if got := progress(700); got != 700 {
		t.Fatalf("with no report for %v the sender shows %d, want the 700 it served", peerStale, got)
	}
	ev.takePeer(&session.Progress{Bytes: 650}) // the session is back
	if got := progress(800); got != 650 {
		t.Fatalf("with the reports back the sender shows %d, want the receiver's 650", got)
	}
}
