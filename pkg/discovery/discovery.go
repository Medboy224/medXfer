package discovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/manifest"
)

const (
	DiscoveryPort = 19998
	ProtocolMagic = "MEDXFER_NODE_V3"
	AppVersion    = "1.0.0"
)

// TransferOffer represents an active file payload offered by a peer. Only its public part
// (publicOffer) ever leaves the device through discovery: what is sent travels later, inside
// the secure channel, with pair_accept (WP-3.5b).
type TransferOffer struct {
	FileName string             `json:"file_name"`
	FileSize int64              `json:"file_size"`
	FileID   string             `json:"file_id,omitempty"` // Protects legacy mode integrity
	IsBatch  bool               `json:"is_batch,omitempty"`
	Batch    *manifest.Manifest `json:"batch,omitempty"`
}

// PrivateTransfer is the file name every offer shows in discovery.
const PrivateTransfer = "[🔒 Private Transfer]"

// publicOffer is the part of an offer that discovery may carry (DISC-06): no file name, size
// or list, and no key; only that a transfer is waiting. The receiver pairs on the device's
// single port, Peer.Port. Discovery is unauthenticated and in clear (bible §3.1, rule R2),
// whatever the pairing code.
func publicOffer(o *TransferOffer) *TransferOffer {
	return &TransferOffer{FileName: PrivateTransfer, IsBatch: o.IsBatch}
}

// DiscoveryQuery represents an authenticated request sent to a peer's discovery port
type DiscoveryQuery struct {
	PairingCode string `json:"pairing_code,omitempty"`
}

// Peer represents an identified medXfer instance on the network
type Peer struct {
	ID          string         `json:"id"`
	DeviceName  string         `json:"device_name"`
	HostIP      string         `json:"host_ip"`
	Port        int            `json:"port"`
	Version     string         `json:"version"`
	Role        string         `json:"role"` // "sender", "receiver", "idle", "node"
	PairingCode string         `json:"pairing_code,omitempty"`
	Offer       *TransferOffer `json:"offer,omitempty"`
	// Tags are the device's recognition tags (DISC-11): a device of mutual trust recognizes it
	// by them, and shows the name it recorded. They name no one.
	Tags []string `json:"tags,omitempty"`
}

// IsLocalNetworkIP checks if an IP belongs to the local machine
func IsLocalNetworkIP(ipStr string) bool {
	if ipStr == "" || ipStr == "127.0.0.1" || ipStr == "localhost" || ipStr == "::1" || ipStr == "0.0.0.0" {
		return true
	}
	ip := net.ParseIP(ipStr)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	for _, n := range localNets() { // the system's list, or the targets where it is refused (#132)
		if n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

func isLocalNetworkIP(ipStr string) bool {
	return IsLocalNetworkIP(ipStr)
}

// DiscoveryServer handles incoming discovery queries and emits periodic beacons
type DiscoveryServer struct {
	mu   sync.RWMutex
	peer Peer
	tags func() []string // the recognition tags to announce (DISC-11)
}

// instanceName is the name a device announces: "medXfer-" and 4 hex digits drawn at each
// launch. DISC-06: neither the host name nor the alias goes out in clear; the alias is sent
// only once the peer is authenticated (pair_accept).
func instanceName() string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return "medXfer-" + hex.EncodeToString(b[:])
}

// NewDiscoveryServer initializes a node's discovery responder. It announces a generic instance
// name, never the device's name (DISC-06, #65).
func NewDiscoveryServer(role string, tcpPort int, offer *TransferOffer) *DiscoveryServer {
	name := instanceName()
	registerOwnName(name) // never list ourselves (#132)
	code, _, _ := GeneratePairingCode("")

	return &DiscoveryServer{
		peer: Peer{
			ID:          name + fmt.Sprintf("-%d", tcpPort),
			DeviceName:  name,
			Port:        tcpPort,
			Version:     AppVersion,
			Role:        role,
			PairingCode: code,
			Offer:       offer,
		},
	}
}

// SetPairingCode updates the advertised 6-digit pairing code in real-time
func (s *DiscoveryServer) SetPairingCode(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peer.PairingCode = code
}

// GetPairingCode returns the current pairing code
func (s *DiscoveryServer) GetPairingCode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.peer.PairingCode
}

// SetOffer updates the active transfer offer in real-time
func (s *DiscoveryServer) SetOffer(offer *TransferOffer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peer.Offer = offer
}

// SetTags gives the recognition tags this device announces (DISC-11), read at each
// announcement: nil announces none.
func (s *DiscoveryServer) SetTags(f func() []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tags = f
}

// tagsNow are the tags to announce at this moment.
func (s *DiscoveryServer) tagsNow() []string {
	s.mu.RLock()
	f := s.tags
	s.mu.RUnlock()
	if f == nil {
		return nil
	}
	return cleanTags(f())
}

// GetPeer returns a thread-safe snapshot of the peer metadata
func (s *DiscoveryServer) GetPeer() Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.peer
}

// answer is the reply to a discovery query: the public offer only. The pairing code, when it
// matches, is echoed so that the querier knows it found the right device; it unlocks nothing
// (WP-3.6b): trust comes from the key, checked in the secure channel.
func (s *DiscoveryServer) answer(q DiscoveryQuery, ip string) Peer {
	p := s.GetPeer()
	p.HostIP = ip
	p.Tags = s.tagsNow()
	expected, _ := NormalizePairingCode(p.PairingCode)
	provided, _ := NormalizePairingCode(q.PairingCode)
	if expected == "" || expected != provided {
		p.PairingCode = ""
	}
	if p.Offer != nil {
		p.Offer = publicOffer(p.Offer)
	}
	return p
}

// beacon is what the UDP broadcast announces from ip: the public offer, and never the pairing
// code, which only a query that already knows it gets echoed.
func (s *DiscoveryServer) beacon(ip string) Peer {
	p := s.GetPeer()
	p.HostIP = ip
	p.PairingCode = ""
	if p.Offer != nil {
		p.Offer = publicOffer(p.Offer)
	}
	return p
}

// Start launches the UDP beacon broadcaster and TCP ingress-reflective responder
func (s *DiscoveryServer) Start(ctx context.Context) {
	// 1. TCP Ingress-Reflective Responder
	go func() {
		listener, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", DiscoveryPort))
		if err != nil {
			return
		}
		defer listener.Close()

		go func() {
			<-ctx.Done()
			listener.Close()
		}()

		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go func(c net.Conn) {
				defer c.Close()

				// Ingress Reflection: Resolve the exact interface IP used by the client
				localTCPAddr, ok := c.LocalAddr().(*net.TCPAddr)
				activeIP := ""
				if ok {
					activeIP = localTCPAddr.IP.String()
				}

				// Read optional client query with pairing code
				var query DiscoveryQuery
				_ = c.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
				_ = json.NewDecoder(c).Decode(&query)

				respPeer := s.answer(query, activeIP)
				data, err := json.Marshal(respPeer)
				if err == nil {
					_ = c.SetWriteDeadline(time.Now().Add(1 * time.Second))
					_, _ = c.Write(data)
				}
			}(conn)
		}
	}()

	// 2. Announce: mDNS on every local interface (DISC-01; Wi-Fi and the phone's own hotspot
	// alike, #132), and the broadcast beacon on every subnet as fallback (DISC-04). One
	// mechanism failing (no multicast, port taken) leaves the others.
	p := s.GetPeer()
	base := Announcement{Instance: p.DeviceName, Port: p.Port, Role: p.Role, Tags: s.tagsNow}
	ifaces := localInterfaces()
	if len(ifaces) == 0 {
		ifaces = []localInterface{{ip: net.ParseIP(GetPrimaryLocalIP()), iface: multicastInterface()}}
	}
	for _, li := range ifaces {
		a := base
		a.IP, a.Iface = li.ip, li.iface
		_ = mdnsDiscoverer{}.Announce(ctx, a)
	}
	_ = beaconDiscoverer{port: DiscoveryPort}.Announce(ctx, base)
}

// multicastInterface is the interface that carries this device's announced address, so that
// multicast leaves by it and not by the default route: on a phone with mobile data on, that is
// the cellular modem, which the local network cannot reach (#109, WP-6.0 note §3.2). nil: the
// system's default.
func multicastInterface() *net.Interface {
	primary := GetPrimaryLocalIP()
	for _, t := range GetActiveNetworkTargets() {
		if t.LocalIP.String() == primary {
			if idx := platformIndex(t.InterfaceName); idx > 0 { // given by the platform (DISC-10)
				return &net.Interface{Index: idx, Name: t.InterfaceName, Flags: net.FlagUp | net.FlagMulticast}
			}
			if ifi, err := interfaceByName(t.InterfaceName); err == nil {
				return ifi
			}
		}
	}
	return nil
}

// DiscoverPeers finds the devices that announce themselves: by mDNS and the fallback beacon
// (DISC-01, DISC-04). If none answers, it falls back to a bounded TCP sweep of the subnets
// (AP-isolated hotspots, multicast and broadcast filtered).
func DiscoverPeers(timeout time.Duration) ([]Peer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout+time.Second)
	defer cancel()
	ds := []Discoverer{beaconDiscoverer{port: DiscoveryPort}}
	for _, li := range localInterfaces() {
		ds = append(ds, mdnsDiscoverer{iface: li.iface})
	}
	if len(ds) == 1 {
		ds = append(ds, mdnsDiscoverer{iface: multicastInterface()})
	}
	if found := browseAll(ctx, ds, timeout); len(found) > 0 {
		peers := make([]Peer, 0, len(found))
		for _, f := range found {
			peers = append(peers, peerOf(f))
		}
		return peers, nil
	}
	return sweepPeers(), nil
}

// sweepPeers probes the discovery port of every address of the active subnets (DISC-09).
func sweepPeers() []Peer {
	discovered := make(map[string]Peer)
	var mu sync.Mutex

	// STEP 2: Fallback Bounded TCP Sweep (For AP-isolated hotspots / blocked UDP)
	targets := GetActiveNetworkTargets()
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 64) // Bounded concurrency limit

	probeHost := func(ip string) {
		if IsLocalNetworkIP(ip) {
			return
		}
		target := net.JoinHostPort(ip, strconv.Itoa(int(DiscoveryPort)))
		conn, err := net.DialTimeout("tcp4", target, 350*time.Millisecond)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
		respBytes, err := io.ReadAll(conn)
		if err != nil {
			return
		}

		var p Peer
		if err := json.Unmarshal(respBytes, &p); err == nil && p.Version == AppVersion && !isOwnName(p.DeviceName) {
			p.HostIP = ip // Probed address is guaranteed routable
			p.Tags = cleanTags(p.Tags)
			key := p.DeviceName // one entry per device, even with several addresses (#132)
			if key == "" {
				key = fmt.Sprintf("%s:%d", p.HostIP, p.Port)
			}
			mu.Lock()
			discovered[key] = p
			mu.Unlock()
		}
	}

	for _, target := range targets {
		for _, hostIP := range target.SweepIPs {
			wg.Add(1)
			go func(ip string) {
				defer wg.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()
				probeHost(ip)
			}(hostIP)
		}
	}

	wg.Wait()

	var results []Peer
	mu.Lock()
	for _, p := range discovered {
		if !IsLocalNetworkIP(p.HostIP) {
			results = append(results, p)
		}
	}
	mu.Unlock()
	return results
}
