package discovery

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"net"
	"os/exec"
	"sort"
	"strings"
)

type NetworkTarget struct {
	InterfaceName string
	LocalIP       net.IP
	BroadcastIP   net.IP
	SweepIPs      []string
}

// scoreInterface evaluates an interface name and IP for priority
func scoreInterface(name string, ip net.IP) int {
	if ip == nil || ip.To4() == nil {
		return -1
	}
	ip4 := ip.To4()
	if ip4.IsLoopback() || ip4.IsUnspecified() || ip4.IsLinkLocalUnicast() || strings.HasPrefix(ip4.String(), "169.254.") {
		return -1
	}

	lower := strings.ToLower(name)
	// Hypervisor / virtual container bridges
	if strings.Contains(lower, "vethernet") || strings.Contains(lower, "virtualbox") ||
		strings.Contains(lower, "vmnet") || strings.Contains(lower, "docker") ||
		strings.Contains(lower, "virbr") {
		return 10
	}
	// Cellular data modem interfaces (lower priority than local Wi-Fi / Hotspot)
	if strings.HasPrefix(lower, "rmnet") || strings.HasPrefix(lower, "ccmni") ||
		strings.HasPrefix(lower, "pdp") || strings.HasPrefix(lower, "wwan") {
		return 20
	}

	score := 50
	// SoftAP / Wi-Fi / Tethering / Ethernet
	if strings.HasPrefix(lower, "ap") || strings.HasPrefix(lower, "softap") ||
		strings.HasPrefix(lower, "wlan") || strings.HasPrefix(lower, "swlan") ||
		strings.HasPrefix(lower, "tether") || strings.HasPrefix(lower, "rndis") ||
		strings.HasPrefix(lower, "eth") || strings.HasPrefix(lower, "en") ||
		strings.HasPrefix(lower, "wl") {
		score += 30
	}

	// Prefer private IPv4 subnets
	if ip4[0] == 192 && ip4[1] == 168 {
		score += 30 // Typical home Wi-Fi & classic Android Hotspot
	} else if ip4[0] == 10 {
		score += 25 // Modern Android Hotspot & corporate LAN
	} else if ip4[0] == 172 && (ip4[1] >= 16 && ip4[1] <= 31) {
		score += 20
	}

	return score
}

// parseIfconfigTargets tries to run ifconfig to discover interfaces when netlink is blocked by SELinux (e.g. Termux on Android 11+)
func parseIfconfigTargets() []NetworkTarget {
	var targets []NetworkTarget

	// Find ifconfig binary path
	var cmdPath string
	candidates := []string{
		"ifconfig",
		"/data/data/com.termux/files/usr/bin/ifconfig",
		"/system/bin/ifconfig",
		"/system/xbin/ifconfig",
		"/sbin/ifconfig",
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			cmdPath = p
			break
		}
	}
	if cmdPath == "" {
		cmdPath = "ifconfig"
	}

	out, err := exec.Command(cmdPath).CombinedOutput()
	if err != nil && len(out) == 0 {
		return targets
	}

	scanner := bufio.NewScanner(bytes.NewReader(out))
	var currentIface string

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				currentIface = strings.TrimSuffix(parts[0], ":")
			}
		}

		if strings.Contains(line, "inet ") {
			fields := strings.Fields(line)
			for i := 0; i < len(fields); i++ {
				if fields[i] == "inet" && i+1 < len(fields) {
					ip := net.ParseIP(fields[i+1])
					if ip == nil || ip.To4() == nil || ip.IsLoopback() {
						continue
					}

					var mask net.IPMask = net.CIDRMask(24, 32)
					var bcast net.IP

					for j := i + 2; j < len(fields); j++ {
						if fields[j] == "netmask" && j+1 < len(fields) {
							if m := net.ParseIP(fields[j+1]); m != nil && m.To4() != nil {
								mask = net.IPMask(m.To4())
							}
						}
						if fields[j] == "broadcast" && j+1 < len(fields) {
							if b := net.ParseIP(fields[j+1]); b != nil && b.To4() != nil {
								bcast = b.To4()
							}
						}
					}

					if bcast == nil && len(mask) == 4 {
						bcast = make(net.IP, 4)
						ip4 := ip.To4()
						for k := 0; k < 4; k++ {
							bcast[k] = ip4[k] | ^mask[k]
						}
					}

					targets = append(targets, NetworkTarget{
						InterfaceName: currentIface,
						LocalIP:       ip.To4(),
						BroadcastIP:   bcast,
						SweepIPs:      calculateSweepRange(ip.To4(), mask),
					})
				}
			}
		}
	}

	return targets
}

// GetActiveNetworkTargets inspects OS interfaces based on capabilities and flags
func GetActiveNetworkTargets() []NetworkTarget {
	var targets []NetworkTarget
	seenSubnets := make(map[string]bool)

	type scoredTarget struct {
		target NetworkTarget
		score  int
	}
	var scoredTargets []scoredTarget

	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			// Skip loopback interfaces
			if iface.Flags&net.FlagLoopback != 0 {
				continue
			}

			// Exclude virtual hypervisor bridges
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
					continue
				}

				ip := ipNet.IP.To4()
				score := scoreInterface(iface.Name, ip)
				if score < 0 {
					continue
				}

				mask := ipNet.Mask
				if len(mask) != 4 {
					mask = net.CIDRMask(24, 32)
				}

				// Subnet Broadcast calculation: IP | (^Mask)
				bcast := make(net.IP, 4)
				for i := 0; i < 4; i++ {
					bcast[i] = ip[i] | ^mask[i]
				}

				subnetKey := ipNet.String()
				if !seenSubnets[subnetKey] {
					seenSubnets[subnetKey] = true
					scoredTargets = append(scoredTargets, scoredTarget{
						target: NetworkTarget{
							InterfaceName: iface.Name,
							LocalIP:       ip,
							BroadcastIP:   bcast,
							SweepIPs:      calculateSweepRange(ip, mask),
						},
						score: score,
					})
				}
			}
		}
	}

	// Fallback to ifconfig if netlink was blocked by SELinux (common on Android 11+ / Termux)
	if len(scoredTargets) == 0 {
		ifconfigTargets := parseIfconfigTargets()
		for _, t := range ifconfigTargets {
			score := scoreInterface(t.InterfaceName, t.LocalIP)
			if score < 0 {
				continue
			}
			subnetKey := t.LocalIP.String()
			if !seenSubnets[subnetKey] {
				seenSubnets[subnetKey] = true
				scoredTargets = append(scoredTargets, scoredTarget{
					target: t,
					score:  score,
				})
			}
		}
	}

	// Fallback to net.InterfaceAddrs() if no targets found (e.g. Android SELinux blocking net.Interfaces())
	if len(scoredTargets) == 0 {
		addrs, err := net.InterfaceAddrs()
		if err == nil {
			for _, addr := range addrs {
				ipNet, ok := addr.(*net.IPNet)
				if !ok || ipNet.IP.To4() == nil {
					continue
				}
				ip := ipNet.IP.To4()
				score := scoreInterface("wlan0", ip)
				if score < 0 {
					continue
				}
				mask := ipNet.Mask
				if len(mask) != 4 {
					mask = net.CIDRMask(24, 32)
				}
				bcast := make(net.IP, 4)
				for i := 0; i < 4; i++ {
					bcast[i] = ip[i] | ^mask[i]
				}
				subnetKey := ipNet.String()
				if !seenSubnets[subnetKey] {
					seenSubnets[subnetKey] = true
					scoredTargets = append(scoredTargets, scoredTarget{
						target: NetworkTarget{
							InterfaceName: "wlan0",
							LocalIP:       ip,
							BroadcastIP:   bcast,
							SweepIPs:      calculateSweepRange(ip, mask),
						},
						score: score,
					})
				}
			}
		}
	}

	// Sort targets descending by score
	sort.SliceStable(scoredTargets, func(i, j int) bool {
		return scoredTargets[i].score > scoredTargets[j].score
	})

	for _, st := range scoredTargets {
		targets = append(targets, st.target)
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

	// Direct interface address inspection as fallback
	bestScore := -1
	bestIP := "127.0.0.1"

	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				ip4 := ipNet.IP.To4()
				if ip4 != nil {
					score := scoreInterface("eth0", ip4)
					if score > bestScore {
						bestScore = score
						bestIP = ip4.String()
					}
				}
			}
		}
	}

	return bestIP
}
