package api

import "github.com/Medboy224/medXfer/pkg/diag"

// Transfer telemetry moved to pkg/diag (DEV-01). These aliases keep the api package
// and the dashboard payloads unchanged.
type (
	TransferSessionTracker = diag.TransferSessionTracker
	TransferSummaryReport  = diag.TransferSummaryReport
	TransferPhaseSample    = diag.TransferPhaseSample
)

// NewTransferSessionTracker creates a tracker for a single file or folder batch.
func NewTransferSessionTracker(name string, totalFiles int, totalBytes int64) *TransferSessionTracker {
	return diag.NewTransferSessionTracker(name, totalFiles, totalBytes)
}
