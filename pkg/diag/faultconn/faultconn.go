//go:build diag

// Package faultconn wraps a net.Conn with a deterministic fault plan (DEV-10): latency,
// jitter, bandwidth limit, cut after N bytes, connection reset. It exists only in builds
// with the "diag" tag; CI checks that it never reaches a production binary.
//
// Install a plan for every engine connection with engine.SetConnWrapper:
//
//	restore := engine.SetConnWrapper(func(_ engine.ConnRole, c net.Conn) net.Conn {
//		return faultconn.Wrap(c, plan)
//	})
//	defer restore()
package faultconn

import (
	"errors"
	"math/rand"
	"net"
	"sync"
	"time"
)

// ErrCut is returned once a connection reached Plan.CutAfter bytes.
var ErrCut = errors.New("faultconn: connection cut by fault plan")

// Plan describes the faults applied to one connection. The zero Plan changes nothing.
type Plan struct {
	Seed     int64         // seeds the jitter; the same seed gives the same delays
	Latency  time.Duration // added before every Read
	Jitter   time.Duration // extra delay in [0, Jitter), drawn from Seed
	Limiter  *Limiter      // bandwidth limit, shared between connections or not; nil = unlimited
	CutAfter int64         // bytes (read + written) after which the connection is closed; 0 = never
	Reset    bool          // at the cut, send a TCP RST instead of a normal close
}

// Limiter caps the throughput of all the connections that share it.
type Limiter struct {
	mu          sync.Mutex
	bytesPerSec float64
	next        time.Time // when the bytes accounted so far are "paid for"
}

// NewLimiter returns a limiter of bytesPerSec bytes per second, summed over its connections.
func NewLimiter(bytesPerSec int64) *Limiter {
	return &Limiter{bytesPerSec: float64(bytesPerSec)}
}

// burst is the largest single Read or Write let through a limiter: 1/20 s of traffic, so the
// rate stays smooth even when the caller hands over a large buffer.
func (l *Limiter) burst() int {
	b := int(l.bytesPerSec / 20)
	if b < 512 {
		b = 512
	}
	return b
}

// wait blocks until n more bytes fit in the rate.
func (l *Limiter) wait(n int) {
	l.mu.Lock()
	now := time.Now()
	if l.next.Before(now) {
		l.next = now
	}
	l.next = l.next.Add(time.Duration(float64(n) / l.bytesPerSec * float64(time.Second)))
	until := l.next
	l.mu.Unlock()
	if d := time.Until(until); d > 0 {
		time.Sleep(d)
	}
}

type conn struct {
	net.Conn
	plan Plan

	mu    sync.Mutex
	rng   *rand.Rand
	total int64
	cut   bool
}

// Wrap returns c with the faults of plan applied.
func Wrap(c net.Conn, plan Plan) net.Conn {
	return &conn{Conn: c, plan: plan, rng: rand.New(rand.NewSource(plan.Seed))}
}

func (c *conn) Read(b []byte) (int, error) {
	if d := c.delay(); d > 0 {
		time.Sleep(d)
	}
	b, err := c.budget(b)
	if err != nil {
		return 0, err
	}
	n, err := c.Conn.Read(b)
	c.account(n)
	return n, err
}

func (c *conn) Write(b []byte) (int, error) {
	written := 0
	for len(b) > 0 {
		part, err := c.budget(b)
		if err != nil {
			return written, err
		}
		n, err := c.Conn.Write(part)
		written += n
		c.account(n)
		if err != nil {
			return written, err
		}
		b = b[n:]
	}
	return written, nil
}

func (c *conn) delay() time.Duration {
	d := c.plan.Latency
	if c.plan.Jitter > 0 {
		c.mu.Lock()
		d += time.Duration(c.rng.Int63n(int64(c.plan.Jitter)))
		c.mu.Unlock()
	}
	return d
}

// budget shortens b to what the cut and the limiter allow, waiting for the limiter, and
// returns ErrCut once the connection has been cut.
func (c *conn) budget(b []byte) ([]byte, error) {
	c.mu.Lock()
	cut := c.cut
	left := c.plan.CutAfter - c.total
	c.mu.Unlock()
	if cut {
		return nil, ErrCut
	}
	if c.plan.CutAfter > 0 {
		if left <= 0 {
			c.doCut()
			return nil, ErrCut
		}
		if int64(len(b)) > left {
			b = b[:left]
		}
	}
	if l := c.plan.Limiter; l != nil && len(b) > 0 {
		if len(b) > l.burst() {
			b = b[:l.burst()]
		}
		l.wait(len(b))
	}
	return b, nil
}

func (c *conn) account(n int) {
	c.mu.Lock()
	c.total += int64(n)
	reached := c.plan.CutAfter > 0 && c.total >= c.plan.CutAfter
	c.mu.Unlock()
	if reached {
		c.doCut()
	}
}

func (c *conn) doCut() {
	c.mu.Lock()
	already := c.cut
	c.cut = true
	c.mu.Unlock()
	if already {
		return
	}
	if tc, ok := c.Conn.(*net.TCPConn); ok && c.plan.Reset {
		_ = tc.SetLinger(0) // Close then sends RST
	}
	_ = c.Conn.Close()
}
