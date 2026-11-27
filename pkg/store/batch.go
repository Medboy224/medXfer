package store

import (
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"sync"
)

// Batch makes several complete files durable at once, then gives them their names (WP-4.11).
// A small file costs little to write and much to sync: on a phone, its fsync and its folder's
// take about 3 of its 3.4 ms (bench-results/README.md). A batch makes all its files durable
// with one syncfs, then renames them and syncs each folder once.
//
// The guarantee of Finalize stays: a file gets its name only once its data is durable
// (STO-01). A stop before the flush loses at most the batch: its files are .part files without
// journal in the transfer's own folder, which a resume starts over (Params.Owned).
type Batch struct {
	root  *os.Root
	fuse  bool
	files []*File
	bytes int64
}

// NewBatch returns a batch for the files of root, or nil where the system has no syncfs (it
// is Linux's and Android's): there, each file is finalized alone.
func NewBatch(root *os.Root) *Batch {
	if !canSyncFS {
		return nil
	}
	return &Batch{root: root, fuse: onFUSE(root)}
}

// Batchable reports whether f may join a batch: a file of its transfer's own folder received
// in one go, which has no journal.
func (f *File) Batchable() bool {
	f.commitMu.Lock()
	defer f.commitMu.Unlock()
	return f.p.Owned && f.journal == nil
}

// Add seals f, complete, into the batch: nothing more is written to it, and Flush gives it its
// name. It fails, and f is left as it was, if f is not complete or already closed.
func (b *Batch) Add(f *File) error {
	if _, err := f.Hashes(); err != nil {
		return err
	}
	if f.markClosed() {
		return fmt.Errorf("store: %s is closed", f.name)
	}
	b.files = append(b.files, f)
	b.bytes += f.p.Size
	return nil
}

// Len is the number of files in the batch.
func (b *Batch) Len() int { return len(b.files) }

// Bytes is their size.
func (b *Batch) Bytes() int64 { return b.bytes }

// Flush makes the files durable at once (syncfs; each file synced, parallelSyncs at a time, if
// that fails or on FUSE, where syncfs makes nothing durable: #302), closes them, gives each its
// name, then syncs each folder once (Unix). errs[i] is the error of the i-th file added: a file
// whose data could not be made durable is not renamed. The batch is empty afterwards.
func (b *Batch) Flush() (errs []error) {
	files := b.files
	b.files, b.bytes = nil, 0
	errs = make([]error, len(files))
	sync := false
	for _, f := range files {
		sync = sync || !f.p.NoSync
	}
	if sync && (b.fuse || syncFS(b.root) != nil) {
		syncEach(files, errs)
	}
	dirs := map[string]bool{}
	for i, f := range files {
		if err := f.part.Close(); errs[i] == nil {
			errs[i] = err
		}
		if errs[i] != nil { // not durable: no name; a resume starts it over
			f.closeJournal()
			continue
		}
		if errs[i] = f.publish(); errs[i] != nil {
			continue
		}
		if !f.p.NoSync {
			dirs[path.Dir(f.name)] = true
		}
		errs[i] = f.dropJournal()
	}
	if runtime.GOOS != "windows" {
		for d := range dirs {
			syncDir(b.root, d)
		}
	}
	return errs
}

// parallelSyncs bounds the fsyncs in flight. On the shared storage of the phones, 8 at a time
// took 0.33 to 0.5 ms a file, against 2 to 3.3 ms one after the other (R-0, bench-results).
const parallelSyncs = 8

// syncEach syncs each file that asks for it, parallelSyncs at a time; errs[i] gets file i's.
func syncEach(files []*File, errs []error) {
	slots := make(chan struct{}, parallelSyncs)
	var wg sync.WaitGroup
	for i, f := range files {
		if f.p.NoSync {
			continue
		}
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = f.part.Sync()
			<-slots
		}()
	}
	wg.Wait()
}

// errNoSyncFS: the system has no syncfs.
var errNoSyncFS = errors.New("store: no syncfs on this system")
