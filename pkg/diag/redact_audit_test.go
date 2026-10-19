package diag

import "testing"

// Audit 13 (#156): a Windows path is masked whole, folders with spaces included, but the text
// after it stays; a quoted path is masked up to its quote.
func TestRedactWindowsPathKeepsTheRestOfTheLine(t *testing.T) {
	r := NewRedactor()
	cases := map[string]string{
		`copied C:\Users\bob\film.mkv in 3 s`:            `copied [PATH] in 3 s`,
		`from C:\Program Files\medXfer\xfer.exe started`: `from [PATH] started`,
		`saved to "C:\Users\bob\my film.mkv" (2 GB)`:     `saved to "[PATH]" (2 GB)`,
		`saved to 'D:\Vidéos\été 2026\a.mp4', done`:      `saved to '[PATH]', done`,
		`two: C:\a\b.txt and D:\c\d.txt, done`:           `two: [PATH] and [PATH], done`,
		`error: open C:\Users\bob\x.bin: access denied`:  `error: open [PATH]: access denied`,
	}
	for in, want := range cases {
		if got := r.Redact(in); got != want {
			t.Errorf("Redact(%q)\n  = %q\nwant %q", in, got, want)
		}
	}
}

// Audit 13 (#156): a user or host name is masked as a word, never inside another word.
func TestReplaceFoldKeepsOtherWords(t *testing.T) {
	cases := map[string]string{
		"user ALI here":            "user [USER] here",
		"alignment of ali's files": "alignment of [USER]'s files",
		"Ali,ali;(ali)":            "[USER],[USER];([USER])",
		"khalid, alibi, ali2":      "khalid, alibi, ali2",
		`C:\Users\ali\x`:           `C:\Users\[USER]\x`,
	}
	for in, want := range cases {
		if got := replaceFold(in, "ali", "[USER]"); got != want {
			t.Errorf("replaceFold(%q)\n  = %q\nwant %q", in, got, want)
		}
	}
}
