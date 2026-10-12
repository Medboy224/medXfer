package main

import (
	"fmt"
	"testing"

	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/session"
)

func nodeTestOffer(n int) protocol.Offer {
	o := protocol.Offer{TransferID: "t", Root: "dir", ChunkSize: protocol.DefaultChunkSize}
	for i := 0; i < n; i++ {
		o.Items = append(o.Items, protocol.OfferItem{Item: i, Path: fmt.Sprintf("dir/f%05d.bin", i), Size: 1})
		o.TotalBytes++
	}
	return o
}

// xfer node receives a paged offer (D2) one message at a time: it asks the user only once
// every item is there, and drops an offer whose pages do not add up.
func TestNodeCompletesAPagedOffer(t *testing.T) {
	offer := nodeTestOffer(3)
	head, pages := offer.Split(50) // one item per page
	if len(pages) < 2 {
		t.Fatalf("expected several pages, got %d", len(pages))
	}
	incoming := &session.Message{Type: "offer", Offer: &head}
	if m, err := completeNodeOffer(&incoming, nil); m != nil || err != nil || incoming == nil {
		t.Fatalf("the head alone was taken as the whole offer: %v %v", m, err)
	}
	var got *session.Message
	for i, p := range pages {
		m, err := completeNodeOffer(&incoming, p)
		if err != nil {
			t.Fatal(err)
		}
		if (m != nil) != (i == len(pages)-1) {
			t.Fatalf("page %d: complete = %v", i, m != nil)
		}
		got = m
	}
	if incoming != nil || len(got.Offer.Items) != 3 || got.Offer.Count != 0 {
		t.Fatalf("completed offer: %+v (incoming %v)", got.Offer, incoming)
	}

	// A page that repeats an item is refused and the offer dropped.
	head, _ = offer.Split(50)
	incoming = &session.Message{Type: "offer", Offer: &head}
	if _, err := completeNodeOffer(&incoming, head.Items); err == nil || incoming != nil {
		t.Fatal("a repeated page was accepted")
	}

	// A complete offer that breaks the limits is refused too (LIM-05).
	bad := nodeTestOffer(1)
	bad.TotalBytes = 99
	incoming = &session.Message{Type: "offer", Offer: &bad}
	if m, err := completeNodeOffer(&incoming, nil); err == nil || m != nil {
		t.Fatal("an invalid offer was accepted")
	}
}
