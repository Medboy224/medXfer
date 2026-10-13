package transfer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/store"
)

// spaceMargin is the room kept free on top of what an offer needs: max(1 %, 64 MiB) (bible
// §9.7).
const spaceMargin = 64 << 20

// ErrNoSpace: the destination has not the room an offer needs; it is refused before its
// first byte (STO-07, decision D9 of the J4b plan).
var ErrNoSpace = errors.New("not enough free space")

// Space is what an offer needs on the destination's disk, and what it has.
type Space struct {
	Free int64 // bytes free for this user; -1 when the system does not say
	Need int64 // bytes still to receive, plus the margin; 0 when nothing is to receive
}

// Enough reports whether the offer fits. When the system does not say, it is taken as
// fitting: a disk that fills up pauses the transfer.
func (s Space) Enough() bool { return s.Free < 0 || s.Free >= s.Need }

// Err is ErrNoSpace with the figures, or nil when the offer fits.
func (s Space) Err() error {
	if s.Enough() {
		return nil
	}
	return fmt.Errorf("%w: %s free, %s needed", ErrNoSpace, mib(s.Free), mib(s.Need))
}

func mib(n int64) string { return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20)) }

// CheckSpace says whether offer fits in r.OutDir, before it is accepted (STO-07): free space
// ≥ what is still to receive + max(1 %, 64 MiB). What a resume finds on disk, or a file the
// policy skips, is not counted. It writes nothing.
func (r *Receiver) CheckSpace(offer protocol.Offer) (Space, error) {
	if err := offer.Validate(); err != nil {
		return Space{}, err
	}
	if r.Sink == SinkNull {
		return Space{Free: -1}, nil
	}
	left := r.left(offer)
	s := Space{Free: -1}
	if left > 0 {
		s.Need = left + max(offer.TotalBytes/100, spaceMargin)
	}
	free, err := engine.FreeSpace(r.OutDir)
	if err == nil {
		s.Free = free
	}
	return s, nil
}

// left is how many bytes of offer are still to receive in r.OutDir: the whole offer, less
// what a resume finds in its partial files and what the policy skips. Any doubt counts the
// item whole.
func (r *Receiver) left(offer protocol.Offer) int64 {
	if fi, err := os.Stat(r.OutDir); err != nil || !fi.IsDir() {
		return offer.TotalBytes // nothing there yet
	}
	root, err := os.OpenRoot(r.OutDir)
	if err != nil {
		return offer.TotalBytes
	}
	defer root.Close()
	ru := newRun(r, offer)
	ru.root = root
	place, err := ru.findRoot()
	switch {
	case err != nil:
		return offer.TotalBytes
	case place.Skip:
		return 0 // the whole offer is refused (D5)
	case ru.inRoot && !place.Resume:
		return offer.TotalBytes // a new root folder: nothing in it yet
	}
	var left int64
	for _, st := range ru.items {
		left += ru.leftOf(st)
	}
	return left
}

// leftOf is how many bytes of item st are still to receive.
func (ru *run) leftOf(st *item) int64 {
	safe, err := engine.SafeRelPath(st.it.Path)
	if err != nil {
		return st.it.Size
	}
	pl, err := store.Resolve(ru.root, ru.itemName(safe), ru.params(st, false), ru.policy())
	switch {
	case err != nil:
		return st.it.Size
	case pl.Done:
		return 0
	case pl.Resume:
		if fi, err := ru.root.Stat(filepath.FromSlash(pl.Name + store.PartSuffix)); err == nil {
			return st.it.Size - min(fi.Size(), st.it.Size)
		}
	}
	return st.it.Size
}
