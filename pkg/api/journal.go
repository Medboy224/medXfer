package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/Medboy224/medXfer/pkg/diag"
)

// journal receives the name of every event the daemon emits, and the text of "log" events
// (DEV-11). It is nil until the program installs one with SetJournal.
var journal atomic.Pointer[diag.Journal]

// SetJournal installs the diagnostic journal (nil removes it).
func SetJournal(j *diag.Journal) { journal.Store(j) }

// JournalDir is where the daemon keeps its diagnostic journal, next to config.json.
func JournalDir() string {
	return filepath.Join(filepath.Dir(GetConfigFilePath()), "journal")
}

// journalEvent records evt: its name only, since event data carry file names, the PIN and
// tokens; for "log" events, the masked level and message.
func journalEvent(evt EventMessage) {
	j := journal.Load()
	if j == nil {
		return
	}
	if evt.Event == "log" {
		if m, ok := evt.Data.(map[string]string); ok {
			j.Printf("log %s %s", m["level"], m["message"])
			return
		}
	}
	j.Event(evt.Event)
}

// registerSecrets tells the redactor about this daemon's secrets as soon as they exist, so
// no journal line or bundle can reveal them (bible chapter 13).
func registerSecrets(pin, webShareToken, controlToken, pairingCode string) {
	diag.Secrets.Add("PIN", pin)
	diag.Secrets.Add("TOKEN", webShareToken, controlToken)
	diag.Secrets.Add("CODE", pairingCode)
}

// DiagnosticConfig returns the settings that help a diagnosis and identify no one: no
// device name, no folder.
func DiagnosticConfig() map[string]any {
	out := map[string]any{"config_file_present": false}
	data, err := os.ReadFile(GetConfigFilePath())
	if err != nil {
		return out
	}
	var cfg Config
	if json.Unmarshal(data, &cfg) != nil {
		return out
	}
	out["config_file_present"] = true
	out["workers"] = cfg.Workers
	out["chunk_size_mb"] = cfg.ChunkSizeMB
	out["collision_policy"] = cfg.CollisionPolicy
	out["custom_download_dir"] = cfg.DownloadDir != ""
	return out
}
