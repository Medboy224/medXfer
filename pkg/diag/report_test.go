package diag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
)

func sampleTracker(name string) *TransferSessionTracker {
	tr := NewTransferSessionTracker(name, 3, 300*1024*1024)
	for i := 0; i < 8; i++ {
		tr.RecordSample(engine.TransferStats{
			SpeedMBps: 40, DiskWriteLatencyMs: 2, NetReadLatencyMs: 9, Bottleneck: "NETWORK",
		}, float64(i*12))
	}
	tr.RecordRetry()
	tr.RecordRetry()
	return tr
}

// DEV-05: by default a report names no file, address or host.
func TestReportHasNoIdentifyingDataByDefault(t *testing.T) {
	name := "Contrat-secret-2026.pdf"
	r := sampleTracker(name).GenerateReport(ReportOptions{})
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	for _, forbidden := range []string{name, "Contrat", "127.0.0.1", "transfer_name"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("default report contains %q:\n%s", forbidden, data)
		}
	}
	if host != "" && strings.Contains(string(data), host) {
		t.Errorf("default report contains the host name %q", host)
	}
	if r.SchemaVersion != SchemaVersion || r.OS == "" || r.Arch == "" || r.GeneratedAt.IsZero() {
		t.Errorf("report header incomplete: %+v", r)
	}
	if r.Retries != 2 {
		t.Errorf("Retries = %d; want 2", r.Retries)
	}

	named := sampleTracker(name).GenerateReport(ReportOptions{IncludeNames: true})
	if named.TransferName != name || !strings.Contains(named.FormattedReport, name) {
		t.Errorf("IncludeNames did not add the name: %q", named.TransferName)
	}
}

func TestSaveLoadAndRotateReports(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "reports")
	base := sampleTracker("x").GenerateReport(ReportOptions{})
	var paths []string
	for i := 0; i < 5; i++ {
		r := *base
		r.GeneratedAt = base.GeneratedAt.Add(time.Duration(i) * time.Second)
		p, err := SaveReport(dir, &r, 3)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	files, err := ListReports(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || files[0] != paths[2] || files[2] != paths[4] {
		t.Fatalf("kept %v; want the 3 newest of %v", files, paths)
	}
	got, err := LoadReport(files[2])
	if err != nil {
		t.Fatal(err)
	}
	if got.AvgSpeedMBps != base.AvgSpeedMBps || got.Retries != base.Retries || len(got.PhaseSamples) != 4 {
		t.Fatalf("round trip changed the report: %+v", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".report-*")); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestLoadReportRejectsUnknownSchema(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"none.json":   `{"avg_speed_mbps": 3}`,
		"future.json": `{"schema_version": 99}`,
		"bad.json":    `not json`,
	} {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte(content), 0o644)
		if _, err := LoadReport(p); err == nil {
			t.Errorf("LoadReport(%s) accepted %s", name, content)
		}
	}
}

func TestCompareReports(t *testing.T) {
	a := &Report{DurationSec: 10, AvgSpeedMBps: 50, PeakSpeedMBps: 60, MinSpeedMBps: 20, Retries: 4,
		AvgDiskLatencyMs: 2, AvgNetLatencyMs: 8, TotalBytes: 500 << 20, TotalFiles: 2, PrimaryBottleneck: "NETWORK"}
	b := &Report{DurationSec: 5, AvgSpeedMBps: 100, PeakSpeedMBps: 60, MinSpeedMBps: 30, Retries: 0,
		AvgDiskLatencyMs: 6, AvgNetLatencyMs: 4, TotalBytes: 500 << 20, TotalFiles: 2, PrimaryBottleneck: "STORAGE"}
	c := Compare(a, b)
	want := map[string]struct {
		pct    float64
		better string
	}{
		"duration":     {-50, "b"},
		"avg_speed":    {100, "b"},
		"peak_speed":   {0, ""},
		"min_speed":    {50, "b"},
		"retries":      {-100, "b"},
		"disk_latency": {200, "a"},
		"net_latency":  {-50, "b"},
		"total_bytes":  {0, ""},
	}
	for _, d := range c.Metrics {
		w, ok := want[d.Metric]
		if !ok {
			continue
		}
		if d.Pct != w.pct || d.Better != w.better {
			t.Errorf("%s: pct %v better %q; want %v %q", d.Metric, d.Pct, d.Better, w.pct, w.better)
		}
	}
	if !c.SameTransfer || c.BottleneckA != "NETWORK" || c.BottleneckB != "STORAGE" {
		t.Errorf("comparison header wrong: %+v", c)
	}
	text := FormatComparison(c)
	if !strings.Contains(text, "NETWORK -> STORAGE") || !strings.Contains(text, "+100.0%") {
		t.Errorf("formatted comparison incomplete:\n%s", text)
	}
}
