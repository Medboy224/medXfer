package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

const (
	DiscoveryPort = 19998
	BeaconMagic   = "MEDXFER_DISCOVERY_V1"
)

type BeaconPacket struct {
	Magic   string `json:"magic"`
	Role    string `json:"role"`
	TCPPort int    `json:"tcp_port"`
	HostIP  string `json:"host_ip"`
}

// BroadcastReceiverBeacon announces receiver readiness on the local subnet (255.255.255.255)
func BroadcastReceiverBeacon(ctx context.Context, tcpPort int) {
	broadcastAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("255.255.255.255:%d", DiscoveryPort))
	if err != nil {
		return
	}

	conn, err := net.DialUDP("udp", nil, broadcastAddr)
	if err != nil {
		return
	}
	defer conn.Close()

	packet := BeaconPacket{
		Magic:   BeaconMagic,
		Role:    "receiver",
		TCPPort: tcpPort,
		HostIP:  GetLocalIP(),
	}

	data, err := json.Marshal(packet)
	if err != nil {
		return
	}

	ticker := time.NewTicker(800 * time.Millisecond)
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

// DiscoverReceiver listens for an active receiver on the LAN
func DiscoverReceiver(timeout time.Duration) (string, int, error) {
	addr := net.UDPAddr{
		Port: DiscoveryPort,
		IP:   net.IPv4zero,
	}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		return "", 0, fmt.Errorf("UDP listen failed: %w", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 1024)

	for {
		n, srcAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return "", 0, fmt.Errorf("no receiver found on LAN (timed out)")
		}

		var pkt BeaconPacket
		if err := json.Unmarshal(buf[:n], &pkt); err != nil {
			continue
		}

		if pkt.Magic == BeaconMagic && pkt.Role == "receiver" {
			targetIP := pkt.HostIP
			if targetIP == "" || targetIP == "127.0.0.1" {
				targetIP = srcAddr.IP.String()
			}
			return targetIP, pkt.TCPPort, nil
		}
	}
}
