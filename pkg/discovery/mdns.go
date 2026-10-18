package discovery

import (
	"context"
	"io"
	"log"
	"net"
	"slices"
	"time"

	"github.com/hashicorp/mdns"
)

// mdnsDiscoverer is DNS-SD over multicast DNS (DISC-01), with github.com/hashicorp/mdns
// (WP-6.0 note: docs/medxfer-j6a-veille-mdns.md). It never enumerates the interfaces, which
// Android refuses; the interface multicast leaves by is given (Announcement.Iface), so that a
// phone with mobile data on does not query the cellular network (§3.2 of the note).
type mdnsDiscoverer struct {
	iface *net.Interface // Browse queries by it; nil: the system's default
}

// quiet drops the library's informative messages (no IPv6 listener, ...).
var quiet = log.New(io.Discard, "", 0)

// tagsCheck is how often an announcement looks whether its tags changed (a new period, a trust
// established or forgotten): the library's zone is fixed, so the server is then replaced.
const tagsCheck = 30 * time.Second

func (mdnsDiscoverer) Announce(ctx context.Context, a Announcement) error {
	serve := func(tags []string) (*mdns.Server, error) {
		svc, err := mdns.NewMDNSService(a.Instance, Service, "", a.Instance+".local.", a.Port, []net.IP{a.IP},
			txtOf(a.Role, tags))
		if err != nil {
			return nil, err
		}
		return mdns.NewServer(&mdns.Config{Zone: svc, Iface: a.Iface, Logger: quiet})
	}
	tags := a.tagsNow()
	srv, err := serve(tags)
	if err != nil {
		return err
	}
	go func() {
		t := time.NewTicker(tagsCheck)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = srv.Shutdown()
				return
			case <-t.C:
				now := a.tagsNow()
				if slices.Equal(now, tags) {
					continue
				}
				_ = srv.Shutdown()
				if next, err := serve(now); err == nil {
					srv, tags = next, now
				} else {
					srv, _ = serve(tags) // keep announcing, with the old tags
				}
			}
		}
	}()
	return nil
}

func (m mdnsDiscoverer) Browse(ctx context.Context, timeout time.Duration) ([]Found, error) {
	entries := make(chan *mdns.ServiceEntry, 64)
	done := make(chan []Found, 1)
	go func() {
		var found []Found
		for e := range entries {
			role, tags, ok := parseTXT(e.InfoFields)
			if !ok || e.AddrV4 == nil {
				continue
			}
			found = append(found, Found{Instance: instanceOf(e.Name), IP: e.AddrV4, Port: e.Port, Role: role, Via: "mdns", Tags: tags})
		}
		done <- found
	}()
	p := mdns.DefaultParams(Service)
	p.Entries, p.Timeout, p.Interface, p.DisableIPv6, p.Logger = entries, timeout, m.iface, true, quiet
	err := mdns.QueryContext(ctx, p)
	close(entries)
	return <-done, err
}
