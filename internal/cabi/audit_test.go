package cabi

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// Audit 12 (#156): a start that comes while the core stops waits for the end of the stop: two
// cores never run at once (same port, same files).
func TestStartWaitsForStop(t *testing.T) {
	t.Cleanup(Stop)
	decode(t, Start(config(t)))
	var mu sync.Mutex
	var stopped time.Time
	inStop := make(chan struct{})
	stopping = func() {
		close(inStop)
		time.Sleep(300 * time.Millisecond)
	}
	t.Cleanup(func() { stopping = func() {} })
	done := make(chan struct{})
	go func() {
		Stop()
		mu.Lock()
		stopped = time.Now()
		mu.Unlock()
		close(done)
	}()
	<-inStop
	stopping = func() {}
	r := decode(t, Start(config(t)))
	started := time.Now()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if r["ok"] != true {
		t.Fatalf("start after stop: %v", r)
	}
	if started.Before(stopped) {
		t.Fatalf("a core started %v before the old one had stopped", stopped.Sub(started))
	}
}

// Audit 12 (#156): no entry point lets a panic cross into C (B5).
func TestEveryEntryPointRecovers(t *testing.T) {
	t.Cleanup(Stop)
	decode(t, Start(config(t)))
	stopping = func() { panic("boom") }
	t.Cleanup(func() { stopping = func() {} })
	Stop() // must not panic
	if Stats() == "" {
		t.Fatal("Stats after a panicking stop")
	}
}

// §3.3 (#156): a negative timeout waits for an event without end, instead of returning at once
// (a reader looping with -1 used a whole processor).
func TestNegativeTimeoutWaits(t *testing.T) {
	t.Cleanup(Stop)
	decode(t, Start(config(t)))
	for { // drain the start's events
		if _, ok := NextEvent(0); !ok {
			break
		}
	}
	got := make(chan string, 1)
	go func() {
		e, ok := NextEvent(-1)
		if !ok {
			e = ""
		}
		got <- e
	}()
	select {
	case e := <-got:
		t.Fatalf("NextEvent(-1) returned at once: %q", e)
	case <-time.After(300 * time.Millisecond):
	}
	Stop()
	select {
	case e := <-got:
		if !strings.Contains(e, "core_stopped") {
			t.Fatalf("NextEvent(-1) after stop: %q, want the sentinel", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not wake a reader waiting without end")
	}
}
