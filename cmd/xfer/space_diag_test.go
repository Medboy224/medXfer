//go:build diag

package main

import (
	"errors"
	"testing"

	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

// D9: the terminal refuses an offer that does not fit, before asking to confirm it.
func TestCheckSpaceRefusesAnOfferTooBig(t *testing.T) {
	offer := protocol.Offer{TransferID: "t", Root: "f.bin", ChunkSize: protocol.MinChunkSize, TotalBytes: 10 << 20,
		Items: []protocol.OfferItem{{Item: 0, Path: "f.bin", Size: 10 << 20}}}
	var sender identity.Fingerprint
	for _, c := range []struct {
		free int64
		want error
	}{
		{70 << 20, transfer.ErrNoSpace}, // 10 MiB + 64 MiB of margin needed
		{80 << 20, nil},
	} {
		restore := engine.SetFreeSpace(func(string) (int64, error) { return c.free, nil })
		err := checkSpace(t.TempDir(), offer, sender)
		restore()
		if !errors.Is(err, c.want) && (err != nil || c.want != nil) {
			t.Fatalf("%d MiB free: %v; want %v", c.free>>20, err, c.want)
		}
	}
}
