package discovery

import (
	"net"
	"strings"
	"sync"
	"time"
)

// This device, as discovery must recognize it (#132): Android refuses net.InterfaceAddrs to
// apps and to Termux (G2A-09), so a phone could not tell its own addresses and listed itself.
// It is recognized by the names it announces, and by the addresses of its network targets
// (ifconfig under Termux, set_network_info in the app).

// own holds the instance names of this process, for the whole process: a process is one
// device (the core, a node, a sender). Two devices run in one process (tests) take each other
// for themselves; they would anyway, by their shared loopback address. A name is drawn at each
// launch and never reused, so the set only grows by one per discovery server (#156).
var own struct {
	mu    sync.RWMutex
	names map[string]bool
}

// registerOwnName records an instance name this process announces.
func registerOwnName(name string) {
	own.mu.Lock()
	defer own.mu.Unlock()
	if own.names == nil {
		own.names = map[string]bool{}
	}
	own.names[name] = true
}

func isOwnName(name string) bool {
	own.mu.RLock()
	defer own.mu.RUnlock()
	return own.names[name]
}

// isSelf reports whether a found device is this one.
func isSelf(f Found) bool {
	return isOwnName(f.Instance) || (f.IP != nil && IsLocalNetworkIP(f.IP.String()))
}

// ownNetsFor is how long localNets keeps its list: under Termux it runs ifconfig, and a sweep
// asks for every address of the subnet.
const ownNetsFor = 3 * time.Second

var ownNets struct {
	mu   sync.Mutex
	at   time.Time
	nets []*net.IPNet
}

// localNets are this device's IPv4 networks: from the system when it allows it, and from the
// network targets (the platform's list, else enumerated; never the ARP table, whose sweep
// order asks IsLocalNetworkIP).
func localNets() []*net.IPNet {
	ownNets.mu.Lock()
	defer ownNets.mu.Unlock()
	if ownNets.nets != nil && time.Since(ownNets.at) < ownNetsFor {
		return ownNets.nets
	}
	ownNets.nets, ownNets.at = readLocalNets(), time.Now()
	return ownNets.nets
}

// forgetLocalNets makes the next localNets read the system again (the platform's list changed).
func forgetLocalNets() {
	ownNets.mu.Lock()
	ownNets.nets = nil
	ownNets.mu.Unlock()
}

func readLocalNets() []*net.IPNet {
	out := []*net.IPNet{}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				out = append(out, n)
			}
		}
	}
	targets, ok := platformTargets()
	if !ok {
		targets = enumerateTargets()
	}
	for _, t := range targets {
		mask := t.Mask
		if len(mask) != 4 {
			mask = net.CIDRMask(32, 32) // the address alone: still recognized as ours
		}
		out = append(out, &net.IPNet{IP: t.LocalIP.To4(), Mask: mask})
	}
	return out
}

// localInterface is an interface to announce and browse on, with its address.
type localInterface struct {
	ip    net.IP
	iface *net.Interface // nil: the system's default
}

// localInterfaces are the interfaces mDNS runs on: every local one (Wi-Fi, the phone's own
// hotspot, Ethernet), never the cellular modem, one entry per interface (#132). Empty when
// none is known: the caller then uses the system's default.
func localInterfaces() []localInterface {
	var out []localInterface
	seen := map[string]bool{}
	for _, t := range GetActiveNetworkTargets() {
		name := strings.ToLower(t.InterfaceName)
		if isCellular(name) || seen[t.InterfaceName] || t.LocalIP == nil {
			continue
		}
		seen[t.InterfaceName] = true
		li := localInterface{ip: t.LocalIP}
		if idx := platformIndex(t.InterfaceName); idx > 0 { // given by the platform (DISC-10)
			li.iface = &net.Interface{Index: idx, Name: t.InterfaceName, Flags: net.FlagUp | net.FlagMulticast}
		} else if ifi, err := interfaceByName(t.InterfaceName); err == nil {
			li.iface = ifi
		}
		out = append(out, li)
	}
	return out
}
