package api

import (
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/session"
)

// Audit §3.7 (#156): the host of a target, IPv6 included.
func TestTargetHost(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.5":       "192.168.1.5",
		"192.168.1.5:18887": "192.168.1.5",
		"[fe80::1]:18887":   "fe80::1",
		"fe80::1":           "fe80::1",
		"[fe80::1]":         "fe80::1",
		"":                  "",
	} {
		if got := targetHost(in); got != want {
			t.Errorf("targetHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// Audit §3.7 (#156): a pong left from an earlier measurement is not taken for the answer to
// this ping.
func TestStalePongIsIgnored(t *testing.T) {
	pongs := make(chan session.Message, 10)
	old := time.Now().Add(-time.Minute).UnixNano()
	pongs <- session.Message{Type: "bench_pong", BenchPingTS: old}
	ts := time.Now().UnixNano()
	go func() {
		time.Sleep(20 * time.Millisecond)
		pongs <- session.Message{Type: "bench_pong", BenchPingTS: ts}
	}()
	rtt, ok := awaitPong(pongs, ts, time.Second)
	if !ok || rtt > 1000 {
		t.Fatalf("RTT %.0f ms (%v): the stale pong was taken", rtt, ok)
	}
}
