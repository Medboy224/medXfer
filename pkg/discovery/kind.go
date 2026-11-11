package discovery

import (
	"net"
	"runtime"
	"sort"
	"strings"
)

// The kind of link a path goes through (WP-E.1 of the cable plan): the interface asks the user
// which one to use when a device is reachable through several (decision E2).
const (
	KindEthernet = "ethernet"
	KindWiFi     = "wifi"
	KindOther    = "other"
)

// Path is one way to reach a device: its address on one of this device's networks, and the
// kind of the local interface it goes through.
type Path struct {
	IP   string `json:"ip"`
	Kind string `json:"kind"`
}

// kindRank orders paths the same way every time: a cable first (it is the fastest one,
// 110 MB/s against 2 to 50 for Wi-Fi in the probe of 30 October 2026), then Wi-Fi.
func kindRank(kind string) int {
	switch kind {
	case KindEthernet:
		return 0
	case KindWiFi:
		return 1
	}
	return 2
}

// interfaceKind is the kind of the local interface name: from the system when it says (Linux:
// /sys/class/net; Windows: the adapter's type), else from the name.
func interfaceKind(name string) string {
	if k := osKind(name); k != "" {
		return k
	}
	return nameKind(name)
}

// nameKind guesses the kind from the name alone: the platform's list on Android (DISC-10), or a
// system that does not say. On Apple systems "en0" is often the Wi-Fi: no guess there.
func nameKind(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(lower, "wl") || strings.HasPrefix(lower, "swlan") ||
		strings.Contains(lower, "wi-fi") || strings.Contains(lower, "wifi"):
		return KindWiFi
	// A phone's own network: Wi-Fi Direct (p2p-wlan0-0 on the Galaxy A57, real test of 10 November
	// 2026) or an access point (ap0, softap0).
	case strings.HasPrefix(lower, "p2p") || strings.HasPrefix(lower, "softap") ||
		(len(lower) > 2 && strings.HasPrefix(lower, "ap") && lower[2] >= '0' && lower[2] <= '9'):
		return KindWiFi
	case strings.HasPrefix(lower, "eth"):
		return KindEthernet
	// A phone's USB tethering, seen from the phone itself (usb0 on the Galaxy A57, real test of 9
	// November 2026; rndis0 and ncm0 elsewhere): a cable.
	case strings.HasPrefix(lower, "usb") || strings.HasPrefix(lower, "rndis") || strings.HasPrefix(lower, "ncm"):
		return KindEthernet
	case strings.HasPrefix(lower, "en") && runtime.GOOS != "darwin" && runtime.GOOS != "ios":
		return KindEthernet
	}
	return KindOther
}

// pathFinder tells which local interface reaches an address, from one reading of the targets.
type pathFinder struct {
	targets []NetworkTarget
	kinds   map[string]string
}

func newPathFinder() *pathFinder {
	targets, ok := platformTargets()
	if !ok {
		targets = enumerateTargets()
	}
	return &pathFinder{targets: targets, kinds: map[string]string{}}
}

// iface is the name of the local interface whose subnet holds ip, "" when none does.
func (pf *pathFinder) iface(ip net.IP) string {
	ip4 := ip.To4()
	if ip4 == nil {
		return ""
	}
	for _, t := range pf.targets {
		mask := t.Mask
		if len(mask) != 4 {
			mask = net.CIDRMask(24, 32)
		}
		if (&net.IPNet{IP: t.LocalIP.Mask(mask), Mask: mask}).Contains(ip4) {
			return t.InterfaceName
		}
	}
	return ""
}

// kind is the kind of the local interface name, read once per finder.
func (pf *pathFinder) kind(name string) string {
	if name == "" {
		return KindOther
	}
	k, ok := pf.kinds[name]
	if !ok {
		k = interfaceKind(name)
		pf.kinds[name] = k
	}
	return k
}

// LocalPaths are this device's addresses, one per network where another device may be: what
// the screen of the PIN shows (WP-E.1), so that a PC joined by a cable and by Wi-Fi gives the
// address of both. Never the cellular modem. Cable first, then Wi-Fi.
func LocalPaths() []Path {
	pf := newPathFinder()
	var out []Path
	seen := map[string]bool{}
	for _, t := range pf.targets {
		if t.LocalIP == nil || isCellular(strings.ToLower(t.InterfaceName)) || seen[t.LocalIP.String()] {
			continue
		}
		seen[t.LocalIP.String()] = true
		out = append(out, Path{IP: t.LocalIP.String(), Kind: pf.kind(t.InterfaceName)})
	}
	sortPaths(out)
	return out
}

// GuestPaths are this device's addresses a browser guest may reach (Web Share, WEB-01): those of
// LocalPaths, never mobile data, but Wi-Fi first. Real tests of 10 November 2026: the A57
// without Wi-Fi served on its mobile data (10.31.5.248), which a PC reached through another
// phone's; a PC on a phone's Wi-Fi and on another phone's cable served on the cable, the one its
// route to the Internet goes through, where the guest was not. A guest is a phone or a computer
// on Wi-Fi more often than on a cable.
func GuestPaths() []Path { return RankForGuest(LocalPaths()) }

// RankForGuest orders paths for a browser guest: Wi-Fi, then a cable, then the rest; a
// link-local address (a cable without DHCP) last.
func RankForGuest(paths []Path) []Path {
	rank := func(p Path) int {
		switch {
		case strings.HasPrefix(p.IP, "169.254."):
			return 3
		case p.Kind == KindWiFi:
			return 0
		case p.Kind == KindEthernet:
			return 1
		}
		return 2
	}
	sort.SliceStable(paths, func(i, j int) bool { return rank(paths[i]) < rank(paths[j]) })
	return paths
}

// sortPaths orders paths by kind, then by address: the same order whatever the order they
// were seen in.
func sortPaths(paths []Path) {
	sort.SliceStable(paths, func(i, j int) bool {
		if ri, rj := kindRank(paths[i].Kind), kindRank(paths[j].Kind); ri != rj {
			return ri < rj
		}
		return lessIP(paths[i].IP, paths[j].IP)
	})
}

// lessIP compares two IPv4 addresses by their bytes, else as text.
func lessIP(a, b string) bool {
	ia, ib := net.ParseIP(a).To4(), net.ParseIP(b).To4()
	if ia == nil || ib == nil {
		return a < b
	}
	for i := range ia {
		if ia[i] != ib[i] {
			return ia[i] < ib[i]
		}
	}
	return false
}
