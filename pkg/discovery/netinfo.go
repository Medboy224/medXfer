package discovery

import (
	"fmt"
	"net"
	"sort"
	"sync"
)

// NetworkInterface is one interface as the platform reports it (DISC-10): Android refuses the
// enumeration of interfaces to apps (net.Interfaces, net.InterfaceAddrs, ifconfig: G2A-09), so
// the platform side (Kotlin, ConnectivityManager) gives names, indexes and addresses.
type NetworkInterface struct {
	Name  string   `json:"name"`
	Index int      `json:"index,omitempty"` // 0: found by SIOCGIFINDEX from the name
	Addrs []string `json:"addrs"`           // IPv4 with prefix: "192.168.1.20/24"
}

var platform struct {
	mu     sync.RWMutex
	ifaces []NetworkInterface // nil: enumerate as usual
}

// SetNetworkInfo replaces the enumeration of interfaces with the platform's list, for
// discovery and for the address this device announces. nil goes back to enumerating. Each
// address must be IPv4 with a prefix.
func SetNetworkInfo(ifaces []NetworkInterface) error {
	defer forgetLocalNets()
	if ifaces == nil {
		platform.mu.Lock()
		platform.ifaces = nil
		platform.mu.Unlock()
		return nil
	}
	clean := make([]NetworkInterface, 0, len(ifaces))
	for _, ifi := range ifaces {
		if ifi.Name == "" {
			return fmt.Errorf("discovery: an interface without a name")
		}
		if ifi.Index < 0 {
			return fmt.Errorf("discovery: interface %s: negative index", ifi.Name)
		}
		for _, a := range ifi.Addrs {
			ip, _, err := net.ParseCIDR(a)
			if err != nil || ip.To4() == nil {
				return fmt.Errorf("discovery: interface %s: %q is not an IPv4 address with a prefix", ifi.Name, a)
			}
		}
		clean = append(clean, NetworkInterface{Name: ifi.Name, Index: ifi.Index, Addrs: append([]string(nil), ifi.Addrs...)})
	}
	platform.mu.Lock()
	platform.ifaces = clean
	platform.mu.Unlock()
	return nil
}

// platformTargets are the targets built from the platform's interfaces, best first; ok is false
// when the platform gave none (enumerate then).
func platformTargets() (targets []NetworkTarget, ok bool) {
	platform.mu.RLock()
	defer platform.mu.RUnlock()
	if platform.ifaces == nil {
		return nil, false
	}
	type scored struct {
		t     NetworkTarget
		score int
	}
	var all []scored
	for _, ifi := range platform.ifaces {
		for _, a := range ifi.Addrs {
			ip, ipNet, err := net.ParseCIDR(a)
			if err != nil || ip.To4() == nil {
				continue
			}
			ip = ip.To4()
			score := scoreInterface(ifi.Name, ip)
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
			all = append(all, scored{NetworkTarget{InterfaceName: ifi.Name, LocalIP: ip, Mask: mask, BroadcastIP: bcast,
				SweepIPs: calculateSweepRange(ip, mask)}, score})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].score > all[j].score })
	for _, s := range all {
		targets = append(targets, s.t)
	}
	return targets, true
}

// platformIndex is the index the platform gave for name, or 0.
func platformIndex(name string) int {
	platform.mu.RLock()
	defer platform.mu.RUnlock()
	for _, ifi := range platform.ifaces {
		if ifi.Name == name {
			return ifi.Index
		}
	}
	return 0
}

// hasIP reports whether ip is the address of one of targets.
func hasIP(targets []NetworkTarget, ip string) bool {
	for _, t := range targets {
		if t.LocalIP.String() == ip {
			return true
		}
	}
	return false
}
