package diag

import (
	"fmt"
	"math"
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
}

// Compare returns the differences in speed, latency, retries and bottleneck between a and b.
func Compare(a, b *Report) Comparison {
	type metric struct {
		name, unit   string
		a, b         float64
		higherBetter bool
		neutral      bool
	}
	ms := []metric{
		{"duration", "s", a.DurationSec, b.DurationSec, false, false},
		{"avg_speed", "MB/s", a.AvgSpeedMBps, b.AvgSpeedMBps, true, false},
		{"peak_speed", "MB/s", a.PeakSpeedMBps, b.PeakSpeedMBps, true, false},
		{"min_speed", "MB/s", a.MinSpeedMBps, b.MinSpeedMBps, true, false},
		{"retries", "", float64(a.Retries), float64(b.Retries), false, false},
		{"disk_latency", "ms", a.AvgDiskLatencyMs, b.AvgDiskLatencyMs, false, false},
		{"net_latency", "ms", a.AvgNetLatencyMs, b.AvgNetLatencyMs, false, false},
		{"total_bytes", "B", float64(a.TotalBytes), float64(b.TotalBytes), false, true},
		{"total_files", "", float64(a.TotalFiles), float64(b.TotalFiles), false, true},
	}
	c := Comparison{
		BottleneckA:  a.PrimaryBottleneck,
		BottleneckB:  b.PrimaryBottleneck,
		SameTransfer: a.TotalBytes == b.TotalBytes && a.TotalFiles == b.TotalFiles,
	}
	for _, m := range ms {
		d := Delta{Metric: m.name, Unit: m.unit, A: m.a, B: m.b, Diff: m.b - m.a}
		if m.a != 0 {
			d.Pct = math.Round(1000*(m.b-m.a)/m.a) / 10
		}
		if !m.neutral && m.a != m.b {
			if (m.b > m.a) == m.higherBetter {
				d.Better = "b"
			} else {
				d.Better = "a"
			}
		}
		c.Metrics = append(c.Metrics, d)
	}
	return c
}

// FormatComparison renders a comparison as a readable table.
func FormatComparison(c Comparison) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-14s %14s %14s %14s %9s\n", "metric", "A", "B", "B - A", "%"))
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
		sb.WriteString(fmt.Sprintf("%-14s %14s %14s %14s %9s%s\n", d.Metric,
			formatValue(d.A, d.Unit), formatValue(d.B, d.Unit), formatSigned(d.Diff, d.Unit), pct, mark))
	}
	bottleneck := c.BottleneckA
	if c.BottleneckA != c.BottleneckB {
		bottleneck = c.BottleneckA + " -> " + c.BottleneckB
	}
	sb.WriteString(fmt.Sprintf("%-14s %s\n", "bottleneck", bottleneck))
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
