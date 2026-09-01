package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

const (
	DiscoveryPort = 19998
	ProtocolMagic = "MEDXFER_NODE_V3"
	AppVersion    = "1.0.0"
)

// TransferOffer represents an active file payload offered by a peer
type TransferOffer struct {
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
}

// Peer represents an identified medXfer instance on the network
type Peer struct {
	ID         string         `json:"id"`
	DeviceName string         `json:"device_name"`
	HostIP     string         `json:"host_ip"`
	Port       int            `json:"port"`
	Version    string         `json:"version"`
	Role       string         `json:"role"` // "sender", "receiver", "idle", "node"
	Offer      *TransferOffer `json:"offer,omitempty"`
}

// isLocalNetworkIP checks if an IP belongs to the local machine
func isLocalNetworkIP(ipStr string) bool {
	if ipStr == "127.0.0.1" || ipStr == "localhost" {
		return true
	}
	ip := net.ParseIP(ipStr)
	if ip == nil || ip.IsLoopback() {
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

// DiscoveryServer handles incoming discovery queries and emits periodic beacons
type DiscoveryServer struct {
	peer Peer
}

// NewDiscoveryServer initializes a node's discovery responder
func NewDiscoveryServer(role string, tcpPort int, offer *TransferOffer) *DiscoveryServer {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "medXfer-Node"
	}

	return &DiscoveryServer{
		peer: Peer{
			ID:         hostname + fmt.Sprintf("-%d", tcpPort),
			DeviceName: hostname,
			Port:       tcpPort,
			Version:    AppVersion,
			Role:       role,
			Offer:      offer,
		},
	}
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

				respPeer := s.peer
				respPeer.HostIP = activeIP

				data, err := json.Marshal(respPeer)
				if err == nil {
					_ = c.SetWriteDeadline(time.Now().Add(1 * time.Second))
					_, _ = c.Write(data)
				}
			}(conn)
		}
	}()

	// 2. Interface-Bound UDP Broadcaster
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
					rAddr := &net.UDPAddr{IP: target.BroadcastIP, Port: DiscoveryPort}

					// Explicitly bind the socket to the source interface IP
					conn, err := net.DialUDP("udp4", lAddr, rAddr)
					if err != nil {
						continue
					}

					beaconPeer := s.peer
					beaconPeer.HostIP = target.LocalIP.String()

					data, err := json.Marshal(beaconPeer)
					if err == nil {
						_, _ = conn.Write(data)
					}
					_ = conn.Close()
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
				if p.HostIP == "" || p.HostIP == "127.0.0.1" {
					p.HostIP = srcAddr.IP.String()
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

	// FIX: If we found ACTUAL remote peers via UDP, return immediately.
	// If we ONLY found ourselves, force the TCP sweep fallback!
	if hasRemote {
		var results []Peer
		for _, p := range discovered {
			results = append(results, p)
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
		target := fmt.Sprintf("%s:%d", ip, DiscoveryPort)
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
		results = append(results, p)
	}
	mu.Unlock()
	return results, nil
}
