package discovery

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GeneratePairingCode creates a 6-digit code: "XXX-YYY".
// XXX is the host's IPv4 last octet (001-254) for direct-probe routing.
// YYY is a cryptographically secure random PIN (100-999).
func GeneratePairingCode(hostIP string) (string, int, int) {
	octet := 0
	ip := net.ParseIP(hostIP)
	if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() {
		primary := GetPrimaryLocalIP()
		ip = net.ParseIP(primary)
	}

	if ip != nil && ip.To4() != nil {
		ip4 := ip.To4()
		octet = int(ip4[3])
	}
	if octet <= 0 || octet >= 255 {
		// Fallback random octet if no valid local network IP
		n, _ := rand.Int(rand.Reader, big.NewInt(253))
		octet = int(n.Int64()) + 1
	}

	// Random 3-digit PIN between 100 and 999
	nPin, _ := rand.Int(rand.Reader, big.NewInt(900))
	pin := int(nPin.Int64()) + 100

	code := fmt.Sprintf("%03d-%03d", octet, pin)
	return code, octet, pin
}

// NormalizePairingCode normalizes variations (e.g., "229481", "229-481", "229 481", "42-123")
// into standard "XXX-YYY" format.
func NormalizePairingCode(input string) (string, error) {
	cleaned := strings.TrimSpace(input)
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")

	if len(cleaned) < 4 || len(cleaned) > 6 {
		return "", fmt.Errorf("pairing code must be 5 or 6 digits (e.g. 229-481), got '%s'", input)
	}

	// Check if all characters are digits
	for _, c := range cleaned {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("invalid character in pairing code '%s'", input)
		}
	}

	var octetStr, pinStr string
	if len(cleaned) == 6 {
		octetStr = cleaned[:3]
		pinStr = cleaned[3:]
	} else if len(cleaned) == 5 {
		// e.g. "42123" -> octet 42, pin 123
		octetStr = cleaned[:2]
		pinStr = cleaned[2:]
	} else {
		// len == 4: e.g. "5123" -> octet 5, pin 123
		octetStr = cleaned[:1]
		pinStr = cleaned[1:]
	}

	octetNum, err := strconv.Atoi(octetStr)
	if err != nil || octetNum < 1 || octetNum > 254 {
		return "", fmt.Errorf("invalid host octet (%d) in pairing code", octetNum)
	}

	pinNum, err := strconv.Atoi(pinStr)
	if err != nil || pinNum < 100 || pinNum > 999 {
		return "", fmt.Errorf("invalid PIN (%d) in pairing code", pinNum)
	}

	return fmt.Sprintf("%03d-%03d", octetNum, pinNum), nil
}

// ParsePairingCode extracts octet and PIN numbers from a pairing code
func ParsePairingCode(input string) (int, int, string, error) {
	normalized, err := NormalizePairingCode(input)
	if err != nil {
		return 0, 0, "", err
	}
	parts := strings.Split(normalized, "-")
	octet, _ := strconv.Atoi(parts[0])
	pin, _ := strconv.Atoi(parts[1])
	return octet, pin, normalized, nil
}

// ResolvePairingCode searches for a peer matching the given 6-digit code.
// Layer 1: Fast direct-octet unicast probe (<50ms, bypasses AP client isolation).
// Layer 2: Parallel multi-vector discovery (UDP broadcast + multicast + active sweep).
func ResolvePairingCode(ctx context.Context, code string, timeout time.Duration) (*Peer, error) {
	octet, _, normalized, err := ParsePairingCode(code)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(timeout)
	ctxTimeout, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	resultChan := make(chan *Peer, 4)
	var once sync.Once
	foundPeer := func(p *Peer) {
		once.Do(func() {
			select {
			case resultChan <- p:
			default:
			}
		})
	}

	// -------------------------------------------------------------
	// LAYER 1: DIRECT-OCTET UNICAST PROBE (AP-Isolation & Link-Local Bypass)
	// -------------------------------------------------------------
	targets := GetActiveNetworkTargets()
	var directWg sync.WaitGroup
	testedCandidates := make(map[string]bool)
	var candMu sync.Mutex

	probeCandidate := func(ip string) {
		if ip == "" || IsLocalNetworkIP(ip) {
			return
		}
		candMu.Lock()
		if testedCandidates[ip] {
			candMu.Unlock()
			return
		}
		testedCandidates[ip] = true
		candMu.Unlock()

		directWg.Add(1)
		go func(candidate string) {
			defer directWg.Done()
			p := probePeerForCode(ctxTimeout, candidate, normalized, 300*time.Millisecond)
			if p != nil {
				foundPeer(p)
			}
		}(ip)
	}

	// 1a) Subnet octet probe: <subnet>.<octet>
	for _, target := range targets {
		if len(target.LocalIP) < 4 {
			continue
		}
		candidateIP := net.IPv4(target.LocalIP[0], target.LocalIP[1], target.LocalIP[2], byte(octet)).String()
		probeCandidate(candidateIP)
	}

	// 1b) ARP neighbor probe: any connected neighbor whose last octet matches `octet`
	arpIPs := GetARPTableIPs()
	for _, aip := range arpIPs {
		parsed := net.ParseIP(aip)
		if parsed != nil && parsed.To4() != nil && parsed.To4()[3] == byte(octet) {
			probeCandidate(aip)
		}
	}

	// Wait up to 350ms for direct probe to finish
	directProbeDone := make(chan struct{})
	go func() {
		directWg.Wait()
		close(directProbeDone)
	}()

	select {
	case p := <-resultChan:
		return p, nil
	case <-directProbeDone:
		// Direct probe done, check if result arrived
		select {
		case p := <-resultChan:
			return p, nil
		default:
		}
	case <-time.After(350 * time.Millisecond):
	}

	// -------------------------------------------------------------
	// LAYER 2: MULTI-VECTOR DISCOVERY & SWEEP FALLBACK
	// -------------------------------------------------------------
	// If direct probe didn't match (e.g. host is on different subnet or behind router),
	// run UDP broadcast/multicast listener and active subnet sweep concurrently.
	var sweepWg sync.WaitGroup
	semaphore := make(chan struct{}, 64)

	// A) Passive UDP/Multicast Listener
	go func() {
		lAddr := net.UDPAddr{Port: DiscoveryPort, IP: net.IPv4zero}
		conn, err := net.ListenUDP("udp4", &lAddr)
		if err != nil {
			return
		}
		defer conn.Close()

		go func() {
			<-ctxTimeout.Done()
			_ = conn.Close()
		}()

		buf := make([]byte, 2048)
		for {
			n, srcAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				break
			}
			var p Peer
			if err := json.Unmarshal(buf[:n], &p); err == nil && p.Version == AppVersion {
				targetIP := p.HostIP
				if targetIP == "" || targetIP == "127.0.0.1" {
					targetIP = srcAddr.IP.String()
				}
				if !IsLocalNetworkIP(targetIP) {
					go func(ip string) {
						if probed := probePeerForCode(ctxTimeout, ip, normalized, 250*time.Millisecond); probed != nil {
							foundPeer(probed)
						}
					}(targetIP)
				}
			}
		}
	}()

	// B) Concurrently sweep all subnet targets
	for _, target := range targets {
		for _, hostIP := range target.SweepIPs {
			if IsLocalNetworkIP(hostIP) {
				continue
			}
			sweepWg.Add(1)
			go func(ip string) {
				defer sweepWg.Done()
				select {
				case <-ctxTimeout.Done():
					return
				case semaphore <- struct{}{}:
				}
				defer func() { <-semaphore }()

				p := probePeerForCode(ctxTimeout, ip, normalized, 250*time.Millisecond)
				if p != nil {
					foundPeer(p)
				}
			}(hostIP)
		}
	}

	// Monitor completion or timeout
	allDone := make(chan struct{})
	go func() {
		sweepWg.Wait()
		close(allDone)
	}()

	select {
	case p := <-resultChan:
		return p, nil
	case <-allDone:
		select {
		case p := <-resultChan:
			return p, nil
		default:
			return nil, fmt.Errorf("device with pairing code '%s' not found on local network", normalized)
		}
	case <-ctxTimeout.Done():
		select {
		case p := <-resultChan:
			return p, nil
		default:
			return nil, fmt.Errorf("timed out searching for pairing code '%s' on local network", normalized)
		}
	}
}

// probePeerForCode connects to `ip:DiscoveryPort` via TCP and verifies pairing code
func probePeerForCode(ctx context.Context, ip, expectedCode string, timeout time.Duration) *Peer {
	d := net.Dialer{Timeout: timeout}
	target := net.JoinHostPort(ip, strconv.Itoa(int(DiscoveryPort)))
	conn, err := d.DialContext(ctx, "tcp4", target)
	if err != nil {
		return nil
	}
	defer conn.Close()

	// Send pairing code query to unmask private transfer offer
	query := DiscoveryQuery{PairingCode: expectedCode}
	queryBytes, _ := json.Marshal(query)
	_ = conn.SetWriteDeadline(time.Now().Add(timeout))
	_, _ = conn.Write(append(queryBytes, '\n'))

	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	respBytes, err := io.ReadAll(conn)
	if err != nil || len(respBytes) == 0 {
		return nil
	}

	var p Peer
	if err := json.Unmarshal(respBytes, &p); err == nil && p.Version == AppVersion {
		normCode, _ := NormalizePairingCode(p.PairingCode)
		if normCode == expectedCode || (p.Offer != nil && p.Offer.FileName != "[🔒 Private Transfer]") {
			p.HostIP = ip
			if p.PairingCode == "" {
				p.PairingCode = expectedCode
			}
			return &p
		}
	}
	return nil
}

// QueryPeerWithCode queries a specific host IP with a pairing code to authenticate and unmask peer details
func QueryPeerWithCode(ctx context.Context, hostIP, code string, timeout time.Duration) (*Peer, error) {
	normCode, err := NormalizePairingCode(code)
	if err != nil {
		return nil, err
	}
	p := probePeerForCode(ctx, hostIP, normCode, timeout)
	if p == nil {
		return nil, fmt.Errorf("device at %s rejected pairing code '%s'", hostIP, normCode)
	}
	return p, nil
}
