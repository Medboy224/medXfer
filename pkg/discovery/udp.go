package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
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

// StartDiscoveryServer runs both UDP beacon and TCP discovery responder on the sender
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

	// 1. TCP Discovery Responder (Handles Hotspot Subnet Sweeps)
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

	// 2. UDP Broadcast Beacon (Handles regular Wi-Fi routers)
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

// ScanForSenders runs both a UDP listener and a concurrent TCP subnet sweep
func ScanForSenders(duration time.Duration) ([]PeerOffer, error) {
	discovered := make(map[string]PeerOffer)
	var mu sync.Mutex

	var wg sync.WaitGroup

	// Task A: Passive UDP Listener
	wg.Add(1)
	go func() {
		defer wg.Done()
		addr := net.UDPAddr{Port: DiscoveryPort, IP: net.IPv4zero}
		conn, err := net.ListenUDP("udp4", &addr)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.SetReadDeadline(time.Now().Add(duration))
		buf := make([]byte, 2048)

		for {
			n, srcAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				break
			}
			var offer PeerOffer
			if err := json.Unmarshal(buf[:n], &offer); err == nil && offer.Magic == BeaconMagic {
				if offer.HostIP == "" || offer.HostIP == "127.0.0.1" {
					offer.HostIP = srcAddr.IP.String()
				}
				mu.Lock()
				discovered[fmt.Sprintf("%s:%d", offer.HostIP, offer.Port)] = offer
				mu.Unlock()
			}
		}
	}()

	// Task B: Active Parallel TCP Subnet Sweep (Bypasses Hotspot Isolation)
	wg.Add(1)
	go func() {
		defer wg.Done()
		localIP := GetLocalIP()
		if localIP == "127.0.0.1" {
			return
		}

		parts := strings.Split(localIP, ".")
		if len(parts) != 4 {
			return
		}
		subnetPrefix := fmt.Sprintf("%s.%s.%s.", parts[0], parts[1], parts[2])

		var sweepWg sync.WaitGroup

		// Sweep all 254 possible hosts concurrently
		for i := 1; i <= 254; i++ {
			targetHost := fmt.Sprintf("%s%d", subnetPrefix, i)
			sweepWg.Add(1)

			go func(ip string) {
				defer sweepWg.Done()

				target := fmt.Sprintf("%s:%d", ip, DiscoveryPort)
				conn, err := net.DialTimeout("tcp4", target, 350*time.Millisecond)
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
					if offer.HostIP == "" || offer.HostIP == "127.0.0.1" {
						offer.HostIP = ip
					}
					mu.Lock()
					discovered[fmt.Sprintf("%s:%d", offer.HostIP, offer.Port)] = offer
					mu.Unlock()
				}
			}(targetHost)
		}
		sweepWg.Wait()
	}()

	wg.Wait()

	var results []PeerOffer
	for _, off := range discovered {
		results = append(results, off)
	}
	return results, nil
}
