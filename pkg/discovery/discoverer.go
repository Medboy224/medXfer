package discovery

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

// Service is the DNS-SD service type of medXfer (DISC-01).
const Service = "_medxfer._tcp"

// TXT keys of an announcement (DISC-01), and the payload of the fallback beacon (DISC-04):
// the protocol version, and the role, which tells a receiver which devices are sending. Neither
// identifies anyone (DISC-06).
const (
	txtVersion = "v=1"
	txtRole    = "r="
)

// Announcement is what a device says about itself on the network: a generic name drawn at
// launch, its single port, its role ("sender" while a send waits, "node" otherwise).
type Announcement struct {
	Instance string
	Port     int
	Role     string
	IP       net.IP         // the address to announce
	Iface    *net.Interface // the interface multicast leaves by; nil: the system's default
}

// Found is a device seen on the network, by one of the discoverers.
type Found struct {
	Instance string
	IP       net.IP
	Port     int
	Role     string
	Via      string // "mdns" or "beacon"
}

// Discoverer announces this device and finds the others (D5 of the J6a plan, D9 of J5: one
// adapter per mechanism; mDNS, the broadcast beacon, later the platform's own).
type Discoverer interface {
	// Announce announces a until ctx ends.
	Announce(ctx context.Context, a Announcement) error
	// Browse returns the devices seen within timeout.
	Browse(ctx context.Context, timeout time.Duration, iface *net.Interface) ([]Found, error)
}

// defaultDiscoverers are mDNS first, the broadcast beacon as fallback (DISC-01, DISC-04).
var defaultDiscoverers = []Discoverer{mdnsDiscoverer{}, beaconDiscoverer{port: DiscoveryPort}}

// browseAll runs every discoverer at once and merges what they see, one entry per address and
// port, mDNS first. This device is left out.
func browseAll(ctx context.Context, ds []Discoverer, timeout time.Duration, iface *net.Interface) []Found {
	var mu sync.Mutex
	var wg sync.WaitGroup
	byKey := map[string]Found{}
	for _, d := range ds {
		wg.Add(1)
		go func(d Discoverer) {
			defer wg.Done()
			found, _ := d.Browse(ctx, timeout, iface)
			mu.Lock()
			defer mu.Unlock()
			for _, f := range found {
				if f.IP == nil || f.Port == 0 || IsLocalNetworkIP(f.IP.String()) {
					continue
				}
				key := net.JoinHostPort(f.IP.String(), itoa(f.Port))
				if old, ok := byKey[key]; ok && old.Via == "mdns" {
					continue
				}
				byKey[key] = f
			}
		}(d)
	}
	wg.Wait()
	out := make([]Found, 0, len(byKey))
	for _, f := range byKey {
		out = append(out, f)
	}
	return out
}

// peerOf is a found device in the shape the callers know.
func peerOf(f Found) Peer {
	p := Peer{ID: f.Instance + "-" + itoa(f.Port), DeviceName: f.Instance, HostIP: f.IP.String(), Port: f.Port,
		Version: AppVersion, Role: f.Role}
	if f.Role == "sender" { // a transfer waits; what it is travels only in the secure channel
		p.Offer = &TransferOffer{FileName: PrivateTransfer}
	}
	return p
}

// parseTXT reads the role from TXT fields; ok is false for another version or another app.
func parseTXT(fields []string) (role string, ok bool) {
	for _, f := range fields {
		if f == txtVersion {
			ok = true
		}
		if strings.HasPrefix(f, txtRole) {
			role = strings.TrimPrefix(f, txtRole)
		}
	}
	return role, ok
}

// instanceOf is the first label of a DNS-SD service instance name.
func instanceOf(name string) string {
	if i := strings.Index(name, "."); i > 0 {
		return name[:i]
	}
	return name
}
