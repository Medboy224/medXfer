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

// StartDiscoveryServer listens for incoming discovery requests on both TCP and UDP
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

	// 1. TCP Probe Responder
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

// ScanForSenders discovers senders using a prioritized gateway probe and a worker pool
func ScanForSenders(duration time.Duration) ([]PeerOffer, error) {
	discovered := make(map[string]PeerOffer)
	var mu sync.Mutex

	probeIP := func(ip string, timeout time.Duration) bool {
		target := fmt.Sprintf("%s:%d", ip, DiscoveryPort)
		conn, err := net.DialTimeout("tcp4", target, timeout)
		if err != nil {
			return false
		}
		defer conn.Close()

		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		respBytes, err := io.ReadAll(conn)
		if err != nil {
			return false
		}

		var offer PeerOffer
		if err := json.Unmarshal(respBytes, &offer); err == nil && offer.Magic == BeaconMagic {
			offer.HostIP = ip
			mu.Lock()
			discovered[fmt.Sprintf("%s:%d", offer.HostIP, offer.Port)] = offer
			mu.Unlock()
			return true
		}
		return false
	}

	subnets := GetAllLocalSubnets()

	// STEP 1: Fast Priority Gateways Probe (Phone hotspot is always .1)
	for _, sub := range subnets {
		probeIP(sub.GatewayIP, 800*time.Millisecond)
	}

	// STEP 2: Check ARP table (if on Android receiver)
	for _, clientIP := range GetConnectedHotspotClients() {
		probeIP(clientIP, 600*time.Millisecond)
	}

	// STEP 3: Controlled Worker Pool for remaining subnet IPs (32 concurrent max)
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 32)

	for _, sub := range subnets {
		for i := 1; i <= 254; i++ {
			targetIP := fmt.Sprintf("%s%d", sub.SubnetPrefix, i)
			wg.Add(1)

			go func(ip string) {
				defer wg.Done()
				semaphore <- struct{}{}        // Acquire token
				defer func() { <-semaphore }() // Release token

				probeIP(ip, 500*time.Millisecond)
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
