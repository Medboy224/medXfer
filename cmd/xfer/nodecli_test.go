package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// node send: a path with spaces goes between double quotes (#314); an apostrophe and a
// backslash stay characters (l'été.jpg, C:\Users).
func TestSplitArgs(t *testing.T) {
	for _, c := range []struct {
		line string
		want []string
	}{
		{`a.txt b.txt`, []string{"a.txt", "b.txt"}},
		{`"Mes documents/x.pdf" c.txt`, []string{"Mes documents/x.pdf", "c.txt"}},
		{`  "C:\Users\me\My file.txt"  `, []string{`C:\Users\me\My file.txt`}},
		{`l'été.jpg aujourd'hui.png`, []string{"l'été.jpg", "aujourd'hui.png"}},
		{`photo" "1.jpg`, []string{"photo 1.jpg"}},
		{`   `, nil},
	} {
		got, err := splitArgs(c.line)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("splitArgs(%q) = %q, %v; want %q", c.line, got, err, c.want)
		}
	}
	if _, err := splitArgs(`"Mes documents/x.pdf`); err == nil {
		t.Error("an open quote was accepted")
	}
}

// xfer node receives into the configured download folder, not the current one (#314).
func TestNodeReceivesInTheDownloadFolder(t *testing.T) {
	cfg, want := t.TempDir(), t.TempDir()
	b, _ := json.Marshal(map[string]string{"download_dir": want})
	if err := os.WriteFile(filepath.Join(cfg, "config.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEDXFER_CONFIG_DIR", cfg)
	if got := nodeReceiveDir(); got != want {
		t.Fatalf("xfer node receives into %q; want %q", got, want)
	}
}

type recordingAnnouncer struct{ calls []string }

func (a *recordingAnnouncer) SetTags(func() []string) { a.calls = append(a.calls, "tags") }
func (a *recordingAnnouncer) Start(context.Context)   { a.calls = append(a.calls, "start") }

// The node's first announcement carries its tags: they are set before it starts (#337).
// Started first, it announced none for 30 s.
func TestNodeSetsItsTagsBeforeAnnouncing(t *testing.T) {
	var a recordingAnnouncer
	startAnnouncing(context.Background(), &a, func() []string { return nil })
	if !slices.Equal(a.calls, []string{"tags", "start"}) {
		t.Fatalf("calls %v; want tags, then start", a.calls)
	}
}
