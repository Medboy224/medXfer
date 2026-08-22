package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
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

func BroadcastSenderOffer(ctx context.Context, fileName string, fileSize int64, tcpPort int) {
	broadcastAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("255.255.255.255:%d", DiscoveryPort))
	if err != nil {
		return
	}

	conn, err := net.DialUDP("udp", nil, broadcastAddr)
	if err != nil {
		return
	}
	defer conn.Close()

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

	data, err := json.Marshal(offer)
	if err != nil {
		return
	}

	ticker := time.NewTicker(600 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = conn.Write(data)
		}
	}
}

func ScanForSenders(duration time.Duration) ([]PeerOffer, error) {
	addr := net.UDPAddr{
		Port: DiscoveryPort,
		IP:   net.IPv4zero,
	}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		return nil, fmt.Errorf("failed to open discovery listener: %w", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(duration))
	discovered := make(map[string]PeerOffer)
	buf := make([]byte, 2048)

	for {
		n, srcAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}

		var offer PeerOffer
		if err := json.Unmarshal(buf[:n], &offer); err != nil {
			continue
		}

		if offer.Magic == BeaconMagic {
			if offer.HostIP == "" || offer.HostIP == "127.0.0.1" {
				offer.HostIP = srcAddr.IP.String()
			}
			key := fmt.Sprintf("%s:%d", offer.HostIP, offer.Port)
			discovered[key] = offer
		}
	}

	var results []PeerOffer
	for _, off := range discovered {
		results = append(results, off)
	}
	return results, nil
}
