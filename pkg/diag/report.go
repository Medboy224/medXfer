package diag

// TransferPhaseSample tracks performance in quartiles (e.g. 0-25%, 25-50%, 50-75%, 75-100%)
type TransferPhaseSample struct {
	Phase          string  `json:"phase"`
	AvgSpeedMBps   float64 `json:"avg_speed_mbps"`
	DiskLatencyMs  float64 `json:"avg_disk_latency_ms"`
	NetLatencyMs   float64 `json:"avg_net_latency_ms"`
	DominantFactor string  `json:"dominant_factor"`
}

// TransferSummaryReport aggregates diagnostic data at the end of a transfer
type TransferSummaryReport struct {
	TransferName      string                `json:"transfer_name"`
	TotalBytes        int64                 `json:"total_bytes"`
	TotalFiles        int                   `json:"total_files"`
	DurationSec       float64               `json:"duration_sec"`
	AvgSpeedMBps      float64               `json:"avg_speed_mbps"`
	PeakSpeedMBps     float64               `json:"peak_speed_mbps"`
	MinSpeedMBps      float64               `json:"min_speed_mbps"`
	PrimaryBottleneck string                `json:"primary_bottleneck"` // "STORAGE", "NETWORK", "BALANCED"
	AvgDiskLatencyMs  float64               `json:"avg_disk_latency_ms"`
	AvgNetLatencyMs   float64               `json:"avg_net_latency_ms"`
	PhaseSamples      []TransferPhaseSample `json:"phase_samples"`
	DiagnosisText     string                `json:"diagnosis_text"`
	FormattedReport   string                `json:"formatted_report"`
}
