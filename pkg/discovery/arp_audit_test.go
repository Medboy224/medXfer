package discovery

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// Audit 9 (#156): `arp -a` is bounded in time, and its answer is kept a moment: status and the
// scans ask for it often, and a stuck arp must not hold them.
func TestARPIsBoundedAndCached(t *testing.T) {
	if hasProcARP() {
		t.Skip("/proc/net/arp answers here: arp -a is not run")
	}
	var runs atomic.Int32
	old := arpCommand
	arpCommand = func(context.Context) ([]byte, error) {
		runs.Add(1)
		time.Sleep(10 * time.Second) // stuck
		return nil, nil
	}
	t.Cleanup(func() { arpCommand = old; resetARPCache() })
	resetARPCache()
	start := time.Now()
	GetARPTableIPs()
	if d := time.Since(start); d > arpTimeout+time.Second {
		t.Fatalf("a stuck arp held the caller %v", d)
	}
	arpCommand = func(context.Context) ([]byte, error) {
		runs.Add(1)
		return []byte("  192.0.2.7   aa-bb-cc-dd-ee-ff   dynamic\n"), nil
	}
	resetARPCache()
	first := GetARPTableIPs()
	second := GetARPTableIPs()
	if len(first) != 1 || first[0] != "192.0.2.7" || len(second) != 1 {
		t.Fatalf("ARP neighbours %q then %q", first, second)
	}
	if n := runs.Load(); n != 2 {
		t.Fatalf("arp ran %d times, want 2: the second call within the cache's life reads the cache", n)
	}
}
