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
	DiscoveryPort  = 19998
	MulticastGroup = "239.255.255.250"
	ProtocolMagic  = "MEDXFER_NODE_V3"
	AppVersion     = "1.0.0"
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
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			if ipnet.IP.Equal(ip) {
				return true
			}
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

	// 2. Interface-Bound UDP & Multicast Broadcaster (Zero-Leak Beacons)
	go func() {
		ticker := time.NewTicker(600 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				targets := GetActiveNetworkTargets()
				for _, target := range targets {
					lAddr := &net.UDPAddr{IP: target.LocalIP, Port: 0}
					data, err := json.Marshal(s.beacon(target.LocalIP.String()))
					if err != nil {
						continue
					}

					// Vector A: Subnet Broadcast
					rAddr := &net.UDPAddr{IP: target.BroadcastIP, Port: DiscoveryPort}
					if conn, err := net.DialUDP("udp4", lAddr, rAddr); err == nil {
						_, _ = conn.Write(data)
						_ = conn.Close()
					}

					// Vector B: SSDP-Style Local Multicast
					mAddr := &net.UDPAddr{IP: net.ParseIP(MulticastGroup), Port: DiscoveryPort}
					if mConn, err := net.DialUDP("udp4", lAddr, mAddr); err == nil {
						_, _ = mConn.Write(data)
						_ = mConn.Close()
					}

					// Vector C: Limited Local Broadcast (for ad-hoc / direct Ethernet cable links)
					bcastAddr := &net.UDPAddr{IP: net.IPv4bcast, Port: DiscoveryPort}
					if bConn, err := net.DialUDP("udp4", lAddr, bcastAddr); err == nil {
						_, _ = bConn.Write(data)
						_ = bConn.Close()
					}
				}
			}
		}
	}()
}

// DiscoverPeers searches the network via UDP beacons and falls back to a bounded TCP sweep
func DiscoverPeers(timeout time.Duration) ([]Peer, error) {
	discovered := make(map[string]Peer)
	var mu sync.Mutex

	// STEP 1: Primary Discovery via Passive UDP Listener
	udpDone := make(chan struct{})
	go func() {
		defer close(udpDone)
		addr := net.UDPAddr{Port: DiscoveryPort, IP: net.IPv4zero}
		conn, err := net.ListenUDP("udp4", &addr)
		if err != nil {
			return
		}
		defer conn.Close()

		// Listen for up to 800ms or until caller timeout
		listenTime := 800 * time.Millisecond
		if timeout < listenTime {
			listenTime = timeout
		}
		_ = conn.SetReadDeadline(time.Now().Add(listenTime))
		buf := make([]byte, 2048)

		for {
			n, srcAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				break
			}

			var p Peer
			if err := json.Unmarshal(buf[:n], &p); err == nil && p.Version == AppVersion {
				srcIPStr := srcAddr.IP.String()
				if p.HostIP == "" || p.HostIP == "127.0.0.1" || !IsSubnetReachable(p.HostIP) {
					p.HostIP = srcIPStr
				}
				key := fmt.Sprintf("%s:%d", p.HostIP, p.Port)
				mu.Lock()
				discovered[key] = p
				mu.Unlock()
			}
		}
	}()

	<-udpDone

	// CHECK FOR REMOTE PEERS
	mu.Lock()
	hasRemote := false
	for _, p := range discovered {
		if !isLocalNetworkIP(p.HostIP) {
			hasRemote = true
			break
		}
	}

	// FIX: If we found ACTUAL remote peers via UDP, return immediately (excluding local machine).
	if hasRemote {
		var results []Peer
		for _, p := range discovered {
			if !IsLocalNetworkIP(p.HostIP) {
				results = append(results, p)
			}
		}
		mu.Unlock()
		return results, nil
	}
	mu.Unlock()

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
		if err := json.Unmarshal(respBytes, &p); err == nil && p.Version == AppVersion {
			p.HostIP = ip // Probed address is guaranteed routable
			key := fmt.Sprintf("%s:%d", p.HostIP, p.Port)
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
	return results, nil
}
