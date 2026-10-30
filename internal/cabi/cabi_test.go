package cabi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/core"
)

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("not JSON (B3): %q: %v", s, err)
	}
	return m
}

func config(t *testing.T) string { return configAt(folder(t), folder(t)) }

func configAt(dataDir, downloadDir string) string {
	b, _ := json.Marshal(map[string]string{"data_dir": dataDir, "download_dir": downloadDir, "device_name": "Lib"})
	return string(b)
}

// folder is a fresh folder for a core, removed once the core has stopped: its journal stays
// open until then (DEV-11), and Windows removes no open file. A stop held on purpose
// (TestStopIsBounded) ends a little after its test: the removal waits for it.
func folder(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cabi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		Stop()
		for end := time.Now().Add(5 * time.Second); os.RemoveAll(dir) != nil && time.Now().Before(end); {
			time.Sleep(20 * time.Millisecond)
		}
	})
	return dir
}

// DEV-11 and #169: the library's core writes its journal in data_dir, where the application's
// diagnostic bundle reads it; Stop closes it.
func TestStartOpensTheJournal(t *testing.T) {
	t.Cleanup(Stop)
	dir := folder(t)
	if r := decode(t, Start(configAt(dir, folder(t)))); r["ok"] != true {
		t.Fatalf("start: %v", r)
	}
	Stop()
	data, err := os.ReadFile(filepath.Join(dir, "journal", "journal.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "start medXfer") {
		t.Fatalf("journal without its start line: %q", data)
	}
	if err := os.Remove(filepath.Join(dir, "journal", "journal.log")); err != nil {
		t.Fatalf("journal still open after Stop: %v", err)
	}
}

// B4: start is idempotent (a hot restart finds the core running); after stop, a new start
// starts a fresh core. Call before start says so.
func TestStartIsIdempotent(t *testing.T) {
	t.Cleanup(Stop)
	if r := decode(t, Call(`{"id":"x","action":"get_status"}`)); r["ok"] != false {
		t.Fatalf("call before start: %v", r)
	}
	first := decode(t, Start(config(t)))
	if first["ok"] != true || first["already_started"] != false || first["schema"] != float64(core.SchemaVersion) {
		t.Fatalf("first start: %v", first)
	}
	again := decode(t, Start(config(t)))
	if again["ok"] != true || again["already_started"] != true || again["start_calls"].(float64) != first["start_calls"].(float64)+1 {
		t.Fatalf("second start: %v", again)
	}
	Stop()
	if fresh := decode(t, Start(config(t))); fresh["already_started"] != false {
		t.Fatalf("start after stop: %v", fresh)
	}
	if bad := decode(t, Start("")); bad["already_started"] != true {
		t.Fatalf("start while running: %v", bad)
	}
	Stop()
	if bad := decode(t, Start(`{not json`)); bad["ok"] != false || !strings.Contains(bad["error"].(string), "invalid config") {
		t.Fatalf("bad config: %v", bad)
	}
	if bad := decode(t, Start(`{}`)); bad["ok"] != false { // no data_dir (B6)
		t.Fatalf("no data_dir: %v", bad)
	}
}

// API-02: a call answers in JSON with its id; events come by NextEvent.
func TestCallAndEvents(t *testing.T) {
	t.Cleanup(Stop)
	decode(t, Start(config(t)))
	r := decode(t, Call(`{"id":"s","action":"get_status"}`))
	if r["ok"] != true || r["id"] != "s" {
		t.Fatalf("get_status: %v", r)
	}
	if r := decode(t, Call(`not json`)); r["ok"] != false {
		t.Fatalf("bad request: %v", r)
	}
	for { // a start may queue events: empty the queue, then expect none
		if _, ok := NextEvent(0); !ok {
			break
		}
	}
	if e, ok := NextEvent(50 * time.Millisecond); ok {
		t.Fatalf("an event out of nothing: %s", e)
	}
	if s := decode(t, Stats()); s["drops"] != float64(0) {
		t.Fatalf("stats: %v", s)
	}
}

// B9: Stop wakes a reader at once with the sentinel; after Stop, every read gets it.
func TestStopWakesReadersWithSentinel(t *testing.T) {
	t.Cleanup(Stop)
	decode(t, Start(config(t)))
	for {
		if _, ok := NextEvent(0); !ok {
			break
		}
	}
	got := make(chan string, 1)
	go func() {
		e, _ := NextEvent(time.Minute)
		got <- e
	}()
	time.Sleep(50 * time.Millisecond)
	Stop()
	select {
	case e := <-got:
		if e != StoppedEvent {
			t.Fatalf("reader got %q, want the sentinel", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not wake the reader")
	}
	if e, ok := NextEvent(time.Minute); !ok || e != StoppedEvent {
		t.Fatalf("read after stop: %q %v", e, ok)
	}
}
