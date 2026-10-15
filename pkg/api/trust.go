package api

// Pairing without the code XXX-YYY (WP-3.6b). The code only finds a device on the network: it
// has about 10 useful bits and travels in clear in discovery. A control connection is let in
// when the peer's key is trusted, when it presents the secret of this daemon's QR code
// (action pair_show, path P1), or when the user accepts it after comparing both fingerprints
// (event trust_request, action trust_respond; decision D3). Otherwise it is refused.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
)

// defaultTrustTimeout is how long a trust_request waits for trust_respond; no answer is a no.
const defaultTrustTimeout = 60 * time.Second

// pairReplyTimeout bounds the wait for pair_accept: the other side may ask its user.
const pairReplyTimeout = defaultTrustTimeout + 10*time.Second

// trustDecision is the user's answer about an unknown key.
type trustDecision int

const (
	trustNo     trustDecision = iota // default: anything but an explicit yes
	trustOnce                        // this session only
	trustAlways                      // saved in trusted.json
)

func parseTrustDecision(s string) trustDecision {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "once", "yes", "y", "o", "oui":
		return trustOnce
	case "always", "a", "t", "toujours":
		return trustAlways
	}
	return trustNo
}

// TrustRespondPayload answers a trust_request: answer is "once", "always" or "no".
type TrustRespondPayload struct {
	Fingerprint string `json:"fingerprint"`
	Answer      string `json:"answer"`
}

// PairShowPayload asks for a QR code; once keeps the trust to the session (PAIR-08).
type PairShowPayload struct {
	Once bool `json:"once,omitempty"`
}

// PairURIPayload pairs with the device that shows a QR code.
type PairURIPayload struct {
	URI  string `json:"uri"`
	Once bool   `json:"once,omitempty"`
}

// errNotTrusted and errKeyChanged are the reasons a peer is refused.
var (
	errNotTrusted = errors.New("device not trusted")
	errKeyChanged = errors.New("device key changed")
)

// keyChanged implements ID-05: ip is the last address of a trusted device, and the key seen
// there now is another one.
func (s *DaemonServer) keyChanged(ip string, fp identity.Fingerprint) (identity.Peer, bool) {
	s.mu.RLock()
	ts := s.trust
	s.mu.RUnlock()
	if ts == nil || ip == "" {
		return identity.Peer{}, false
	}
	p, ok := ts.ByAddress(ip)
	if !ok || p.Fingerprint == fp {
		return identity.Peer{}, false
	}
	return p, true
}

// rememberTrust records a decision: a new trusted device (trustAlways), or the last address
// of an already trusted one.
func (s *DaemonServer) rememberTrust(fp identity.Fingerprint, name, ip string, d trustDecision) error {
	s.mu.RLock()
	ts := s.trust
	s.mu.RUnlock()
	if ts == nil {
		return nil
	}
	if p, ok := ts.Get(fp); ok {
		if p.LastAddress == ip && (name == "" || p.Alias == name) {
			return nil
		}
		p.LastAddress = ip
		if name != "" {
			p.Alias = name
		}
		return ts.Add(p)
	}
	if d != trustAlways {
		return nil
	}
	return ts.Add(identity.Peer{Fingerprint: fp, Alias: name, LastAddress: ip})
}

// TrustDevice adds a device to the trusted ones (tests, and testkit.StartPair).
func (s *DaemonServer) TrustDevice(fingerprint, alias string) error {
	fp, err := identity.ParseFingerprint(fingerprint)
	if err != nil {
		return err
	}
	return s.rememberTrust(fp, alias, "", trustAlways)
}

// Fingerprint is this device's key fingerprint (full form), or "" before Serve.
func (s *DaemonServer) Fingerprint() string { return s.fingerprint() }

// askTrust shows an unknown key to the user (event trust_request, with both fingerprints to
// compare) and waits for trust_respond. No answer within the timeout is a no.
func (s *DaemonServer) askTrust(ctx context.Context, fp identity.Fingerprint, name, ip, direction string) trustDecision {
	key := fp.String()
	reply := make(chan trustDecision, 1)
	s.mu.Lock()
	if s.pendingTrust == nil {
		s.pendingTrust = make(map[string]chan trustDecision)
	}
	if _, busy := s.pendingTrust[key]; busy || s.identity == nil {
		s.mu.Unlock()
		return trustNo
	}
	s.pendingTrust[key] = reply
	self := s.identity.Fingerprint
	timeout := s.trustTimeout
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pendingTrust, key)
		s.mu.Unlock()
	}()
	if timeout <= 0 {
		timeout = defaultTrustTimeout
	}
	s.Broadcast(NewEvent("trust_request", map[string]any{
		"fingerprint": key,
		"short":       fp.Short(),
		"self":        self.Short(),
		"device_name": name,
		"ip":          ip,
		"direction":   direction, // "incoming": the peer connects; "outgoing": we do
		"timeout_s":   int(timeout.Seconds()),
	}))
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case d := <-reply:
		return d
	case <-t.C:
	case <-ctx.Done():
	}
	s.Broadcast(NewEvent("trust_expired", map[string]string{"fingerprint": key}))
	return trustNo
}

func (s *DaemonServer) handleTrustRespond(rs Responder, req RequestMessage) {
	var p TrustRespondPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil || p.Fingerprint == "" {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid trust_respond payload"}, req.ID))
		return
	}
	s.mu.Lock()
	reply, ok := s.pendingTrust[p.Fingerprint]
	s.mu.Unlock()
	if !ok {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "no pending trust request for this device"}, req.ID))
		return
	}
	select {
	case reply <- parseTrustDecision(p.Answer):
	default: // already answered
	}
}

// admitIncoming decides about the peer of an incoming pair_request: ID-05 first, then the QR
// code's secret, then trusted keys, then the user.
func (s *DaemonServer) admitIncoming(conn *securechan.Conn, msg session.Message, ip string) (trustDecision, error) {
	fp := conn.PeerFingerprint()
	if known, changed := s.keyChanged(ip, fp); changed {
		s.Broadcast(NewEvent("log", map[string]string{"level": "WARN", "message": fmt.Sprintf(
			"REFUSED: %s is the address of '%s' (%s), but the device there presents another key (%s)",
			ip, known.Alias, known.Fingerprint.Short(), fp.Short())}))
		return trustNo, errKeyChanged
	}
	if msg.Secret != "" {
		s.mu.RLock()
		inv, once := s.invite, s.inviteOnce
		s.mu.RUnlock()
		if inv == nil || inv.Redeem(pairing.DecodeSecret(msg.Secret), time.Now()) != nil {
			return trustNo, pairing.ErrRefused // PAIR-07: nothing more
		}
		if once {
			return trustOnce, nil
		}
		return trustAlways, nil
	}
	if conn.State() == securechan.Auth {
		return trustOnce, nil // a trusted device
	}
	// The user may take up to the trust timeout: the Unauth deadline must not cut the
	// connection meanwhile (the Gate still limits how many wait).
	_ = conn.SetDeadline(time.Now().Add(pairReplyTimeout))
	if d := s.askTrust(s.ctx, fp, msg.DeviceName, ip, "incoming"); d != trustNo {
		return d, nil
	}
	return trustNo, errNotTrusted
}

// handlePairShow creates a QR code invitation for this daemon's control listener (path P1)
// and returns its URI in a pair_show event. A new invitation replaces the previous one.
func (s *DaemonServer) handlePairShow(rs Responder, req RequestMessage) {
	var p PairShowPayload
	if len(req.Payload) > 0 {
		_ = json.Unmarshal(req.Payload, &p)
	}
	s.mu.RLock()
	id, ln := s.identity, s.nodeLn
	s.mu.RUnlock()
	if id == nil || ln == nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "pairing listener not started"}, req.ID))
		return
	}
	inv, err := pairing.NewInvitation(pairing.TTL)
	if err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
		return
	}
	u := inv.URI(id.Fingerprint, discovery.GetPrimaryLocalIP(), ln.Addr().(*net.TCPAddr).Port)
	s.mu.Lock()
	s.invite, s.inviteOnce = inv, p.Once
	s.mu.Unlock()
	rs.Reply(NewEvent("pair_show", map[string]any{
		"uri":         u.String(),
		"expires":     u.Expires.Unix(),
		"fingerprint": id.Fingerprint.Short(),
		"instance":    s.instance(), // the name in the other device's list (DISC-07)
	}, req.ID))
}

// handlePairURI pairs with the device that shows a QR code: its key is pinned, the secret
// proves we saw the code, and both sides trust each other (unless once).
func (s *DaemonServer) handlePairURI(rs Responder, req RequestMessage) {
	var p PairURIPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid pair_uri payload"}, req.ID))
		return
	}
	u, err := pairing.Parse(strings.TrimSpace(p.URI), time.Now())
	if err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
		return
	}
	go func() {
		if _, err := s.pairWith(u.Addr(), &u, p.Once, req.ID); err != nil {
			s.Broadcast(NewEvent("action_error", map[string]string{"error": fmt.Sprintf("failed to pair with %s: %v", u.Addr(), err)}, req.ID))
		}
	}()
}

// pairWith opens a control session with target. With a QR code (u), its key is pinned and
// its secret sent; otherwise an unknown key is shown to our user first (D3). The peer, in
// turn, decides about us. On success the session becomes the active one.
func (s *DaemonServer) pairWith(target string, u *pairing.URI, once bool, reqID string) (*session.Channel, error) {
	if !strings.Contains(target, ":") {
		target = net.JoinHostPort(target, strconv.Itoa(18887))
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	cfg := s.pairingChanConfig()
	if u != nil {
		cfg.Expected, cfg.AllowUnknown = u.Fingerprint, false
	}
	connPeer, err := dialSecure(s.ctx, target, cfg)
	if err != nil {
		return nil, err
	}
	fp := connPeer.PeerFingerprint()
	msg := session.Message{Type: "pair_request"}
	s.mu.RLock()
	msg.DeviceName = s.config.DeviceName
	s.mu.RUnlock()

	decision := trustOnce
	switch {
	case u != nil:
		msg.Secret = pairing.EncodeSecret(u.Secret)
		decision = trustAlways
		if once {
			decision = trustOnce
		}
	default:
		if known, changed := s.keyChanged(host, fp); changed {
			connPeer.Close()
			return nil, fmt.Errorf("REFUSED: %s is the address of '%s' (%s), but the device there presents another key (%s)",
				host, known.Alias, known.Fingerprint.Short(), fp.Short())
		}
		if connPeer.State() == securechan.Unauth {
			_ = connPeer.SetDeadline(time.Now().Add(pairReplyTimeout))
			if decision = s.askTrust(s.ctx, fp, "", host, "outgoing"); decision == trustNo {
				connPeer.Close()
				return nil, errNotTrusted
			}
		}
	}

	ch := session.NewChannel(connPeer)
	if err := ch.Send(msg); err != nil {
		ch.Close()
		return nil, err
	}
	return s.finishOutgoing(ch, connPeer, fp, host, decision, reqID)
}

// finishOutgoing waits for the peer's pair_accept, trusts it as decided, and makes the session
// the active one.
func (s *DaemonServer) finishOutgoing(ch *session.Channel, connPeer *securechan.Conn, fp identity.Fingerprint, host string,
	decision trustDecision, reqID string) (*session.Channel, error) {
	_ = connPeer.SetReadDeadline(time.Now().Add(pairReplyTimeout))
	resp, err := ch.Read()
	_ = connPeer.SetDeadline(time.Time{})
	if err != nil || resp.Type != "pair_accept" {
		ch.Close()
		if err != nil {
			return nil, err
		}
		if resp.FileName != "" {
			return nil, errors.New(resp.FileName)
		}
		return nil, errors.New("pairing rejected by peer")
	}
	ch.Authenticate()
	peerName := resp.DeviceName
	if peerName == "" {
		peerName = host
	}
	if err := s.rememberTrust(fp, resp.DeviceName, host, decision); err != nil {
		s.Broadcast(NewEvent("log", map[string]string{"level": "WARN", "message": "could not save the trusted device: " + err.Error()}))
	}

	s.mu.Lock()
	if s.sessionGraceTimer != nil {
		s.sessionGraceTimer.Stop()
		s.sessionGraceTimer = nil
	}
	s.isReconnecting = false
	if s.activeSession != nil {
		s.activeSession.Close()
	}
	s.activeSession = ch
	s.pairedDeviceName = peerName
	s.mu.Unlock()

	s.Broadcast(NewEvent("paired", map[string]string{
		"ip":          host,
		"device_name": peerName,
		"fingerprint": fp.Short(),
	}, reqID))
	go s.listenToSession(ch)
	return ch, nil
}
