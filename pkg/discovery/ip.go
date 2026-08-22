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

// GetLocalIP returns the best active LAN or Hotspot IPv4 address
func GetLocalIP() string {
	subnets := GetAllLocalSubnets()
	for _, s := range subnets {
		// Prefer actual Wi-Fi / Hotspot ranges
		if strings.HasPrefix(s.IP, "192.168.43.") || strings.HasPrefix(s.IP, "192.168.1.") || strings.HasPrefix(s.IP, "192.168.0.") {
			return s.IP
		}
	}
	if len(subnets) > 0 {
		return subnets[0].IP
	}
	return "127.0.0.1"
}

// GetConnectedHotspotClients checks the Linux/Android ARP table
func GetConnectedHotspotClients() []string {
	var clients []string
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return clients
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if scanner.Scan() { // skip header
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

// GetAllLocalSubnets returns real active network subnets
func GetAllLocalSubnets() []SubnetInfo {
	var results []SubnetInfo
	seenPrefixes := make(map[string]bool)

	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}

			// Skip common virtual adapters (VMware, VirtualBox, WSL)
			name := strings.ToLower(iface.Name)
			if strings.Contains(name, "vEthernet") || strings.Contains(name, "virtual") || strings.Contains(name, "vmnet") {
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

	// Fallback for Android Termux where hotspot interface is masked
	if len(results) == 0 {
		results = append(results, SubnetInfo{
			InterfaceName: "hotspot_default",
			IP:            "192.168.43.1",
			SubnetPrefix:  "192.168.43.",
			GatewayIP:     "192.168.43.1",
		})
	}

	return results
}
