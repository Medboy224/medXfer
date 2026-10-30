package discovery

import (
	"os"
	"path/filepath"
	"strings"
)

// sysClassNet is where Linux describes its interfaces; tests point it elsewhere.
var sysClassNet = "/sys/class/net"

// osKind reads the kind of an interface from sysfs: a Wi-Fi card has "wireless" (or
// "phy80211"); a wired card is of type 1 (ARPHRD_ETHER) and has a device behind it, unlike a
// bridge or a virtual link. "" when sysfs does not know the name (Android may hide it).
func osKind(name string) string {
	dir := filepath.Join(sysClassNet, name)
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	for _, w := range []string{"wireless", "phy80211"} {
		if _, err := os.Stat(filepath.Join(dir, w)); err == nil {
			return KindWiFi
		}
	}
	typ, err := os.ReadFile(filepath.Join(dir, "type"))
	if err != nil {
		return ""
	}
	if strings.TrimSpace(string(typ)) == "1" {
		if _, err := os.Stat(filepath.Join(dir, "device")); err == nil {
			return KindEthernet
		}
	}
	return KindOther
}
