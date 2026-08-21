package ui

import (
	"fmt"
	"strings"
	"time"
)

type ProgressBar struct {
	startTime time.Time
	lastBytes int64
	lastTime  time.Time
}

func NewProgressBar() *ProgressBar {
	now := time.Now()
	return &ProgressBar{
		startTime: now,
		lastTime:  now,
	}
}

func (p *ProgressBar) Render(currentBytes, totalBytes int64, speedMBps float64) {
	const barWidth = 30
	percent := float64(currentBytes) / float64(totalBytes)
	if percent > 1.0 {
		percent = 1.0
	}

	filled := int(percent * float64(barWidth))
	empty := barWidth - filled
	if empty < 0 {
		empty = 0
	}

	bar := strings.Repeat("█", filled) + strings.Repeat("░", empty)

	currentMB := float64(currentBytes) / (1024 * 1024)
	totalMB := float64(totalBytes) / (1024 * 1024)

	// Calculate ETA
	etaStr := "--:--"
	if speedMBps > 0.1 && currentBytes < totalBytes {
		remainingBytes := totalBytes - currentBytes
		remainingSecs := float64(remainingBytes) / (speedMBps * 1024 * 1024)
		eta := time.Duration(remainingSecs) * time.Second
		etaStr = fmt.Sprintf("%02d:%02d", int(eta.Minutes()), int(eta.Seconds())%60)
	}

	fmt.Printf("\r\033[K[%s] %5.1f%% | %7.1f/%7.1f MB | %6.1f MB/s | ETA: %s",
		bar, percent*100, currentMB, totalMB, speedMBps, etaStr)
}

func (p *ProgressBar) Finish() {
	elapsed := time.Since(p.startTime).Seconds()
	fmt.Printf("\n[+] Transfer completed in %.2f seconds!\n", elapsed)
}
