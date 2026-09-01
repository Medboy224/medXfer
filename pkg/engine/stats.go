package engine

import "time"

// TransferStats holds a snapshot of live transfer metrics
type TransferStats struct {
	BytesTransferred int64
	TotalBytes       int64
	SpeedMBps        float64
	ActiveStreams    int
	ProgressPercent  float64
}

// TransferListener defines callbacks for UI/CLI integration
type TransferListener interface {
	OnStart(fileName string, fileSize int64, totalChunks uint32)
	OnProgress(stats TransferStats)
	OnChunkFailed(chunkIndex uint32, retryCount int, err error)
	OnComplete(savePath string, duration time.Duration)
	OnError(err error)
}
