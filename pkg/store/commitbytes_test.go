package store

import (
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// syncFails is a file whose Sync fails while fail is set.
type syncFails struct {
	handle
	fail *atomic.Bool
}

func (f syncFails) Sync() error {
	if f.fail.Load() {
		return errors.New("disk error")
	}
	return f.handle.Sync()
}

// #313: a commit that fails puts its chunks back, and their bytes: the byte threshold still
// counts them, and the next chunk makes the commit due again, without waiting for the interval.
func TestFailedCommitKeepsItsBytes(t *testing.T) {
	var fail atomic.Bool
	saved := openFile
	openFile = func(root *os.Root, name string, flag int) (handle, error) {
		h, err := saved(root, name, flag)
		if err != nil {
			return nil, err
		}
		return syncFails{h, &fail}, nil
	}
	t.Cleanup(func() { openFile = saved })

	root, _ := openRoot(t)
	data, sums := content(t, 3*testChunk)
	p := params(len(data))
	p.CommitBytes, p.CommitInterval = 2*testChunk, time.Hour // only the bytes make it due
	f, err := Open(root, "f.bin", p)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeChunk(f, 0, chunkOf(data, 0), sums[0]); err != nil { // half the threshold
		t.Fatal(err)
	}
	fail.Store(true)
	if err := f.Commit(); err == nil {
		t.Fatal("the commit did not fail")
	}
	fail.Store(false)
	if err := writeChunk(f, 1, chunkOf(data, 1), sums[1]); err != nil { // the threshold, with chunk 0
		t.Fatal(err)
	}
	if err := f.Settle(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	left := len(f.pending)
	f.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d chunks still waiting: the failed commit's bytes no longer count", left)
	}
}
