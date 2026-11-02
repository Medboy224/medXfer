package discovery

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"net"
	"strconv"
	"testing"
	"time"
)

// freeAddr is a loopback address with a free port below every system's ephemeral range (Linux
// from 32768, Windows and macOS from 49152). A port the system gave from that range may go to
// another test's connection between the moment it is freed here and the moment the responder
// binds it: the packages' tests run side by side (CI of #253, port 45117 found held).
func freeAddr(t *testing.T) string {
	t.Helper()
	start := 20000 + rand.IntN(10000)
	for p := start; p < start+200; p++ {
		l, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(p))
		if err == nil {
			l.Close()
			return l.Addr().String()
		}
	}
	t.Fatal("no free port")
	return ""
}

// Audit 15b (#156): visibility turned off then on again at once answers the probes: the new
// responder does not find its port still held by the old one.
func TestProbesComeBackAtOnce(t *testing.T) {
	addr := freeAddr(t)
	s := NewDiscoveryServer("node", 18887, &TransferOffer{})
	for i := range 20 {
		ctx, cancel := context.WithCancel(context.Background())
		s.startProbes(ctx, addr)
		if i < 19 {
			cancel() // off, then on again at once
			continue
		}
		defer cancel()
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := net.DialTimeout("tcp4", addr, time.Second)
		if err == nil {
			_ = c.SetDeadline(time.Now().Add(2 * time.Second))
			_ = json.NewEncoder(c).Encode(DiscoveryQuery{})
			var p Peer
			err = json.NewDecoder(c).Decode(&p)
			c.Close()
			if err == nil && p.Port == 18887 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no answer to a probe after visibility came back: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
