package transfer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/protocol"
)

// ErrNotRegularFile: an open file given to BuildOfferFiles is not a regular file (a folder, a
// pipe), which an offer cannot describe.
var ErrNotRegularFile = errors.New("transfer: not a regular file")

// BuildOfferFiles describes files already open as one offer: those an Android system picker
// gave as descriptors (STO-16, WP-B.0), read where they are, never copied. names are what the
// user sees; only their last element is kept, and two of one name (letter case aside) are
// numbered as the receiver numbers a collision. Sizes and times come from the files.
func BuildOfferFiles(files []*os.File, names []string, chunkSize uint32) (protocol.Offer, error) {
	if len(files) == 0 || len(files) != len(names) {
		return protocol.Offer{}, fmt.Errorf("transfer: %d files for %d names", len(files), len(names))
	}
	var offer protocol.Offer
	offer.ChunkSize = chunkSize
	used := map[string]bool{}
	for i, f := range files {
		if f == nil {
			return protocol.Offer{}, fmt.Errorf("%w: item %d", ErrNotRegularFile, i)
		}
		fi, err := f.Stat()
		if err != nil {
			return protocol.Offer{}, fmt.Errorf("transfer: item %d: %w", i, err)
		}
		if !fi.Mode().IsRegular() {
			return protocol.Offer{}, fmt.Errorf("%w: %s", ErrNotRegularFile, names[i])
		}
		name := filepath.Base(strings.ReplaceAll(names[i], `\`, "/"))
		if name == "" || name == "." || name == ".." || name == "/" {
			name = fmt.Sprintf("file-%d", i+1)
		}
		_, base, ext := engine.SplitNameAndExt(name)
		for n := 0; ; n++ {
			candidate := engine.FormatCandidateName("", base, n, ext)
			if key := strings.ToLower(candidate); !used[key] {
				used[key] = true
				name = candidate
				break
			}
		}
		offer.Items = append(offer.Items, protocol.OfferItem{Item: i, Path: name, Size: fi.Size(), MTime: fi.ModTime().UnixNano()})
		offer.TotalBytes += fi.Size()
	}
	if len(offer.Items) == 1 {
		offer.Root = offer.Items[0].Path
	} else {
		offer.Root = time.Now().Format("medXfer 2006-01-02 15h04") // as BuildOffer (D5 of the J4b plan)
	}
	return offer, offer.Validate()
}

// NewSenderFiles serves offer from files already open (BuildOfferFiles): files[i] is item i.
// They are read where they are and stay open until the end of Serve, which closes them: a
// descriptor cannot be opened again, and the receiver may ask for a chunk again (a digest that
// did not match, a resume).
func NewSenderFiles(offer protocol.Offer, files []*os.File) (*Sender, error) {
	paths := make([]string, len(files))
	for i, f := range files {
		if f == nil {
			return nil, fmt.Errorf("%w: item %d", ErrNotRegularFile, i)
		}
		paths[i] = f.Name()
	}
	s, err := NewSender(offer, paths)
	if err != nil {
		return nil, err
	}
	s.fixed = make(map[int]*os.File, len(files))
	for i, f := range files {
		s.fixed[i] = f
	}
	return s, nil
}
