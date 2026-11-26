package discovery

import (
	"context"
	"testing"
	"time"
)

// #313: ifconfig is bounded in time, as arp is: a stuck one does not hold the caller.
func TestIfconfigIsBounded(t *testing.T) {
	old := ifconfigCommand
	ifconfigCommand = func(context.Context, string, ...string) ([]byte, error) {
		time.Sleep(10 * time.Second) // stuck
		return nil, nil
	}
	t.Cleanup(func() { ifconfigCommand = old })
	start := time.Now()
	if got := parseIfconfigTargets(); len(got) != 0 {
		t.Fatalf("targets from a stuck ifconfig: %+v", got)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("a stuck ifconfig held the caller %v", d)
	}
}
