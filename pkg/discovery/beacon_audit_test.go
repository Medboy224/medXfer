package discovery

import (
	"context"
	"encoding/json"
	"net"
	"runtime"
	"testing"
	"time"
)

// Audit 15a (#156): the search by pairing code hears today's beacon: the device that sent it
// is probed, at the address it sent from. Other packets are ignored.
func TestCodeSearchHearsTheBeacon(t *testing.T) {
	src := &net.UDPAddr{IP: net.IPv4(192, 0, 2, 9), Port: 50000}
	beacon, _ := json.Marshal(beaconMsg{V: 1, Role: "node", Name: "medXfer-3fa2", Port: 18887})
	if ip, ok := codeProbeTarget(beacon, src); !ok || ip != "192.0.2.9" {
		t.Fatalf("beacon: probe %q, %v; want 192.0.2.9", ip, ok)
	}
	for _, other := range []string{`{"v":2,"n":"x","p":1}`, `{"hello":1}`, `not json`} {
		if ip, ok := codeProbeTarget([]byte(other), src); ok {
			t.Errorf("%s: probe %q", other, ip)
		}
	}
}

// §3.7 (#156): Browse leaves no goroutine behind once it is over, even with a context that
// never ends.
func TestBrowseLeavesNoGoroutine(t *testing.T) {
	l, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := l.LocalAddr().(*net.UDPAddr).Port
	l.Close()
	b := beaconDiscoverer{port: port}
	before := runtime.NumGoroutine()
	for range 20 {
		_, _ = b.Browse(context.Background(), 10*time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if n := runtime.NumGoroutine() - before; n > 2 {
		t.Fatalf("%d goroutines left by 20 browses", n)
	}
}
