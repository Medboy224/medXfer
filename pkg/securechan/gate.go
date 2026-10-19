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
	// An address that authenticated a known key, or paired, in the last KnownFor gets the
	// larger quota KnownPerIPOneMinute: its failed handshakes are most likely a flaky network
	// or a restarted app, not a guesser (#93).
	DefaultKnownPerIPOneMinute = 60
	DefaultKnownFor            = 10 * time.Minute
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
//
// The gate decides before the handshake, so before it knows who connects: a paired device
// whose handshakes fail (Wi-Fi cut mid-handshake, app killed and restarted) would use up the
// quota of its address and be refused for up to a minute. So an address that authenticated
// recently gets KnownPerIPOneMinute instead (#93). MaxUnauth still bounds everyone.
//
// A handshake still running counts in its address's quota too: otherwise one address could
// take every place before any of its connections is charged (audit 5a, #156).
type Gate struct {
	MaxUnauth           int
	PerIPOneMinute      int
	KnownPerIPOneMinute int
	KnownFor            time.Duration
	now                 func() time.Time // tests

	mu         sync.Mutex
	active     int
	recent     map[string][]time.Time
	handshakes map[string]int       // address -> its handshakes still running, not charged yet
	known      map[string]time.Time // address -> its last authentication
	swept      time.Time            // last sweep of the tables (sweepLocked)
}

// NewGate returns a gate with the default limits.
func NewGate() *Gate {
	return &Gate{MaxUnauth: DefaultMaxUnauth, PerIPOneMinute: DefaultPerIPOneMinute,
		KnownPerIPOneMinute: DefaultKnownPerIPOneMinute, KnownFor: DefaultKnownFor}
}

// Admission is the place a connection holds in the gate during its handshake.
type Admission struct {
	g       *Gate
	ip      string
	once    sync.Once
	charged sync.Once
	running bool // counted in g.handshakes; guarded by g.mu
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
	if len(kept) == 0 {
		delete(g.recent, ip) // audit 5b (#156): an address does not stay once its minute is over
		return nil
	}
	g.recent[ip] = kept
	return kept
}

// sweepLocked drops, once a minute, the addresses that no longer count: those that never come
// back would otherwise stay in the tables for good (audit 5b, #156).
func (g *Gate) sweepLocked(t time.Time) {
	if t.Sub(g.swept) < time.Minute {
		return
	}
	g.swept = t
	for ip := range g.recent {
		g.recentLocked(ip, t)
	}
	for ip, at := range g.known {
		if t.Sub(at) >= g.KnownFor {
			delete(g.known, ip)
		}
	}
}

// endHandshakeLocked takes a's handshake out of its address's running ones.
func (a *Admission) endHandshakeLocked() {
	if !a.running {
		return
	}
	a.running = false
	if a.g.handshakes[a.ip]--; a.g.handshakes[a.ip] <= 0 {
		delete(a.g.handshakes, a.ip)
	}
}

// quotaLocked is the number of unauthenticated connections ip may open per minute: the
// larger quota while its last authentication is recent (#93).
func (g *Gate) quotaLocked(ip string, t time.Time) int {
	if at, ok := g.known[ip]; ok {
		if g.KnownPerIPOneMinute > g.PerIPOneMinute && t.Sub(at) < g.KnownFor {
			return g.KnownPerIPOneMinute
		}
		if t.Sub(at) >= g.KnownFor {
			delete(g.known, ip)
		}
	}
	return g.PerIPOneMinute
}

// Admit reserves a place for a connection from addr, before its handshake. It is refused if
// the address has used up its quota of unauthenticated connections this minute, counting its
// handshakes still running, or if too many connections are already waiting. The quota itself
// is only charged by Charge, once the handshake has shown the connection to be
// unauthenticated; a handshake that authenticates a key gives its share back (Release).
func (g *Gate) Admit(addr net.Addr) (*Admission, error) {
	ip := addr.String()
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	t := g.clock()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweepLocked(t)
	if q := g.quotaLocked(ip, t); q > 0 && len(g.recentLocked(ip, t))+g.handshakes[ip] >= q {
		return nil, ErrRateLimited
	}
	if g.MaxUnauth > 0 && g.active >= g.MaxUnauth {
		return nil, ErrTooManyUnauth
	}
	g.active++
	if g.handshakes == nil {
		g.handshakes = map[string]int{}
	}
	g.handshakes[ip]++
	return &Admission{g: g, ip: ip, running: true}, nil
}

// Charge counts the connection in its address's quota for the minute. Calling it more than
// once has no effect.
func (a *Admission) Charge() {
	a.charged.Do(func() {
		t := a.g.clock()
		a.g.mu.Lock()
		a.endHandshakeLocked()
		if a.g.recent == nil {
			a.g.recent = map[string][]time.Time{}
		}
		a.g.recent[a.ip] = append(a.g.recentLocked(a.ip, t), t)
		a.g.mu.Unlock()
	})
}

// Authenticated records that the connection's address authenticated a key: by a known key
// during the handshake, or by pairing afterwards. For KnownFor, the address gets the larger
// quota (#93).
func (a *Admission) Authenticated() {
	t := a.g.clock()
	a.g.mu.Lock()
	if a.g.known == nil {
		a.g.known = map[string]time.Time{}
	}
	a.g.known[a.ip] = t
	a.g.mu.Unlock()
}

// Release gives the place back. Calling it more than once has no effect.
func (a *Admission) Release() {
	a.once.Do(func() {
		a.g.mu.Lock()
		a.g.active--
		a.endHandshakeLocked()
		a.g.mu.Unlock()
	})
}
