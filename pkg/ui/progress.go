package ui

import (
	"fmt"
	"strings"
	"time"
)

type ProgressBar struct {
	startTime time.Time
}

func NewProgressBar() *ProgressBar {
	return &ProgressBar{
		startTime: time.Now(),
	}
}

func (p *ProgressBar) Render(current, total int64, speedMBps float64) {
	fmt.Print(p.line(current, total, speedMBps))
}

// line is the bar's line for current of total bytes.
func (p *ProgressBar) line(current, total int64, speedMBps float64) string {
	percent := 0.0
	if total > 0 {
		// Within [0, 100] whatever the counts say (a resume, a size that changed): a
		// negative share would make the bar panic (§3.7, #156).
		percent = min(max(float64(current)/float64(total)*100, 0), 100)
	}

	elapsed := time.Since(p.startTime).Seconds()

	var timeStr string
	if current >= total {
		timeStr = fmt.Sprintf("%.1fs", elapsed)
	} else if speedMBps >= 0.05 { // below, the speed shows as 0.0 (a pause): no time to tell
		remMB := float64(total-current) / (1024 * 1024)
		etaSec := remMB / speedMBps
		timeStr = fmt.Sprintf("%.0fs", etaSec)
	} else {
		timeStr = "--s"
	}

	// Compact 15-block bar specifically designed to fit on Android Termux screens
	barWidth := 15
	completed := int((percent / 100) * float64(barWidth))
	if completed > barWidth {
		completed = barWidth
	}

	bar := strings.Repeat("█", completed) + strings.Repeat("░", barWidth-completed)

	// \r returns cursor to start, \033[K strictly clears the rest of the line
	return fmt.Sprintf("\r\033[K[%s] %5.1f%% | %5.1f MB/s | %s", bar, percent, speedMBps, timeStr)
}

func (p *ProgressBar) Finish() {
	fmt.Println() // Lock in the final render on a new line
}
