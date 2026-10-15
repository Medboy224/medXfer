package discovery

import (
	"context"
	"io"
	"log"
	"net"
	"time"

	"github.com/hashicorp/mdns"
)

// mdnsDiscoverer is DNS-SD over multicast DNS (DISC-01), with github.com/hashicorp/mdns
// (WP-6.0 note: docs/medxfer-j6a-veille-mdns.md). It never enumerates the interfaces, which
// Android refuses; the interface multicast leaves by is given (Announcement.Iface), so that a
// phone with mobile data on does not query the cellular network (§3.2 of the note).
type mdnsDiscoverer struct{}

// quiet drops the library's informative messages (no IPv6 listener, ...).
var quiet = log.New(io.Discard, "", 0)

func (mdnsDiscoverer) Announce(ctx context.Context, a Announcement) error {
	svc, err := mdns.NewMDNSService(a.Instance, Service, "", a.Instance+".local.", a.Port, []net.IP{a.IP},
		[]string{txtVersion, txtRole + a.Role})
	if err != nil {
		return err
	}
	srv, err := mdns.NewServer(&mdns.Config{Zone: svc, Iface: a.Iface, Logger: quiet})
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown()
	}()
	return nil
}

func (mdnsDiscoverer) Browse(ctx context.Context, timeout time.Duration, iface *net.Interface) ([]Found, error) {
	entries := make(chan *mdns.ServiceEntry, 64)
	done := make(chan []Found, 1)
	go func() {
		var found []Found
		for e := range entries {
			role, ok := parseTXT(e.InfoFields)
			if !ok || e.AddrV4 == nil {
				continue
			}
			found = append(found, Found{Instance: instanceOf(e.Name), IP: e.AddrV4, Port: e.Port, Role: role, Via: "mdns"})
		}
		done <- found
	}()
	p := mdns.DefaultParams(Service)
	p.Entries, p.Timeout, p.Interface, p.DisableIPv6, p.Logger = entries, timeout, iface, true, quiet
	err := mdns.QueryContext(ctx, p)
	close(entries)
	return <-done, err
}
