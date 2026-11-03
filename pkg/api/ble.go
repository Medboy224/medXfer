package api

// The path by BLE (bible §6.6, J6d): two devices that share no network find each other by
// Bluetooth Low Energy, and one gives the other the network it created. The platform carries
// the GATT messages (advertising, scanning, connections); the core makes and reads them
// (pkg/pairing/ble.go). Two actions, each one step of an exchange on one GATT connection, which
// the platform names by conn:
//   - ble_server: another device touched this one; this one answers;
//   - ble_client: this device's user touched another; this one asks.
//
// Then pair_ble opens the session on the network, from the device that joined it to the one
// that created it, the key BLE authenticated pinned (BLE-10).

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/session"
)

// BLEPayload is the payload of ble_server and ble_client.
type BLEPayload struct {
	Conn string `json:"conn"` // the platform's name of the GATT connection
	Step string `json:"step"`
	Data string `json:"data,omitempty"` // a message, in base64
	PIN  string `json:"pin,omitempty"`  // step pin: the PIN the user typed
	// What this device can do: the bands it can create a network on, and join one on (bit 0:
	// 2.4 GHz, bit 1: 5 GHz). ble_server: at each message, Wi-Fi may have changed meanwhile.
	Create  int             `json:"create,omitempty"`
	Join    int             `json:"join,omitempty"`
	Outcome int             `json:"outcome,omitempty"` // step answer: 1 this device created the network, 2 the other creates it, 3 none can
	Network *BLENetworkInfo `json:"network,omitempty"` // the network this device created
}

// BLENetworkInfo is a network created for the occasion (DISC-12), as the platform gives it and
// takes it.
type BLENetworkInfo struct {
	SSID       string   `json:"ssid"`
	Passphrase string   `json:"passphrase"` // a secret of the network: masked in the journal
	Security   int      `json:"security"`   // 1 WPA2, 2 WPA3 transition, 3 WPA3
	Band       int      `json:"band"`       // 1: 2.4 GHz, 2: 5 GHz
	Port       int      `json:"port"`       // the creator's medXfer port; 0: this device's, filled by the core
	Addrs      []string `json:"addrs"`      // the creator's addresses on it
}

func (n *BLENetworkInfo) toPairing() (pairing.BLENetwork, error) {
	if n.Security < 0 || n.Security > 255 || n.Band < 0 || n.Band > 255 || n.Port < 1 || n.Port > 65535 {
		return pairing.BLENetwork{}, errors.New("invalid network")
	}
	out := pairing.BLENetwork{SSID: n.SSID, Passphrase: n.Passphrase, Security: byte(n.Security), Band: pairing.BLEBands(n.Band), Port: uint16(n.Port)}
	for _, a := range n.Addrs {
		addr, err := netip.ParseAddr(a)
		if err != nil {
			return pairing.BLENetwork{}, err
		}
		out.Addrs = append(out.Addrs, addr)
	}
	return out, nil
}

// networkInfo is n for the platform; its passphrase is masked in the journal from now on
// (chapter 13).
func networkInfo(n *pairing.BLENetwork) *BLENetworkInfo {
	diag.Secrets.Add("network passphrase", n.Passphrase)
	out := &BLENetworkInfo{SSID: n.SSID, Passphrase: n.Passphrase, Security: int(n.Security), Band: int(n.Band), Port: int(n.Port)}
	for _, a := range n.Addrs {
		out.Addrs = append(out.Addrs, a.String())
	}
	return out
}

// bleMaxConns bounds the GATT connections in progress, of each side (BLE-11). One idle for
// bleIdle gives its place to a new one: the platform may not have seen it go.
const (
	bleMaxConns = 4
	bleIdle     = 2 * time.Minute
)

// bleState is this device's side of the exchanges by BLE.
type bleState struct {
	mu        sync.Mutex
	responder *pairing.BLEResponder
	own       pairing.BLERequest
	conns     map[string]*bleServerConn
	clients   map[string]*bleClientConn
	auth      pairing.BLEAuthenticated // the keys BLE authenticated, for pair_ble (BLE-10)
}

type bleServerConn struct {
	conn    *pairing.BLEConn
	peer    identity.Fingerprint
	trusted bool
	last    time.Time
}

type bleClientConn struct {
	client *pairing.BLEClient
	last   time.Time
}

// roomFor makes room for one more connection in a map of n held, last seen at lastOf: refused
// when bleMaxConns are busy, else the oldest idle one goes (and drop closes it).
func roomFor[T any](m map[string]T, last func(T) time.Time, drop func(string)) bool {
	if len(m) < bleMaxConns {
		return true
	}
	oldest, at := "", time.Now()
	for id, c := range m {
		if t := last(c); t.Before(at) {
			oldest, at = id, t
		}
	}
	if time.Since(at) < bleIdle {
		return false
	}
	drop(oldest)
	return true
}

func bands(create, join int) pairing.BLERequest {
	return pairing.BLERequest{Create: pairing.BLEBands(create) & (pairing.BLEBand24 | pairing.BLEBand5),
		Join: pairing.BLEBands(join) & (pairing.BLEBand24 | pairing.BLEBand5)}
}

// instanceSuffix is the 4 hex digits that end this device's generic name (DISC-07).
func instanceSuffix(instance string) (string, error) {
	i := strings.LastIndexByte(instance, '-')
	if i < 0 || len(instance)-i-1 != 4 {
		return "", errors.New("no generic name yet")
	}
	return instance[i+1:], nil
}

// bleResponder is this device's responder, made once its identity and generic name exist.
func (s *DaemonServer) bleResponder() (*pairing.BLEResponder, error) {
	suffix, err := instanceSuffix(s.instance())
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	id, trust := s.identity, s.trust
	s.mu.RUnlock()
	if id == nil {
		return nil, errors.New("no identity yet")
	}
	s.ble.mu.Lock()
	defer s.ble.mu.Unlock()
	if s.ble.responder != nil {
		return s.ble.responder, nil
	}
	r, err := pairing.NewBLEResponder(pairing.BLEResponderConfig{Self: id.Fingerprint, Suffix: suffix, Trust: trust, PIN: s.blePIN,
		Own: func() pairing.BLERequest {
			s.ble.mu.Lock()
			defer s.ble.mu.Unlock()
			return s.ble.own
		}})
	if err != nil {
		return nil, err
	}
	s.ble.responder = r
	return r, nil
}

// blePIN is the PIN an unknown device asks for by BLE: the one pair_pin_show put on screen, else
// one drawn on demand and shown by pin_requested, with the asker's generic name (BLE-07). The
// same PIN as over TLS: one PIN, one count of attempts.
func (s *DaemonServer) blePIN(suffix string) (*pairing.PIN, error) {
	s.mu.Lock()
	shown := s.pin
	if shown != nil && !shown.Live() {
		shown = nil
	}
	if s.pinDemand == nil {
		s.pinDemand = pairing.NewPINOnDemand(pairing.PINConfig{Show: s.showOnDemand})
	}
	demand := s.pinDemand
	s.pinDemandFrom = "medXfer-" + suffix
	s.mu.Unlock()
	if shown != nil {
		return shown, nil
	}
	return demand.Get()
}

// bleRefusePIN is the user's "Refuser" of a PIN asked for by BLE (pin_refuse): its next message
// is refused.
func (s *DaemonServer) bleRefusePIN() {
	s.ble.mu.Lock()
	r := s.ble.responder
	s.ble.mu.Unlock()
	if r != nil {
		r.RefusePIN()
	}
}

func encode64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// handleBLEServer is one step of another device's exchange with this one (bible §6.6):
//   - message: the client wrote data; ble_server gives the reply to send, or the request
//     authenticated (request: who, what it can do, who creates the network), or the network the
//     client created (network);
//   - answer: the network this device created (outcome 1), or who creates it; the reply to send;
//   - close: the connection went.
//
// refused: send the reply, then close the connection.
func (s *DaemonServer) handleBLEServer(rs Responder, req RequestMessage) {
	var p BLEPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil || p.Conn == "" {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid ble_server payload"}, req.ID))
		return
	}
	fail := func(msg string) { rs.Reply(NewEvent("action_error", map[string]string{"error": msg}, req.ID)) }
	reply := func(out map[string]any) {
		out["conn"] = p.Conn
		rs.Reply(NewEvent("ble_server", out, req.ID))
	}
	switch p.Step {
	case "message":
		msg, err := base64.StdEncoding.DecodeString(p.Data)
		if err != nil {
			fail("invalid data")
			return
		}
		r, err := s.bleResponder()
		if err != nil {
			fail(err.Error())
			return
		}
		s.ble.mu.Lock()
		s.ble.own = bands(p.Create, p.Join)
		c := s.ble.conns[p.Conn]
		if c == nil {
			if s.ble.conns == nil {
				s.ble.conns = map[string]*bleServerConn{}
			}
			if !roomFor(s.ble.conns, func(c *bleServerConn) time.Time { return c.last }, s.bleDropLocked) {
				s.ble.mu.Unlock()
				reply(map[string]any{"data": encode64(pairing.BLERefusal()), "refused": true})
				return
			}
			c = &bleServerConn{conn: r.Conn()}
			s.ble.conns[p.Conn] = c
		}
		c.last = time.Now()
		s.ble.mu.Unlock()
		step, err := c.conn.Receive(msg)
		out := map[string]any{}
		if step.Reply != nil {
			out["data"] = encode64(step.Reply)
		}
		if err != nil {
			out["refused"] = true
			s.bleDrop(p.Conn)
		}
		s.ble.mu.Lock()
		if a := step.Request; a != nil {
			c.peer, c.trusted = a.Peer, a.Trusted
		}
		peer := c.peer
		s.ble.mu.Unlock()
		if a := step.Request; a != nil {
			request := map[string]any{"fingerprint": a.Peer.String(), "trusted": a.Trusted,
				"create": int(a.Request.Create), "join": int(a.Request.Join), "outcome": int(a.Outcome), "band": int(a.Band)}
			if ts := s.trustStore(); ts != nil && a.Trusted {
				if known, ok := ts.Get(a.Peer); ok {
					request["alias"] = known.Alias
				}
			}
			out["request"] = request
		}
		if n := step.Network; n != nil {
			out["network"] = networkInfo(n)
			out["fingerprint"] = peer.String() // the creator, to pin on the network (pair_ble)
		}
		reply(out)
	case "answer":
		s.ble.mu.Lock()
		c := s.ble.conns[p.Conn]
		var peer identity.Fingerprint
		var trusted bool
		if c != nil {
			peer, trusted = c.peer, c.trusted
		}
		s.ble.mu.Unlock()
		if c == nil {
			fail("no such connection")
			return
		}
		var n *pairing.BLENetwork
		if p.Network != nil {
			nn, err := s.blePort(p.Network).toPairing()
			if err != nil {
				fail(err.Error())
				return
			}
			diag.Secrets.Add("network passphrase", nn.Passphrase)
			n = &nn
		}
		data, err := c.conn.Answer(pairing.BLEOutcome(p.Outcome), n)
		if err != nil {
			fail(err.Error())
			return
		}
		if pairing.BLEOutcome(p.Outcome) == pairing.BLEResponderCreates && !trusted {
			s.ble.auth.Add(peer) // it joins this device's network, then connects (BLE-10)
		}
		reply(map[string]any{"data": encode64(data)})
	case "close":
		s.bleDrop(p.Conn)
		reply(map[string]any{"closed": true})
	default:
		fail("unknown step")
	}
}

func (s *DaemonServer) bleDrop(conn string) {
	s.ble.mu.Lock()
	defer s.ble.mu.Unlock()
	s.bleDropLocked(conn)
}

func (s *DaemonServer) bleDropLocked(conn string) {
	if c := s.ble.conns[conn]; c != nil {
		c.conn.Close() // an attempt on the PIN begun counts (PAIR-04)
		delete(s.ble.conns, conn)
	}
}

// handleBLEClient is one step of this device's exchange with another (bible §6.6), each giving
// the message to write (data) or reading the reply:
//   - hello, then recognize: who the other is (its generic name's suffix; a device of mutual
//     trust, by its tags);
//   - trusted: the request to a device of mutual trust;
//   - pin_wanted, pin_ready, pin, pin_reply: the request to an unknown device, by the PIN it
//     shows;
//   - reply: the other's answer (who it is, who creates the network, its network);
//   - network, ack: the network this device created, when it does;
//   - close: the connection went.
//
// refused: the other refused (a wrong PIN among others, PAIR-07); the exchange is over.
func (s *DaemonServer) handleBLEClient(rs Responder, req RequestMessage) {
	var p BLEPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil || p.Conn == "" {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid ble_client payload"}, req.ID))
		return
	}
	fail := func(msg string) { rs.Reply(NewEvent("action_error", map[string]string{"error": msg}, req.ID)) }
	reply := func(out map[string]any) {
		out["conn"] = p.Conn
		rs.Reply(NewEvent("ble_client", out, req.ID))
	}
	refused := func() {
		s.bleDropClient(p.Conn)
		reply(map[string]any{"refused": true})
	}
	if p.Step == "close" {
		s.bleDropClient(p.Conn)
		reply(map[string]any{"closed": true})
		return
	}
	c, err := s.bleClient(p.Conn, p.Step == "hello" || p.Step == "pin_wanted")
	if err != nil {
		fail(err.Error())
		return
	}
	msg, err := base64.StdEncoding.DecodeString(p.Data)
	if err != nil {
		fail("invalid data")
		return
	}
	var data []byte
	switch p.Step {
	case "hello":
		data, err = c.Hello()
	case "recognize":
		peer, err := c.Recognize(msg)
		if err != nil {
			refused()
			return
		}
		out := map[string]any{"suffix": peer.Suffix, "trusted": peer.Trusted != nil}
		if peer.Trusted != nil {
			out["fingerprint"], out["alias"] = peer.Trusted.Fingerprint.String(), peer.Trusted.Alias
		}
		reply(out)
		return
	case "trusted":
		data, err = c.Trusted(bands(p.Create, p.Join))
	case "pin_wanted":
		data, err = c.PINWanted()
	case "pin_ready":
		if c.PINReady(msg) != nil {
			refused()
			return
		}
		reply(map[string]any{"ready": true})
		return
	case "pin":
		data, err = c.PIN(p.PIN)
	case "pin_reply":
		if data, err = c.PINReply(msg, bands(p.Create, p.Join)); errors.Is(err, pairing.ErrRefused) {
			refused()
			return
		}
	case "reply":
		a, err := c.Reply(msg)
		if err != nil {
			refused()
			return
		}
		if a.Outcome == pairing.BLEInitiatorCreates {
			s.ble.auth.Add(a.Peer) // it joins this device's network, then connects (BLE-10)
		}
		out := map[string]any{"fingerprint": a.Peer.String(), "outcome": int(a.Outcome), "band": int(a.Band)}
		if a.Network != nil {
			out["network"] = networkInfo(a.Network)
		}
		reply(out)
		return
	case "network":
		if p.Network == nil {
			fail("no network")
			return
		}
		n, nerr := s.blePort(p.Network).toPairing()
		if nerr != nil {
			fail(nerr.Error())
			return
		}
		diag.Secrets.Add("network passphrase", n.Passphrase)
		data, err = c.Network(n)
	case "ack":
		if c.Ack(msg) != nil {
			refused()
			return
		}
		s.bleDropClient(p.Conn)
		reply(map[string]any{"done": true})
		return
	default:
		fail("unknown step")
		return
	}
	if err != nil {
		fail(err.Error())
		return
	}
	reply(map[string]any{"data": encode64(data)})
}

// bleClient is the exchange on conn; fresh: a new one, replacing any.
func (s *DaemonServer) bleClient(conn string, fresh bool) (*pairing.BLEClient, error) {
	s.ble.mu.Lock()
	defer s.ble.mu.Unlock()
	if c := s.ble.clients[conn]; c != nil && !fresh {
		c.last = time.Now()
		return c.client, nil
	}
	if !fresh {
		return nil, errors.New("no such connection")
	}
	suffix, err := instanceSuffix(s.instance())
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	id, trust := s.identity, s.trust
	s.mu.RUnlock()
	if id == nil {
		return nil, errors.New("no identity yet")
	}
	if s.ble.clients == nil {
		s.ble.clients = map[string]*bleClientConn{}
	}
	delete(s.ble.clients, conn)
	if !roomFor(s.ble.clients, func(c *bleClientConn) time.Time { return c.last }, func(id string) { delete(s.ble.clients, id) }) {
		return nil, errors.New("too many connections")
	}
	c, err := pairing.NewBLEClient(pairing.BLEClientConfig{Self: id.Fingerprint, Suffix: suffix, Trust: trust})
	if err != nil {
		return nil, err
	}
	s.ble.clients[conn] = &bleClientConn{client: c, last: time.Now()}
	return c, nil
}

func (s *DaemonServer) bleDropClient(conn string) {
	s.ble.mu.Lock()
	defer s.ble.mu.Unlock()
	delete(s.ble.clients, conn)
}

// PairBLEPayload opens the session with the device whose network this one joined after BLE:
// its address on that network, and the key BLE authenticated (BLE-10).
type PairBLEPayload struct {
	Target      string `json:"target"`
	Fingerprint string `json:"fingerprint"`
	Trust       bool   `json:"trust,omitempty"` // offer mutual trust once paired (PAIR-10)
}

// handlePairBLE connects to the device that created the network, its key pinned, as the QR
// code does (§6.1). A refusal is a pair_failed event without detail (PAIR-07).
func (s *DaemonServer) handlePairBLE(rs Responder, req RequestMessage) {
	var p PairBLEPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil || strings.TrimSpace(p.Target) == "" {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid pair_ble payload"}, req.ID))
		return
	}
	fp, err := identity.ParseFingerprint(p.Fingerprint)
	if err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
		return
	}
	go func() {
		_, err := s.pairByBLE(strings.TrimSpace(p.Target), fp, p.Trust, req.ID)
		var rej *rejectedError
		switch {
		case errors.As(err, &rej):
			s.Broadcast(NewEvent("pair_failed", map[string]string{}, req.ID))
		case err != nil:
			s.Broadcast(pairFailed(p.Target, err, req.ID))
		}
	}()
}

// pairByBLE opens a control session with target, pinning fp, and sends pair_ble.
func (s *DaemonServer) pairByBLE(target string, fp identity.Fingerprint, offer bool, reqID string) (*session.Channel, error) {
	if !strings.Contains(target, ":") {
		target = net.JoinHostPort(target, strconv.Itoa(18887))
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	cfg := s.pairingChanConfig()
	cfg.Expected, cfg.AllowUnknown = fp, false
	connPeer, err := dialSecure(s.ctx, target, cfg)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	name := s.config.DeviceName
	s.mu.RUnlock()
	ch := session.NewChannel(connPeer)
	if err := ch.Send(session.Message{Type: pairing.TypeBLE, DeviceName: name, TrustsYou: s.declares(fp)}); err != nil {
		ch.Close()
		return nil, err
	}
	sess, err := s.finishOutgoing(ch, connPeer, fp, host, offer, reqID)
	if err == nil {
		s.dialedFor(sess, target)
	}
	return sess, err
}

// trustStore is this device's list of trusted devices.
func (s *DaemonServer) trustStore() *identity.TrustStore {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.trust
}

// blePort is n with this device's medXfer port when the platform gave none: it creates the
// network, the core knows where it listens.
func (s *DaemonServer) blePort(n *BLENetworkInfo) *BLENetworkInfo {
	if n.Port != 0 {
		return n
	}
	s.mu.RLock()
	ln := s.nodeLn
	s.mu.RUnlock()
	out := *n
	if ln != nil {
		out.Port = ln.Addr().(*net.TCPAddr).Port
	}
	return &out
}
