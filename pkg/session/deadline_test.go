package session

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// deadlineConn remembers the write deadline asked for, as securechan.Conn does.
type deadlineConn struct {
	net.Conn
	mu      sync.Mutex
	writeAt time.Time
}

func (c *deadlineConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeAt = t
	c.mu.Unlock()
	return c.Conn.SetWriteDeadline(t)
}

func (c *deadlineConn) WriteDeadline() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeAt
}

// Audit 7 (#156): Send bounds its write, then gives the caller's write deadline back instead
// of clearing it.
func TestSendRestoresWriteDeadline(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() { _, _ = io.Copy(io.Discard, b) }()
	conn := &deadlineConn{Conn: a}
	at := time.Now().Add(time.Hour)
	_ = conn.SetWriteDeadline(at)
	ch := NewChannel(conn)
	if err := ch.Send(Message{Type: "ping"}); err != nil {
		t.Fatal(err)
	}
	if got := conn.WriteDeadline(); !got.Equal(at) {
		t.Fatalf("write deadline after Send: %v, want the caller's %v", got, at)
	}
}

// A caller's deadline earlier than Send's own still bounds the write.
func TestSendKeepsEarlierWriteDeadline(t *testing.T) {
	a, b := net.Pipe() // nobody reads b: the write blocks
	defer a.Close()
	defer b.Close()
	conn := &deadlineConn{Conn: a}
	_ = conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	ch := NewChannel(conn)
	start := time.Now()
	if err := ch.Send(Message{Type: "ping"}); err == nil {
		t.Fatal("the write passed a deadline")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the caller's earlier deadline was replaced: the write ended after %v", d)
	}
}

// §3.6 (#156): SetMaxControl may run while another goroutine sends (go test -race).
func TestSetMaxControlWhileSending(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() { _, _ = io.Copy(io.Discard, b) }()
	ch := NewChannel(a)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			ch.SetMaxControl(uint32(1<<20 + i))
		}
	}()
	for i := 0; i < 100; i++ {
		_ = ch.Send(Message{Type: "ping"})
	}
	wg.Wait()
}
