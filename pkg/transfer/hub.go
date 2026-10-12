package transfer

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/securechan"
)

// DefaultJoinTimeout bounds the wait for the first frame of an authenticated connection: a
// data connection must name its transfer within 5 s (CHAN-06). Unauthenticated connections
// are already bounded by securechan.UnauthTimeout.
const DefaultJoinTimeout = 5 * time.Second

var ErrDuplicateTransfer = errors.New("transfer: a transfer with this id is already registered")

// firstFrame is what may open a connection on the device's port.
var firstFrame = protocol.Limits{
	protocol.TypeControl:  protocol.MaxControl,
	protocol.TypeDataJoin: protocol.DataJoinLen,
}

// Hub owns the device's single listener (CHAN-01). Each connection first goes through the
// secure channel, then its first frame says what it is:
//   - CONTROL: a pairing or a session. The connection is handed, unread, to the control
//     listener (Control).
//   - DATA_JOIN{transfer_id}: a data connection. It is handed to the transfer registered under
//     that id, and only if its key is the peer of that transfer (CHAN-06); otherwise it is
//     closed.
type Hub struct {
	cfg securechan.Config
	// JoinTimeout defaults to DefaultJoinTimeout (a parameter, rule R4).
	JoinTimeout time.Duration
	// Wrap, when set, wraps each accepted connection before its handshake: the fault
	// injection of diag builds acts on the network bytes (engine.WrapAccepted).
	Wrap func(net.Conn) net.Conn

	mu        sync.Mutex
	addr      net.Addr
	control   *Listener
	transfers map[ID]*Listener
}

// NewHub returns a hub whose connections use cfg. The peers of registered transfers are
// added to the keys cfg accepts as authenticated (securechan.Config.Known).
func NewHub(cfg securechan.Config) *Hub {
	return &Hub{cfg: cfg, transfers: map[ID]*Listener{}}
}

// Known reports whether fp is the peer of a registered transfer.
func (h *Hub) Known(fp identity.Fingerprint) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, l := range h.transfers {
		if l.peer == fp {
			return true
		}
	}
	return false
}

func (h *Hub) config() securechan.Config {
	cfg := h.cfg
	outer := cfg.Known
	cfg.Known = func(fp identity.Fingerprint) bool {
		return h.Known(fp) || (outer != nil && outer(fp))
	}
	return cfg
}

// Addr is the address of the listener the hub serves, or nil before Serve.
func (h *Hub) Addr() net.Addr {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.addr
}

// Serve accepts connections on ln until it is closed. Each connection is handled in its own
// goroutine, so that a slow handshake never holds up the others.
func (h *Hub) Serve(ln net.Listener) error {
	h.mu.Lock()
	h.addr = ln.Addr()
	h.mu.Unlock()
	for {
		raw, err := ln.Accept()
		if err != nil {
			return err
		}
		go h.handle(raw)
	}
}

func (h *Hub) handle(raw net.Conn) {
	if h.Wrap != nil {
		raw = h.Wrap(raw)
	}
	conn, err := securechan.Server(context.Background(), raw, h.config())
	if err != nil {
		return
	}
	auth := conn.State() == securechan.Auth
	if auth {
		timeout := h.JoinTimeout
		if timeout <= 0 {
			timeout = DefaultJoinTimeout
		}
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
	}
	hdr, err := protocol.ReadFrameHeader(conn, firstFrame)
	if err != nil {
		conn.Close()
		return
	}
	switch hdr.Type {
	case protocol.TypeControl:
		if auth {
			_ = conn.SetReadDeadline(time.Time{})
		}
		conn.Unread(protocol.EncodeHeader(hdr))
		h.mu.Lock()
		l := h.control
		h.mu.Unlock()
		if l == nil {
			conn.Close()
			return
		}
		l.deliver(conn)
	case protocol.TypeDataJoin:
		var id ID
		if hdr.PayloadLen != protocol.DataJoinLen {
			conn.Close()
			return
		}
		if _, err := io.ReadFull(conn, id[:]); err != nil {
			conn.Close()
			return
		}
		h.mu.Lock()
		l := h.transfers[id]
		h.mu.Unlock()
		if l == nil || conn.PeerFingerprint() != l.peer {
			conn.Close() // CHAN-06: only the peer of this transfer may join it
			return
		}
		_ = conn.SetReadDeadline(time.Time{})
		l.deliver(conn)
	}
}

// Control returns the listener of control connections, already secured, their first frame
// unread. There is one at a time: a new one replaces the previous, which is closed.
func (h *Hub) Control() *Listener {
	l := newListener(h, ID{}, identity.Fingerprint{})
	h.mu.Lock()
	old := h.control
	h.control = l
	h.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return l
}

// Register makes the transfer id joinable by peer, and returns the listener of its data
// connections. Closing the listener unregisters the transfer.
func (h *Hub) Register(id ID, peer identity.Fingerprint) (*Listener, error) {
	l := newListener(h, id, peer)
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, dup := h.transfers[id]; dup {
		return nil, ErrDuplicateTransfer
	}
	h.transfers[id] = l
	return l, nil
}

func (h *Hub) remove(l *Listener) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if l.control {
		if h.control == l {
			h.control = nil
		}
		return
	}
	if h.transfers[l.id] == l {
		delete(h.transfers, l.id)
	}
}

// Listener is a net.Listener of the connections the hub routes to one consumer: the control
// connections, or the data connections of one transfer. Accept returns *securechan.Conn.
type Listener struct {
	hub     *Hub
	id      ID
	peer    identity.Fingerprint
	control bool

	ch   chan net.Conn
	done chan struct{}

	mu     sync.Mutex
	closed bool
}

func newListener(h *Hub, id ID, peer identity.Fingerprint) *Listener {
	return &Listener{hub: h, id: id, peer: peer, control: id.IsZero(),
		ch: make(chan net.Conn, 32), done: make(chan struct{})}
}

func (l *Listener) deliver(c net.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		c.Close()
		return
	}
	select {
	case l.ch <- c:
	default:
		c.Close() // a consumer this far behind gets no more connections
	}
}

// Accept waits for the next connection routed to this listener.
func (l *Listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close unregisters the listener; connections still waiting are closed.
func (l *Listener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	close(l.done)
	l.mu.Unlock()
	l.hub.remove(l)
	for {
		select {
		case c := <-l.ch:
			c.Close()
		default:
			return nil
		}
	}
}

// Addr is the address of the hub's listener.
func (l *Listener) Addr() net.Addr {
	if a := l.hub.Addr(); a != nil {
		return a
	}
	return &net.TCPAddr{}
}

// JoinFunc secures a data connection to a peer's device port with cfg, then names the
// transfer it joins (DATA_JOIN). It has the shape of engine.SecureFunc: the engine's receiver
// uses it on every connection it opens.
func JoinFunc(cfg securechan.Config, id ID) func(context.Context, net.Conn) (net.Conn, error) {
	return func(ctx context.Context, c net.Conn) (net.Conn, error) {
		sc, err := securechan.Client(ctx, c, cfg)
		if err != nil {
			return nil, err
		}
		if err := protocol.WriteFrame(sc, protocol.TypeDataJoin, id[:]); err != nil {
			sc.Close()
			return nil, err
		}
		return sc, nil
	}
}
