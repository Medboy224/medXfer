package discovery

import (
	"net"
	"strings"
)

// SubnetInfo contains interface details for targeted subnet scanning
type SubnetInfo struct {
	InterfaceName string
	IP            string
	SubnetPrefix  string // e.g. "192.168.43."
	GatewayIP     string // e.g. "192.168.43.1"
}

// GetLocalIP returns the most likely LAN/Hotspot IP address
func GetLocalIP() string {
	subnets := GetAllLocalSubnets()
	if len(subnets) > 0 {
		// Prioritize standard Wi-Fi / Hotspot ranges
		for _, s := range subnets {
			if strings.HasPrefix(s.IP, "192.168.43.") || strings.HasPrefix(s.IP, "192.168.1.") {
				return s.IP
			}
		}
		return subnets[0].IP
	}
	return "127.0.0.1"
}

// GetAllLocalSubnets extracts all active IPv4 subnets across physical & wireless interfaces
func GetAllLocalSubnets() []SubnetInfo {
	var results []SubnetInfo

	ifaces, err := net.Interfaces()
	if err != nil {
		return results
	}

	for _, iface := range ifaces {
		// Skip down and loopback interfaces
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

			ip := ipNet.IP.To4().String()

			// Skip link-local (169.254.x.x) and loopback
			if strings.HasPrefix(ip, "169.254.") || ip == "127.0.0.1" {
				continue
			}

			parts := strings.Split(ip, ".")
			if len(parts) == 4 {
				prefix := fmtSubnetPrefix(parts)
				results = append(results, SubnetInfo{
					InterfaceName: iface.Name,
					IP:            ip,
					SubnetPrefix:  prefix,
					GatewayIP:     prefix + "1", // Phone hotspot is always host .1
				})
			}
		}
	}
	return results
}

func fmtSubnetPrefix(parts []string) string {
	return parts[0] + "." + parts[1] + "." + parts[2] + "."
}
