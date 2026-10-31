package discovery

import (
	"context"
	"net"
	"slices"
	"strings"
	"sync"
	"time"
)

// Service is the DNS-SD service type of medXfer (DISC-01).
const Service = "_medxfer._tcp"

// TXT keys of an announcement (DISC-01), and the payload of the fallback beacon (DISC-04):
// the protocol version, the role, which tells a receiver which devices are sending, and the
// recognition tags (DISC-11): random to anyone but the devices of mutual trust, and changed
// every few minutes. None identifies anyone (DISC-06).
const (
	txtVersion = "v=1"
	txtRole    = "r="
	txtTag     = "t="
)

// maxTags bounds the tags read from one announcement, and their length: what comes from the
// network is checked (the tags themselves are pkg/pairing's).
const (
	maxTags   = 16
	maxTagLen = 32
)

// dedupTags drops repeated tags, keeping their order.
func dedupTags(in []string) []string {
	var out []string
	for _, t := range in {
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// cleanTags keeps at most maxTags tags of a sane length and alphabet.
func cleanTags(in []string) []string {
	var out []string
	for _, t := range in {
		if len(out) == maxTags {
			break
		}
		if t == "" || len(t) > maxTagLen || strings.Trim(t, "abcdefghijklmnopqrstuvwxyz234567") != "" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// Announcement is what a device says about itself on the network: a generic name drawn at
// launch, its single port, its role ("sender" while a send waits, "node" otherwise).
type Announcement struct {
	Instance string
	Port     int
	Role     string
	IP       net.IP         // the address to announce
	Iface    *net.Interface // the interface multicast leaves by; nil: the system's default
	// Tags gives the recognition tags to announce now (DISC-11); nil: none. They change with the
	// period and with the trusted devices: the announcement follows.
	Tags func() []string
}

// tagsNow are a's tags at this moment, checked.
func (a Announcement) tagsNow() []string {
	if a.Tags == nil {
		return nil
	}
	return cleanTags(a.Tags())
}

// Found is a device seen on the network, by one of the discoverers.
type Found struct {
	Instance string
	IP       net.IP
	Port     int
	Role     string
	Via      string   // "mdns" or "beacon"
	Tags     []string // recognition tags (DISC-11)
	// Paths are the device's addresses, one per local network it was seen on (a cable and a
	// Wi-Fi: WP-E.1 of the cable plan), cable first; IP is the first one's. Set by browseAll.
	Paths []Path
}

// Discoverer announces this device and finds the others (D5 of the J6a plan, D9 of J5: one
// adapter per mechanism; mDNS, the broadcast beacon, later the platform's own).
type Discoverer interface {
	// Announce announces a until ctx ends.
	Announce(ctx context.Context, a Announcement) error
	// Browse returns the devices seen within timeout.
	Browse(ctx context.Context, timeout time.Duration) ([]Found, error)
}

// browseAll runs every discoverer at once and merges what they see (#132):
//   - this device is left out, by the names it announces and by its addresses;
//   - one entry per device (instance name), with one path per local network it was seen on
//     (merge): two PCs on a cable and on the same Wi-Fi see each other by both, and the
//     address no longer depends on which answer came last (WP-E.0: it changed from one scan
//     to the next, 110 MB/s or 2.4).
func browseAll(ctx context.Context, ds []Discoverer, timeout time.Duration) []Found {
	var mu sync.Mutex
	var wg sync.WaitGroup
	var all []Found
	for _, d := range ds {
		wg.Add(1)
		go func(d Discoverer) {
			defer wg.Done()
			found, _ := d.Browse(ctx, timeout)
			mu.Lock()
			all = append(all, found...)
			mu.Unlock()
		}(d)
	}
	wg.Wait()
	byName := map[string][]Found{}
	var order []string
	known := len(localNets()) > 0
	for _, f := range all {
		if f.IP == nil || f.Port == 0 || f.Instance == "" || isSelf(f) {
			continue
		}
		// An mDNS answer may carry the address of another interface of the device (a Linux
		// bridge answers the Wi-Fi's queries too): outside our subnets, it is left out. A beacon's
		// address is where its packet came from: always kept.
		if known && f.Via == "mdns" && !IsSubnetReachable(f.IP.String()) {
			continue
		}
		if _, ok := byName[f.Instance]; !ok {
			order = append(order, f.Instance)
		}
		byName[f.Instance] = append(byName[f.Instance], f)
	}
	pf := newPathFinder()
	out := make([]Found, 0, len(order))
	for _, name := range order {
		out = append(out, merge(byName[name], pf))
	}
	return out
}

// merge makes one entry of the sightings of a device:
//   - the tags of every sighting: an mDNS answer may lag behind the beacon after the tags
//     changed (a trust just established, DISC-11);
//   - one path per local network the device was seen on, the best sighting of each (better),
//     cable first (sortPaths). An address outside our subnets (a beacon's) is a path only when
//     there is no other.
func merge(seen []Found, pf *pathFinder) Found {
	var tags []string
	best := map[string]Found{} // by local interface; "" outside our subnets
	for _, f := range seen {
		tags = append(tags, f.Tags...)
		key := pf.iface(f.IP)
		if old, ok := best[key]; !ok || better(f, old) {
			best[key] = f
		}
	}
	byIP := map[string]Found{}
	var paths []Path
	for key, f := range best {
		if key == "" && len(best) > 1 {
			continue
		}
		p := Path{IP: f.IP.String(), Kind: pf.kind(key)}
		paths = append(paths, p)
		byIP[p.IP] = f
	}
	sortPaths(paths)
	f := byIP[paths[0].IP]
	if len(seen) > 1 {
		f.Tags = cleanTags(dedupTags(tags))
	}
	f.Paths = paths
	return f
}

// better reports whether a is a better way to reach a device than b: an address in one of our
// subnets first, then mDNS before the beacon, then the lower address (the same choice whatever
// the order the answers came in).
func better(a, b Found) bool {
	ra, rb := IsSubnetReachable(a.IP.String()), IsSubnetReachable(b.IP.String())
	if ra != rb {
		return ra
	}
	if (a.Via == "mdns") != (b.Via == "mdns") {
		return a.Via == "mdns"
	}
	return lessIP(a.IP.String(), b.IP.String())
}

// peerOf is a found device in the shape the callers know.
func peerOf(f Found) Peer {
	p := Peer{ID: f.Instance + "-" + itoa(f.Port), DeviceName: f.Instance, HostIP: f.IP.String(), Port: f.Port,
		Version: AppVersion, Role: f.Role, Tags: f.Tags, Paths: f.Paths}
	if f.Role == "sender" { // a transfer waits; what it is travels only in the secure channel
		p.Offer = &TransferOffer{FileName: PrivateTransfer}
	}
	return p
}

// parseTXT reads the role and the tags from TXT fields; ok is false for another version or
// another app.
func parseTXT(fields []string) (role string, tags []string, ok bool) {
	for _, f := range fields {
		if f == txtVersion {
			ok = true
		}
		if strings.HasPrefix(f, txtRole) {
			role = strings.TrimPrefix(f, txtRole)
		}
		if strings.HasPrefix(f, txtTag) {
			tags = append(tags, strings.TrimPrefix(f, txtTag))
		}
	}
	return role, cleanTags(tags), ok
}

// txtOf is the TXT of an announcement with tags.
func txtOf(role string, tags []string) []string {
	txt := []string{txtVersion, txtRole + role}
	for _, t := range tags {
		txt = append(txt, txtTag+t)
	}
	return txt
}

// instanceOf is the first label of a DNS-SD service instance name.
func instanceOf(name string) string {
	if i := strings.Index(name, "."); i > 0 {
		return name[:i]
	}
	return name
}
