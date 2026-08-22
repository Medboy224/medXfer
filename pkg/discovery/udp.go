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

// GetAllBroadcastAddresses calculates the directed broadcast IP for all active adapters
func GetAllBroadcastAddresses() []string {
	var broadcasts []string

	ifaces, err := net.Interfaces()
	if err != nil {
		return []string{"255.255.255.255"}
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}

			ip := ipNet.IP.To4()
			mask := ipNet.Mask
			if len(mask) == 4 {
				// Calculate subnet broadcast: IP | (^Mask)
				bcast := net.IPv4(
					ip[0]|^mask[0],
					ip[1]|^mask[1],
					ip[2]|^mask[2],
					ip[3]|^mask[3],
				)
				broadcasts = append(broadcasts, bcast.String())
			}
		}
	}

	// Always append global broadcast as fallback
	broadcasts = append(broadcasts, "255.255.255.255")
	return broadcasts
}

// BroadcastSenderOffer announces an available file across all active network interfaces
func BroadcastSenderOffer(ctx context.Context, fileName string, fileSize int64, tcpPort int) {
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
			// Send beacons to every detected broadcast interface
			targets := GetAllBroadcastAddresses()
			for _, bcastIP := range targets {
				rAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", bcastIP, DiscoveryPort))
				if err != nil {
					continue
				}

				conn, err := net.DialUDP("udp4", nil, rAddr)
				if err != nil {
					continue
				}
				_, _ = conn.Write(data)
				_ = conn.Close()
			}
		}
	}
}

// ScanForSenders collects active senders on the LAN within a timeout window
func ScanForSenders(duration time.Duration) ([]PeerOffer, error) {
	addr := net.UDPAddr{
		Port: DiscoveryPort,
		IP:   net.IPv4zero,
	}
	conn, err := net.ListenUDP("udp4", &addr)
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
			break // Timeout reached
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
