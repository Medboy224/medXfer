package engine

import (
	"net"
	"testing"
)

func TestPortRebind(t *testing.T) {
	l0, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	addr := l0.Addr().String()
	l0.Close()

	for i := 0; i < 20; i++ {
		l, err := net.Listen("tcp4", addr)
		if err != nil {
			t.Fatalf("Iteration %d failed to rebind: %v", i, err)
		}
		go func() {
			c, err := l.Accept()
			if err == nil {
				c.Close()
			}
		}()
		client, err := net.Dial("tcp4", addr)
		if err == nil {
			client.Close()
		}
		l.Close()
	}
	t.Logf("20 consecutive rebinds to %s succeeded!", addr)
}
