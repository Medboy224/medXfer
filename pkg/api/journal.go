package api

import (
	"encoding/json"
	"io"
	"log"
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

// JournalDir is where this server's diagnostic bundle reads the journal: in its data folder.
func (s *DaemonServer) JournalDir() string { return filepath.Join(s.dataDir, "journal") }

// StartJournal opens the diagnostic journal in dir, sends the log package and the daemon's
// events to it (DEV-11), and returns the function that closes it. A journal that cannot be
// opened only costs the diagnosis: the program starts all the same. The function removes the
// journal only while it is still the one installed: a core that ends late, after another one
// started (internal/cabi), leaves the new one's journal alone.
func StartJournal(dir string) (stop func()) {
	j, err := diag.OpenJournal(dir, diag.JournalBudget, nil)
	if err != nil {
		log.Printf("[diag] journal unavailable: %v", err)
		return func() {}
	}
	log.SetOutput(io.MultiWriter(os.Stderr, j))
	journal.Store(j)
	j.Printf("start medXfer %s", diag.AppVersion)
	return func() {
		if journal.CompareAndSwap(j, nil) {
			log.SetOutput(os.Stderr)
		}
		_ = j.Close()
	}
}

// journalf writes a line to the journal alone, masked like every other.
func journalf(format string, args ...any) {
	if j := journal.Load(); j != nil {
		j.Printf(format, args...)
	}
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
func registerSecrets(pin, webShareToken, pairingCode string) {
	diag.Secrets.Add("PIN", pin)
	diag.Secrets.Add("TOKEN", webShareToken)
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
