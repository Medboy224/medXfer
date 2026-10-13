package store

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// countSyncFS replaces syncfs with a counter that answers err; it runs on every system.
func countSyncFS(t *testing.T, err error) *int {
	t.Helper()
	calls := 0
	saved := syncFS
	syncFS = func(*os.Root) error { calls++; return err }
	t.Cleanup(func() { syncFS = saved })
	return &calls
}

// received opens name in its transfer's own folder and writes all of data, in one chunk.
func received(t *testing.T, root *os.Root, name string, data []byte) *File {
	t.Helper()
	f, err := Open(root, name, owned(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.WriteChunk(0, data, sha256.Sum256(data)); err != nil {
		t.Fatal(err)
	}
	return f
}

// WP-4.11: the files of a batch get their names only at the flush, all made durable by one
// syncfs; each keeps its content.
func TestBatchFlush(t *testing.T) {
	calls := countSyncFS(t, nil)
	root, dir := openRoot(t)
	b := &Batch{root: root}
	names := []string{"a/f1.bin", "a/f2.bin", "b/f3.bin"}
	var all [][]byte
	for k, name := range names {
		data, _ := content(t, 100+k)
		f := received(t, root, name, data)
		if !f.Batchable() {
			t.Fatalf("%s: a file received in one go in its own folder is batchable", name)
		}
		if err := b.Add(f); err != nil {
			t.Fatal(err)
		}
		all = append(all, data)
	}
	if b.Len() != 3 || b.Bytes() != 303 {
		t.Fatalf("batch of %d files, %d bytes", b.Len(), b.Bytes())
	}
	for _, name := range names {
		if exists(dir, name) {
			t.Fatalf("%s has its name before the flush", name)
		}
	}
	for k, err := range b.Flush() {
		if err != nil {
			t.Fatalf("%s: %v", names[k], err)
		}
	}
	if *calls != 1 {
		t.Fatalf("%d syncfs for the batch; want 1", *calls)
	}
	for k, name := range names {
		if got, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name))); !bytes.Equal(got, all[k]) {
			t.Fatalf("%s differs", name)
		}
		if exists(dir, name+PartSuffix) || exists(dir, name+JournalSuffix) {
			t.Fatalf("%s left its partial files", name)
		}
	}
	if b.Len() != 0 || len(b.Flush()) != 0 {
		t.Fatal("the batch is not empty after its flush")
	}
}

// A name taken meanwhile: that file fails and stays resumable (its journal notes its chunks);
// the others of the batch get their names.
func TestBatchNameTaken(t *testing.T) {
	countSyncFS(t, nil)
	root, dir := openRoot(t)
	b := &Batch{root: root}
	data, _ := content(t, 50)
	for _, name := range []string{"x.bin", "y.bin"} {
		if err := b.Add(received(t, root, name, data)); err != nil {
			t.Fatal(err)
		}
	}
	writeRaw(t, dir, "x.bin", "the user's, arrived meanwhile")
	errs := b.Flush()
	if !errors.Is(errs[0], ErrExists) || errs[1] != nil {
		t.Fatalf("flush errors %v; want ErrExists, then nil", errs)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "x.bin")); string(got) != "the user's, arrived meanwhile" {
		t.Fatal("the file of that name was replaced")
	}
	g, err := Open(root, "x.bin", owned(len(data)))
	if err != nil || !g.Has(0) {
		t.Fatalf("the file whose name was taken is not resumable: %v", err)
	}
	g.Close()
	if !exists(dir, "y.bin") {
		t.Fatal("the other file of the batch has no name")
	}
}

// Without syncfs (it failed), each file is synced alone: the batch still ends.
func TestBatchSyncFSFails(t *testing.T) {
	calls := countSyncFS(t, errNoSyncFS)
	root, dir := openRoot(t)
	b := &Batch{root: root}
	data, _ := content(t, 10)
	if err := b.Add(received(t, root, "f.bin", data)); err != nil {
		t.Fatal(err)
	}
	if errs := b.Flush(); errs[0] != nil || *calls != 1 || !exists(dir, "f.bin") {
		t.Fatalf("flush %v after %d syncfs; want the file named", errs, *calls)
	}
}

// Only a complete file of its own folder, without journal, joins a batch; Add leaves any other
// file as it was.
func TestBatchTakesOnlyCompleteFiles(t *testing.T) {
	countSyncFS(t, nil)
	root, dir := openRoot(t)
	b := &Batch{root: root}
	data, sums := content(t, 2*testChunk)
	f, _ := Open(root, "half.bin", owned(len(data)))
	_ = f.WriteChunk(0, chunkOf(data, 0), sums[0])
	if err := b.Add(f); err == nil || b.Len() != 0 {
		t.Fatal("a file with a chunk missing joined the batch")
	}
	_ = f.WriteChunk(1, chunkOf(data, 1), sums[1]) // still open: it goes on
	if err := f.Finalize(); err != nil || !exists(dir, "half.bin") {
		t.Fatalf("the file left aside by the batch could not end: %v", err)
	}

	single, _ := Open(root, "single.bin", params(10)) // not in its own folder
	if single.Batchable() {
		t.Fatal("a file outside its transfer's own folder is batchable")
	}
	single.Close()
	paused, _ := Open(root, "paused.bin", owned(len(data)))
	_ = paused.WriteChunk(0, chunkOf(data, 0), sums[0])
	_ = paused.Close() // a journal now
	again, _ := Open(root, "paused.bin", owned(len(data)))
	if again.Batchable() {
		t.Fatal("a file with a journal is batchable")
	}
	again.Close()
}
