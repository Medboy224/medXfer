package discovery

import (
	"encoding/binary"
	"net"
	"strings"
)

type NetworkTarget struct {
	InterfaceName string
	LocalIP       net.IP
	BroadcastIP   net.IP
	SweepIPs      []string
}

// GetActiveNetworkTargets inspects OS interfaces based on capabilities and flags
func GetActiveNetworkTargets() []NetworkTarget {
	var targets []NetworkTarget
	seenSubnets := make(map[string]bool)

	ifaces, err := net.Interfaces()
	if err != nil {
		return targets
	}

	for _, iface := range ifaces {
		// 1. Primary Filter: Interface Capabilities & Flags
		if iface.Flags&net.FlagUp == 0 {
			continue // Interface is down
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue // Skip loopback
		}
		if iface.Flags&net.FlagPointToPoint != 0 {
			continue // Skip point-to-point tunnels/cellular modems without broadcast
		}

		// 2. Secondary Filter: Exclude virtual hypervisor bridges
		name := strings.ToLower(iface.Name)
		if strings.Contains(name, "vethernet") || strings.Contains(name, "virtualbox") ||
			strings.Contains(name, "vmnet") || strings.Contains(name, "docker") ||
			strings.Contains(name, "virbr") {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue // IPv4 only
			}

			ip := ipNet.IP.To4()

			// Exclude non-routable, link-local, and unspecified addresses
			if ip.IsLinkLocalUnicast() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
				continue
			}

			mask := ipNet.Mask
			if len(mask) != 4 {
				continue
			}

			// Subnet Broadcast calculation: IP | (^Mask)
			bcast := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				bcast[i] = ip[i] | ^mask[i]
			}

			subnetKey := ipNet.String()
			if !seenSubnets[subnetKey] {
				seenSubnets[subnetKey] = true
				targets = append(targets, NetworkTarget{
					InterfaceName: iface.Name,
					LocalIP:       ip,
					BroadcastIP:   bcast,
					SweepIPs:      calculateSweepRange(ip, mask),
				})
			}
		}
	}

	return targets
}

// calculateSweepRange computes host IPs bounded safely to a local /24 slice
func calculateSweepRange(ip net.IP, mask net.IPMask) []string {
	var ips []string

	ipInt := binary.BigEndian.Uint32(ip)
	maskInt := binary.BigEndian.Uint32(mask)
	networkInt := ipInt & maskInt
	broadcastInt := ipInt | ^maskInt

	// On networks larger than /24, clamp sweep to the surrounding /24 slice
	if broadcastInt-networkInt > 255 {
		networkInt = ipInt & 0xFFFFFF00
		broadcastInt = networkInt | 0x000000FF
	}

	// Always prioritize the .1 Gateway IP
	gwInt := networkInt + 1
	if gwInt != ipInt && gwInt < broadcastInt {
		gwIP := make(net.IP, 4)
		binary.BigEndian.PutUint32(gwIP, gwInt)
		ips = append(ips, gwIP.String())
	}

	for cur := networkInt + 1; cur < broadcastInt; cur++ {
		if cur == gwInt || cur == ipInt {
			continue
		}
		hostIP := make(net.IP, 4)
		binary.BigEndian.PutUint32(hostIP, cur)
		ips = append(ips, hostIP.String())
	}

	return ips
}

// GetPrimaryLocalIP returns the primary routable IPv4 address
func GetPrimaryLocalIP() string {
	targets := GetActiveNetworkTargets()
	if len(targets) > 0 {
		return targets[0].LocalIP.String()
	}
	return "127.0.0.1"
}
