package diag

import (
	"testing"
	"time"
)

// DEV-04: the limiting stage is the one with the most time per unit of parallelism. A stage
// that runs on 4 connections at once may add up more time than one that runs alone.
func TestBottleneckWeighsParallelism(t *testing.T) {
	s := NewStages()
	for i := 0; i < 100; i++ {
		s.OnStage("net_wait", 4, 30*time.Millisecond, 1<<20) // 3 s, 0.75 s per connection
		s.OnStage("hash", 4, 2*time.Millisecond, 1<<20)
	}
	for i := 0; i < 10; i++ {
		s.OnStage("fsync", 1, 100*time.Millisecond, 0) // 1 s, alone
	}
	if b := s.Bottleneck(); b.Stage != "fsync" || b.Confidence != "medium" {
		t.Fatalf("bottleneck %+v; want fsync, medium (1 s against 0.75 s)", b)
	}
	st := s.Stats()["net_wait"]
	if st.Count != 100 || st.Bytes != 100<<20 || st.BusyMs != 3000 || st.P50Ms != 30 || st.Parallel != 4 {
		t.Fatalf("net_wait stats %+v", st)
	}
}

func TestBottleneckConfidence(t *testing.T) {
	for _, c := range []struct {
		a, b time.Duration
		want string
	}{{300, 100, "high"}, {130, 100, "medium"}, {105, 100, "low"}} {
		s := NewStages()
		s.OnStage("disk_write", 1, c.a, 1)
		s.OnStage("net_wait", 1, c.b, 1)
		if b := s.Bottleneck(); b.Stage != "disk_write" || b.Confidence != c.want {
			t.Errorf("%v against %v: %+v, want %s", c.a, c.b, b, c.want)
		}
	}
	if b := NewStages().Bottleneck(); b.Stage != "" {
		t.Fatalf("no measure gave %+v", b)
	}
}

// Percentiles come from a bounded sample, whatever the number of chunks.
func TestStagesMemoryIsBounded(t *testing.T) {
	s := NewStages()
	for i := 0; i < maxSamples+5000; i++ {
		s.OnStage("hash", 1, time.Duration(i%100)*time.Millisecond, 1)
	}
	if n := len(s.m["hash"].samples); n != maxSamples {
		t.Fatalf("%d samples kept; want %d", n, maxSamples)
	}
	st := s.Stats()["hash"]
	if st.P50Ms < 40 || st.P50Ms > 60 || st.P95Ms < 90 {
		t.Fatalf("percentiles off: %+v", st)
	}
}
