package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Medboy224/medXfer/pkg/engine"
)

// Policy says what to do when a name is already taken by a file of the user.
type Policy int

const (
	PolicyAutoRename Policy = iota // receive as "name (1).ext", "name (2).ext"...
	PolicyOverwrite                // replace it, once the new file is complete and verified
	PolicySkip                     // keep it and receive nothing
)

// Place is where a file goes.
type Place struct {
	Name      string // slash-separated, relative to the destination
	Resume    bool   // a partial file of this transfer is there: Open resumes it
	Done      bool   // PolicySkip: a file of that name is there, nothing to receive
	Overwrite bool   // PolicyOverwrite: Finalize replaces the file of that name
}

// maxCandidates bounds the "name (N)" tried before giving up.
const maxCandidates = 1000

// Resolve finds where the file name goes, without writing anything (#75): its own partial
// file to resume, a free name, or a file already there that the policy skips or overwrites.
// A partial file of another transfer is never touched (STO-09): the next name is tried.
func Resolve(root *os.Root, name string, p Params, policy Policy) (Place, error) {
	dir, base, ext := engine.SplitNameAndExt(name)
	if err := checkFolders(root, dir); err != nil {
		return Place{}, err
	}
	for count := 0; count < maxCandidates; count++ {
		c := engine.FormatCandidateName(dir, base, count, ext)
		state, err := peek(root, c, p)
		if err != nil {
			return Place{}, err
		}
		if state == partialForeign && p.Owned {
			// In this transfer's own folder, a partial file is this transfer's: Open resumes
			// it, or starts it over if a stop left it without a readable journal.
			state = partialOurs
		}
		switch state {
		case partialOurs:
			return Place{Name: c, Resume: true, Overwrite: policy == PolicyOverwrite}, nil
		case partialForeign:
			continue
		}
		fi, err := root.Lstat(filepath.FromSlash(c))
		switch {
		case errors.Is(err, os.ErrNotExist):
			return Place{Name: c}, nil
		case err != nil: // a file where a folder is needed, or a link out of the destination
			return Place{}, fmt.Errorf("%w: %s: %v", ErrPathConflict, c, err)
		case fi.IsDir():
			continue // a folder of that name: never touched, whatever the policy
		case policy == PolicySkip:
			return Place{Name: c, Done: true}, nil
		case policy == PolicyOverwrite:
			return Place{Name: c, Overwrite: true}, nil
		}
	}
	return Place{}, fmt.Errorf("store: no free name for %s after %d tries", name, maxCandidates)
}

// checkFolders makes sure each folder of dir is a folder, or does not exist yet: a file where a
// folder is needed is a conflict (STO-11), found before anything is written. Windows reports
// a path through a file as "not found", so the folders are checked one by one.
func checkFolders(root *os.Root, dir string) error {
	if dir == "" || dir == "." {
		return nil
	}
	prefix := ""
	for _, part := range strings.Split(dir, "/") {
		prefix = path.Join(prefix, part)
		fi, err := root.Stat(filepath.FromSlash(prefix)) // follows links inside the destination only
		switch {
		case errors.Is(err, os.ErrNotExist):
			return nil // created later, by Open
		case err != nil:
			return fmt.Errorf("%w: %s: %v", ErrPathConflict, prefix, err)
		case !fi.IsDir():
			return fmt.Errorf("%w: %s is a file", ErrPathConflict, prefix)
		}
	}
	return nil
}

type partialState int

const (
	partialNone partialState = iota
	partialOurs
	partialForeign
)

// peek says whether name has a partial file, and whose, without changing anything.
func peek(root *os.Root, name string, p Params) (partialState, error) {
	j, err := root.Open(filepath.FromSlash(name + JournalSuffix))
	if err == nil {
		defer j.Close()
		h := make([]byte, headerLen)
		if _, err := io.ReadFull(j, h); err != nil {
			return partialForeign, nil
		}
		f := &File{p: p, n: p.chunks()}
		if string(h) != string(f.header()) { // key, size, chunk size, CRC
			return partialForeign, nil
		}
		return partialOurs, nil
	}
	if _, err := root.Lstat(filepath.FromSlash(name + PartSuffix)); err == nil {
		return partialForeign, nil // data without a journal: not ours
	}
	return partialNone, nil
}
