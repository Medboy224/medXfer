package pairing

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
)

// Tests of the path by BLE (bible §6.6, §14.1): the platform's radio is left out, the messages
// go from one side to the other as the GATT connection would carry them.

var bleAll = BLERequest{Create: BLEBand24 | BLEBand5, Join: BLEBand24 | BLEBand5}

func bleNet() BLENetwork {
	return BLENetwork{SSID: "DIRECT-ZA-medXfer", Passphrase: "K3PQ8Z2MXA7D", Security: 1, Band: BLEBand5, Port: 18887,
		Addrs: []netip.Addr{netip.MustParseAddr("192.168.49.1")}}
}

// shownPINs records the PINs a responder shows, as its screen would.
type shownPINs struct {
	mu    sync.Mutex
	codes []string
}

func (s *shownPINs) show(code string, _ time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if code != "" {
		s.codes = append(s.codes, code)
	}
}

func (s *shownPINs) last() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.codes) == 0 {
		return ""
	}
	return s.codes[len(s.codes)-1]
}

// bleResponder is device o answering by BLE, with a PIN on demand shown on shown.
func bleResponder(t *testing.T, o Options, shown *shownPINs, cfg PINConfig) (*BLEResponder, *PINOnDemand) {
	t.Helper()
	cfg.Show = shown.show
	pins := NewPINOnDemand(cfg)
	r, err := NewBLEResponder(BLEResponderConfig{Self: o.Identity.Fingerprint, Suffix: "4f2a", Trust: o.Trust, Own: func() BLERequest { return bleAll },
		PIN: func(string) (*PIN, error) { return pins.Get() }})
	if err != nil {
		t.Fatal(err)
	}
	return r, pins
}

func bleClient(t *testing.T, o Options) *BLEClient {
	t.Helper()
	c, err := NewBLEClient(BLEClientConfig{Self: o.Identity.Fingerprint, Suffix: "9c1b", Trust: o.Trust})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// send gives conn msg and returns its reply, failing on a refusal.
func send(t *testing.T, conn *BLEConn, msg []byte) BLEStep {
	t.Helper()
	step, err := conn.Receive(msg)
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	return step
}

func refused(t *testing.T, conn *BLEConn, msg []byte, what string) {
	t.Helper()
	step, err := conn.Receive(msg)
	if !errors.Is(err, ErrRefused) || !bytes.Equal(step.Reply, bleEncode(bleRefuse)) {
		t.Fatalf("%s: not refused (%v)", what, err)
	}
}

func recognize(t *testing.T, c *BLEClient, conn *BLEConn) BLEPeer {
	t.Helper()
	hello, err := c.Hello()
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Recognize(send(t, conn, hello).Reply)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// BLE-03: the format, and what it refuses.
func TestBLEFormat(t *testing.T) {
	msg := bleEncode(blePINWanted, make([]byte, 16), []byte{0x4f, 0x2a})
	typ, f, err := bleDecode(msg)
	if err != nil || typ != blePINWanted || !sized(f, 16, 2) {
		t.Fatalf("round trip: %d %v %v", typ, f, err)
	}
	long := bleEncode(bleHello, make([]byte, BLEMaxMessage))
	for name, bad := range map[string][]byte{
		"empty":            {},
		"another version":  append([]byte{2}, msg[1:]...),
		"over 512 bytes":   long,
		"a length too big": {BLEVersion, bleHello, 5, 1, 2},
		"a length cut":     {BLEVersion, bleHello, 0x80},
		"not the shortest": {BLEVersion, bleHello, 0x80, 0x00},
	} {
		if _, _, err := bleDecode(bad); err == nil {
			t.Errorf("%s: read", name)
		}
	}
	// A field of the wrong size, or one too many, is refused by the responder.
	a := device(t, "a")
	r, _ := bleResponder(t, a, &shownPINs{}, PINConfig{})
	refused(t, r.Conn(), bleEncode(bleHello, make([]byte, 15)), "short challenge")
	refused(t, r.Conn(), bleEncode(bleHello, make([]byte, 16), nil), "one field too many")
	refused(t, r.Conn(), bleEncode(bleTrusted, make([]byte, 8), nil), "a request before hello")
}

// BLE-04, BLE-05: two devices of mutual trust recognize each other, then one gives the other its
// network, encrypted; each is authenticated by the pair's key.
func TestBLETrusted(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	trustThrough(t, a, b)
	r, _ := bleResponder(t, b, &shownPINs{}, PINConfig{})
	c := bleClient(t, a)
	conn := r.Conn()
	p := recognize(t, c, conn)
	if p.Suffix != "4f2a" || p.Trusted == nil || p.Trusted.Fingerprint != b.Identity.Fingerprint {
		t.Fatalf("recognized %+v", p)
	}
	req, err := c.Trusted(BLERequest{Join: BLEBand24 | BLEBand5}) // a PC: it joins
	if err != nil {
		t.Fatal(err)
	}
	step := send(t, conn, req)
	ar := step.Request
	if ar == nil || ar.Peer != a.Identity.Fingerprint || !ar.Trusted || ar.Outcome != BLEResponderCreates {
		t.Fatalf("request %+v", ar)
	}
	n := bleNet()
	reply, err := conn.Answer(ar.Outcome, &n)
	if err != nil {
		t.Fatal(err)
	}
	ans, err := c.Reply(reply)
	if err != nil || ans.Peer != b.Identity.Fingerprint || ans.Outcome != BLEResponderCreates || ans.Network == nil {
		t.Fatalf("answer %+v, %v", ans, err)
	}
	if got := *ans.Network; got.SSID != n.SSID || got.Passphrase != n.Passphrase || got.Port != n.Port || got.Addrs[0] != n.Addrs[0] {
		t.Fatalf("network %+v", got)
	}
}

// BLE-05: one side forgot the other: no recognition, and a request under the old key is refused
// (PAIR-09).
func TestBLETrustOneSided(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	trustThrough(t, a, b)
	c := bleClient(t, a)
	r, _ := bleResponder(t, b, &shownPINs{}, PINConfig{})
	conn := r.Conn()
	recognize(t, c, conn)
	req, _ := c.Trusted(bleAll)
	if _, err := b.Trust.Forget(a.Identity.Fingerprint); err != nil {
		t.Fatal(err)
	}
	refused(t, conn, req, "a device forgotten")
	if p := recognize(t, c, r.Conn()); p.Trusted != nil {
		t.Fatal("still recognized by a device that forgot it")
	}
}

// BLE-04: always 16 tags, new at each challenge, whatever the number of devices of trust; an
// unknown device recognizes nothing in them.
func TestBLEHelloTags(t *testing.T) {
	stranger := bleClient(t, device(t, "stranger"))
	for _, trusted := range []int{0, 1, 17} {
		b := device(t, "b")
		for i := 0; i < trusted; i++ {
			var fp identity.Fingerprint
			key := make([]byte, tagKeySize)
			_, _ = rand.Read(fp[:])
			_, _ = rand.Read(key)
			if err := b.Trust.Add(identity.Peer{Fingerprint: fp, TagKey: key, AddedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
		}
		r, _ := bleResponder(t, b, &shownPINs{}, PINConfig{})
		var replies [][]byte
		for range 2 {
			hello, _ := stranger.Hello()
			reply := send(t, r.Conn(), hello).Reply
			_, f, _ := bleDecode(reply)
			if !sized(f, 128, 2, 16) {
				t.Fatalf("%d trusted: hello_reply of %d fields", trusted, len(f))
			}
			if p, _ := stranger.Recognize(reply); p.Trusted != nil {
				t.Fatal("a stranger recognized something")
			}
			replies = append(replies, f[0])
		}
		if bytes.Equal(replies[0], replies[1]) {
			t.Fatalf("%d trusted: the same tags for two challenges", trusted)
		}
	}
}

// BLE-05, BLE-08: a request recorded does not open in another exchange, nor after the
// challenge's minute.
func TestBLEReplay(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	trustThrough(t, a, b)
	now := time.Now()
	r, err := NewBLEResponder(BLEResponderConfig{Self: b.Identity.Fingerprint, Suffix: "4f2a", Trust: b.Trust, Own: func() BLERequest { return bleAll }, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	c := bleClient(t, a)
	recognize(t, c, r.Conn())
	recorded, _ := c.Trusted(bleAll)

	conn := r.Conn()
	hello, _ := bleClient(t, a).Hello()
	send(t, conn, hello) // a new challenge from the responder
	refused(t, conn, recorded, "a request replayed")

	late := r.Conn()
	c2 := bleClient(t, a)
	recognize(t, c2, late)
	req, _ := c2.Trusted(bleAll)
	now = now.Add(bleChallengeAge + time.Second)
	refused(t, late, req, "a request past the challenge's minute")
}

// pinRun runs the path of the PIN up to the request, with the PIN typed.
func pinRun(t *testing.T, c *BLEClient, conn *BLEConn, typed string) (*BLEAuthRequest, error) {
	t.Helper()
	wanted, _ := c.PINWanted()
	step, err := conn.Receive(wanted)
	if err != nil {
		return nil, err
	}
	if err := c.PINReady(step.Reply); err != nil {
		return nil, err
	}
	pin, err := c.PIN(typed)
	if err != nil {
		return nil, err
	}
	step, err = conn.Receive(pin)
	if err != nil {
		return nil, err
	}
	confirm, err := c.PINReply(step.Reply, BLERequest{Join: BLEBand24})
	if err != nil {
		return nil, err
	}
	step, err = conn.Receive(confirm)
	return step.Request, err
}

// BLE-06, BLE-07, BLE-10: an unknown device pairs by the PIN the responder shows; the keys go
// encrypted, the PIN is used, and the initiator creates the network when it alone can.
func TestBLEPIN(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	shown := &shownPINs{}
	r, pins := bleResponder(t, b, shown, PINConfig{})
	r.cfg.Own = func() BLERequest { return BLERequest{Join: BLEBand24 | BLEBand5} } // a PC that cannot create at 5 GHz, here not at all
	c := bleClient(t, a)
	c.cfg.Trust = nil
	conn := r.Conn()

	wanted, _ := c.PINWanted()
	step := send(t, conn, wanted)
	if err := c.PINReady(step.Reply); err != nil {
		t.Fatal(err)
	}
	pin, _ := c.PIN(FormatPIN(shown.last()))
	step = send(t, conn, pin)
	confirm, err := c.PINReply(step.Reply, BLERequest{Create: BLEBand24 | BLEBand5, Join: BLEBand24 | BLEBand5})
	if err != nil {
		t.Fatal(err)
	}
	ar := send(t, conn, confirm).Request
	if ar == nil || ar.Peer != a.Identity.Fingerprint || ar.Trusted || ar.Outcome != BLEInitiatorCreates {
		t.Fatalf("request %+v", ar)
	}
	if pins.Current() != nil {
		t.Fatal("the PIN is still live once used (PAIR-02)")
	}
	reply, err := conn.Answer(BLEInitiatorCreates, nil)
	if err != nil {
		t.Fatal(err)
	}
	ans, err := c.Reply(reply)
	// The initiator creates the network, and learns on which band: the responder joins 5 GHz.
	if err != nil || ans.Peer != b.Identity.Fingerprint || ans.Outcome != BLEInitiatorCreates || ans.Band != BLEBand5 || ans.Network != nil {
		t.Fatalf("answer %+v, %v", ans, err)
	}
	netMsg, err := c.Network(bleNet())
	if err != nil {
		t.Fatal(err)
	}
	step = send(t, conn, netMsg)
	if step.Network == nil || step.Network.SSID != "DIRECT-ZA-medXfer" {
		t.Fatalf("network %+v", step.Network)
	}
	if err := c.Ack(step.Reply); err != nil {
		t.Fatal(err)
	}
}

// BLE-07, PAIR-04: three wrong PINs by BLE destroy the PIN, and a new one is drawn; an attempt
// abandoned after the reply counts.
func TestBLEWrongPIN(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	shown := &shownPINs{}
	r, _ := bleResponder(t, b, shown, PINConfig{})
	var first string
	for i := 0; i < PINAttempts; i++ {
		conn := r.Conn()
		_, err := pinRun(t, bleClient(t, a), conn, "000000")
		conn.Close() // the platform saw the connection go: the attempt answered counts
		if !errors.Is(err, ErrRefused) {
			t.Fatal(err)
		}
		if i == 0 {
			first = shown.last()
		}
	}
	if shown.last() == first || shown.last() == "" {
		t.Fatal("the PIN was not destroyed after three wrong attempts")
	}
	// The PIN destroyed no longer pairs; the new one does.
	old := r.Conn()
	if _, err := pinRun(t, bleClient(t, a), old, first); !errors.Is(err, ErrRefused) {
		t.Fatal("the destroyed PIN still pairs")
	}
	old.Close()
	if ar, err := pinRun(t, bleClient(t, a), r.Conn(), shown.last()); err != nil || ar == nil {
		t.Fatalf("the new PIN does not pair: %v", err)
	}
}

// PAIR-07: a PIN no longer valid answers on a decoy, as a wrong PIN would.
func TestBLEDecoy(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	shown := &shownPINs{}
	r, _ := bleResponder(t, b, shown, PINConfig{TTL: 50 * time.Millisecond})
	c := bleClient(t, a)
	conn := r.Conn()
	wanted, _ := c.PINWanted()
	if err := c.PINReady(send(t, conn, wanted).Reply); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond) // the PIN expires
	pin, _ := c.PIN(shown.last())
	step := send(t, conn, pin) // a reply, not a refusal: the client cannot tell
	if typ, _, _ := bleDecode(step.Reply); typ != blePINReply {
		t.Fatalf("type %d", typ)
	}
	if _, err := c.PINReply(step.Reply, bleAll); !errors.Is(err, ErrRefused) {
		t.Fatal("an expired PIN confirmed")
	}
}

// BLE-03, BLE-08: a message altered, cut or lengthened is refused; a reply altered too.
func TestBLETampered(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	trustThrough(t, a, b)
	r, _ := bleResponder(t, b, &shownPINs{}, PINConfig{})
	flip := func(m []byte, at int) []byte {
		m = bytes.Clone(m)
		m[at] ^= 1
		return m
	}
	for name, change := range map[string]func([]byte) []byte{
		"a bit of the ciphertext": func(m []byte) []byte { return flip(m, len(m)-3) },
		"a bit of the tag":        func(m []byte) []byte { return flip(m, 3) },
		"cut":                     func(m []byte) []byte { return m[:len(m)-1] },
		"lengthened":              func(m []byte) []byte { return append(bytes.Clone(m), 0) },
		"another type":            func(m []byte) []byte { m = bytes.Clone(m); m[1] = blePINConfirm; return m },
	} {
		c := bleClient(t, a)
		conn := r.Conn()
		recognize(t, c, conn)
		req, _ := c.Trusted(bleAll)
		refused(t, conn, change(req), name)
	}
	c := bleClient(t, a)
	conn := r.Conn()
	recognize(t, c, conn)
	req, _ := c.Trusted(bleAll)
	send(t, conn, req)
	n := bleNet()
	reply, _ := conn.Answer(BLEResponderCreates, &n)
	if _, err := c.Reply(flip(reply, len(reply)-1)); !errors.Is(err, ErrRefused) {
		t.Fatal("an altered reply was read")
	}
}

// BLE-06: a relay that changes a nonce on the way breaks the exchange: the two sides no longer
// share the sid, the confirmation fails.
func TestBLERelayChangesNonce(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	shown := &shownPINs{}
	r, _ := bleResponder(t, b, shown, PINConfig{})
	c := bleClient(t, a)
	conn := r.Conn()
	wanted, _ := c.PINWanted()
	ready := send(t, conn, wanted).Reply
	ready[len(ready)-1] ^= 1 // n_R changed
	if err := c.PINReady(ready); err != nil {
		t.Fatal(err)
	}
	pin, _ := c.PIN(shown.last())
	if _, err := c.PINReply(send(t, conn, pin).Reply, bleAll); !errors.Is(err, ErrRefused) {
		t.Fatal("confirmed through a relay that changed a nonce")
	}
}

// BLE-07: one request of a PIN at a time; "Refuser" refuses it.
func TestBLEOnePINRequest(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	shown := &shownPINs{}
	r, _ := bleResponder(t, b, shown, PINConfig{})
	first, second := r.Conn(), r.Conn()
	c1, c2 := bleClient(t, a), bleClient(t, a)
	w1, _ := c1.PINWanted()
	if err := c1.PINReady(send(t, first, w1).Reply); err != nil {
		t.Fatal(err)
	}
	w2, _ := c2.PINWanted()
	refused(t, second, w2, "a second request meanwhile")
	r.RefusePIN()
	pin, _ := c1.PIN(shown.last())
	refused(t, first, pin, "a request refused")
	third := r.Conn()
	w3, _ := bleClient(t, a).PINWanted()
	send(t, third, w3) // the screen is free again
}

// BLE-07, PAIR-04: one PIN, one count of attempts. Two wrong PINs over TLS and one by BLE
// destroy the PIN; the bound of 18 attempts a wait holds for both paths together.
func TestBLEAndTLSShareAttempts(t *testing.T) {
	responder := device(t, "pc")
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	p, screen := newPIN(t, PINConfig{})
	responder.PIN = p
	code := next(t, screen).code
	done := serve(ln, nil, responder)
	for i := 0; i < PINAttempts-1; i++ {
		if _, err := JoinPIN(context.Background(), ln.Addr().String(), wrong(code), device(t, "attacker")); !errors.Is(err, ErrRefused) {
			t.Fatalf("over TLS: %v", err)
		}
	}
	r, err := NewBLEResponder(BLEResponderConfig{Self: responder.Identity.Fingerprint, Suffix: "4f2a", Trust: responder.Trust, Own: func() BLERequest { return bleAll },
		PIN: func(string) (*PIN, error) { return p, nil }})
	if err != nil {
		t.Fatal(err)
	}
	conn := r.Conn()
	if _, err := pinRun(t, bleClient(t, device(t, "attacker")), conn, wrong(code)); !errors.Is(err, ErrRefused) {
		t.Fatalf("by BLE: %v", err)
	}
	conn.Close()
	fresh := next(t, screen)
	if fresh.code == "" || fresh.code == code {
		t.Fatalf("after %d failures over both paths the screen shows %q", PINAttempts, fresh.code)
	}
	if _, err := JoinPIN(context.Background(), ln.Addr().String(), fresh.code, device(t, "phone")); err != nil {
		t.Fatalf("the new PIN: %v", err)
	}
	if s := <-done; s.err != nil {
		t.Fatal(s.err)
	}
}

// BLE-07: a connection gone without the platform seeing it does not hold the screen past a
// PIN's life; it is refused if it comes back.
func TestBLEStalePINRequest(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	shown := &shownPINs{}
	r, _ := bleResponder(t, b, shown, PINConfig{})
	now := time.Now()
	r.cfg.Now = func() time.Time { return now }
	gone, next := r.Conn(), r.Conn()
	c1 := bleClient(t, a)
	w1, _ := c1.PINWanted()
	if err := c1.PINReady(send(t, gone, w1).Reply); err != nil {
		t.Fatal(err)
	}
	now = now.Add(typingTime + time.Second)
	w2, _ := bleClient(t, a).PINWanted()
	send(t, next, w2)
	pin, _ := c1.PIN(shown.last())
	refused(t, gone, pin, "the request gone stale")
}

// Never in clear: no fingerprint of either side, no passphrase, in any message of either path.
func TestBLENothingStableInClear(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	shown := &shownPINs{}
	r, _ := bleResponder(t, b, shown, PINConfig{})
	var sent [][]byte
	c := bleClient(t, a)
	conn := r.Conn()
	wanted, _ := c.PINWanted()
	ready := send(t, conn, wanted).Reply
	_ = c.PINReady(ready)
	pin, _ := c.PIN(shown.last())
	replyPIN := send(t, conn, pin).Reply
	confirm, _ := c.PINReply(replyPIN, bleAll)
	send(t, conn, confirm)
	n := bleNet()
	reply, _ := conn.Answer(BLEResponderCreates, &n)
	sent = append(sent, wanted, ready, pin, replyPIN, confirm, reply)
	for _, m := range sent {
		for _, secret := range [][]byte{a.Identity.Fingerprint[:], b.Identity.Fingerprint[:], []byte(n.Passphrase), []byte(n.SSID)} {
			if bytes.Contains(m, secret[:12]) {
				t.Fatalf("%q in clear in a message", secret[:12])
			}
		}
	}
}

// Bible §6.6: who creates the network.
func TestBLECreator(t *testing.T) {
	phone := BLERequest{Create: BLEBand24 | BLEBand5, Join: BLEBand24 | BLEBand5}
	pc := BLERequest{Create: BLEBand24, Join: BLEBand24 | BLEBand5}
	old := BLERequest{Create: BLEBand24, Join: BLEBand24} // a phone at 2.4 GHz only
	cannot := BLERequest{Join: BLEBand24}                 // creates no network
	for _, tc := range []struct {
		i, r BLERequest
		want BLEOutcome
		band BLEBands
	}{
		{pc, phone, BLEResponderCreates, BLEBand5},    // the phone, at 5 GHz
		{phone, pc, BLEInitiatorCreates, BLEBand5},    // the phone, at 5 GHz
		{phone, phone, BLEResponderCreates, BLEBand5}, // both can: the responder
		{old, phone, BLEResponderCreates, BLEBand24},  // at 2.4 GHz: the old one cannot join 5 GHz
		{phone, cannot, BLEInitiatorCreates, BLEBand24},
		{pc, pc, BLEResponderCreates, BLEBand24}, // two PCs, at 2.4 GHz
		{cannot, cannot, BLENoNetwork, 0},
	} {
		if got, band := Creator(tc.i, tc.r); got != tc.want || band != tc.band {
			t.Errorf("Creator(%+v, %+v) = %d at %d, want %d at %d", tc.i, tc.r, got, band, tc.want, tc.band)
		}
	}
}

// BLE-09: only a network that holds together goes out.
func TestBLENetworkChecked(t *testing.T) {
	for name, change := range map[string]func(*BLENetwork){
		"no name":             func(n *BLENetwork) { n.SSID = "" },
		"a short passphrase":  func(n *BLENetwork) { n.Passphrase = "short" },
		"a passphrase of 64":  func(n *BLENetwork) { n.Passphrase = strings.Repeat("x", 64) },
		"an unknown security": func(n *BLENetwork) { n.Security = 4 },
		"two bands":           func(n *BLENetwork) { n.Band = BLEBand24 | BLEBand5 },
		"no port":             func(n *BLENetwork) { n.Port = 0 },
		"no address":          func(n *BLENetwork) { n.Addrs = nil },
		"an address unset":    func(n *BLENetwork) { n.Addrs = []netip.Addr{netip.IPv4Unspecified()} },
	} {
		n := bleNet()
		change(&n)
		if _, err := n.fields(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	n := bleNet()
	n.Addrs = append(n.Addrs, netip.MustParseAddr("fd00::1"))
	f, err := n.fields()
	if err != nil {
		t.Fatal(err)
	}
	if back, ok := readNetwork(f); !ok || len(back.Addrs) != 2 || back.Addrs[1] != n.Addrs[1] {
		t.Fatalf("read back %+v", back)
	}
}

// BLE-03: whatever a client writes, in any order: never a panic, never a request authenticated
// without the pair's key or the PIN. Run: go test -run XXX -fuzz FuzzBLEReceive ./pkg/pairing
func FuzzBLEReceive(f *testing.F) {
	a, b := device(f, "a"), device(f, "b")
	pins := NewPINOnDemand(PINConfig{})
	r, err := NewBLEResponder(BLEResponderConfig{Self: b.Identity.Fingerprint, Suffix: "4f2a", Trust: b.Trust, Own: func() BLERequest { return bleAll },
		PIN: func(string) (*PIN, error) { return pins.Get() }})
	if err != nil {
		f.Fatal(err)
	}
	c, err := NewBLEClient(BLEClientConfig{Self: a.Identity.Fingerprint, Suffix: "9c1b"})
	if err != nil {
		f.Fatal(err)
	}
	hello, _ := c.Hello()
	wanted, _ := c.PINWanted()
	f.Add(hello, bleEncode(bleTrusted, make([]byte, 8), make([]byte, 40)), []byte{})
	f.Add(wanted, bleEncode(blePIN, []byte(PINSuite), make([]byte, 32)), bleEncode(blePINConfirm, make([]byte, 64), make([]byte, 60)))
	f.Fuzz(func(t *testing.T, m1, m2, m3 []byte) {
		conn := r.Conn()
		defer conn.Close()
		for _, m := range [][]byte{m1, m2, m3} {
			if step, _ := conn.Receive(m); step.Request != nil || step.Network != nil {
				t.Fatalf("authenticated by %x", m)
			}
		}
	})
}

// BLE-10: a key authenticated by BLE opens one connection, within two minutes.
func TestBLEAuthenticated(t *testing.T) {
	now := time.Now()
	var a BLEAuthenticated
	a.Now = func() time.Time { return now }
	var fp, other identity.Fingerprint
	fp[0], other[0] = 1, 2
	a.Add(fp)
	if a.Take(other) || !a.Take(fp) || a.Take(fp) {
		t.Fatal("taken by another key, or twice")
	}
	a.Add(fp)
	now = now.Add(BLEAuthLife + time.Second)
	if a.Take(fp) {
		t.Fatal("taken past its life")
	}
}
