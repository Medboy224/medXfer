package diag

import (
	"encoding/json"
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
		t.Logf("Phase %d (%s): Speed=%.1f MB/s, Factor=%s", idx, ps.Phase, ps.AvgSpeedMBps, ps.DominantFactor)
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

// After a resume, the average speed is the session's: what crossed the network divided by the
// time, not the whole file (#304). Resumed at half of 100 MiB, 50 MiB in 10 s is 5 MB/s, not 10.
func TestAverageCountsTheSessionOnly(t *testing.T) {
	tr := NewTransferSessionTracker("a.bin", 1, 100<<20)
	tr.StartTime = time.Now().Add(-10 * time.Second)
	tr.RecordSample(engine.TransferStats{BytesTransferred: 100 << 20, TotalBytes: 100 << 20, SpeedMBps: 5, SessionBytes: 50 << 20}, 100)
	tr.Finish(OutcomeCompleted)
	if r := tr.GenerateReport(ReportOptions{}); r.AvgSpeedMBps < 4.9 || r.AvgSpeedMBps > 5.1 {
		t.Fatalf("average %.1f MB/s; want 5, the session's", r.AvgSpeedMBps)
	}
	// Without a resume, the session moves about the whole file: the average is the file's.
	whole := NewTransferSessionTracker("a.bin", 1, 100<<20)
	whole.StartTime = time.Now().Add(-10 * time.Second)
	whole.RecordSample(engine.TransferStats{BytesTransferred: 100 << 20, TotalBytes: 100 << 20, SpeedMBps: 10, SessionBytes: 101 << 20}, 100)
	whole.Finish(OutcomeCompleted)
	if r := whole.GenerateReport(ReportOptions{}); r.AvgSpeedMBps < 9.9 || r.AvgSpeedMBps > 10.1 {
		t.Fatalf("average %.1f MB/s; want 10", r.AvgSpeedMBps)
	}
}

// A report says no latency it did not measure (#304): nothing fills those fields any more, and
// the report showed "Disk Latency: 0.0 ms | Net Latency: 0.0 ms", quartiles at 0 ms, and a
// diagnosis of "latency ~0.0 ms". The time of each stage says it instead (DEV-03).
func TestReportSaysNoLatency(t *testing.T) {
	tr := NewTransferSessionTracker("a.bin", 1, 4<<20)
	tr.OnStage("net_wait", 4, 400*time.Millisecond, 4<<20)
	tr.OnStage("hash", 4, 40*time.Millisecond, 4<<20)
	tr.RecordSample(engine.TransferStats{BytesTransferred: 4 << 20, TotalBytes: 4 << 20, SpeedMBps: 12}, 100)
	tr.Finish(OutcomeCompleted)
	r := tr.GenerateReport(ReportOptions{})
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if text := strings.ToLower(string(b)); strings.Contains(text, "latency") {
		t.Fatalf("the report speaks of a latency nobody measured:\n%s", r.FormattedReport)
	}
	if text := strings.ToLower(FormatComparison(Compare(r, r))); strings.Contains(text, "latency") {
		t.Fatalf("the comparison speaks of a latency:\n%s", text)
	}
}

// Decrypting is the receiver's own work, not the network's (#304): a report limited by it says
// so, and shows the stage next to the network wait.
func TestReportSaysTheDecryption(t *testing.T) {
	tr := NewTransferSessionTracker("a.bin", 1, 4<<20)
	tr.OnStage("net_wait", 4, 40*time.Millisecond, 4<<20)
	tr.OnStage("tls", 4, 400*time.Millisecond, 4<<20)
	tr.OnStage("hash", 4, 20*time.Millisecond, 4<<20)
	r := tr.GenerateReport(ReportOptions{})
	if r.Bottleneck == nil || r.Bottleneck.Stage != "tls" || r.PrimaryBottleneck != "CPU" {
		t.Fatalf("bottleneck %+v, %s; want tls, CPU", r.Bottleneck, r.PrimaryBottleneck)
	}
	if !strings.Contains(r.DiagnosisText, "decrypting") {
		t.Fatalf("diagnosis %q does not say the decryption", r.DiagnosisText)
	}
	if text := FormatStages(r.Stages); strings.Index(text, "tls") < strings.Index(text, "net_wait") || strings.Index(text, "tls") > strings.Index(text, "hash") {
		t.Fatalf("tls not between net_wait and hash:\n%s", text)
	}
}

// The Wi-Fi of the transfer in its report: the network, its frequency, and another Wi-Fi this
// device was on as well, which stays once seen (real tests of 17 November 2026).
func TestReportSaysTheLink(t *testing.T) {
	tr := NewTransferSessionTracker("a.bin", 1, 4096)
	if r := tr.GenerateReport(ReportOptions{}); r.Link != nil || strings.Contains(r.FormattedReport, "Wi-Fi:") {
		t.Fatalf("no link said, yet reported: %+v", r.Link)
	}
	tr.SetLink(&Link{Network: LinkCreated, FrequencyMHz: 5220, OtherWifiMHz: 2412})
	tr.SetLink(&Link{Network: LinkCreated, FrequencyMHz: 5220}) // the other Wi-Fi left
	tr.SetLink(&Link{Network: "mine", FrequencyMHz: 5220})      // not one the app says: ignored
	r := tr.GenerateReport(ReportOptions{})
	if r.Link == nil || *r.Link != (Link{Network: LinkCreated, FrequencyMHz: 5220, OtherWifiMHz: 2412}) {
		t.Fatalf("link %+v; want the network at 5220 MHz, another Wi-Fi at 2412 kept", r.Link)
	}
	if !strings.Contains(r.FormattedReport, "Wi-Fi: network created, 5220 MHz | also on another Wi-Fi, 2412 MHz") {
		t.Fatalf("text:\n%s", r.FormattedReport)
	}
	if !strings.Contains(r.DiagnosisText, "another Wi-Fi") {
		t.Fatalf("diagnosis %q does not say the other Wi-Fi", r.DiagnosisText)
	}

	alone := NewTransferSessionTracker("a.bin", 1, 4096)
	alone.SetLink(&Link{Network: LinkJoined, FrequencyMHz: 5805})
	r = alone.GenerateReport(ReportOptions{})
	if !strings.Contains(r.FormattedReport, "Wi-Fi: network joined, 5805 MHz\n") || strings.Contains(r.DiagnosisText, "another Wi-Fi") {
		t.Fatalf("alone on its network:\n%s", r.FormattedReport)
	}
}
