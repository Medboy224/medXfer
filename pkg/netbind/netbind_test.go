package netbind

import (
	"net"
	"syscall"
	"testing"
)

// With a network set, every socket of a Dialer goes through bindFD with its handle; without,
// none does. Elsewhere than Android, bindFD is nil and nothing changes.
func TestControlTiesSocketsToTheNetwork(t *testing.T) {
	old := bindFD
	t.Cleanup(func() { bindFD = old; SetNetwork(0) })
	var got []uint64
	bindFD = func(h uint64, fd uintptr) error {
		got = append(got, h)
		return nil
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	dial := func() {
		c, err := Dialer(0).Dial("tcp4", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
	}
	dial()
	if len(got) != 0 {
		t.Fatalf("tied without a network: %v", got)
	}
	SetNetwork(0x95facade)
	dial()
	if len(got) != 1 || got[0] != 0x95facade {
		t.Fatalf("tied to %v, want the network given", got)
	}
	SetNetwork(0)
	dial()
	if len(got) != 1 {
		t.Fatalf("still tied once untied: %v", got)
	}
}

// A socket the platform refuses to tie is not used: the dial fails with its error.
func TestControlFailureFailsTheDial(t *testing.T) {
	old := bindFD
	t.Cleanup(func() { bindFD = old; SetNetwork(0) })
	bindFD = func(uint64, uintptr) error { return syscall.EPERM }
	SetNetwork(1)
	if _, err := Dialer(0).Dial("tcp4", "127.0.0.1:9"); err == nil {
		t.Fatal("a socket not tied to the network was used")
	}
}
