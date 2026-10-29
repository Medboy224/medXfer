package discovery

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeServer struct{ shut atomic.Int32 }

func (f *fakeServer) Shutdown() error {
	f.shut.Add(1)
	return nil
}

// The tags change while the interface is gone (the network of the PC stopped): neither server
// comes up, and none is shut down at the next tick. A nil one was, and the app crashed (real
// test of 29 October 2026). The announcement comes back once a server does.
func TestAnnouncingOutlivesAServerThatDoesNotComeUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tick := make(chan time.Time)
	var mu sync.Mutex
	var served []*fakeServer
	var tries atomic.Int32
	// The first six fail: two per tick (the new tags, then the old ones) for three ticks.
	serve := func([]string) (shutdowner, error) {
		if tries.Add(1) <= 6 {
			return nil, errors.New("no such interface")
		}
		s := &fakeServer{}
		mu.Lock()
		served = append(served, s)
		mu.Unlock()
		return s, nil
	}
	first := &fakeServer{}
	done := make(chan struct{})
	go func() {
		keepAnnouncing(ctx, tick, func() []string { return []string{"b"} }, serve, first, []string{"a"})
		close(done)
	}()
	for range 5 {
		// Ticks 1 to 3: the old server goes, then none comes up; 4: back with the new tags;
		// 5: the tags unchanged, nothing to do.
		tick <- time.Now()
	}
	cancel()
	<-done

	if got := first.shut.Load(); got != 1 {
		t.Fatalf("the first server shut down %d times, want 1", got)
	}
	if got := tries.Load(); got != 7 {
		t.Fatalf("%d servers tried, want 7", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(served) != 1 || served[0].shut.Load() != 1 {
		t.Fatalf("want one server back, shut down at the end: %d", len(served))
	}
}
