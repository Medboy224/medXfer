package store

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Medboy224/medXfer/pkg/engine"
)

// A transfer of several files is received in a root folder of its own (STO-10): its name is
// resolved once, and nothing inside is merged with what the user already has. A marker file
// in that root holds the transfer's key, so that a resume finds the folder again (decision D11
// of the J4b plan) — "Photos (1)", not a new "Photos (2)".
const MarkerName = ".medxfer-transfer"

var markerMagic = [8]byte{'M', 'X', 'R', 'O', 'O', 'T', 0, 1}

// RootKey identifies a transfer's root folder: the same sender, root name, number of items
// and size mean the same transfer, to resume.
func RootKey(sender [32]byte, root string, items int, total int64) Key {
	h := sha256.New()
	h.Write([]byte("medxfer-v1-root"))
	h.Write(sender[:])
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(root)))
	h.Write(n[:])
	h.Write([]byte(root))
	binary.BigEndian.PutUint64(n[:], uint64(items))
	h.Write(n[:])
	binary.BigEndian.PutUint64(n[:], uint64(total))
	h.Write(n[:])
	var k Key
	copy(k[:], h.Sum(nil))
	return k
}

// RootPlace is where a transfer's root folder goes.
type RootPlace struct {
	Name   string // the folder, relative to the destination
	Resume bool   // the folder of this transfer, from an earlier session
	Skip   bool   // PolicySkip: a folder of that name is there; the whole offer is refused
}

// ResolveRoot finds the root folder of a transfer, without writing anything: base, or
// "base (1)", "base (2)"... A folder is reused only if its marker holds key (a resume).
// "Overwrite" behaves as "rename" for a folder: a folder of the user is never emptied nor
// merged (decision D5); "skip" refuses the whole offer if base is taken.
func ResolveRoot(root *os.Root, base string, key Key, policy Policy) (RootPlace, error) {
	if base == "" {
		return RootPlace{}, fmt.Errorf("store: empty root name")
	}
	for count := 0; count < maxCandidates; count++ {
		c := engine.FormatCandidateName("", base, count, "")
		fi, err := root.Lstat(filepath.FromSlash(c))
		switch {
		case errors.Is(err, os.ErrNotExist):
			return RootPlace{Name: c}, nil
		case err != nil:
			return RootPlace{}, fmt.Errorf("%w: %s: %v", ErrPathConflict, c, err)
		}
		if fi.IsDir() && hasMarker(root, c, key) {
			return RootPlace{Name: c, Resume: true}, nil
		}
		if count == 0 && policy == PolicySkip {
			return RootPlace{Name: c, Skip: true}, nil
		}
	}
	return RootPlace{}, fmt.Errorf("store: no free folder name for %s after %d tries", base, maxCandidates)
}

func hasMarker(root *os.Root, dir string, key Key) bool {
	f, err := root.Open(filepath.FromSlash(dir + "/" + MarkerName))
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, len(markerMagic)+len(key))
	if _, err := io.ReadFull(f, b); err != nil {
		return false
	}
	return string(b[:8]) == string(markerMagic[:]) && string(b[8:]) == string(key[:])
}

// MarkRoot creates the folder dir and its marker, if not there yet: called before the first
// file of the transfer is written (#75: nothing before a verified chunk).
func MarkRoot(root *os.Root, dir string, key Key) error {
	if hasMarker(root, dir, key) {
		return nil
	}
	if err := root.MkdirAll(filepath.FromSlash(dir), 0o755); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrPathConflict, dir, err)
	}
	f, err := root.OpenFile(filepath.FromSlash(dir+"/"+MarkerName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(markerMagic[:], key[:]...))
	return err
}

// UnmarkRoot removes the marker once every file of the transfer was received: the folder is
// then the user's, like any other. A marker of another transfer is never removed (STO-09).
func UnmarkRoot(root *os.Root, dir string, key Key) error {
	if !hasMarker(root, dir, key) {
		return nil
	}
	return root.Remove(filepath.FromSlash(dir + "/" + MarkerName))
}
