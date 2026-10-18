package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"time"
)

// beaconEvery is the pace of the fallback beacon: one every 2 s at most (DISC-04).
const beaconEvery = 2 * time.Second

// beaconMsg is the fallback beacon: the TXT of the mDNS announcement, plus the name and port
// that DNS-SD carries elsewhere (DISC-04). No address: the receiver takes the sender's.
type beaconMsg struct {
	V    int    `json:"v"`
	Role string `json:"r"`
	Name string `json:"n"`
	Port int    `json:"p"`
}

// beaconDiscoverer is the fallback of mDNS (DISC-04, D6 of the J6a plan): a UDP broadcast on
// each local subnet, to port, with no multicast group (a group of 224.0.0.0/24 is assigned by
// the IANA, never taken; 239.255.255.250 is SSDP's).
type beaconDiscoverer struct {
	port  int
	dests func() []*net.UDPAddr // tests aim at the loopback; nil: broadcastDests
}

// broadcastDests are the broadcast address of each active subnet, and the limited broadcast
// (an ad-hoc link without a subnet broadcast).
func broadcastDests(port int) []*net.UDPAddr {
	var out []*net.UDPAddr
	for _, t := range GetActiveNetworkTargets() {
		if t.BroadcastIP != nil {
			out = append(out, &net.UDPAddr{IP: t.BroadcastIP, Port: port})
		}
	}
	return append(out, &net.UDPAddr{IP: net.IPv4bcast, Port: port})
}

func (b beaconDiscoverer) Announce(ctx context.Context, a Announcement) error {
	data, err := json.Marshal(beaconMsg{V: 1, Role: a.Role, Name: a.Instance, Port: a.Port})
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return err
	}
	send := func() {
		dests := b.dests
		if dests == nil {
			dests = func() []*net.UDPAddr { return broadcastDests(b.port) }
		}
		for _, d := range dests() {
			_, _ = conn.WriteToUDP(data, d) // a transient error is tolerated (DISC-10, G2A-09)
		}
	}
	go func() {
		defer conn.Close()
		send()
		t := time.NewTicker(beaconEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				send()
			}
		}
	}()
	return nil
}

// Browse listens to the beacons for timeout. Another listener on the port (this device's own
// browse in another process) makes it return nothing: mDNS still runs beside it.
func (b beaconDiscoverer) Browse(ctx context.Context, timeout time.Duration) ([]Found, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: b.port})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetReadDeadline(deadline)
	go func() {
		<-ctx.Done()
		_ = conn.SetReadDeadline(time.Now())
	}()
	var found []Found
	buf := make([]byte, 2048)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return found, nil
			}
			return found, err
		}
		var m beaconMsg
		if json.Unmarshal(buf[:n], &m) != nil || m.V != 1 || m.Port <= 0 || m.Port > 65535 || m.Name == "" {
			continue // another app, or another version
		}
		found = append(found, Found{Instance: m.Name, IP: src.IP.To4(), Port: m.Port, Role: m.Role, Via: "beacon"})
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
