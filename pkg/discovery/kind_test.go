package discovery

import (
	"context"
	"net"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// The kind of a path from the interface's name, when the system says nothing (DISC-13). The
// names of the tests are made up: no real interface of the machine running them has them.
func TestNameKind(t *testing.T) {
	want := map[string]string{
		"wlan0": KindWiFi, "wlo1": KindWiFi, "swlan0": KindWiFi, "Wi-Fi": KindWiFi, "Wi-Fi 2": KindWiFi,
		"eth0": KindEthernet, "lxcbr0": KindOther, "rmnet_data0": KindOther, "": KindOther,
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "ios" {
		want["enp0s25"] = KindEthernet
	}
	for name, kind := range want {
		if got := nameKind(name); got != kind {
			t.Errorf("nameKind(%q) = %q, want %q", name, got, kind)
		}
	}
}

// WP-E.0 of the cable plan: Zorin showed 10.0.3.1, its LXC bridge, down and without cable. The
// bridges of virtual machines and containers are not a network another device is on; a real
// bridge (br0, over the Ethernet card) is.
func TestIsVirtual(t *testing.T) {
	for _, name := range []string{"lxcbr0", "lxdbr0", "br-1a2b3c4d5e6f", "docker0", "virbr0", "veth8f2c1d",
		"cni0", "flannel.1", "podman0", "vboxnet0", "vEthernet (WSL)", "VirtualBox Host-Only Network", "VMnet8"} {
		if !isVirtual(strings.ToLower(name)) {
			t.Errorf("%q not taken for virtual", name)
		}
	}
	for _, name := range []string{"br0", "eth0", "enp0s25", "wlo1", "Ethernet", "Wi-Fi", "swlan0"} {
		if isVirtual(strings.ToLower(name)) {
			t.Errorf("%q taken for virtual", name)
		}
	}
}

// An interface without a link keeps its address (an unplugged cable, a bridge with nothing on
// it): the enumeration leaves it out, as it does loopback and virtual bridges.
func TestUsable(t *testing.T) {
	up := net.FlagUp | net.FlagRunning | net.FlagBroadcast | net.FlagMulticast
	cases := []struct {
		iface net.Interface
		want  bool
	}{
		{net.Interface{Name: "enp0s25", Flags: up}, true},
		{net.Interface{Name: "enp0s25", Flags: net.FlagUp | net.FlagBroadcast}, false}, // no carrier
		{net.Interface{Name: "wlo1", Flags: net.FlagBroadcast}, false},                 // down
		{net.Interface{Name: "lo", Flags: up | net.FlagLoopback}, false},
		{net.Interface{Name: "lxcbr0", Flags: up}, false},
	}
	for _, c := range cases {
		if got := usable(c.iface); got != c.want {
			t.Errorf("usable(%s, %v) = %v, want %v", c.iface.Name, c.iface.Flags, got, c.want)
		}
	}
}

// WP-E.0 of the cable plan: two PCs on a cable and on the same Wi-Fi saw each other by one or
// the other, depending on which answer came last. Now both paths, the cable first, whatever
// the order; the address outside our subnets is not a path while there are others.
func TestBrowseAllKeepsAPathPerNetwork(t *testing.T) {
	t.Cleanup(func() { _ = SetNetworkInfo(nil) })
	if err := SetNetworkInfo([]NetworkInterface{
		{Name: "wltest0", Addrs: []string{"192.0.2.174/24"}},
		{Name: "ethtest0", Addrs: []string{"169.254.185.66/16"}},
	}); err != nil {
		t.Fatal(err)
	}
	wifi, cable := net.IPv4(192, 0, 2, 91), net.IPv4(169, 254, 57, 98)
	viaWiFi := fixedDiscoverer{
		{Instance: "medXfer-1c6e", IP: wifi, Port: 18887, Role: "node", Via: "mdns"},
		{Instance: "medXfer-1c6e", IP: wifi, Port: 18887, Role: "node", Via: "beacon"},
	}
	viaCable := fixedDiscoverer{
		{Instance: "medXfer-1c6e", IP: cable, Port: 18887, Role: "node", Via: "beacon"},
		{Instance: "medXfer-1c6e", IP: net.IPv4(100, 64, 0, 9), Port: 18887, Role: "node", Via: "beacon"},
	}
	want := []Path{{IP: "169.254.57.98", Kind: KindEthernet}, {IP: "192.0.2.91", Kind: KindWiFi}}
	for _, order := range [][]Discoverer{{viaWiFi, viaCable}, {viaCable, viaWiFi}} {
		got := browseAll(context.Background(), order, time.Second)
		if len(got) != 1 {
			t.Fatalf("browsed %+v, want one device", got)
		}
		if !slices.Equal(got[0].Paths, want) || !got[0].IP.Equal(cable) {
			t.Fatalf("paths %+v at %v, want %+v at the cable's address", got[0].Paths, got[0].IP, want)
		}
		if p := peerOf(got[0]); p.HostIP != "169.254.57.98" || !slices.Equal(p.Paths, want) {
			t.Fatalf("peer %+v", p)
		}
	}
}

// The screen of the PIN shows an address per network, cable first, never the cellular modem:
// the primary address alone was the Wi-Fi's, out of reach by the cable (WP-E.0).
func TestLocalPaths(t *testing.T) {
	t.Cleanup(func() { _ = SetNetworkInfo(nil) })
	if err := SetNetworkInfo([]NetworkInterface{
		{Name: "wltest0", Addrs: []string{"192.0.2.174/24"}},
		{Name: "rmnet_data0", Addrs: []string{"203.0.113.40/28"}},
		{Name: "ethtest0", Addrs: []string{"169.254.185.66/16"}},
	}); err != nil {
		t.Fatal(err)
	}
	want := []Path{{IP: "169.254.185.66", Kind: KindEthernet}, {IP: "192.0.2.174", Kind: KindWiFi}}
	if got := LocalPaths(); !slices.Equal(got, want) {
		t.Fatalf("LocalPaths() = %+v, want %+v", got, want)
	}
}
