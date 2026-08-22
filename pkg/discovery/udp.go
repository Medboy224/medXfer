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
	BeaconMagic   = "MEDXFER_BEACON_V2"
)

type PeerOffer struct {
	Magic      string `json:"magic"`
	DeviceName string `json:"device_name"`
	HostIP     string `json:"host_ip"`
	Port       int    `json:"port"`
	FileName   string `json:"file_name"`
	FileSize   int64  `json:"file_size"`
}

// StartDiscoveryServer runs on the sender to reply to discovery sweeps
func StartDiscoveryServer(ctx context.Context, fileName string, fileSize int64, tcpPort int) {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "Device"
	}

	offer := PeerOffer{
		Magic:      BeaconMagic,
		DeviceName: hostname,
		HostIP:     GetLocalIP(),
		Port:       tcpPort,
		FileName:   fileName,
		FileSize:   fileSize,
	}

	offerBytes, err := json.Marshal(offer)
	if err != nil {
		return
	}

	// 1. TCP Sweep Responder (Unicast probe listener)
	go func() {
		listener, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", DiscoveryPort))
		if err != nil {
			fmt.Printf("[!] Warning: Could not bind discovery port %d: %v\n", DiscoveryPort, err)
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
				_ = c.SetWriteDeadline(time.Now().Add(1 * time.Second))
				_, _ = c.Write(offerBytes)
			}(conn)
		}
	}()

	// 2. UDP Broadcast Beacon
	go func() {
		bcastAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("255.255.255.255:%d", DiscoveryPort))
		if err != nil {
			return
		}
		conn, err := net.DialUDP("udp4", nil, bcastAddr)
		if err != nil {
			return
		}
		defer conn.Close()

		ticker := time.NewTicker(600 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = conn.Write(offerBytes)
			}
		}
	}()
}

// ScanForSenders sweeps all active subnets across all network cards
func ScanForSenders(duration time.Duration) ([]PeerOffer, error) {
	discovered := make(map[string]PeerOffer)
	var mu sync.Mutex
	var wg sync.WaitGroup

	subnets := GetAllLocalSubnets()
	if len(subnets) == 0 {
		return nil, fmt.Errorf("no active network adapters found")
	}

	// Helper function to probe a specific IP address
	probeIP := func(ip string, timeout time.Duration) {
		target := fmt.Sprintf("%s:%d", ip, DiscoveryPort)
		conn, err := net.DialTimeout("tcp4", target, timeout)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		respBytes, err := io.ReadAll(conn)
		if err != nil {
			return
		}

		var offer PeerOffer
		if err := json.Unmarshal(respBytes, &offer); err == nil && offer.Magic == BeaconMagic {
			// Always use the probed IP as the true working routing address
			offer.HostIP = ip
			mu.Lock()
			discovered[fmt.Sprintf("%s:%d", offer.HostIP, offer.Port)] = offer
			mu.Unlock()
		}
	}

	// Priority Phase: Probe default gateways (.1) on all adapters first
	for _, sub := range subnets {
		wg.Add(1)
		go func(gw string) {
			defer wg.Done()
			probeIP(gw, 400*time.Millisecond)
		}(sub.GatewayIP)
	}

	// Full Subnet Sweep Phase: Probe 1-254 across all detected network cards
	for _, sub := range subnets {
		for i := 1; i <= 254; i++ {
			targetIP := fmt.Sprintf("%s%d", sub.SubnetPrefix, i)
			wg.Add(1)
			go func(ip string) {
				defer wg.Done()
				probeIP(ip, 350*time.Millisecond)
			}(targetIP)
		}
	}

	wg.Wait()

	var results []PeerOffer
	for _, off := range discovered {
		results = append(results, off)
	}
	return results, nil
}
