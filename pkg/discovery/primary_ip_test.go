package discovery

import (
	"net"
	"testing"
)

func target(name, ip string) NetworkTarget {
	return NetworkTarget{InterfaceName: name, LocalIP: net.ParseIP(ip).To4()}
}

// #19 and #109: the default route's address, unless it is a cellular modem's while a local
// interface is up.
func TestChoosePrimaryIP(t *testing.T) {
	phone := []NetworkTarget{target("swlan0", "10.86.69.197"), target("rmnet0", "10.30.122.151")}
	for _, c := range []struct {
		name, defaultIP string
		targets         []NetworkTarget
		want            string
	}{
		{"phone, mobile data on (the 13 October run)", "10.30.122.151", phone, "10.86.69.197"},
		{"phone, Wi-Fi carries the default route", "10.86.69.197", phone, "10.86.69.197"},
		{"phone, mobile data only", "10.30.122.151", []NetworkTarget{target("rmnet0", "10.30.122.151")}, "10.30.122.151"},
		{"phone sharing its hotspot", "100.64.3.9", []NetworkTarget{target("ap0", "192.168.43.1"), target("ccmni1", "100.64.3.9")}, "192.168.43.1"},
		// #19: an interface without link keeps its address and must not win over the route.
		{"Windows, Ethernet unplugged", "192.168.1.20", []NetworkTarget{target("Ethernet", "192.168.137.1"), target("Wi-Fi", "192.168.1.20")}, "192.168.1.20"},
		{"default route not among the targets", "10.1.2.3", phone, "10.1.2.3"},
		{"no default route", "", phone, "10.86.69.197"},
		{"nothing", "", nil, ""},
	} {
		if got := choosePrimaryIP(c.defaultIP, c.targets); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
