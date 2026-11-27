package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// #313, STO-09: a file that appears at the final name while the .part is being renamed is never
// replaced (Overwrite off). The received file stays a .part, resumable; the other is untouched.
func TestPublishNeverReplacesAFileThatAppears(t *testing.T) {
	root, dir := openRoot(t)
	saved := rename
	t.Cleanup(func() { rename = saved })
	rename = func(r *os.Root, from, to string, replace bool) error {
		// Another program writes the final name now, after any check.
		if err := os.WriteFile(filepath.Join(dir, to), []byte("the user's file"), 0o644); err != nil {
			t.Fatal(err)
		}
		return saved(r, from, to, replace)
	}
	data, sums := content(t, 10)
	f, err := Open(root, "f.bin", params(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeChunk(f, 0, data, sums[0]); err != nil {
		t.Fatal(err)
	}
	err = f.Finalize()
	got, _ := os.ReadFile(filepath.Join(dir, "f.bin"))
	if !errors.Is(err, ErrExists) || string(got) != "the user's file" {
		t.Fatalf("Finalize = %v, f.bin holds %q; want ErrExists and the user's file", err, got)
	}
	if !exists(dir, "f.bin"+PartSuffix) {
		t.Fatal("the received file is lost; want it kept as a .part")
	}
}

// The rename that never replaces takes paths on Windows: a file deep in folders, its path longer
// than MAX_PATH, still gets its name.
func TestPublishDeepPath(t *testing.T) {
	root, dir := openRoot(t)
	deep := ""
	for len(dir)+len(deep) < 300 {
		deep += "un-dossier-assez-long/"
	}
	if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(deep)), 0o755); err != nil {
		t.Fatal(err)
	}
	data, sums := content(t, 10)
	f, err := Open(root, deep+"f.bin", params(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeChunk(f, 0, data, sums[0]); err != nil {
		t.Fatal(err)
	}
	if err := f.Finalize(); err != nil || !exists(dir, deep+"f.bin") {
		t.Fatalf("Finalize = %v for a path of %d characters", err, len(dir)+len(deep)+5)
	}
}
