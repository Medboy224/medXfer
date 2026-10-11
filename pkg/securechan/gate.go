package securechan

import (
	"errors"
	"net"
	"sync"
	"time"
)

// Defaults of the admission gate (bible chapter 12; parameters, rule R4).
const (
	DefaultMaxUnauth      = 16 // LIM-01: unauthenticated connections at the same time
	DefaultPerIPOneMinute = 5  // LIM-01: new connections per minute and per IP
)

var (
	ErrTooManyUnauth = errors.New("securechan: too many unauthenticated connections")
	ErrRateLimited   = errors.New("securechan: too many new connections from this address")
)

// Gate admits incoming connections before authentication (LIM-01): at most MaxUnauth at a
// time, from the handshake until the connection is authenticated or closed, and at most
// PerIPOneMinute new connections per minute from one IP address. A connection whose peer is
// trusted leaves the gate as soon as its handshake ends.
type Gate struct {
	MaxUnauth      int
	PerIPOneMinute int
	now            func() time.Time // tests

	mu     sync.Mutex
	active int
	recent map[string][]time.Time
}

// NewGate returns a gate with the default limits.
func NewGate() *Gate {
	return &Gate{MaxUnauth: DefaultMaxUnauth, PerIPOneMinute: DefaultPerIPOneMinute}
}

// Admit reserves a place for a connection from addr. release gives it back; calling it more
// than once has no effect.
func (g *Gate) Admit(addr net.Addr) (release func(), err error) {
	ip := addr.String()
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	now := time.Now
	if g.now != nil {
		now = g.now
	}
	t := now()

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.recent == nil {
		g.recent = map[string][]time.Time{}
	}
	kept := g.recent[ip][:0]
	for _, at := range g.recent[ip] {
		if t.Sub(at) < time.Minute {
			kept = append(kept, at)
		}
	}
	g.recent[ip] = kept
	if g.PerIPOneMinute > 0 && len(kept) >= g.PerIPOneMinute {
		return nil, ErrRateLimited
	}
	if g.MaxUnauth > 0 && g.active >= g.MaxUnauth {
		return nil, ErrTooManyUnauth
	}
	g.recent[ip] = append(kept, t)
	g.active++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.active--
			g.mu.Unlock()
		})
	}, nil
}
