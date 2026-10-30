package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/identity"
)

// The application manages what the CLI does with `xfer trust` and `xfer bundle` (WP-B.0):
// the trusted devices of this device, and the diagnostic bundle.

// TrustedDevice is a device of the trusted list, as the interface shows it.
type TrustedDevice struct {
	Fingerprint string    `json:"fingerprint"` // whole, for trust_forget and trust_rename
	Short       string    `json:"short"`       // what the user compares
	Alias       string    `json:"alias"`
	LastAddress string    `json:"last_address,omitempty"`
	AddedAt     time.Time `json:"added_at"`
	Renamed     bool      `json:"renamed,omitempty"` // the user chose this name: the device's own no longer replaces it
}

// TrustPayload names a trusted device by its fingerprint, or a prefix of it, as xfer trust.
type TrustPayload struct {
	Fingerprint string `json:"fingerprint"`
	Name        string `json:"name,omitempty"` // trust_rename
}

// ExportBundlePayload is the folder where export_bundle writes the bundle.
type ExportBundlePayload struct {
	Dir string `json:"dir"`
}

// trustedDevices lists the trusted devices, as the trusted_devices event carries them.
func (s *DaemonServer) trustedDevices() []TrustedDevice {
	s.mu.RLock()
	ts := s.trust
	s.mu.RUnlock()
	out := []TrustedDevice{}
	if ts == nil {
		return out
	}
	for _, p := range ts.List() {
		out = append(out, TrustedDevice{Fingerprint: p.Fingerprint.String(), Short: p.Fingerprint.Short(),
			Alias: p.Alias, LastAddress: p.LastAddress, AddedAt: p.AddedAt, Renamed: p.Renamed})
	}
	return out
}

func (s *DaemonServer) handleTrustList(rs Responder, req RequestMessage) {
	rs.Reply(NewEvent("trusted_devices", map[string]any{"devices": s.trustedDevices()}, req.ID))
}

// trustTarget reads a trust_forget or trust_rename payload and finds its device.
func (s *DaemonServer) trustTarget(req RequestMessage) (*identity.TrustStore, identity.Peer, TrustPayload, error) {
	var p TrustPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil || p.Fingerprint == "" {
		return nil, identity.Peer{}, p, errors.New("give the device's fingerprint")
	}
	s.mu.RLock()
	ts := s.trust
	s.mu.RUnlock()
	if ts == nil {
		return nil, identity.Peer{}, p, errors.New("no trusted devices yet")
	}
	peer, err := ts.Find(p.Fingerprint)
	return ts, peer, p, err
}

// handleTrustForget forgets a device (ID-06): trust ends for both, since this device no
// longer declares it, and it is no longer recognized in the scans (DISC-11).
func (s *DaemonServer) handleTrustForget(rs Responder, req RequestMessage) {
	ts, peer, _, err := s.trustTarget(req)
	if err == nil {
		_, err = ts.Forget(peer.Fingerprint)
	}
	if err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
		return
	}
	rs.Reply(NewEvent("trusted_devices", map[string]any{"devices": s.trustedDevices()}, req.ID))
}

// handleTrustRename names a trusted device on this device only; the name it sends no longer
// replaces this one (N6 of the « Noms » plan).
func (s *DaemonServer) handleTrustRename(rs Responder, req RequestMessage) {
	ts, peer, p, err := s.trustTarget(req)
	name := CleanDeviceName(p.Name)
	if err == nil && name == "" {
		err = errors.New("give a name")
	}
	if err == nil {
		peer.Alias, peer.Renamed = name, true
		err = ts.Add(peer)
	}
	if err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
		return
	}
	rs.Reply(NewEvent("trusted_devices", map[string]any{"devices": s.trustedDevices()}, req.ID))
}

// diagnosticConfig is DiagnosticConfig for this server's own configuration: the settings that
// help a diagnosis and identify no one.
func (s *DaemonServer) diagnosticConfig() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]any{
		"workers":             s.config.Workers,
		"chunk_size_mb":       s.config.ChunkSizeMB,
		"collision_policy":    s.config.CollisionPolicy,
		"custom_download_dir": s.config.DownloadDir != "",
	}
}

// handleExportBundle writes the diagnostic bundle (DEV-11, DEV-19-R) in the folder the
// interface chose: this server's reports and journal, masked again on the way out. It never
// replaces a file.
func (s *DaemonServer) handleExportBundle(rs Responder, req RequestMessage) {
	var p ExportBundlePayload
	if err := json.Unmarshal(req.Payload, &p); err != nil || p.Dir == "" {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "give the folder of the bundle"}, req.ID))
		return
	}
	path := filepath.Join(p.Dir, "medxfer-bundle-"+time.Now().Format("20060102-150405")+".zip")
	err := func() error {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		werr := diag.WriteBundle(f, diag.BundleOptions{
			ReportsDir: filepath.Join(s.dataDir, "reports"),
			JournalDir: s.JournalDir(),
			Config:     s.diagnosticConfig(),
		})
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			_ = os.Remove(path)
		}
		return werr
	}()
	if err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": fmt.Sprintf("diagnostic bundle: %v", err)}, req.ID))
		return
	}
	rs.Reply(NewEvent("bundle_exported", map[string]string{"path": path}, req.ID))
}
