package transfer

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// valve lets a test stop a transfer at an exact point (#80): the receiver's connections read
// a budget of bytes in all, then every read waits until the test opens the valve. A closed
// connection (a cancel) ends its waiting read. Unlike watching the progress, which is
// reported every 200 ms, nothing can run past the point the test chose.
type valve struct {
	mu      sync.Mutex
	budget  int64
	reached chan struct{} // closed once the budget is spent
	opened  chan struct{} // closed by open
	once    sync.Once
	opening sync.Once
}

func newValve(budget int64) *valve {
	return &valve{budget: budget, reached: make(chan struct{}), opened: make(chan struct{})}
}

// dial is a Receiver.dial: connections that go through the valve.
func (v *valve) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	return &valveConn{Conn: c, v: v, closed: make(chan struct{})}, nil
}

// wait waits until the budget is spent.
func (v *valve) wait(t *testing.T) {
	t.Helper()
	select {
	case <-v.reached:
	case <-time.After(30 * time.Second):
		t.Fatal("the transfer never spent the valve's budget")
	}
}

func (v *valve) open() { v.opening.Do(func() { close(v.opened) }) }

type valveConn struct {
	net.Conn
	v         *valve
	closed    chan struct{}
	closeOnce sync.Once
}

func (c *valveConn) Read(p []byte) (int, error) {
	v := c.v
	v.mu.Lock()
	allowed := min(int64(len(p)), v.budget)
	v.budget -= allowed
	spent := v.budget == 0
	v.mu.Unlock()
	if spent {
		v.once.Do(func() { close(v.reached) })
	}
	if allowed == 0 {
		select {
		case <-v.opened:
			return c.Conn.Read(p)
		case <-c.closed:
			return 0, net.ErrClosed
		}
	}
	n, err := c.Conn.Read(p[:allowed])
	if short := allowed - int64(n); short > 0 {
		v.mu.Lock()
		v.budget += short // not read: still to spend
		v.mu.Unlock()
	}
	return n, err
}

func (c *valveConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.Conn.Close()
}
