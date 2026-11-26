package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
	"time"
)

// #308: an announcement always carries maxTags tags. After a trust is established, a lagging mDNS
// answer still has the old full set while the beacon has the new one, where a real tag took the
// place of a completing one: the merged entry keeps it, in whichever order the sightings come.
func TestMergeKeepsTheNewTagOfFullAnnouncements(t *testing.T) {
	t.Cleanup(func() { _ = SetNetworkInfo(nil) })
	if err := SetNetworkInfo([]NetworkInterface{{Name: "wlan0", Addrs: []string{"192.0.2.1/24"}}}); err != nil {
		t.Fatal(err)
	}
	var old []string
	for i := 0; i < maxTags; i++ {
		old = append(old, fakeTag(0, i))
	}
	const real = "realtagaaaaaa"
	fresh := append(slices.Clone(old[:maxTags-1]), real)
	slices.Sort(fresh)
	remote := net.IPv4(192, 0, 2, 7)
	lagging := fixedDiscoverer{{Instance: "medXfer-taa01", IP: remote, Port: 18888, Role: "node", Via: "mdns", Tags: old}}
	current := fixedDiscoverer{{Instance: "medXfer-taa01", IP: remote, Port: 18888, Role: "node", Via: "beacon", Tags: fresh}}
	for _, order := range [][]Discoverer{{lagging, current}, {current, lagging}} {
		got := browseAll(context.Background(), order, time.Second)
		if len(got) != 1 || !slices.Contains(got[0].Tags, real) {
			t.Fatalf("merged %+v: the new tag is lost", got)
		}
	}
}

// #308: the tags merged from many sightings stay bounded: two full announcements at most.
func TestMergedTagsAreBounded(t *testing.T) {
	t.Cleanup(func() { _ = SetNetworkInfo(nil) })
	if err := SetNetworkInfo([]NetworkInterface{{Name: "wlan0", Addrs: []string{"192.0.2.1/24"}}}); err != nil {
		t.Fatal(err)
	}
	var seen fixedDiscoverer
	for s := 0; s < 10; s++ {
		var tags []string
		for i := 0; i < maxTags; i++ {
			tags = append(tags, fakeTag(1+s, i))
		}
		seen = append(seen, Found{Instance: "medXfer-taa01", IP: net.IPv4(192, 0, 2, byte(10+s)), Port: 18888, Role: "node", Via: "beacon", Tags: tags})
	}
	got := browseAll(context.Background(), []Discoverer{seen}, time.Second)
	if len(got) != 1 || len(got[0].Tags) != maxMergedTags {
		t.Fatalf("merged %d entries, %d tags; want 1 entry, %d tags", len(got), len(got[0].Tags), maxMergedTags)
	}
}

// #308: a full announcement stays small. Its TXT is under the 400 bytes RFC 6763 §6.2 prefers,
// and its beacon fits the buffer it is read into.
func TestFullAnnouncementSize(t *testing.T) {
	var tags []string
	for i := 0; i < maxTags; i++ {
		tags = append(tags, strings.Repeat("a", 13)) // pkg/pairing's tags: 8 bytes in base32
	}
	size := 0
	for _, s := range txtOf("sender", tags) {
		size += 1 + len(s)
	}
	if size > 400 {
		t.Fatalf("TXT of %d bytes", size)
	}
	b, err := json.Marshal(beaconMsg{V: 1, Role: "sender", Name: "medXfer-1a2b", Port: 18888, Tags: tags})
	if err != nil || len(b) > beaconReadSize {
		t.Fatalf("beacon of %d bytes: %v", len(b), err)
	}
}

// fakeTag is the i-th tag of announcement s, of the shape cleanTags keeps (lowercase base32).
func fakeTag(s, i int) string {
	return fmt.Sprintf("%c%c%cxxxxxxxxxx", 'a'+s, 'a'+i/26, 'a'+i%26)
}
