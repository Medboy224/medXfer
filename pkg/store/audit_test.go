package store

import (
	"runtime"
	"sync"
	"testing"
)

// Audit §3.6 (#156): verify allocates a chunk only when there is something to check: a file
// resumed with an empty journal costs nothing.
func TestVerifyAllocatesOnlyWhatItChecks(t *testing.T) {
	f := &File{p: Params{ChunkSize: 64 << 20}}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if err := f.verify(); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if n := after.TotalAlloc - before.TotalAlloc; n > 1<<20 {
		t.Fatalf("verify allocated %d bytes with nothing to check", n)
	}
}

// Audit §3.6 (#156): Suspend reads the journal under commitMu, as every writer of it does
// (go test -race).
func TestSuspendWhileCommitting(t *testing.T) {
	for range 20 {
		root, _ := openRoot(t)
		data, sums := content(t, 3*testChunk)
		f, err := Open(root, "f.bin", params(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range uint32(3) {
				_ = f.WriteChunk(i, chunkOf(data, int(i)), sums[i])
				_ = f.Commit()
			}
		}()
		_ = f.Suspend()
		wg.Wait()
	}
}
