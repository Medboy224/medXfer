package discovery

import (
	"context"
	"net"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// DISC-04, D6: the fallback beacon carries the TXT (version, role), the name and the port, and
// is read back by Browse; another app's datagrams, or another version's, are ignored.
func TestBeaconAnnounceAndBrowse(t *testing.T) {
	port := freeUDPPort(t)
	loop := []*net.UDPAddr{{IP: net.IPv4(127, 0, 0, 1), Port: port}}
	b := beaconDiscoverer{port: port, dests: func() []*net.UDPAddr { return loop }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan []Found, 1)
	go func() {
		found, _ := b.Browse(ctx, 2*beaconEvery+500*time.Millisecond)
		results <- found
	}()
	time.Sleep(100 * time.Millisecond) // Browse listens
	other, err := net.DialUDP("udp4", nil, loop[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, junk := range []string{`not json`, `{"v":2,"r":"node","n":"x","p":1}`, `{"v":1,"r":"node","n":"","p":1}`, `{"v":1,"n":"x","p":70000}`} {
		_, _ = other.Write([]byte(junk))
	}
	other.Close()
	// The tags are read at each beacon: the second one carries the new ones.
	var calls atomic.Int32
	tags := func() []string {
		if calls.Add(1) == 1 {
			return []string{"aaaaaaaaaaaaa"}
		}
		return []string{"bbbbbbbbbbbbb", "BAD!"}
	}
	if err := b.Announce(ctx, Announcement{Instance: "medXfer-be01", Port: 18888, Role: "sender", Tags: tags}); err != nil {
		t.Fatal(err)
	}
	found := <-results
	if len(found) < 2 { // one at once, then one every beaconEvery
		t.Fatalf("found %+v, want the beacon at least twice", found)
	}
	for _, f := range found {
		if f.Instance != "medXfer-be01" || f.Port != 18888 || f.Role != "sender" || f.Via != "beacon" || !f.IP.Equal(net.IPv4(127, 0, 0, 1)) {
			t.Fatalf("beacon read as %+v", f)
		}
	}
	if !slices.Equal(found[0].Tags, []string{"aaaaaaaaaaaaa"}) || !slices.Equal(found[1].Tags, []string{"bbbbbbbbbbbbb"}) {
		t.Fatalf("tags read: %v then %v", found[0].Tags, found[1].Tags)
	}
}

// DISC-11: the TXT carries the tags as t= fields, read back checked: at most 16, of a sane
// length and alphabet; a found device keeps them.
func TestTagsInTXT(t *testing.T) {
	var many []string
	for i := 0; i < 20; i++ {
		many = append(many, "abcdefghijklm")
	}
	txt := txtOf("node", append([]string{"x y", strings.Repeat("a", 40)}, many...))
	role, tags, ok := parseTXT(txt)
	if !ok || role != "node" || len(tags) != maxTags || tags[0] != "abcdefghijklm" {
		t.Fatalf("parseTXT: %q %v (%d) %v", role, tags, len(tags), ok)
	}
	if p := peerOf(Found{Instance: "n", IP: net.IPv4(192, 0, 2, 7), Port: 1, Role: "node", Tags: tags}); len(p.Tags) != maxTags {
		t.Fatalf("peerOf dropped the tags: %+v", p)
	}
}

type fixedDiscoverer []Found

func (fixedDiscoverer) Announce(context.Context, Announcement) error { return nil }
func (f fixedDiscoverer) Browse(context.Context, time.Duration) ([]Found, error) {
	return f, nil
}

// One entry per device, mDNS first, with the tags of every sighting (an mDNS answer may lag
// behind the beacon after the tags changed); this device and incomplete entries are left out.
func TestBrowseAllMerges(t *testing.T) {
	t.Cleanup(func() { _ = SetNetworkInfo(nil) })
	if err := SetNetworkInfo([]NetworkInterface{{Name: "wlan0", Addrs: []string{"192.0.2.1/24"}}}); err != nil {
		t.Fatal(err)
	}
	remote := net.IPv4(192, 0, 2, 7) // TEST-NET-1: never a local address
	mdnsSeen := fixedDiscoverer{{Instance: "medXfer-aa01", IP: remote, Port: 18888, Role: "node", Via: "mdns", Tags: []string{"aaaaaaaaaaaaa"}}}
	beaconSeen := fixedDiscoverer{
		{Instance: "medXfer-aa01", IP: remote, Port: 18888, Role: "node", Via: "beacon", Tags: []string{"aaaaaaaaaaaaa", "bbbbbbbbbbbbb"}},
		{Instance: "medXfer-bb02", IP: net.IPv4(192, 0, 2, 8), Port: 18888, Role: "sender", Via: "beacon"},
		{Instance: "self", IP: net.IPv4(127, 0, 0, 1), Port: 18888, Via: "beacon"},
		{Instance: "noport", IP: net.IPv4(192, 0, 2, 9), Via: "beacon"},
	}
	for _, order := range [][]Discoverer{{mdnsSeen, beaconSeen}, {beaconSeen, mdnsSeen}} {
		got := browseAll(context.Background(), order, time.Second)
		if len(got) != 2 {
			t.Fatalf("merged %+v, want 2 devices", got)
		}
		for _, f := range got {
			if f.IP.Equal(remote) && (f.Via != "mdns" || !slices.Equal(f.Tags, []string{"aaaaaaaaaaaaa", "bbbbbbbbbbbbb"}) &&
				!slices.Equal(f.Tags, []string{"bbbbbbbbbbbbb", "aaaaaaaaaaaaa"})) {
				t.Fatalf("merged as %+v: want the mDNS entry with both tags, once each", f)
			}
		}
	}
}

// #132, as a phone without net.InterfaceAddrs sees it (the platform gives its interfaces):
//   - itself, by its own name or by one of its addresses (Wi-Fi, its own hotspot), is left out;
//   - a device seen under two addresses appears once, under the one in our subnets, even when
//     only the beacon saw that one.
func TestBrowseAllSelfAndDuplicates(t *testing.T) {
	t.Cleanup(func() { _ = SetNetworkInfo(nil) })
	if err := SetNetworkInfo([]NetworkInterface{
		{Name: "wlan0", Addrs: []string{"192.0.2.50/24"}},
		{Name: "swlan0", Addrs: []string{"198.51.100.1/24"}}, // the phone's own hotspot
		{Name: "rmnet0", Addrs: []string{"203.0.113.40/28"}},
	}); err != nil {
		t.Fatal(err)
	}
	registerOwnName("medXfer-se1f")
	seen := fixedDiscoverer{
		{Instance: "medXfer-se1f", IP: net.IPv4(203, 0, 113, 99), Port: 18887, Via: "mdns"}, // own name
		{Instance: "medXfer-0th3", IP: net.IPv4(192, 0, 2, 50), Port: 18887, Via: "mdns"},   // own Wi-Fi address
		{Instance: "medXfer-0th4", IP: net.IPv4(198, 51, 100, 1), Port: 18887, Via: "beacon"},
		{Instance: "medXfer-pc01", IP: net.IPv4(100, 64, 0, 9), Port: 18887, Via: "mdns"}, // not reachable
		{Instance: "medXfer-pc01", IP: net.IPv4(198, 51, 100, 7), Port: 18887, Via: "beacon"},
		{Instance: "medXfer-pc01", IP: net.IPv4(198, 51, 100, 8), Port: 18887, Via: "beacon"},
	}
	got := browseAll(context.Background(), []Discoverer{seen}, time.Second)
	if len(got) != 1 || got[0].Instance != "medXfer-pc01" || !got[0].IP.Equal(net.IPv4(198, 51, 100, 7)) {
		t.Fatalf("browsed %+v, want medXfer-pc01 once, at 198.51.100.7", got)
	}
	for _, ip := range []string{"192.0.2.50", "198.51.100.1", "203.0.113.40"} {
		if !IsLocalNetworkIP(ip) {
			t.Fatalf("%s not recognized as ours", ip)
		}
	}
	if IsLocalNetworkIP("198.51.100.7") || !IsSubnetReachable("198.51.100.7") || IsSubnetReachable("100.64.0.9") {
		t.Fatal("subnets from the platform's list")
	}
	// mDNS runs on Wi-Fi and the hotspot, never on the cellular modem.
	var names []string
	for _, li := range localInterfaces() {
		names = append(names, li.ip.String())
	}
	if strings.Join(names, " ") != "192.0.2.50 198.51.100.1" {
		t.Fatalf("mDNS interfaces: %v", names)
	}
}

// A Linux device announces on each of its interfaces, and each server answers the queries of
// all of them: on Wi-Fi, the answer of its LXC bridge (10.0.3.1) came too, and the mDNS client
// kept whichever came first (seen with Zorin, 20 October). An mDNS address outside our subnets
// is left out: the device is reached by the beacon's address, or found at the next scan.
func TestBrowseAllDropsAnotherInterfacesAnswer(t *testing.T) {
	t.Cleanup(func() { _ = SetNetworkInfo(nil) })
	if err := SetNetworkInfo([]NetworkInterface{{Name: "wlan0", Addrs: []string{"192.0.2.123/24"}}}); err != nil {
		t.Fatal(err)
	}
	bridge := fixedDiscoverer{{Instance: "medXfer-c81e", IP: net.IPv4(10, 0, 3, 1), Port: 18887, Via: "mdns"}}
	if got := browseAll(context.Background(), []Discoverer{bridge}, time.Second); len(got) != 0 {
		t.Fatalf("browsed %+v: the bridge's address is unreachable from here", got)
	}
	beacon := fixedDiscoverer{{Instance: "medXfer-c81e", IP: net.IPv4(192, 0, 2, 91), Port: 18887, Via: "beacon"}}
	got := browseAll(context.Background(), []Discoverer{bridge, beacon}, time.Second)
	if len(got) != 1 || !got[0].IP.Equal(net.IPv4(192, 0, 2, 91)) {
		t.Fatalf("browsed %+v, want the device at its Wi-Fi address", got)
	}
}

// A found device has the shape the callers know: a sender has an offer (whose content travels
// only in the secure channel), a node has none.
func TestPeerOfAndTXT(t *testing.T) {
	p := peerOf(Found{Instance: "medXfer-cc03", IP: net.IPv4(192, 0, 2, 7), Port: 18888, Role: "sender"})
	if p.DeviceName != "medXfer-cc03" || p.HostIP != "192.0.2.7" || p.Port != 18888 || p.Role != "sender" ||
		p.Offer == nil || p.Offer.FileName != PrivateTransfer || p.ID != "medXfer-cc03-18888" {
		t.Fatalf("peerOf sender: %+v", p)
	}
	if p := peerOf(Found{Instance: "n", IP: net.IPv4(192, 0, 2, 7), Port: 1, Role: "node"}); p.Offer != nil {
		t.Fatalf("a node has an offer: %+v", p)
	}
	if role, tags, ok := parseTXT([]string{"v=1", "r=sender"}); !ok || role != "sender" || tags != nil {
		t.Fatalf("parseTXT: %q %v %v", role, tags, ok)
	}
	if _, _, ok := parseTXT([]string{"v=2", "r=sender"}); ok {
		t.Fatal("another version accepted")
	}
	if got := instanceOf("medXfer-cc03._medxfer._tcp.local."); got != "medXfer-cc03" {
		t.Fatal(got)
	}
}

// mDNS for real, in this process: announce, then browse. Multicast is not reliable on CI
// runners, so this runs on demand, on the real devices (MEDXFER_MDNS_TEST=1, WP-6.7).
func TestMDNSAnnounceAndBrowse(t *testing.T) {
	if os.Getenv("MEDXFER_MDNS_TEST") != "1" {
		t.Skip("set MEDXFER_MDNS_TEST=1 to run mDNS on this machine's network")
	}
	ip := net.ParseIP(GetPrimaryLocalIP())
	name := instanceName()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := (mdnsDiscoverer{}).Announce(ctx, Announcement{Instance: name, Port: 18999, Role: "node", IP: ip, Iface: multicastInterface()}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	found, err := (mdnsDiscoverer{iface: multicastInterface()}).Browse(ctx, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range found {
		names = append(names, f.Instance)
		if f.Instance == name && f.Port == 18999 && f.Role == "node" && f.IP.Equal(ip) {
			return
		}
	}
	t.Fatalf("own announcement %s not found (seen: %s)", name, strings.Join(names, ", "))
}
