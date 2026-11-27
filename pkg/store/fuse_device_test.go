package store

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fuseDirEnv names a folder on a real FUSE file system (a phone's shared storage): the tests below
// run only on such a device, by adb.
const fuseDirEnv = "MEDXFER_TEST_FUSE_DIR"

// #302, on a phone: its shared storage is recognized as FUSE, its app storage is not.
func TestOnFUSEDetectsSharedStorage(t *testing.T) {
	dir := os.Getenv(fuseDirEnv)
	if dir == "" {
		t.Skip("needs " + fuseDirEnv + ": a folder of a phone's shared storage")
	}
	shared, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	private, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer private.Close()
	if !onFUSE(shared) || onFUSE(private) {
		t.Fatalf("FUSE: shared storage %v, app storage %v; want true, false", onFUSE(shared), onFUSE(private))
	}
}

// #302, on a phone: the time a batch of small files takes to become durable on its shared
// storage, by syncfs (with its fallback) and by an fsync per file, parallelSyncs at a time.
func TestBatchFlushTimeOnSharedStorage(t *testing.T) {
	dir := os.Getenv(fuseDirEnv)
	if dir == "" {
		t.Skip("needs " + fuseDirEnv + ": a folder of a phone's shared storage")
	}
	const files, size = 200, 16 << 10
	data := make([]byte, size)
	sum := sha256.Sum256(data)
	for _, mode := range []struct {
		name string
		fuse bool
	}{{"syncfs", false}, {"fsync per file", true}} {
		sub := filepath.Join(dir, fmt.Sprintf("flush-%d", time.Now().UnixNano()))
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(sub)
		if err != nil {
			t.Fatal(err)
		}
		b := &Batch{root: root, fuse: mode.fuse}
		p := owned(size)
		p.ChunkSize = size // a small file: one chunk
		for i := 0; i < files; i++ {
			f, err := Open(root, fmt.Sprintf("f%03d.bin", i), p)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.WriteChunk(0, data, sum); err != nil {
				t.Fatal(err)
			}
			if err := b.Add(f); err != nil {
				t.Fatal(err)
			}
		}
		start := time.Now()
		for i, err := range b.Flush() {
			if err != nil {
				t.Fatalf("%s, file %d: %v", mode.name, i, err)
			}
		}
		d := time.Since(start)
		syncErr := syncFS(root)
		root.Close()
		t.Logf("%-15s %d files of %d KiB: %v, %.2f ms a file (syncfs answers: %v)", mode.name, files, size>>10, d.Round(time.Millisecond), float64(d.Microseconds())/1000/files, syncErr)
		_ = os.RemoveAll(sub)
	}
}
