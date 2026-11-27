package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

// #297: an item's event carries its index and status, never the whole queue. For thousands of
// files, the events grow with the number of files, not with its square.
func TestItemEventsDoNotCopyTheQueue(t *testing.T) {
	const files, perFile = 5000, 1024 // bytes of events a file may cost, at most
	s := NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Solo")
	offer := protocol.Offer{TransferID: "t", Root: "photos"}
	for i := 0; i < files; i++ {
		offer.Items = append(offer.Items, protocol.OfferItem{Item: i, Path: fmt.Sprintf("photos/IMG_%05d.jpg", i), Size: 1 << 20})
	}
	s.mu.Lock()
	s.currentBatchItems = queueOf(offer)
	s.mu.Unlock()

	var total atomic.Int64
	var last = map[string]json.RawMessage{}
	cancel := s.SubscribeJSON(func(b []byte) {
		total.Add(int64(len(b)))
		var e struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if json.Unmarshal(b, &e) == nil {
			last[e.Event] = e.Data
		}
	})
	defer cancel()

	e := newDaemonEvents(s, offer, false, nil)
	for i := 0; i < files && total.Load() <= files*perFile; i++ {
		it := offer.Items[i]
		switch {
		case i%100 == 1:
			s.itemAction(itemPause, i, false, "")
			s.itemAction(itemResume, i, false, "")
		case i%100 == 2:
			s.itemAction(itemSkip, i, false, "")
			continue
		}
		e.OnItem(transfer.ItemEvent{Item: i, Path: it.Path, Size: it.Size, State: transfer.ItemStarted})
		if i%100 == 3 {
			e.OnItem(transfer.ItemEvent{Item: i, Path: it.Path, Size: it.Size, State: transfer.ItemFailed, Err: errors.New("disk error")})
			continue
		}
		e.OnItem(transfer.ItemEvent{Item: i, Path: it.Path, Size: it.Size, State: transfer.ItemDone})
	}
	if n := total.Load(); n > files*perFile {
		t.Fatalf("over %d bytes of events for %d files: an item's event copies the queue", n, files)
	}

	for event, want := range map[string]string{
		"batch_item_transferring": "transferring",
		"item_completed":          "completed",
		"file_paused":             "paused",
		"file_resumed":            "pending",
		"file_skipped":            "skipped",
		"item_failed":             "failed",
	} {
		var d struct {
			Index  *int             `json:"item_index"`
			Status string           `json:"status"`
			Items  *json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(last[event], &d); err != nil {
			t.Fatalf("%s: %v", event, err)
		}
		if d.Index == nil || d.Status != want || d.Items != nil {
			t.Fatalf("%s: %s; want its item_index and status %q, no items", event, last[event], want)
		}
	}
}
