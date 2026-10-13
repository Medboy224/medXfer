package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Resolve finds a place without writing anything (#75), whatever the policy.
func TestResolve(t *testing.T) {
	for _, c := range []struct {
		name     string
		existing []string
		policy   Policy
		want     Place
	}{
		{"free name", nil, PolicyAutoRename, Place{Name: "a.txt"}},
		{"renamed", []string{"a.txt"}, PolicyAutoRename, Place{Name: "a (1).txt"}},
		{"renamed twice", []string{"a.txt", "a (1).txt"}, PolicyAutoRename, Place{Name: "a (2).txt"}},
		{"overwrite", []string{"a.txt"}, PolicyOverwrite, Place{Name: "a.txt", Overwrite: true}},
		{"skip", []string{"a.txt"}, PolicySkip, Place{Name: "a.txt", Done: true}},
		{"folder of that name", []string{"a.txt/inside"}, PolicyOverwrite, Place{Name: "a (1).txt"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			root, dir := openRoot(t)
			for _, e := range c.existing {
				write(t, dir, e, "user's")
			}
			got, err := Resolve(root, "a.txt", params(10), c.policy)
			if err != nil || got != c.want {
				t.Fatalf("Resolve = %+v, %v; want %+v", got, err, c.want)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != len(dirsOf(c.existing)) {
				t.Fatalf("Resolve wrote something: %v", entries)
			}
		})
	}
}

// dirsOf is the set of top-level entries that paths create.
func dirsOf(paths []string) map[string]bool {
	top := map[string]bool{}
	for _, p := range paths {
		top[strings.SplitN(p, "/", 2)[0]] = true
	}
	return top
}

// The name of a compound extension and of a file in a subfolder are numbered before the
// extension, in the same folder.
func TestResolveNames(t *testing.T) {
	root, dir := openRoot(t)
	write(t, dir, "backup.tar.gz", "old")
	write(t, dir, "assets/icons/logo.png", "old")
	for name, want := range map[string]string{
		"backup.tar.gz":         "backup (1).tar.gz",
		"assets/icons/logo.png": "assets/icons/logo (1).png",
	} {
		if got, err := Resolve(root, name, params(10), PolicyAutoRename); err != nil || got.Name != want {
			t.Errorf("Resolve(%s) = %+v, %v; want %s", name, got, err, want)
		}
	}
}

// A partial file of this transfer is resumed; one of another transfer, or data without a
// journal, is stepped over and never touched (STO-09).
func TestResolvePartials(t *testing.T) {
	root, dir := openRoot(t)
	data, sums := content(t, 2*testChunk)
	f, _ := Open(root, "a.txt", params(len(data)))
	_ = writeChunk(f, 0, chunkOf(data, 0), sums[0])
	_ = f.Close()

	if got, _ := Resolve(root, "a.txt", params(len(data)), PolicyAutoRename); got != (Place{Name: "a.txt", Resume: true}) {
		t.Fatalf("own partial: %+v", got)
	}
	other := params(len(data))
	other.Key = Key{9}
	if got, _ := Resolve(root, "a.txt", other, PolicyOverwrite); got.Name != "a (1).txt" || got.Resume {
		t.Fatalf("another transfer's partial: %+v", got)
	}
	write(t, dir, "b.txt"+PartSuffix, "someone's data")
	if got, _ := Resolve(root, "b.txt", params(10), PolicyOverwrite); got.Name != "b (1).txt" {
		t.Fatalf("data without journal: %+v", got)
	}
}

// Fault 7 (bible §9.8): the destination holds the file "Contrat.pdf", an offer brings
// "Contrat.pdf/x.txt". Nothing is deleted: the item fails with a path conflict (STO-11).
func TestFileWhereAFolderIsNeeded(t *testing.T) {
	root, dir := openRoot(t)
	write(t, dir, "Contrat.pdf", "signed contract")
	if _, err := Resolve(root, "Contrat.pdf/x.txt", params(10), PolicyOverwrite); !errors.Is(err, ErrPathConflict) {
		t.Fatalf("Resolve = %v; want ErrPathConflict", err)
	}
	if _, err := Open(root, "Contrat.pdf/x.txt", params(10)); !errors.Is(err, ErrPathConflict) {
		t.Fatalf("Open = %v; want ErrPathConflict", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "Contrat.pdf")); string(got) != "signed contract" {
		t.Fatalf("the user's file was damaged: %q", got)
	}
}

// STO-13: os.Root follows a relative link that stays in the destination, and refuses one that
// leads out of it, or an absolute one even if it points inside (os.Root cannot tell where an
// absolute link goes without leaving the root). Nothing is ever written outside.
func TestLinks(t *testing.T) {
	root, dir := openRoot(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "out")); err != nil {
		t.Skipf("cannot create a link here: %v", err)
	}
	_ = os.Mkdir(filepath.Join(dir, "real"), 0o755)
	_ = os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "absolute"))
	_ = os.Symlink("real", filepath.Join(dir, "relative"))

	for _, name := range []string{"out/x.txt", "absolute/x.txt"} {
		if f, err := Open(root, name, params(10)); err == nil {
			f.Close()
			t.Errorf("%s: opened through a link os.Root must refuse", name)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("something was written outside the destination: %v", entries)
	}
	f, err := Open(root, "relative/sub/x.txt", params(10))
	if err != nil {
		t.Fatalf("a relative link inside the destination was refused: %v", err)
	}
	f.Close()
	if _, err := os.Stat(filepath.Join(dir, "real", "sub", "x.txt"+PartSuffix)); err != nil {
		t.Fatalf("not written through the inside link: %v", err)
	}
}

// A failed sync at the end keeps the file resumable: the journal stays, nothing gets the
// final name.
func TestFinalizeSyncFailureKeepsState(t *testing.T) {
	root, dir := openRoot(t)
	m := &memFS{files: map[string]*memFile{}}
	useMemFS(t, m)
	data, sums := content(t, testChunk)
	f, _ := Open(root, "f.bin", params(len(data)))
	_ = writeChunk(f, 0, data, sums[0])
	_ = f.Commit()
	m.files["f.bin"+PartSuffix].failSync = true
	if err := f.Finalize(); err == nil {
		t.Fatal("Finalize succeeded although the data sync failed")
	}
	if exists(dir, "f.bin") || len(m.files["f.bin"+JournalSuffix].dur) == 0 {
		t.Fatal("a failed sync gave the final name or lost the journal")
	}
}

// STO-10, D5, D11: the root of a transfer is base if free; a folder of the user is stepped
// over ("overwrite" too); "skip" refuses; a folder whose marker holds the key is resumed.
func TestResolveRoot(t *testing.T) {
	root, dir := openRoot(t)
	key := RootKey([32]byte{1}, "Photos", 3, 100)
	if got, _ := ResolveRoot(root, "Photos", key, PolicyAutoRename); got != (RootPlace{Name: "Photos"}) {
		t.Fatalf("free: %+v", got)
	}
	write(t, dir, "Photos/mine.jpg", "user's")
	for _, policy := range []Policy{PolicyAutoRename, PolicyOverwrite} {
		if got, _ := ResolveRoot(root, "Photos", key, policy); got != (RootPlace{Name: "Photos (1)"}) {
			t.Fatalf("policy %d, taken: %+v", policy, got)
		}
	}
	if got, _ := ResolveRoot(root, "Photos", key, PolicySkip); got != (RootPlace{Name: "Photos", Skip: true}) {
		t.Fatalf("skip: %+v", got)
	}
	if err := MarkRoot(root, "Photos (1)", key); err != nil {
		t.Fatal(err)
	}
	if got, _ := ResolveRoot(root, "Photos", key, PolicyAutoRename); got != (RootPlace{Name: "Photos (1)", Resume: true}) {
		t.Fatalf("own root: %+v", got)
	}
	other := RootKey([32]byte{2}, "Photos", 3, 100)
	if got, _ := ResolveRoot(root, "Photos", other, PolicyAutoRename); got.Name != "Photos (2)" || got.Resume {
		t.Fatalf("another transfer's root: %+v", got)
	}
	if err := UnmarkRoot(root, "Photos (1)", other); err != nil || !exists(dir, "Photos (1)/"+MarkerName) {
		t.Fatal("the marker of another transfer was removed")
	}
	if err := UnmarkRoot(root, "Photos (1)", key); err != nil || exists(dir, "Photos (1)/"+MarkerName) {
		t.Fatalf("own marker not removed: %v", err)
	}
	if got := RootKey([32]byte{1}, "Photos", 4, 100); got == key {
		t.Fatal("another number of items gives the same root key")
	}
}
