package session

import (
	"fmt"

	"github.com/Medboy224/medXfer/pkg/protocol"
)

// TypeOfferItems carries a page of a paged offer (decision D2 of the J4a plan).
const TypeOfferItems = "offer_items"

// offerPageBytes leaves room in a CONTROL frame for the message's other fields.
const offerPageBytes = protocol.MaxControl - 64<<10

// OfferMessages puts offer in msg (its head, if the offer needs pages) and returns the
// offer_items messages to send right after it.
func OfferMessages(msg Message, offer protocol.Offer) (Message, []Message) {
	head, pages := offer.Split(offerPageBytes)
	msg.Offer = &head
	var follow []Message
	for _, p := range pages {
		follow = append(follow, Message{Type: TypeOfferItems, TransferID: offer.TransferID, Items: p})
	}
	return msg, follow
}

// ReadOffer completes the offer of first, reading its offer_items pages from ch, then
// validates it (LIM-05).
func ReadOffer(ch *Channel, first Message) (protocol.Offer, error) {
	if first.Offer == nil {
		return protocol.Offer{}, fmt.Errorf("%w: no offer in %q", protocol.ErrBadOffer, first.Type)
	}
	offer := *first.Offer
	if err := offer.CheckHead(); err != nil {
		return protocol.Offer{}, err
	}
	for !offer.Complete() {
		m, err := ch.Read()
		if err != nil {
			return protocol.Offer{}, err
		}
		if m.Type != TypeOfferItems || m.TransferID != offer.TransferID {
			return protocol.Offer{}, fmt.Errorf("%w: %q while the offer's items were expected", protocol.ErrBadOffer, m.Type)
		}
		if err := offer.AddPage(m.Items); err != nil {
			return protocol.Offer{}, err
		}
	}
	offer.Count = 0
	return offer, offer.Validate()
}
