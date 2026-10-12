package transfer

import (
	"time"

	"github.com/Medboy224/medXfer/pkg/manifest"
	"github.com/Medboy224/medXfer/pkg/protocol"
)

// BuildOffer describes paths (a file, several files or a folder) as one offer (bible §8.3)
// in chunks of chunkSize, and returns the file behind each item. The offer has no
// TransferID yet: the sender sets it when it registers the transfer.
func BuildOffer(paths []string, chunkSize uint32) (protocol.Offer, []string, error) {
	m, err := manifest.Build(paths)
	if err != nil {
		return protocol.Offer{}, nil, err
	}
	offer := protocol.Offer{Root: m.RootName, ChunkSize: chunkSize, TotalBytes: m.TotalBytes}
	files := make([]string, len(m.Items))
	for i, it := range m.Items {
		offer.Items = append(offer.Items, protocol.OfferItem{Item: i, Path: it.RelPath, Size: it.Size, MTime: it.MTime})
		files[i] = it.FullPath
	}
	switch {
	case len(paths) > 1:
		// Several paths have no name in common: the receiver puts them in a folder named
		// after the moment of the send (decision D5 of the J4b plan).
		offer.Root = time.Now().Format("medXfer 2006-01-02 15h04")
	case offer.Root == "" && len(offer.Items) == 1:
		offer.Root = offer.Items[0].Path
	}
	return offer, files, offer.Validate()
}
