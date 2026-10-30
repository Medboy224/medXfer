package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

// sysfs says the kind: a Wi-Fi card has "wireless" or "phy80211"; a wired card is of type 1
// with a device behind it; a bridge of type 1 without a device is not a cable. An interface
// sysfs does not know is left to the name.
func TestOSKindFromSysfs(t *testing.T) {
	root := t.TempDir()
	old := sysClassNet
	sysClassNet = root
	t.Cleanup(func() { sysClassNet = old })
	mk := func(name string, files ...string) {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if f == "wireless" || f == "phy80211" || f == "device" {
				if err := os.MkdirAll(filepath.Join(dir, f), 0o755); err != nil {
					t.Fatal(err)
				}
				continue
			}
			if err := os.WriteFile(filepath.Join(dir, "type"), []byte(f+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("wlo1", "1", "wireless", "device")
	mk("wlp2s0", "1", "phy80211", "device")
	mk("enp0s25", "1", "device")
	mk("lxcbr0", "1")
	mk("tun0", "65534")
	want := map[string]string{"wlo1": KindWiFi, "wlp2s0": KindWiFi, "enp0s25": KindEthernet, "lxcbr0": KindOther,
		"tun0": KindOther, "absent0": ""}
	for name, kind := range want {
		if got := osKind(name); got != kind {
			t.Errorf("osKind(%q) = %q, want %q", name, got, kind)
		}
	}
}
