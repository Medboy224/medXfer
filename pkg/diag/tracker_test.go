package diag

import (
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
)

func TestTransferSessionTracker(t *testing.T) {
	totalBytes := int64(18 * 1024 * 1024 * 1024) // 18 GB
	totalFiles := 28
	tracker := NewTransferSessionTracker("Naruto Season 1", totalFiles, totalBytes)

	// Simulate Q1 (0-25%): Network 22MB/s, Disk latency 5ms, Net latency 6ms (BALANCED)
	for i := 0; i < 5; i++ {
		tracker.RecordSample(engine.TransferStats{
			SpeedMBps:          22.0,
			DiskWriteLatencyMs: 5.0,
			NetReadLatencyMs:   6.0,
			Bottleneck:         "BALANCED",
		}, float64(i*4))
	}

	// Simulate Q2 (25-50%): Network 22MB/s, Disk latency 6ms, Net latency 6ms (BALANCED)
	for i := 0; i < 5; i++ {
		tracker.RecordSample(engine.TransferStats{
			SpeedMBps:          22.5,
			DiskWriteLatencyMs: 6.0,
			NetReadLatencyMs:   6.0,
			Bottleneck:         "BALANCED",
		}, float64(26+i*4))
	}

	// Simulate Q3 (50-75%): Speed drops on external HDD if unoptimized, but here stable
	for i := 0; i < 5; i++ {
		tracker.RecordSample(engine.TransferStats{
			SpeedMBps:          21.5,
			DiskWriteLatencyMs: 7.0,
			NetReadLatencyMs:   6.2,
			Bottleneck:         "BALANCED",
		}, float64(51+i*4))
	}

	// Simulate Q4 (75-100%): Stable through to completion
	for i := 0; i < 5; i++ {
		tracker.RecordSample(engine.TransferStats{
			SpeedMBps:          21.0,
			DiskWriteLatencyMs: 6.8,
			NetReadLatencyMs:   6.1,
			Bottleneck:         "BALANCED",
		}, float64(76+i*4))
	}

	// Fake duration of 14 minutes
	tracker.StartTime = time.Now().Add(-14 * time.Minute)
	tracker.EndTime = time.Now()

	report := tracker.GenerateReport(ReportOptions{IncludeNames: true})
	if report == nil {
		t.Fatal("Expected report, got nil")
	}

	if report.TransferName != "Naruto Season 1" {
		t.Errorf("Unexpected name: %s", report.TransferName)
	}
	if report.TotalFiles != 28 {
		t.Errorf("Expected 28 files, got %d", report.TotalFiles)
	}
	if report.PrimaryBottleneck != "BALANCED" {
		t.Errorf("Expected BALANCED primary bottleneck, got %s", report.PrimaryBottleneck)
	}
	if len(report.PhaseSamples) != 4 {
		t.Fatalf("Expected 4 phase samples, got %d", len(report.PhaseSamples))
	}

	for idx, ps := range report.PhaseSamples {
		t.Logf("Phase %d (%s): Speed=%.1f MB/s, Disk=%.1f ms, Net=%.1f ms, Factor=%s",
			idx, ps.Phase, ps.AvgSpeedMBps, ps.DiskLatencyMs, ps.NetLatencyMs, ps.DominantFactor)
		if ps.AvgSpeedMBps <= 0 {
			t.Errorf("Expected positive speed for phase %s", ps.Phase)
		}
	}

	if !strings.Contains(report.FormattedReport, "medXfer Diagnostic Summary") {
		t.Errorf("Formatted report missing header")
	}
	if !strings.Contains(report.FormattedReport, "Naruto Season 1") {
		t.Errorf("Formatted report missing the transfer name requested by IncludeNames")
	}

	t.Logf("Generated Diagnostic Report:\n%s", report.FormattedReport)
}

// WP-B.8: the report of a transfer that failed says so, with what this side had, and measures
// its speed on that, not on the bytes offered.
func TestFailedTransferReport(t *testing.T) {
	tr := NewTransferSessionTracker("x", 1, 1000<<20)
	tr.StartTime = time.Now().Add(-10 * time.Second)
	tr.RecordSample(engine.TransferStats{BytesTransferred: 100 << 20, SpeedMBps: 10}, 10)
	tr.Finish(OutcomeFailed)
	r := tr.GenerateReport(ReportOptions{})
	if r.Outcome != OutcomeFailed || r.DoneBytes != 100<<20 {
		t.Fatalf("outcome %q, done %d: want failed with 100 MiB", r.Outcome, r.DoneBytes)
	}
	if r.AvgSpeedMBps < 9 || r.AvgSpeedMBps > 11 {
		t.Fatalf("average %.1f MB/s, want about 10 (100 MiB in 10 s)", r.AvgSpeedMBps)
	}
	if text := FormatText(r); !strings.Contains(text, "Outcome: FAILED, 100.00 MB of 1000.00 MB") {
		t.Fatalf("the text does not say it failed:\n%s", text)
	}

	done := NewTransferSessionTracker("y", 1, 1000<<20)
	done.Finish(OutcomeCompleted)
	if r := done.GenerateReport(ReportOptions{}); r.DoneBytes != 0 || strings.Contains(FormatText(r), "Outcome") {
		t.Fatalf("a completed transfer: done %d, text %q", r.DoneBytes, FormatText(r))
	}
}
