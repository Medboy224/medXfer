package api

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

// Real test of 1 November: the receiver never reached the sender (its firewall let medXfer in
// on its cable only), and the transfer failed after 43 s, said by its raw error. transfer_failed
// now carries the code the application turns into words, on this side (and to the peer, in the
// session's message).
func TestTransferFailedSaysTheSenderWasUnreachable(t *testing.T) {
	s, n := startOwnIdentity(t, "Receiver")
	offer := protocol.Offer{TransferID: "t1", Items: []protocol.OfferItem{{Path: "a.zip", Size: 10}}}
	s.transferFailed(offer, fmt.Errorf("%w: dial tcp4 192.0.2.7:18887: i/o timeout", transfer.ErrSenderUnreachable))
	var failed struct{ Code, By string }
	if err := n.WaitEvent("transfer_failed", 0).Decode(&failed); err != nil {
		t.Fatal(err)
	}
	if failed.Code != "sender_unreachable" || failed.By != "self" {
		t.Fatalf("transfer_failed code %q by %q; want sender_unreachable by self", failed.Code, failed.By)
	}

	s.transferFailed(offer, errors.New("disk full"))
	var other struct{ Code string }
	if err := n.WaitEvent("transfer_failed", 0).Decode(&other); err != nil {
		t.Fatal(err)
	}
	if other.Code != "" {
		t.Fatalf("a failure of another kind has the code %q", other.Code)
	}
}
