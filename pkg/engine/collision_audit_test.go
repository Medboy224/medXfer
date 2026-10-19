package engine

import "testing"

// Audit §3.6 (#156): a hidden file has no extension: ".gitignore" becomes ".gitignore (1)",
// not " (1).gitignore"; one with an extension keeps it last.
func TestHiddenFileCandidates(t *testing.T) {
	for in, want := range map[string]string{
		".gitignore":    ".gitignore (1)",
		"dir/.env":      "dir/.env (1)",
		".bashrc.bak":   ".bashrc (1).bak",
		"photo.jpg":     "photo (1).jpg",
		"backup.tar.gz": "backup (1).tar.gz",
		"README":        "README (1)",
	} {
		dir, base, ext := SplitNameAndExt(in)
		if got := FormatCandidateName(dir, base, 1, ext); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// Audit §3.6 (#156): DEL (0x7f) is a control character too.
func TestDELIsRefused(t *testing.T) {
	if _, err := SafeRelPath("dir/a\x7fb.txt"); err == nil {
		t.Fatal("a name with DEL was accepted")
	}
}
