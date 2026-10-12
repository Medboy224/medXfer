package transfer

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
)

func newID(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// startHub serves a hub for device on a loopback port. Unknown keys are allowed (pairing),
// as on the daemon's port.
func startHub(t *testing.T, device *identity.Identity) (*Hub, string) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	h := NewHub(securechan.Config{Identity: device, AllowUnknown: true, Gate: securechan.NewGate()})
	go func() { _ = h.Serve(ln) }()
	return h, ln.Addr().String()
}

func dial(t *testing.T, addr string, cfg securechan.Config) *securechan.Conn {
	t.Helper()
	c, err := securechan.Dial(context.Background(), addr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func accept(t *testing.T, l net.Listener, within time.Duration) (net.Conn, bool) {
	t.Helper()
	got := make(chan net.Conn, 1)
	go func() {
		if c, err := l.Accept(); err == nil {
			got <- c
		}
	}()
	select {
	case c := <-got:
		t.Cleanup(func() { c.Close() })
		return c, true
	case <-time.After(within):
		return nil, false
	}
}

// closedByPeer reports whether the hub closed c: a read ends with an error, not a timeout.
func closedByPeer(c net.Conn) bool {
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := c.Read(make([]byte, 1))
	var ne net.Error
	return err != nil && !(errors.As(err, &ne) && ne.Timeout())
}

// A CONTROL connection reaches the control listener with its first frame unread.
func TestHubRoutesControl(t *testing.T) {
	device, peer := newID(t), newID(t)
	h, addr := startHub(t, device)
	control := h.Control()
	c := dial(t, addr, securechan.Config{Identity: peer, Expected: device.Fingerprint})
	go func() { _ = session.NewChannel(c).Send(session.Message{Type: "pair_request", DeviceName: "phone"}) }()
	sc, ok := accept(t, control, 5*time.Second)
	if !ok {
		t.Fatal("the control connection was not routed")
	}
	m, err := session.NewChannel(sc).Read()
	if err != nil || m.Type != "pair_request" || m.DeviceName != "phone" {
		t.Fatalf("control message: %+v, %v", m, err)
	}
	if fp := sc.(*securechan.Conn).PeerFingerprint(); fp != peer.Fingerprint {
		t.Fatal("the routed connection lost its peer key")
	}
}

// CHAN-06: a data connection reaches its transfer only from the transfer's peer, which is
// authenticated by its key at the handshake even though it is not a trusted device.
func TestHubDataJoin(t *testing.T) {
	device, peer, other := newID(t), newID(t), newID(t)
	h, addr := startHub(t, device)
	id, _ := NewID()
	tl, err := h.Register(id, peer.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Register(id, peer.Fingerprint); !errors.Is(err, ErrDuplicateTransfer) {
		t.Fatalf("second registration: %v", err)
	}

	join := func(who *identity.Identity, as ID) net.Conn {
		raw, err := net.Dial("tcp4", addr)
		if err != nil {
			t.Fatal(err)
		}
		c, err := JoinFunc(securechan.Config{Identity: who, Expected: device.Fingerprint}, as)(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}

	c := join(peer, id)
	if c.(*securechan.Conn).State() != securechan.Auth {
		t.Fatal("client side not authenticated")
	}
	go func() { _, _ = c.Write([]byte("chunk")) }()
	got, ok := accept(t, tl, 5*time.Second)
	if !ok {
		t.Fatal("the transfer's peer was not routed")
	}
	if sc := got.(*securechan.Conn); sc.State() != securechan.Auth || sc.PeerFingerprint() != peer.Fingerprint {
		t.Fatal("the data connection is not authenticated as the peer")
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(got, buf); err != nil || string(buf) != "chunk" {
		t.Fatalf("payload after DATA_JOIN: %q, %v", buf, err)
	}

	// Another key, or an unknown transfer: closed, never routed.
	for name, c := range map[string]net.Conn{
		"another key":      join(other, id),
		"unknown transfer": join(peer, ID{1, 2, 3}),
	} {
		if !closedByPeer(c) {
			t.Errorf("%s: the connection was not closed", name)
		}
	}
	if _, ok := accept(t, tl, 300*time.Millisecond); ok {
		t.Fatal("a refused connection was routed to the transfer")
	}

	// Unregistered: the peer is no longer known, its joins are refused.
	tl.Close()
	if h.Known(peer.Fingerprint) {
		t.Fatal("the peer of a closed transfer is still known")
	}
	if !closedByPeer(join(peer, id)) {
		t.Fatal("a join to a closed transfer was accepted")
	}
}

// CHAN-06: an authenticated connection that names no transfer is closed after the timeout,
// as is a DATA_JOIN of the wrong length.
func TestHubFirstFrameRules(t *testing.T) {
	device, peer := newID(t), newID(t)
	h, addr := startHub(t, device)
	h.JoinTimeout = 300 * time.Millisecond
	id, _ := NewID()
	if _, err := h.Register(id, peer.Fingerprint); err != nil {
		t.Fatal(err)
	}
	cfg := securechan.Config{Identity: peer, Expected: device.Fingerprint}

	silent := dial(t, addr, cfg)
	start := time.Now()
	if !closedByPeer(silent) || time.Since(start) > 3*time.Second {
		t.Fatal("a silent connection was not closed at the join timeout")
	}

	short := dial(t, addr, cfg)
	_ = protocol.WriteFrame(short, protocol.TypeDataJoin, id[:8])
	if !closedByPeer(short) {
		t.Fatal("a short DATA_JOIN was not refused")
	}
}

// A whole offer through the device's single port: the sender serves on the hub's transfer
// listener, the receiver joins with DATA_JOIN on every connection, and once the sender stops
// the transfer is no longer registered.
func TestTransferThroughTheHub(t *testing.T) {
	offer, paths, contents := tree(t, []int{3<<20 + 12345, 10})
	p := newOfferPair(t)
	offer.TransferID = p.id.String()
	s, err := NewSender(offer, paths)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx, p.tl) }()

	out := t.TempDir()
	runOffer(t, p.receiver(out), p.addr, offer)
	checkFiles(t, out, offer, contents, nil)
	cancel()
	<-served
	if p.h.Known(p.peer.Fingerprint) {
		t.Fatal("the transfer stayed registered after the sender stopped")
	}
}

func TestIDRoundTrip(t *testing.T) {
	id, _ := NewID()
	back, err := ParseID(id.String())
	if err != nil || back != id {
		t.Fatalf("round trip: %v %v", back, err)
	}
	if _, err := ParseID("zz"); err == nil {
		t.Fatal("an invalid id was parsed")
	}
}
