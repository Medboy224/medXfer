// Package diag observes transfers and produces diagnostic reports (bible chapter 17).
//
// It subscribes to engine events and is never called by the engine to decide anything
// (DEV-01): the engine, protocol and session packages must not import it.
package diag

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
)

// TransferSessionTracker records continuous telemetry samples during transfers
type TransferSessionTracker struct {
	mu sync.Mutex

	TransferName string
	TotalFiles   int
	TotalBytes   int64
	StartTime    time.Time
	EndTime      time.Time

	// Aggregated metrics
	samplesCount     int
	sumSpeed         float64
	peakSpeed        float64
	minSpeed         float64
	sumDiskLatency   float64
	sumNetLatency    float64
	diskSampleCount  int
	netSampleCount   int
	bottleneckCounts map[string]int
	retries          int

	// 4 Quartile buckets (0: 0-25%, 1: 25-50%, 2: 50-75%, 3: 75-100%)
	phaseSpeedSum       [4]float64
	phaseSpeedCount     [4]int
	phaseDiskLatencySum [4]float64
	phaseDiskCount      [4]int
	phaseNetLatencySum  [4]float64
	phaseNetCount       [4]int
	phaseBottlenecks    [4]map[string]int
}

// NewTransferSessionTracker creates a tracker for a single file or folder batch
func NewTransferSessionTracker(name string, totalFiles int, totalBytes int64) *TransferSessionTracker {
	if totalFiles <= 0 {
		totalFiles = 1
	}
	t := &TransferSessionTracker{
		TransferName:     name,
		TotalFiles:       totalFiles,
		TotalBytes:       totalBytes,
		StartTime:        time.Now(),
		minSpeed:         math.MaxFloat64,
		bottleneckCounts: make(map[string]int),
	}
	for i := 0; i < 4; i++ {
		t.phaseBottlenecks[i] = make(map[string]int)
	}
	return t
}

// RecordSample records a live progress measurement
func (t *TransferSessionTracker) RecordSample(stats engine.TransferStats, progressPercent float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Track speed
	if stats.SpeedMBps > 0 {
		t.samplesCount++
		t.sumSpeed += stats.SpeedMBps
		if stats.SpeedMBps > t.peakSpeed {
			t.peakSpeed = stats.SpeedMBps
		}
		if stats.SpeedMBps < t.minSpeed {
			t.minSpeed = stats.SpeedMBps
		}
	}

	// Track disk & net latency
	if stats.DiskWriteLatencyMs > 0 {
		t.sumDiskLatency += stats.DiskWriteLatencyMs
		t.diskSampleCount++
	}
	if stats.NetReadLatencyMs > 0 {
		t.sumNetLatency += stats.NetReadLatencyMs
		t.netSampleCount++
	}

	// Track bottleneck
	if stats.Bottleneck != "" {
		t.bottleneckCounts[stats.Bottleneck]++
	}

	// Determine quartile (0 to 3)
	q := int(progressPercent / 25.0)
	if q < 0 {
		q = 0
	}
	if q > 3 {
		q = 3
	}

	if stats.SpeedMBps > 0 {
		t.phaseSpeedSum[q] += stats.SpeedMBps
		t.phaseSpeedCount[q]++
	}
	if stats.DiskWriteLatencyMs > 0 {
		t.phaseDiskLatencySum[q] += stats.DiskWriteLatencyMs
		t.phaseDiskCount[q]++
	}
	if stats.NetReadLatencyMs > 0 {
		t.phaseNetLatencySum[q] += stats.NetReadLatencyMs
		t.phaseNetCount[q]++
	}
	if stats.Bottleneck != "" {
		t.phaseBottlenecks[q][stats.Bottleneck]++
	}
}

// RecordRetry counts a chunk that had to be requested again.
func (t *TransferSessionTracker) RecordRetry() {
	t.mu.Lock()
	t.retries++
	t.mu.Unlock()
}

// GenerateReport compiles the full transfer diagnostic summary. With the zero ReportOptions
// it carries no file name (DEV-05).
func (t *TransferSessionTracker) GenerateReport(opts ReportOptions) *Report {
	t.mu.Lock()
	defer t.mu.Unlock()

	endTime := time.Now()
	if !t.EndTime.IsZero() {
		endTime = t.EndTime
	}
	duration := endTime.Sub(t.StartTime)
	durSec := duration.Seconds()
	if durSec < 0.1 {
		durSec = 0.1
	}

	avgSpeed := 0.0
	if t.TotalBytes > 0 && durSec > 0 {
		avgSpeed = (float64(t.TotalBytes) / (1024 * 1024)) / durSec
	} else if t.samplesCount > 0 {
		avgSpeed = t.sumSpeed / float64(t.samplesCount)
	}

	minSpeed := t.minSpeed
	if minSpeed == math.MaxFloat64 {
		minSpeed = 0
	}

	avgDiskLatency := 0.0
	if t.diskSampleCount > 0 {
		avgDiskLatency = t.sumDiskLatency / float64(t.diskSampleCount)
	}

	avgNetLatency := 0.0
	if t.netSampleCount > 0 {
		avgNetLatency = t.sumNetLatency / float64(t.netSampleCount)
	}

	// Find primary bottleneck
	primaryBottleneck := "BALANCED"
	maxCount := 0
	for b, count := range t.bottleneckCounts {
		if count > maxCount {
			maxCount = count
			primaryBottleneck = b
		}
	}

	// Phase samples
	phaseNames := []string{"0-25%", "25-50%", "50-75%", "75-100%"}
	var phaseSamples []PhaseSample

	for i := 0; i < 4; i++ {
		pSpeed := 0.0
		if t.phaseSpeedCount[i] > 0 {
			pSpeed = t.phaseSpeedSum[i] / float64(t.phaseSpeedCount[i])
		} else {
			pSpeed = avgSpeed
		}

		pDisk := 0.0
		if t.phaseDiskCount[i] > 0 {
			pDisk = t.phaseDiskLatencySum[i] / float64(t.phaseDiskCount[i])
		} else {
			pDisk = avgDiskLatency
		}

		pNet := 0.0
		if t.phaseNetCount[i] > 0 {
			pNet = t.phaseNetLatencySum[i] / float64(t.phaseNetCount[i])
		} else {
			pNet = avgNetLatency
		}

		dom := "BALANCED"
		dCount := 0
		for b, count := range t.phaseBottlenecks[i] {
			if count > dCount {
				dCount = count
				dom = b
			}
		}

		phaseSamples = append(phaseSamples, PhaseSample{
			Phase:          phaseNames[i],
			AvgSpeedMBps:   math.Round(pSpeed*10) / 10,
			DiskLatencyMs:  math.Round(pDisk*10) / 10,
			NetLatencyMs:   math.Round(pNet*10) / 10,
			DominantFactor: dom,
		})
	}

	// Formulate diagnostic diagnosis text
	var diagnosisText string
	switch primaryBottleneck {
	case "STORAGE":
		diagnosisText = fmt.Sprintf("⚠️ Goulot d'étranglement Stockage : Latence disque élevée (~%.1f ms). Typique d'un disque dur externe (HDD) ou tampon saturé.", avgDiskLatency)
	case "NETWORK":
		diagnosisText = fmt.Sprintf("⚡ Goulot d'étranglement Réseau : Vitesse limitée par le lien réseau (latence ~%.1f ms). L'écriture disque est rapide et fluide (%.1f ms).", avgNetLatency, avgDiskLatency)
	default:
		diagnosisText = fmt.Sprintf("✓ Transfert optimal et équilibré : Écriture séquentielle continue (latence disque %.1f ms, latence réseau %.1f ms).", avgDiskLatency, avgNetLatency)
	}

	r := newReportHeader()
	if opts.IncludeNames {
		r.TransferName = t.TransferName
	}
	r.TotalBytes = t.TotalBytes
	r.TotalFiles = t.TotalFiles
	r.DurationSec = math.Round(durSec*10) / 10
	r.AvgSpeedMBps = math.Round(avgSpeed*10) / 10
	r.PeakSpeedMBps = math.Round(t.peakSpeed*10) / 10
	r.MinSpeedMBps = math.Round(minSpeed*10) / 10
	r.Retries = t.retries
	r.PrimaryBottleneck = primaryBottleneck
	r.AvgDiskLatencyMs = math.Round(avgDiskLatency*10) / 10
	r.AvgNetLatencyMs = math.Round(avgNetLatency*10) / 10
	r.PhaseSamples = phaseSamples
	r.DiagnosisText = diagnosisText
	r.FormattedReport = FormatText(&r)
	return &r
}

// FormatBytes formats an integer byte count into human-readable string (KB, MB, GB)
func FormatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
