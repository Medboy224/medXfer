package api

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/testkit"
)

// WP-B.0: the application lists, renames and forgets the trusted devices, as xfer trust does.
func TestManageTrustedDevices(t *testing.T) {
	s := NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Solo")
	n := testkit.StartNode(t, s)
	other, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TrustDevice(other.Fingerprint.String(), "phone"); err != nil {
		t.Fatal(err)
	}
	devices := func(e testkit.Event) []TrustedDevice {
		t.Helper()
		if e.Event != "trusted_devices" {
			t.Fatalf("event %s %s, want trusted_devices", e.Event, e.Data)
		}
		var d struct{ Devices []TrustedDevice }
		_ = json.Unmarshal(e.Data, &d)
		return d.Devices
	}
	list := devices(n.WaitReply(n.Request("trust_list", nil), 0))
	if len(list) != 1 || list[0].Alias != "phone" || list[0].Fingerprint != other.Fingerprint.String() {
		t.Fatalf("trusted devices %+v", list)
	}
	list = devices(n.WaitReply(n.Request("trust_rename", map[string]any{"fingerprint": list[0].Short, "name": "  Naruto's phone "}), 0))
	if list[0].Alias != "Naruto's phone" || !list[0].Renamed {
		t.Fatalf("after rename: %+v", list)
	}
	if e := n.WaitReply(n.Request("trust_rename", map[string]any{"fingerprint": list[0].Short, "name": " "}), 0); e.Event != "action_error" {
		t.Fatalf("an empty name: %s", e.Event)
	}
	list = devices(n.WaitReply(n.Request("trust_forget", map[string]any{"fingerprint": list[0].Fingerprint}), 0))
	if len(list) != 0 {
		t.Fatalf("after forget: %+v", list)
	}
}

// WP-B.0 (DEV-19-R): the bundle is written in the folder given, never over a file.
func TestExportBundle(t *testing.T) {
	s := NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Solo")
	n := testkit.StartNode(t, s)
	dir := t.TempDir()
	e := n.WaitReply(n.Request("export_bundle", map[string]any{"dir": dir}), 0)
	var d struct{ Path string }
	_ = json.Unmarshal(e.Data, &d)
	if e.Event != "bundle_exported" || d.Path == "" {
		t.Fatalf("export_bundle: %s %s", e.Event, e.Data)
	}
	if fi, err := os.Stat(d.Path); err != nil || fi.Size() == 0 {
		t.Fatalf("bundle %s: %v", d.Path, err)
	}
	if e := n.WaitReply(n.Request("export_bundle", map[string]any{"dir": ""}), 0); e.Event != "action_error" {
		t.Fatalf("no folder: %s", e.Event)
	}
}
