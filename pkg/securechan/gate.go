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
	DefaultPerIPOneMinute = 5  // LIM-01: new unauthenticated connections per minute and per IP
)

var (
	ErrTooManyUnauth = errors.New("securechan: too many unauthenticated connections")
	ErrRateLimited   = errors.New("securechan: too many new connections from this address")
)

// Gate admits incoming connections before authentication (LIM-01): at most MaxUnauth at a
// time, from the handshake until the connection is authenticated or closed, and at most
// PerIPOneMinute new unauthenticated connections per minute from one IP address.
//
// A connection whose peer is authenticated by its key during the handshake (a trusted or
// expected key) leaves the gate at once and is not counted in its address's quota: the limit
// is on unauthenticated connections, so a paired device may open as many data connections
// as it needs on the same port (CHAN-01).
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

// Admission is the place a connection holds in the gate during its handshake.
type Admission struct {
	g       *Gate
	ip      string
	once    sync.Once
	charged sync.Once
}

func (g *Gate) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// recentLocked drops the timestamps of ip older than a minute and returns the others.
func (g *Gate) recentLocked(ip string, t time.Time) []time.Time {
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
	return kept
}

// Admit reserves a place for a connection from addr, before its handshake. It is refused if
// the address has used up its quota of unauthenticated connections this minute, or if too
// many connections are already waiting. The quota itself is only charged by Charge, once the
// handshake has shown the connection to be unauthenticated.
func (g *Gate) Admit(addr net.Addr) (*Admission, error) {
	ip := addr.String()
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	t := g.clock()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.PerIPOneMinute > 0 && len(g.recentLocked(ip, t)) >= g.PerIPOneMinute {
		return nil, ErrRateLimited
	}
	if g.MaxUnauth > 0 && g.active >= g.MaxUnauth {
		return nil, ErrTooManyUnauth
	}
	g.active++
	return &Admission{g: g, ip: ip}, nil
}

// Charge counts the connection in its address's quota for the minute. Calling it more than
// once has no effect.
func (a *Admission) Charge() {
	a.charged.Do(func() {
		t := a.g.clock()
		a.g.mu.Lock()
		a.g.recent[a.ip] = append(a.g.recentLocked(a.ip, t), t)
		a.g.mu.Unlock()
	})
}

// Release gives the place back. Calling it more than once has no effect.
func (a *Admission) Release() {
	a.once.Do(func() {
		a.g.mu.Lock()
		a.g.active--
		a.g.mu.Unlock()
	})
}
