package webshare

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// #313: the connections whose first byte is awaited are bounded. 64 mute ones keep maxSniffing
// at most, the others being closed at once, and a guest that comes after them still gets in.
func TestMuteConnectionsDoNotKeepAGuestOut(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := newSniffing(ln, "https://127.0.0.1/s")
	defer l.Close()

	const mute = 64
	var closed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < mute; i++ {
		c, err := net.Dial("tcp4", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
			var b [1]byte
			if _, err := c.Read(b[:]); err != nil && !isTimeout(err) {
				closed.Add(1) // closed by the listener, not by our deadline
			}
		}()
	}

	guest, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	if _, err := guest.Write([]byte{0x16, 0x03, 0x01}); err != nil {
		t.Fatal(err)
	}
	got := make(chan net.Conn, 1)
	go func() {
		c, _ := l.Accept()
		got <- c
	}()
	select {
	case c := <-got:
		var b [1]byte
		if c == nil {
			t.Fatal("no connection handed on")
		} else if _, err := c.Read(b[:]); err != nil || b[0] != 0x16 {
			t.Fatalf("handed on %x, %v; want the guest's TLS record", b, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the guest did not get in past the mute connections")
	}

	wg.Wait()
	if n := int(closed.Load()); n < mute-maxSniffing {
		t.Fatalf("%d mute connections closed, want at least %d: they are not bounded", n, mute-maxSniffing)
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}
