package store

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// STO-13: the rename that never replaces goes through the root's handle, never by its path. The
// root opened through a link that is then pointed at another folder (a junction on Windows), the
// file still gets its name in the folder the root holds, never in the other one.
func TestPublishFollowsTheRootNotItsPath(t *testing.T) {
	parent := t.TempDir()
	real, other, link := filepath.Join(parent, "real"), filepath.Join(parent, "other"), filepath.Join(parent, "link")
	for _, d := range []string{real, other} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	makeLink(t, real, link)
	root, err := os.OpenRoot(link)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	data, sums := content(t, 10)
	f, err := Open(root, "f.bin", params(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeChunk(f, 0, data, sums[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil { // the link only, not its folder
		t.Fatal(err)
	}
	makeLink(t, other, link)
	if err := f.Finalize(); err != nil || !exists(real, "f.bin") || exists(other, "f.bin") {
		t.Fatalf("Finalize = %v; want f.bin in the folder the root holds, and only there", err)
	}
}

// makeLink makes link point to the folder target: a junction on Windows, which any user may
// make, a symbolic link elsewhere.
func makeLink(t *testing.T, target, link string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		return
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("no junction here: %v %s", err, out)
	}
}
