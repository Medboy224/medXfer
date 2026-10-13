//go:build diag

package transfer

// A full disk pauses the transfer (STO-07, bible §9.7): nothing fails, the partial file stays,
// and the transfer goes on once the user resumes with room again.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag/faultfs"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/store"
)

// fullDisk fills the disk of the files opened while full is set: a .part takes room bytes,
// the journal nothing at all if journal is set.
type fullDisk struct {
	full    atomic.Bool
	room    int64
	journal bool
}

func (d *fullDisk) install(t *testing.T) {
	d.full.Store(true)
	t.Cleanup(engine.SetStorageWrapper(func(f engine.StorageFile, path string) engine.StorageFile {
		switch {
		case !d.full.Load():
			return f
		case strings.HasSuffix(path, store.JournalSuffix):
			if d.journal {
				return faultfs.Wrap(f, faultfs.Plan{NoSpaceAfter: 1})
			}
			return f
		}
		return faultfs.Wrap(f, faultfs.Plan{NoSpaceAfter: d.room})
	}))
}

// diskFullRecorder is a receiver's events, with its full disk.
type diskFullRecorder struct {
	recorder
	full chan error
}

func (r *diskFullRecorder) OnDiskFull(err error) {
	select {
	case r.full <- err:
	default:
	}
}

// pausedOnFullDisk receives the 8-chunk file f.bin onto a full disk and waits until the
// receiver pauses. It returns the receiver, its events, and the end of its Run.
func pausedOnFullDisk(t *testing.T, d *fullDisk, out string) (*Receiver, *diskFullRecorder, chan []ItemResult, []byte) {
	t.Helper()
	d.install(t)
	offer, paths, data := singleFile(t, 8*testChunk+5)
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	offer.TransferID = p.id.String()
	r := p.receiver(out)
	ev := &diskFullRecorder{full: make(chan error, 1)}
	r.Events = ev
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	done := make(chan []ItemResult, 1)
	go func() {
		res, err := r.Run(ctx, p.addr, offer)
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	select {
	case err := <-ev.full:
		if !engine.IsDiskFull(err) {
			t.Fatalf("OnDiskFull(%v): not a full disk", err)
		}
	case res := <-done:
		t.Fatalf("the transfer ended on a full disk: %+v", res)
	case <-time.After(30 * time.Second):
		t.Fatal("no full disk reported")
	}
	ev.mu.Lock()
	defer ev.mu.Unlock()
	for _, e := range ev.items {
		if e.State == ItemFailed {
			t.Fatalf("an item failed on a full disk: %+v", e)
		}
	}
	return r, ev, done, data
}

// resumeWithRoom gives the disk room again, resumes, and checks that f.bin arrives whole
// under its own name.
func resumeWithRoom(t *testing.T, r *Receiver, d *fullDisk, done chan []ItemResult, out string, data []byte) {
	t.Helper()
	d.full.Store(false)
	r.Resume()
	var res []ItemResult
	select {
	case res = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the transfer did not go on after the resume")
	}
	want := filepath.Join(out, "f.bin")
	if res[0].State != ItemDone || res[0].SavedPath != want {
		t.Fatalf("after the resume: %+v; want done in %s", res[0], want)
	}
	if got, _ := os.ReadFile(want); !bytes.Equal(got, data) {
		t.Fatal("the file received across a full disk differs")
	}
	for _, suffix := range []string{store.PartSuffix, store.JournalSuffix} {
		if _, err := os.Stat(want + suffix); err == nil {
			t.Fatalf("%s left after the transfer", suffix)
		}
	}
}

// STO-07: the disk fills up after 3 chunks; the transfer pauses with its partial file and
// journal, then ends once there is room and the user resumes.
func TestDiskFullPausesThenResumes(t *testing.T) {
	d := &fullDisk{room: 3 * testChunk}
	out := t.TempDir()
	r, _, done, data := pausedOnFullDisk(t, d, out)
	for _, suffix := range []string{store.PartSuffix, store.JournalSuffix} {
		if _, err := os.Stat(filepath.Join(out, "f.bin"+suffix)); err != nil {
			t.Fatalf("the partial file was not kept: %v", err)
		}
	}
	resumeWithRoom(t, r, d, done, out, data)
}

// A disk too full for even the journal: the .part that could not get one goes (it would read
// as another transfer's), and the resume receives the file under its own name, not "f (1)".
func TestDiskFullBeforeTheJournal(t *testing.T) {
	d := &fullDisk{room: testChunk / 2, journal: true}
	out := t.TempDir()
	r, _, done, data := pausedOnFullDisk(t, d, out)
	for _, suffix := range []string{store.PartSuffix, store.JournalSuffix} {
		if _, err := os.Stat(filepath.Join(out, "f.bin"+suffix)); err == nil {
			t.Fatalf("%s without a journal was left on the full disk", suffix)
		}
	}
	resumeWithRoom(t, r, d, done, out, data)
}
