package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	msg := EventMessage{
		Event:     event,
		Data:      data,
		Timestamp: time.Now().UnixMilli(),
	}
	if len(id) > 0 {
		msg.ID = id[0]
	}
	return msg
}

// Config represents the persisted daemon configuration
type Config struct {
	DeviceName      string `json:"device_name"`
	DownloadDir     string `json:"download_dir"`
	CollisionPolicy string `json:"collision_policy"` // "auto_rename", "overwrite", "skip"
	Workers         int    `json:"workers"`
	ChunkSizeMB     int    `json:"chunk_size_mb"`
}

var (
	configMu        sync.RWMutex
	customConfigDir string
)

// SetCustomConfigDir allows tests or custom CLI flags to isolate config storage
func SetCustomConfigDir(dir string) {
	configMu.Lock()
	defer configMu.Unlock()
	customConfigDir = dir
}

// GetConfigFilePath returns the absolute path to config.json
func GetConfigFilePath() string {
	configMu.RLock()
	cDir := customConfigDir
	configMu.RUnlock()

	if cDir != "" {
		return filepath.Join(cDir, "config.json")
	}
	if envDir := os.Getenv("MEDXFER_CONFIG_DIR"); envDir != "" {
		return filepath.Join(envDir, "config.json")
	}
	if cfgDir, err := os.UserConfigDir(); err == nil && cfgDir != "" {
		return filepath.Join(cfgDir, "medxfer", "config.json")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".medxfer", "config.json")
	}
	return "config.json"
}

// LoadConfig loads the saved settings from disk or returns defaults
func LoadConfig(explicitOutDir, explicitDeviceName string) Config {
	cfg := Config{
		CollisionPolicy: "auto_rename",
		Workers:         4,
		ChunkSizeMB:     2,
	}

	// 1. Read saved config from disk if available
	cfgPath := GetConfigFilePath()
	if data, err := os.ReadFile(cfgPath); err == nil {
		var saved Config
		if err := json.Unmarshal(data, &saved); err == nil {
			if saved.DeviceName != "" {
				cfg.DeviceName = saved.DeviceName
			}
			if saved.DownloadDir != "" {
				cfg.DownloadDir = saved.DownloadDir
			}
			if saved.CollisionPolicy != "" {
				cfg.CollisionPolicy = saved.CollisionPolicy
			}
			if saved.Workers > 0 {
				cfg.Workers = saved.Workers
			}
			if saved.ChunkSizeMB > 0 {
				cfg.ChunkSizeMB = saved.ChunkSizeMB
			}
		}
	}

	// 2. Explicit parameters override saved configuration
	if explicitDeviceName != "" {
		cfg.DeviceName = explicitDeviceName
	}
	if explicitOutDir != "" && explicitOutDir != "." {
		cfg.DownloadDir = explicitOutDir
	}

	// 3. Fallback defaults if still empty or invalid on host OS
	if cfg.DeviceName == "" {
		if h, _ := os.Hostname(); h != "" {
			cfg.DeviceName = h
		} else {
			cfg.DeviceName = "medXfer-Node"
		}
	}

	needFallbackDir := false
	if cfg.DownloadDir == "" || cfg.DownloadDir == "." {
		needFallbackDir = true
	} else if runtime.GOOS == "windows" && strings.HasPrefix(cfg.DownloadDir, "/tmp") {
		needFallbackDir = true
	} else if _, err := os.Stat(cfg.DownloadDir); err != nil {
		if err := os.MkdirAll(cfg.DownloadDir, 0755); err != nil {
			needFallbackDir = true
		}
	}

	if needFallbackDir {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			dl := filepath.Join(home, "Downloads")
			if fi, err := os.Stat(dl); err == nil && fi.IsDir() {
				cfg.DownloadDir = dl
			} else {
				cfg.DownloadDir = home
			}
		} else {
			cfg.DownloadDir = "."
		}
	}

	return cfg
}

// SaveConfig writes the configuration to disk
func SaveConfig(cfg Config) error {
	cfgPath := GetConfigFilePath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(cfgPath, data, 0644)
}

// DaemonStatus represents the current state of the backend
type DaemonStatus struct {
	Status          string `json:"status"` // "idle", "paired", "transferring"
	DeviceName      string `json:"device_name"`
	DownloadDir     string `json:"download_dir"`
	CollisionPolicy string `json:"collision_policy"`
	Paired          bool   `json:"paired"`
	PairedIP        string `json:"paired_ip,omitempty"`
	PairedDevice    string `json:"paired_device,omitempty"`
	ActiveTransfer  bool   `json:"active_transfer"`
	IsPaused        bool   `json:"is_paused"`
	Version         string `json:"version"`
	LocalIP         string `json:"local_ip,omitempty"`
	LocalPort       int    `json:"local_port,omitempty"`
	PortalURL       string `json:"portal_url,omitempty"`
	PortalQR        string `json:"portal_qr,omitempty"`
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

// SkipFilePayload parameters for "skip_file" action
type SkipFilePayload struct {
	ItemIndex int `json:"item_index"`
}

// PauseFilePayload parameters for "pause_file" action
type PauseFilePayload struct {
	ItemIndex int `json:"item_index"`
}

// ResumeFilePayload parameters for "resume_file" action
type ResumeFilePayload struct {
	ItemIndex int `json:"item_index"`
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
	IsPaused        bool    `json:"is_paused"`
}

// IncomingOfferData payload sent when a remote peer wants to send a file/batch
type IncomingOfferData struct {
	SenderIP   string          `json:"sender_ip"`
	DeviceName string          `json:"device_name"`
	IsBatch    bool            `json:"is_batch"`
	FileName   string          `json:"file_name"`
	FileSize   int64           `json:"file_size"`
	TotalFiles int             `json:"total_files"`
	BatchID    string          `json:"batch_id,omitempty"`
	Items      []BatchFileInfo `json:"items,omitempty"`
}

// BatchFileInfo summarizes each file in a batch for the queue UI
type BatchFileInfo struct {
	Index   int    `json:"index"`
	RelPath string `json:"rel_path"`
	Size    int64  `json:"size"`
	Status  string `json:"status"` // "pending", "transferring", "completed", "skipped"
}
