package engine

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeRelPath(t *testing.T) {
	valid := map[string]string{
		"a.txt":               "a.txt",
		"dir/sub/f.bin":       "dir/sub/f.bin",
		`dir\sub\f.bin`:       "dir/sub/f.bin",
		"mon fichier (1).pdf": "mon fichier (1).pdf",
		"a..b.txt":            "a..b.txt",
		"..foo":               "..foo",
		".gitignore":          ".gitignore",
		"é/ü.txt":             "é/ü.txt",
		"backup (1).tar.gz":   "backup (1).tar.gz",
	}
	invalid := []string{
		"", "/abs", `\abs`, "../x", `..\x`, "a/../../x", "a/./b", "a//b",
		"a/b/..", "a/", "a\x00b", "a\x01b", "a\nb",
		strings.Repeat("x", 256), strings.Repeat("a/", 512) + "b",
	}
	// Windows-only rules (decision D0-1): valid elsewhere, rejected when the receiver runs Windows.
	// Drive prefixes ("C:x", `C:\x`) are harmless names on Linux/Android; on Windows ':' blocks them.
	windowsOnly := []string{"CON", "con.txt", "NUL.tar.gz", "com1", "LPT9.log", "x.", "x ", "dir/x./f",
		"ab:c", "a:b", "C:x", "c:/x", `C:\x`, "c:notes.txt", "a*b", "a?b", "a<b", `a"b`, "a|b"}
	windowsOK := []string{"CONSOLE.txt", "COM0", "LPT10", "nul_file", "con-tract.pdf"}

	for _, win := range []bool{false, true} {
		for in, want := range valid {
			got, err := safeRelPath(in, win)
			if err != nil || got != want {
				t.Errorf("safeRelPath(%q, win=%v) = %q, %v; want %q, nil", in, win, got, err, want)
			}
		}
		for _, in := range invalid {
			if got, err := safeRelPath(in, win); !errors.Is(err, ErrBadPath) {
				t.Errorf("safeRelPath(%q, win=%v) = %q, %v; want ErrBadPath", in, win, got, err)
			}
		}
		for _, in := range windowsOK {
			if _, err := safeRelPath(in, win); err != nil {
				t.Errorf("safeRelPath(%q, win=%v) = %v; want valid", in, win, err)
			}
		}
	}
	for _, in := range windowsOnly {
		if _, err := safeRelPath(in, false); err != nil {
			t.Errorf("safeRelPath(%q, win=false) = %v; want valid", in, err)
		}
		if _, err := safeRelPath(in, true); !errors.Is(err, ErrBadPath) {
			t.Errorf("safeRelPath(%q, win=true) = %v; want ErrBadPath", in, err)
		}
	}
}

// Attack scenario of fault 7: the receive folder holds "Contrat.pdf"; a peer offers
// "Contrat.pdf/x.txt". The old ensureDirectory deleted Contrat.pdf to create the folder.
func TestEnsureSubdirsNeverDeletes(t *testing.T) {
	recvDir := t.TempDir()
	victim := filepath.Join(recvDir, "Contrat.pdf")
	content := []byte("signed contract")
	if err := os.WriteFile(victim, content, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := ResolveCollision(recvDir, "Contrat.pdf/x.txt", "id", 10, 64*1024, PolicyAutoRename)
	if err != nil {
		t.Fatalf("ResolveCollision: %v", err)
	}
	dm, err := CreateAndPreallocate(recvDir, res.ResolvedName, 10, 64*1024, "id")
	if dm != nil {
		_ = dm.Close()
	}
	if !errors.Is(err, ErrPathConflict) {
		t.Fatalf("CreateAndPreallocate error = %v; want ErrPathConflict", err)
	}
	if got, err := os.ReadFile(victim); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("victim file damaged: %q, %v", got, err)
	}
}

func TestForgedStateFileDoesNotDestroyUserFile(t *testing.T) {
	for _, policy := range []CollisionPolicy{PolicyAutoRename, PolicySkip} {
		recvDir := t.TempDir()
		victim := filepath.Join(recvDir, "victim.docx")
		content := []byte("user document")
		_ = os.WriteFile(victim, content, 0o644)
		_ = os.WriteFile(victim+".medxfer", []byte(strings.Repeat("F", 40)), 0o644) // forged state

		res, err := ResolveCollision(recvDir, "victim.docx", "attacker-id", 50, 64*1024, policy)
		if err != nil {
			t.Fatalf("policy %d: ResolveCollision: %v", policy, err)
		}
		switch policy {
		case PolicyAutoRename:
			if res.ResolvedName != "victim (1).docx" {
				t.Fatalf("auto-rename resolved %q; want %q", res.ResolvedName, "victim (1).docx")
			}
			dm, err := CreateAndPreallocate(recvDir, res.ResolvedName, 50, 64*1024, "attacker-id")
			if err != nil {
				t.Fatalf("CreateAndPreallocate: %v", err)
			}
			_ = dm.Close()
		case PolicySkip:
			if !res.IsDuplicate {
				t.Fatalf("skip policy did not skip: %+v", res)
			}
		}
		if got, err := os.ReadFile(victim); err != nil || !bytes.Equal(got, content) {
			t.Fatalf("policy %d: victim file damaged: %q, %v", policy, got, err)
		}
	}
}

func TestPeekResumeOffsetRejectsBadPath(t *testing.T) {
	base := t.TempDir()
	recvDir := filepath.Join(base, "recv")
	_ = os.Mkdir(recvDir, 0o755)
	// A valid-looking state file outside the receive folder must never be read.
	id := strings.Repeat("a", 32)
	_ = os.WriteFile(filepath.Join(base, "secret.medxfer"), append([]byte(id), 1), 0o644)

	n, err := PeekResumeOffset(recvDir, "../secret", id, 10, 64*1024)
	if n != 0 || err != nil {
		t.Fatalf("PeekResumeOffset = %d, %v; want 0, nil", n, err)
	}
}

// The rules the tar extraction used to check, now without it (StreamTar is gone, plan J4a D3).
func TestPathRules(t *testing.T) {
	// "..foo" is a legitimate name (an old prefix check rejected it).
	for _, ok := range []string{"dir/..foo", "a..b.txt"} {
		if _, err := SafeRelPath(ok); err != nil {
			t.Fatalf("SafeRelPath(%q) = %v; want accepted", ok, err)
		}
	}

	// A file where a folder is needed: conflict, nothing deleted.
	dst := t.TempDir()
	_ = os.WriteFile(filepath.Join(dst, "a"), []byte("keep"), 0o644)
	if err := ensureSubdirs(dst, "a"); !errors.Is(err, ErrPathConflict) {
		t.Fatalf("ensureSubdirs over a file = %v; want ErrPathConflict", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "a")); string(got) != "keep" {
		t.Fatalf("file 'a' was damaged: %q", got)
	}

	// Windows reserved names: rejected only when the receiver runs Windows.
	if _, err := safeRelPath("dir/CON.txt", true); !errors.Is(err, ErrBadPath) {
		t.Fatalf("reserved name accepted under Windows rules: %v", err)
	}
}

func TestSymlinkOutsideRootRejected(t *testing.T) {
	recvDir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(recvDir, "out")); err != nil {
		t.Skipf("cannot create symlink on this platform: %v", err)
	}
	if err := ensureSubdirs(recvDir, "out/sub"); !errors.Is(err, ErrPathConflict) {
		t.Fatalf("ensureSubdirs through outside link = %v; want ErrPathConflict", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("a folder was created outside the receive folder: %v", entries)
	}
}

func TestSymlinkInsideRootAccepted(t *testing.T) {
	recvDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(recvDir, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(recvDir, "real"), filepath.Join(recvDir, "alias")); err != nil {
		t.Skipf("cannot create symlink on this platform: %v", err)
	}
	if err := ensureSubdirs(recvDir, "alias/sub"); err != nil {
		t.Fatalf("ensureSubdirs through inside link = %v; want nil", err)
	}
	if fi, err := os.Stat(filepath.Join(recvDir, "real", "sub")); err != nil || !fi.IsDir() {
		t.Fatalf("sub folder not created through the link: %v", err)
	}
}
