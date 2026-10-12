package diag

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"
)

// StageStats is what a report keeps of one stage of the chunks (DEV-03).
type StageStats struct {
	BusyMs   float64 `json:"busy_ms"`  // time spent in the stage, all connections added up
	Bytes    int64   `json:"bytes"`    // bytes that went through it
	Count    int     `json:"count"`    // measures: one per chunk, one per commit for fsync
	P50Ms    float64 `json:"p50_ms"`   // median duration of one measure
	P95Ms    float64 `json:"p95_ms"`   // 95th percentile
	Parallel int     `json:"parallel"` // how many may run at once (connections, or 1)
}

// Bottleneck is the limiting stage (DEV-04) and how clearly it stands out.
type Bottleneck struct {
	Stage      string `json:"stage"`
	Confidence string `json:"confidence"` // "high", "medium" or "low"
}

// maxSamples bounds the durations kept per stage for the percentiles: past it, a uniform
// sample is kept (reservoir), so a transfer of any size uses bounded memory.
const maxSamples = 1 << 16

// Stages aggregates the stage times a transfer reports, through transfer.StageEvents. It is
// safe for concurrent use and decides nothing for the transfer (DEV-01).
type Stages struct {
	mu  sync.Mutex
	m   map[string]*stageAcc
	rnd *rand.Rand
}

type stageAcc struct {
	busy     time.Duration
	bytes    int64
	count    int
	parallel int
	samples  []time.Duration
}

func NewStages() *Stages {
	return &Stages{m: map[string]*stageAcc{}, rnd: rand.New(rand.NewPCG(1, 2))}
}

// OnStage records one measure of a stage.
func (s *Stages) OnStage(stage string, parallel int, d time.Duration, bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.m[stage]
	if a == nil {
		a = &stageAcc{}
		s.m[stage] = a
	}
	a.busy += d
	a.bytes += bytes
	a.count++
	a.parallel = max(a.parallel, parallel, 1)
	if len(a.samples) < maxSamples {
		a.samples = append(a.samples, d)
	} else if k := s.rnd.IntN(a.count); k < maxSamples {
		a.samples[k] = d
	}
}

// Stats returns each stage measured so far.
func (s *Stages) Stats() map[string]StageStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]StageStats, len(s.m))
	for name, a := range s.m {
		sorted := append([]time.Duration(nil), a.samples...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		out[name] = StageStats{
			BusyMs:   ms(a.busy),
			Bytes:    a.bytes,
			Count:    a.count,
			P50Ms:    ms(percentile(sorted, 50)),
			P95Ms:    ms(percentile(sorted, 95)),
			Parallel: a.parallel,
		}
	}
	return out
}

// Bottleneck is the stage whose time, divided by how many may run at once, is the highest
// (bible DEV-04): the stage that keeps the transfer waiting. Every chunk goes through every
// stage of its side, so the time per byte compares like the time itself. The confidence
// says how far ahead of the next stage it is: x1.5 or more is high, x1.15 medium.
func (s *Stages) Bottleneck() Bottleneck {
	s.mu.Lock()
	defer s.mu.Unlock()
	type load struct {
		stage string
		v     float64
	}
	var loads []load
	for name, a := range s.m {
		if a.count > 0 {
			loads = append(loads, load{name, float64(a.busy) / float64(a.parallel)})
		}
	}
	if len(loads) == 0 {
		return Bottleneck{}
	}
	sort.Slice(loads, func(i, j int) bool {
		if loads[i].v != loads[j].v {
			return loads[i].v > loads[j].v
		}
		return loads[i].stage < loads[j].stage
	})
	b := Bottleneck{Stage: loads[0].stage, Confidence: "high"}
	if len(loads) > 1 && loads[1].v > 0 {
		switch ratio := loads[0].v / loads[1].v; {
		case ratio < 1.15:
			b.Confidence = "low"
		case ratio < 1.5:
			b.Confidence = "medium"
		}
	}
	return b
}

// stageOrder is the order stages are shown in: receiver's, then sender's.
var stageOrder = []string{"net_wait", "hash", "disk_write", "fsync", "disk_read", "net_write"}

// FormatStages renders stages as text lines, in the order of a chunk's way.
func FormatStages(stages map[string]StageStats) string {
	var sb strings.Builder
	seen := map[string]bool{}
	show := func(name string, st StageStats) {
		sb.WriteString(fmt.Sprintf("  - %-10s busy %8.1f ms /%d | p50 %6.1f ms | p95 %6.1f ms | %s\n",
			name, st.BusyMs, st.Parallel, st.P50Ms, st.P95Ms, FormatBytes(st.Bytes)))
	}
	for _, name := range stageOrder {
		if st, ok := stages[name]; ok {
			show(name, st)
			seen[name] = true
		}
	}
	var rest []string
	for name := range stages {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	for _, name := range rest {
		show(name, stages[name])
	}
	return sb.String()
}

// Kind is the family of a stage, as the report's primary_bottleneck names it.
func Kind(stage string) string {
	switch stage {
	case "net_wait", "net_write":
		return "NETWORK"
	case "disk_write", "disk_read", "fsync":
		return "STORAGE"
	case "hash":
		return "CPU"
	}
	return "BALANCED"
}

func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[(len(sorted)-1)*p/100]
}

func ms(d time.Duration) float64 { return math.Round(float64(d)/float64(time.Millisecond)*100) / 100 }
