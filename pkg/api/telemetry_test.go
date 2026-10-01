package api

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

	report := tracker.GenerateReport()
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
	if !strings.Contains(report.FormattedReport, "Ordered Sequential Writer Active") {
		t.Errorf("Formatted report missing pipeline confirmation")
	}

	t.Logf("Generated Diagnostic Report:\n%s", report.FormattedReport)
}
