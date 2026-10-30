package api

import (
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readJournal(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "journal.log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// StartJournal's stop removes its own journal only: a core that ends late, after another one
// started (internal/cabi), leaves the new one's journal installed.
func TestStartJournalStopLeavesANewerOne(t *testing.T) {
	t.Cleanup(func() { SetJournal(nil); log.SetOutput(os.Stderr) })
	stopOld := StartJournal(t.TempDir())
	dir := t.TempDir()
	stopNew := StartJournal(dir)
	stopOld()
	journalf("after the old one stopped")
	stopNew()
	journalf("after both stopped")
	text := readJournal(t, dir)
	if !strings.Contains(text, "after the old one stopped") {
		t.Errorf("the old journal's stop removed the new one: %q", text)
	}
	if strings.Contains(text, "after both stopped") || journal.Load() != nil {
		t.Errorf("a journal still installed after its stop: %q", text)
	}
}

// #169: a pairing broken off leaves its cause in the journal, not only in the toast that goes
// in seconds (a failure seen once in a real test stayed unexplained, 30 October 2026).
func TestPairFailureCauseInJournal(t *testing.T) {
	dir := t.TempDir()
	stop := StartJournal(dir)
	t.Cleanup(func() { stop(); SetJournal(nil); log.SetOutput(os.Stderr) })
	_, n := startOwnIdentity(t, "Typist")
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	target := ln.Addr().String()
	ln.Close() // nothing answers there
	n.Request("pair_pin", map[string]any{"target": target, "pin": "123456"})
	n.WaitEvent("action_error", 0)
	stop()
	if text := readJournal(t, dir); !strings.Contains(text, "log WARN failed to pair with "+target+": ") {
		t.Fatalf("the journal lacks the failure's cause: %q", text)
	}
}
