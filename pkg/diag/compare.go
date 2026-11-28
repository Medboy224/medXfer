package diag

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Delta is one metric of a report comparison (DEV-06).
type Delta struct {
	Metric string  `json:"metric"`
	Unit   string  `json:"unit"`
	A      float64 `json:"a"`
	B      float64 `json:"b"`
	Diff   float64 `json:"diff"`             // B - A
	Pct    float64 `json:"pct"`              // 100 * (B - A) / A; 0 when A is 0
	Better string  `json:"better,omitempty"` // "a", "b" or "" (equal, or no preferred direction)
}

// Comparison lists the differences between two reports, A being the reference.
type Comparison struct {
	Metrics      []Delta `json:"metrics"`
	BottleneckA  string  `json:"bottleneck_a"`
	BottleneckB  string  `json:"bottleneck_b"`
	SameTransfer bool    `json:"same_transfer"` // same byte and file counts
	// The limiting stage of each (DEV-04), as "stage (confidence)"; "-" in a report without stages.
	LimitingA string `json:"limiting_a"`
	LimitingB string `json:"limiting_b"`
	// The Wi-Fi of each and another network it was on, when one of them says its Wi-Fi.
	LinkA      string `json:"link_a,omitempty"`
	LinkB      string `json:"link_b,omitempty"`
	OtherWifiA string `json:"other_wifi_a,omitempty"`
	OtherWifiB string `json:"other_wifi_b,omitempty"`
}

// Compare returns the differences in speed, retries, the time of each stage, the limiting
// stage and the Wi-Fi between a and b (DEV-06).
func Compare(a, b *Report) Comparison {
	c := Comparison{
		BottleneckA:  a.PrimaryBottleneck,
		BottleneckB:  b.PrimaryBottleneck,
		SameTransfer: a.TotalBytes == b.TotalBytes && a.TotalFiles == b.TotalFiles,
		Metrics: []Delta{
			delta("duration", "s", a.DurationSec, b.DurationSec, false, false),
			delta("avg_speed", "MB/s", a.AvgSpeedMBps, b.AvgSpeedMBps, true, false),
			delta("peak_speed", "MB/s", a.PeakSpeedMBps, b.PeakSpeedMBps, true, false),
			delta("min_speed", "MB/s", a.MinSpeedMBps, b.MinSpeedMBps, true, false),
			delta("retries", "", float64(a.Retries), float64(b.Retries), false, false),
			delta("total_bytes", "B", float64(a.TotalBytes), float64(b.TotalBytes), false, true),
			delta("total_files", "", float64(a.TotalFiles), float64(b.TotalFiles), false, true),
		},
		LimitingA: limiting(a.Bottleneck),
		LimitingB: limiting(b.Bottleneck),
	}
	// Each stage's time divided by how many may run at once: what DEV-04 compares. A stage one
	// report did not measure (an older one, the other side) makes neither better.
	for _, name := range stageNames(a.Stages, b.Stages) {
		both := hasStage(a.Stages, name) && hasStage(b.Stages, name)
		c.Metrics = append(c.Metrics, delta("stage "+name, "ms", load(a.Stages, name), load(b.Stages, name), false, !both))
	}
	if a.Link != nil || b.Link != nil {
		c.LinkA, c.LinkB = network(a.Link), network(b.Link)
		c.OtherWifiA, c.OtherWifiB = otherWifi(a.Link), otherWifi(b.Link)
	}
	return c
}

// delta is one metric of a and b; neutral has no better side.
func delta(name, unit string, a, b float64, higherBetter, neutral bool) Delta {
	d := Delta{Metric: name, Unit: unit, A: a, B: b, Diff: b - a}
	if a != 0 {
		d.Pct = math.Round(1000*(b-a)/a) / 10
	}
	if !neutral && a != b {
		if (b > a) == higherBetter {
			d.Better = "b"
		} else {
			d.Better = "a"
		}
	}
	return d
}

// stageNames are the stages of either report, in the order of a chunk's way.
func stageNames(a, b map[string]StageStats) []string {
	seen := map[string]bool{}
	var names, rest []string
	for _, name := range stageOrder {
		if _, ok := a[name]; ok || hasStage(b, name) {
			names = append(names, name)
			seen[name] = true
		}
	}
	for _, m := range []map[string]StageStats{a, b} {
		for name := range m {
			if !seen[name] {
				rest = append(rest, name)
				seen[name] = true
			}
		}
	}
	sort.Strings(rest)
	return append(names, rest...)
}

func hasStage(m map[string]StageStats, name string) bool {
	_, ok := m[name]
	return ok
}

// load is a stage's time divided by how many may run at once, in ms; 0 when not measured.
func load(m map[string]StageStats, name string) float64 {
	st, ok := m[name]
	if !ok {
		return 0
	}
	return math.Round(st.BusyMs/float64(max(st.Parallel, 1))*10) / 10
}

func limiting(b *Bottleneck) string {
	if b == nil || b.Stage == "" {
		return "-"
	}
	return fmt.Sprintf("%s (%s)", b.Stage, b.Confidence)
}

func network(l *Link) string {
	if l == nil {
		return "not said"
	}
	return fmt.Sprintf("%s, %s", l.Network, mhz(l.FrequencyMHz))
}

func otherWifi(l *Link) string {
	if l == nil || l.OtherWifiMHz == 0 {
		return "none"
	}
	return mhz(l.OtherWifiMHz)
}

// arrow is a value of A and B: one when they are the same.
func arrow(a, b string) string {
	if a == b {
		return a
	}
	return a + " -> " + b
}

// FormatComparison renders a comparison as a readable table.
func FormatComparison(c Comparison) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-18s %14s %14s %14s %9s\n", "metric", "A", "B", "B - A", "%"))
	for _, d := range c.Metrics {
		mark := ""
		switch d.Better {
		case "a":
			mark = "  (A better)"
		case "b":
			mark = "  (B better)"
		}
		pct := "-"
		if d.A != 0 {
			pct = fmt.Sprintf("%+.1f%%", d.Pct)
		}
		sb.WriteString(fmt.Sprintf("%-18s %14s %14s %14s %9s%s\n", d.Metric,
			formatValue(d.A, d.Unit), formatValue(d.B, d.Unit), formatSigned(d.Diff, d.Unit), pct, mark))
	}
	sb.WriteString(fmt.Sprintf("%-18s %s\n", "bottleneck", arrow(c.BottleneckA, c.BottleneckB)))
	sb.WriteString(fmt.Sprintf("%-18s %s\n", "limiting stage", arrow(c.LimitingA, c.LimitingB)))
	if c.LinkA != "" || c.LinkB != "" {
		sb.WriteString(fmt.Sprintf("%-18s %s\n", "wi-fi", arrow(c.LinkA, c.LinkB)))
		sb.WriteString(fmt.Sprintf("%-18s %s\n", "other wi-fi", arrow(c.OtherWifiA, c.OtherWifiB)))
	}
	if !c.SameTransfer {
		sb.WriteString("note: the two reports do not cover the same amount of data\n")
	}
	return sb.String()
}

func formatValue(v float64, unit string) string {
	switch unit {
	case "B":
		return FormatBytes(int64(v))
	case "":
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f %s", v, unit)
}

func formatSigned(v float64, unit string) string {
	if unit == "B" {
		if v < 0 {
			return "-" + FormatBytes(int64(-v))
		}
		return "+" + FormatBytes(int64(v))
	}
	s := formatValue(v, unit)
	if v >= 0 {
		s = "+" + s
	}
	return s
}
