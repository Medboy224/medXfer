package engine

import (
	"errors"
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

// The rules the tar extraction used to check, now without it (StreamTar is gone, plan J4a D3).
func TestPathRules(t *testing.T) {
	// "..foo" is a legitimate name (an old prefix check rejected it).
	for _, ok := range []string{"dir/..foo", "a..b.txt"} {
		if _, err := SafeRelPath(ok); err != nil {
			t.Fatalf("SafeRelPath(%q) = %v; want accepted", ok, err)
		}
	}

	// Windows reserved names: rejected only when the receiver runs Windows.
	if _, err := safeRelPath("dir/CON.txt", true); !errors.Is(err, ErrBadPath) {
		t.Fatalf("reserved name accepted under Windows rules: %v", err)
	}
}
