package api

import (
	"encoding/json"
	"time"
)

// RequestMessage represents an incoming command from Flutter to the Daemon
type RequestMessage struct {
	ID      string          `json:"id,omitempty"`
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// EventMessage represents an outgoing event from the Daemon to Flutter
type EventMessage struct {
	ID        string      `json:"id,omitempty"`
	Event     string      `json:"event"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp int64       `json:"timestamp"`
}

// NewEvent helper constructor
func NewEvent(event string, data interface{}, id ...string) EventMessage {
	reqID := ""
	if len(id) > 0 {
		reqID = id[0]
	}
	return EventMessage{
		ID:        reqID,
		Event:     event,
		Data:      data,
		Timestamp: time.Now().Unix(),
	}
}

// Config holds runtime configurable settings for the daemon
type Config struct {
	DeviceName      string `json:"device_name"`
	DownloadDir     string `json:"download_dir"`
	CollisionPolicy string `json:"collision_policy"` // "auto_rename", "overwrite", "skip"
	Workers         int    `json:"workers"`
	ChunkSizeMB     int    `json:"chunk_size_mb"`
}

// DaemonStatus represents the current state of the backend
type DaemonStatus struct {
	Status          string `json:"status"` // "idle", "paired", "transferring"
	DeviceName      string `json:"device_name"`
	DownloadDir     string `json:"download_dir"`
	CollisionPolicy string `json:"collision_policy"`
	Paired          bool   `json:"paired"`
	PairedIP        string `json:"paired_ip,omitempty"`
	ActiveTransfer  bool   `json:"active_transfer"`
	Version         string `json:"version"`
}

// PairPayload parameters for "pair" action
type PairPayload struct {
	IP   string `json:"ip"`
	Port int    `json:"port,omitempty"`
}

// SendPayload parameters for "send" action
type SendPayload struct {
	Paths    []string `json:"paths"`
	TargetIP string   `json:"target_ip,omitempty"` // For one-shot send without pairing
}

// RespondOfferPayload parameters for "respond_offer" action
type RespondOfferPayload struct {
	Accept          bool   `json:"accept"`
	SaveDir         string `json:"save_dir,omitempty"`
	CollisionPolicy string `json:"collision_policy,omitempty"`
}

// TransferProgressData live progress telemetry for single files or folder batches
type TransferProgressData struct {
	CurrentFile     string  `json:"current_file"`
	FileIndex       int     `json:"file_index"`
	TotalFiles      int     `json:"total_files"`
	FileBytes       int64   `json:"file_bytes"`
	FileTotalBytes  int64   `json:"file_total_bytes"`
	BatchBytes      int64   `json:"batch_bytes"`
	BatchTotalBytes int64   `json:"batch_total_bytes"`
	SpeedMBps       float64 `json:"speed_mbps"`
	FilePercent     float64 `json:"file_percent"`
	BatchPercent    float64 `json:"batch_percent"`
	EtaSeconds      int     `json:"eta_seconds"`
	IsSmartSkip     bool    `json:"is_smart_skip,omitempty"`
}

// IncomingOfferData payload sent when a remote peer wants to send a file/batch
type IncomingOfferData struct {
	SenderIP   string `json:"sender_ip"`
	DeviceName string `json:"device_name"`
	IsBatch    bool   `json:"is_batch"`
	FileName   string `json:"file_name"`
	FileSize   int64  `json:"file_size"`
	TotalFiles int    `json:"total_files"`
	BatchID    string `json:"batch_id,omitempty"`
}
