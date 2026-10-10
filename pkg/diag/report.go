package diag

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is the version of the JSON report format (DEV-05). Bump it on any change
// that an older `xfer report` could misread.
const SchemaVersion = 1

// AppVersion is written into every report. The program sets it at startup.
var AppVersion = "dev"

// ReportOptions controls what identifying data a report may carry. The zero value is the
// default required by DEV-05: no file names, IP addresses, host names or fingerprints.
type ReportOptions struct {
	IncludeNames bool // adds the transfer (file or folder) name
}

// PhaseSample tracks performance in quartiles (e.g. 0-25%, 25-50%, 50-75%, 75-100%)
type PhaseSample struct {
	Phase          string  `json:"phase"`
	AvgSpeedMBps   float64 `json:"avg_speed_mbps"`
	DiskLatencyMs  float64 `json:"avg_disk_latency_ms"`
	NetLatencyMs   float64 `json:"avg_net_latency_ms"`
	DominantFactor string  `json:"dominant_factor"`
}

// Report is the versioned diagnostic report produced at the end of a transfer.
type Report struct {
	SchemaVersion int       `json:"schema_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	AppVersion    string    `json:"app_version"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`

	TransferName      string        `json:"transfer_name,omitempty"` // only with ReportOptions.IncludeNames
	TotalBytes        int64         `json:"total_bytes"`
	TotalFiles        int           `json:"total_files"`
	DurationSec       float64       `json:"duration_sec"`
	AvgSpeedMBps      float64       `json:"avg_speed_mbps"`
	PeakSpeedMBps     float64       `json:"peak_speed_mbps"`
	MinSpeedMBps      float64       `json:"min_speed_mbps"`
	Retries           int           `json:"retries"`
	PrimaryBottleneck string        `json:"primary_bottleneck"` // "STORAGE", "NETWORK", "BALANCED"
	AvgDiskLatencyMs  float64       `json:"avg_disk_latency_ms"`
	AvgNetLatencyMs   float64       `json:"avg_net_latency_ms"`
	PhaseSamples      []PhaseSample `json:"phase_samples"`
	DiagnosisText     string        `json:"diagnosis_text"`
	FormattedReport   string        `json:"formatted_report"` // FormatText of this report, for the dashboard
}

// FormatText renders the report as the clipboard-ready text shown by the dashboard and by
// `xfer report show`.
func FormatText(r *Report) string {
	durMinutes := int(r.DurationSec) / 60
	durRemainderSec := int(r.DurationSec) % 60

	var sb strings.Builder
	sb.WriteString("================ medXfer Diagnostic Summary ================\n")
	if r.TransferName != "" {
		sb.WriteString(fmt.Sprintf("Transfer: %s (%d files, %s)\n", r.TransferName, r.TotalFiles, FormatBytes(r.TotalBytes)))
	} else {
		sb.WriteString(fmt.Sprintf("Transfer: %d files, %s\n", r.TotalFiles, FormatBytes(r.TotalBytes)))
	}
	sb.WriteString(fmt.Sprintf("Duration: %02dm %02ds | Avg Speed: %.1f MB/s | Peak: %.1f MB/s | Min: %.1f MB/s\n",
		durMinutes, durRemainderSec, r.AvgSpeedMBps, r.PeakSpeedMBps, r.MinSpeedMBps))
	sb.WriteString(fmt.Sprintf("Bottleneck: %s (Disk Latency: %.1f ms | Net Latency: %.1f ms) | Retries: %d\n",
		r.PrimaryBottleneck, r.AvgDiskLatencyMs, r.AvgNetLatencyMs, r.Retries))
	sb.WriteString("Quartile Breakdown:\n")
	for _, ps := range r.PhaseSamples {
		sb.WriteString(fmt.Sprintf("  - %s: %5.1f MB/s | Disk: %5.1f ms | Net: %5.1f ms [%s]\n",
			ps.Phase, ps.AvgSpeedMBps, ps.DiskLatencyMs, ps.NetLatencyMs, ps.DominantFactor))
	}
	sb.WriteString(fmt.Sprintf("Analysis: %s\n", r.DiagnosisText))
	sb.WriteString(fmt.Sprintf("medXfer %s · %s/%s · %s\n", r.AppVersion, r.OS, r.Arch, r.GeneratedAt.Local().Format("2006-01-02 15:04:05")))
	sb.WriteString("============================================================")
	return sb.String()
}

func newReportHeader() Report {
	return Report{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		AppVersion:    AppVersion,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
	}
}

// LoadReport reads a report file and checks its schema version.
func LoadReport(path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: not a medXfer report: %w", path, err)
	}
	switch {
	case r.SchemaVersion == 0:
		return nil, fmt.Errorf("%s: not a medXfer report (no schema_version)", path)
	case r.SchemaVersion > SchemaVersion:
		return nil, fmt.Errorf("%s: report schema %d is newer than this program (%d)", path, r.SchemaVersion, SchemaVersion)
	}
	return &r, nil
}

const reportPrefix = "report-"

// SaveReport writes r into dir as report-<UTC time>.json and keeps only the newest keep
// reports. The file is written to a temporary name first, so a reader never sees half a report.
func SaveReport(dir string, r *Report, keep int) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	name := reportPrefix + r.GeneratedAt.UTC().Format("20060102-150405.000") + ".json"
	path := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".report-*.tmp")
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if keep > 0 {
		files, _ := ListReports(dir)
		for len(files) > keep {
			os.Remove(files[0])
			files = files[1:]
		}
	}
	return path, nil
}

// ListReports returns the report files of dir, oldest first.
func ListReports(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), reportPrefix) && strings.HasSuffix(e.Name(), ".json") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files) // the UTC timestamp in the name sorts chronologically
	return files, nil
}
