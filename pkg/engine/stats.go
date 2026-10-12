package engine

// TransferStats holds a snapshot of live transfer metrics
type TransferStats struct {
	BytesTransferred   int64
	TotalBytes         int64
	SpeedMBps          float64
	ActiveStreams      int
	ProgressPercent    float64
	DiskWriteLatencyMs float64
	NetReadLatencyMs   float64
	Bottleneck         string // "STORAGE", "NETWORK", "BALANCED"
	BottleneckReason   string
}
