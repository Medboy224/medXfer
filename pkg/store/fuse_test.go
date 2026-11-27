package store

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// syncCounted is a file whose Sync is counted, and lasts long enough to overlap with others.
type syncCounted struct {
	handle
	calls, inFlight, most *atomic.Int32
}

func (f syncCounted) Sync() error {
	f.calls.Add(1)
	n := f.inFlight.Add(1)
	for {
		m := f.most.Load()
		if n <= m || f.most.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(20 * time.Millisecond)
	f.inFlight.Add(-1)
	return f.handle.Sync()
}

// #302: on FUSE (Android's shared storage), syncfs makes nothing durable: it fails, or succeeds
// without writing anything. A batch there never relies on it: each file is synced, several at a
// time, before it gets its name.
func TestBatchOnFUSESyncsEachFile(t *testing.T) {
	var calls, inFlight, most atomic.Int32
	saved := openFile
	var mu sync.Mutex
	openFile = func(root *os.Root, name string, flag int) (handle, error) {
		h, err := saved(root, name, flag)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		return syncCounted{h, &calls, &inFlight, &most}, nil
	}
	t.Cleanup(func() { openFile = saved })
	syncfs := countSyncFS(t, nil) // it would answer success, as on the TECNO

	root, dir := openRoot(t)
	b := &Batch{root: root, fuse: true}
	const n = 12
	names := make([]string, n)
	for i := range names {
		names[i] = string(rune('a'+i)) + ".bin"
		data, _ := content(t, 100+i)
		if err := b.Add(received(t, root, names[i], data)); err != nil {
			t.Fatal(err)
		}
	}
	calls.Store(0) // only the flush's syncs count
	for i, err := range b.Flush() {
		if err != nil || !exists(dir, names[i]) {
			t.Fatalf("file %d: %v", i, err)
		}
	}
	if *syncfs != 0 || calls.Load() < n {
		t.Fatalf("%d syncfs and %d fsyncs for %d files on FUSE; want no syncfs and one fsync each", *syncfs, calls.Load(), n)
	}
	if m := most.Load(); m < 2 || m > parallelSyncs {
		t.Fatalf("%d fsyncs at once; want several, %d at most", m, parallelSyncs)
	}
}
