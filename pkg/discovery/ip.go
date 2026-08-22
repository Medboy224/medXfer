package discovery

import (
	"bufio"
	"net"
	"os"
	"strings"
)

type SubnetInfo struct {
	InterfaceName string
	IP            string
	SubnetPrefix  string // e.g. "192.168.43."
	GatewayIP     string // e.g. "192.168.43.1"
}

// GetLocalIP returns the best local IP, prioritizing Wi-Fi/Hotspot over Cellular
func GetLocalIP() string {
	subnets := GetAllLocalSubnets()

	// 1. Prioritize Wi-Fi and Hotspot ranges
	for _, s := range subnets {
		if strings.HasPrefix(s.IP, "192.168.43.") {
			return s.IP
		}
	}
	for _, s := range subnets {
		if strings.HasPrefix(s.IP, "192.168.") || strings.HasPrefix(s.IP, "172.20.10.") {
			return s.IP
		}
	}

	// 2. Fallback to any detected adapter
	if len(subnets) > 0 && !strings.HasPrefix(subnets[0].IP, "127.") {
		return subnets[0].IP
	}

	return "192.168.43.1"
}

// GetConnectedHotspotClients checks the ARP cache (if accessible)
func GetConnectedHotspotClients() []string {
	var clients []string
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return clients
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 4 {
				ip := fields[0]
				flags := fields[2]
				if flags == "0x2" && !strings.HasPrefix(ip, "127.") {
					clients = append(clients, ip)
				}
			}
		}
	}
	return clients
}

// GetAllLocalSubnets returns all physical subnets AND forces standard hotspot ranges
func GetAllLocalSubnets() []SubnetInfo {
	var results []SubnetInfo
	seenPrefixes := make(map[string]bool)

	// 1. Detect physical/Wi-Fi network adapters
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}

			// Filter out virtual network adapters
			name := strings.ToLower(iface.Name)
			if strings.Contains(name, "vethernet") || strings.Contains(name, "virtual") || strings.Contains(name, "vmnet") {
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

				ip := ipNet.IP.To4().String()
				// Skip link-local and loopback
				if strings.HasPrefix(ip, "169.254.") || ip == "127.0.0.1" {
					continue
				}

				parts := strings.Split(ip, ".")
				if len(parts) == 4 {
					prefix := parts[0] + "." + parts[1] + "." + parts[2] + "."
					if !seenPrefixes[prefix] {
						seenPrefixes[prefix] = true
						results = append(results, SubnetInfo{
							InterfaceName: iface.Name,
							IP:            ip,
							SubnetPrefix:  prefix,
							GatewayIP:     prefix + "1",
						})
					}
				}
			}
		}
	}

	// 2. UNCONDITIONALLY inject standard Hotspot ranges
	// Guarantees Termux always scans the hotspot network even when mobile data is active
	mandatoryHotspotPrefixes := []string{
		"192.168.43.", // Android Hotspot (Standard)
		"192.168.44.", // Android Hotspot (Secondary)
		"192.168.49.", // Wi-Fi Direct
		"192.168.1.",  // Standard Home Wi-Fi
		"192.168.0.",  // Standard Home Wi-Fi
		"172.20.10.",  // iOS Hotspot
	}

	for _, prefix := range mandatoryHotspotPrefixes {
		if !seenPrefixes[prefix] {
			seenPrefixes[prefix] = true
			results = append(results, SubnetInfo{
				InterfaceName: "hotspot_forced",
				IP:            prefix + "1",
				SubnetPrefix:  prefix,
				GatewayIP:     prefix + "1",
			})
		}
	}

	return results
}
