package engine

import (
	"testing"
)

func TestSplitNameAndExt(t *testing.T) {
	cases := []struct {
		input   string
		wantDir string
		wantBas string
		wantExt string
	}{
		{"video.mp4", "", "video", ".mp4"},
		{"archive.tar.gz", "", "archive", ".tar.gz"},
		{"folder/sub/data.tar.bz2", "folder/sub", "data", ".tar.bz2"},
		{"folder\\sub\\doc.pdf", "folder/sub", "doc", ".pdf"},
		{"Dockerfile", "", "Dockerfile", ""},
		{".gitignore", "", ".gitignore", ""}, // a hidden file has no extension (#156)
		{"folder/notes.txt", "folder", "notes", ".txt"},
	}

	for _, c := range cases {
		d, b, e := SplitNameAndExt(c.input)
		if d != c.wantDir || b != c.wantBas || e != c.wantExt {
			t.Errorf("SplitNameAndExt(%q) = (%q, %q, %q); want (%q, %q, %q)",
				c.input, d, b, e, c.wantDir, c.wantBas, c.wantExt)
		}
	}
}
