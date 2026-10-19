package discovery

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

// Audit 15b (#156): visibility turned off then on again at once answers the probes: the new
// responder does not find its port still held by the old one.
func TestProbesComeBackAtOnce(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
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
