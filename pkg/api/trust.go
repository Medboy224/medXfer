package api

// Pairing and trust (bible chapter 6, lot « Modèle de confiance »). A device seen for the first
// time is reached by a QR code (pair_show / pair_uri, path P1) or a PIN (pair_pin_show /
// pair_pin, or the PIN on demand of a device picked in a list, path P2): never by comparing
// fingerprints by eye (PAIR-12). Such a session writes nothing (PAIR-08). Mutual trust is asked
// within a session (trust_offer / trust_answer, PAIR-10), and then lets both devices reconnect
// without a code (path P3), as long as both keep the other in their list (PAIR-09).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
)

// pairReplyTimeout bounds the wait for pair_accept: no user is asked on the other side.
const pairReplyTimeout = 15 * time.Second

// codePINRequired is the pair_reject code of a device that is not of mutual trust: a PIN or a
// QR code is needed (PAIR-12).
const codePINRequired = "pin_required"

// PairShowPayload asks for a QR code; trust offers mutual trust once paired (PAIR-10).
type PairShowPayload struct {
	Trust bool `json:"trust,omitempty"`
}

// PairURIPayload pairs with the device that shows a QR code.
type PairURIPayload struct {
	URI   string `json:"uri"`
	Trust bool   `json:"trust,omitempty"`
}

// TrustAnswerPayload answers the peer's trust_offer.
type TrustAnswerPayload struct {
	Accept bool `json:"accept"`
}

// errKeyChanged, errPINRequired and errPINNeeded are the reasons a pairing stops.
var (
	errKeyChanged  = errors.New("device key changed")
	errPINRequired = errors.New("a PIN or a QR code is needed to pair with this device")
	// errPINNeeded: the other device shows a PIN on demand; pin_needed tells the user to type
	// it with pair_pin. Not an error for the user.
	errPINNeeded = errors.New("type the PIN shown on the other device")
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

func keyChangedError(host string, known identity.Peer, fp identity.Fingerprint) error {
	return fmt.Errorf("REFUSED: %s is the address of '%s' (%s), but the device there presents another key (%s)",
		host, known.Alias, known.Fingerprint.Short(), fp.Short())
}

// touchTrust records the address and name of a device already trusted. It never adds one:
// only the trust negotiation does (PAIR-08, PAIR-10).
func (s *DaemonServer) touchTrust(fp identity.Fingerprint, name, ip string) error {
	s.mu.RLock()
	ts := s.trust
	s.mu.RUnlock()
	if ts == nil {
		return nil
	}
	p, ok := ts.Get(fp)
	if !ok || (p.LastAddress == ip && (name == "" || p.Alias == name || p.Renamed)) {
		return nil
	}
	p.LastAddress = ip
	if name != "" && !p.Renamed {
		p.Alias = name
	}
	return ts.Add(p)
}

// declares is what this device tells peer in trusts_you (PAIR-09).
func (s *DaemonServer) declares(peer identity.Fingerprint) bool {
	s.mu.RLock()
	ts := s.trust
	s.mu.RUnlock()
	return pairing.Declares(ts, peer)
}

// mutual reports whether peer, which declared, is a device of mutual trust (PAIR-09).
func (s *DaemonServer) mutual(peer identity.Fingerprint, declared bool) bool {
	s.mu.RLock()
	ts := s.trust
	s.mu.RUnlock()
	return pairing.Mutual(ts, peer, declared)
}

// reportIncomplete tells the user that a trust is on one side only: "local" when this device
// has the other in its list but the other no longer has it, "remote" the other way round. The
// entry gives nothing (PAIR-09); the user may forget it or ask for trust again.
func (s *DaemonServer) reportIncomplete(fp identity.Fingerprint, name, side string) {
	s.Broadcast(NewEvent("trust_incomplete", map[string]string{
		"fingerprint": fp.Short(),
		"device_name": name,
		"side":        side,
	}))
}

// TrustDevice adds a device to the trusted ones (tests, and testkit.StartPair, which trusts
// each device on both sides: a mutual trust).
func (s *DaemonServer) TrustDevice(fingerprint, alias string) error {
	fp, err := identity.ParseFingerprint(fingerprint)
	if err != nil {
		return err
	}
	s.mu.RLock()
	ts := s.trust
	s.mu.RUnlock()
	if ts == nil {
		return errors.New("trusted list not loaded")
	}
	if p, ok := ts.Get(fp); ok {
		p.Alias = alias
		return ts.Add(p)
	}
	return ts.Add(identity.Peer{Fingerprint: fp, Alias: alias})
}

// Fingerprint is this device's key fingerprint (full form), or "" before Serve.
func (s *DaemonServer) Fingerprint() string { return s.fingerprint() }

// admitIncoming decides about the peer of an incoming pair_request: ID-05 first, then the QR
// code's secret, then mutual trust. Anything else needs a PIN or a QR code. offer: this side
// asked to offer mutual trust once paired (pair_show with trust).
func (s *DaemonServer) admitIncoming(conn *securechan.Conn, msg session.Message, ip string) (offer bool, err error) {
	fp := conn.PeerFingerprint()
	if known, changed := s.keyChanged(ip, fp); changed {
		s.Broadcast(NewEvent("log", map[string]string{"level": "WARN", "message": keyChangedError(ip, known, fp).Error()}))
		return false, errKeyChanged
	}
	if msg.Secret != "" {
		s.mu.RLock()
		inv, trust := s.invite, s.inviteTrust
		s.mu.RUnlock()
		if inv == nil || inv.Redeem(pairing.DecodeSecret(msg.Secret), time.Now()) != nil {
			return false, pairing.ErrRefused // PAIR-07: nothing more
		}
		return trust, nil
	}
	if s.mutual(fp, msg.TrustsYou) {
		return false, nil // path P3
	}
	switch {
	case msg.TrustsYou:
		s.reportIncomplete(fp, msg.DeviceName, "remote") // it trusts us, we no longer trust it
	case s.declares(fp):
		s.reportIncomplete(fp, msg.DeviceName, "local") // we trust it, it no longer trusts us
	}
	return false, errPINRequired
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
	s.invite, s.inviteTrust = inv, p.Trust
	s.mu.Unlock()
	rs.Reply(NewEvent("pair_show", map[string]any{
		"uri":         u.String(),
		"expires":     u.Expires.Unix(),
		"fingerprint": id.Fingerprint.Short(),
		"instance":    s.instance(), // the name in the other device's list (DISC-07)
	}, req.ID))
}

// handlePairURI pairs with the device that shows a QR code: its key is pinned and the secret
// proves we saw the code.
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
		if _, err := s.pairWith(u.Addr(), &u, p.Trust, req.ID); err != nil {
			s.Broadcast(NewEvent("action_error", map[string]string{"error": fmt.Sprintf("failed to pair with %s: %v", u.Addr(), err)}, req.ID))
		}
	}()
}

// pairWith opens a control session with target. With a QR code (u), its key is pinned and its
// secret sent. Without one, a device of mutual trust is reached at once (P3); any other gets a
// PIN on demand: it shows a PIN, pin_needed asks our user to type it (pair_pin), and pairWith
// returns errPINNeeded. offer: offer mutual trust once paired.
func (s *DaemonServer) pairWith(target string, u *pairing.URI, offer bool, reqID string) (*session.Channel, error) {
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
	s.mu.RLock()
	name := s.config.DeviceName
	s.mu.RUnlock()
	msg := session.Message{Type: "pair_request", DeviceName: name, TrustsYou: s.declares(fp)}
	ch := session.NewChannel(connPeer)

	if u != nil {
		msg.Secret = pairing.EncodeSecret(u.Secret)
	} else {
		if known, changed := s.keyChanged(host, fp); changed {
			ch.Close()
			return nil, keyChangedError(host, known, fp)
		}
		if !msg.TrustsYou { // not in our list: a first contact (PAIR-12)
			return nil, s.askPIN(ch, connPeer, target, reqID)
		}
	}
	if err := ch.Send(msg); err != nil {
		ch.Close()
		return nil, err
	}
	sess, err := s.finishOutgoing(ch, connPeer, fp, host, offer, reqID)
	var rej *rejectedError
	if u == nil && errors.As(err, &rej) && rej.code == codePINRequired {
		// We trust it, it no longer trusts us: the entry gives nothing (PAIR-09).
		s.reportIncomplete(fp, "", "local")
		again, derr := dialSecure(s.ctx, target, s.pairingChanConfig())
		if derr != nil {
			return nil, derr
		}
		return nil, s.askPIN(session.NewChannel(again), again, target, reqID)
	}
	return sess, err
}

// askPIN asks the device on ch to show a PIN (PAIR-11), then closes the connection: the user
// types the PIN with pair_pin, on a new one. It returns errPINNeeded once the PIN is shown.
func (s *DaemonServer) askPIN(ch *session.Channel, conn *securechan.Conn, target, reqID string) error {
	defer ch.Close()
	if err := pairing.RequestPIN(ch, conn); err != nil {
		if errors.Is(err, pairing.ErrRefused) {
			s.Broadcast(NewEvent("pair_failed", map[string]string{}, reqID))
			return nil
		}
		return err
	}
	s.Broadcast(NewEvent("pin_needed", map[string]string{"target": target}, reqID))
	return errPINNeeded
}

// rejectedError is a pair_reject, with its reason and code.
type rejectedError struct {
	reason, code string
}

func (e *rejectedError) Error() string { return e.reason }

// finishOutgoing waits for the peer's pair_accept and makes the session the active one. offer:
// offer mutual trust once paired.
func (s *DaemonServer) finishOutgoing(ch *session.Channel, connPeer *securechan.Conn, fp identity.Fingerprint, host string,
	offer bool, reqID string) (*session.Channel, error) {
	_ = connPeer.SetReadDeadline(time.Now().Add(pairReplyTimeout))
	resp, err := ch.Read()
	_ = connPeer.SetDeadline(time.Time{})
	if err != nil || resp.Type != "pair_accept" {
		ch.Close()
		if err != nil {
			return nil, err
		}
		reason := resp.FileName
		if reason == "" {
			reason = "pairing rejected by peer"
		}
		return nil, &rejectedError{reason: reason, code: resp.Code}
	}
	ch.Authenticate()
	peerName := resp.DeviceName
	if peerName == "" {
		peerName = host
	}
	if err := s.touchTrust(fp, resp.DeviceName, host); err != nil {
		s.Broadcast(NewEvent("log", map[string]string{"level": "WARN", "message": "could not save the trusted device: " + err.Error()}))
	}
	s.startSession(ch, fp, peerName, host, s.mutual(fp, resp.TrustsYou), offer, reqID)
	return ch, nil
}

// startSession makes ch the active session with the device fp, starts its trust negotiation,
// and says so (paired). offer: offer mutual trust at once.
func (s *DaemonServer) startSession(ch *session.Channel, fp identity.Fingerprint, name, ip string, mutual, offer bool, reqID string) {
	s.mu.Lock()
	if s.sessionGraceTimer != nil {
		s.sessionGraceTimer.Stop()
		s.sessionGraceTimer = nil
	}
	s.isReconnecting = false
	if s.activeSession != nil && s.activeSession != ch {
		s.activeSession.Close()
		if s.trustPeer != fp { // another device: the old one's transfers end with it (§3.7, #156)
			s.dropTransfers()
		}
	}
	s.activeSession = ch
	s.pairedDeviceName = name
	s.trustTalk = pairing.NewTrustTalk(s.trust, s.identity.Fingerprint, identity.Peer{Fingerprint: fp, Alias: name, LastAddress: ip})
	s.trustPeer = fp
	s.trustOffered = time.Time{}
	s.mu.Unlock()

	var ids []string
	if reqID != "" {
		ids = append(ids, reqID)
	}
	s.Broadcast(NewEvent("paired", map[string]any{
		"ip":          ip,
		"device_name": name,
		"fingerprint": fp.Short(),
		"mutual":      mutual, // a device of mutual trust (P3); otherwise this session only
	}, ids...))
	go s.listenToSession(ch)
	if offer && !mutual {
		s.offerTrust()
	}
}

// offerTrust sends trust_offer on the active session; past TrustAnswerTime without an answer,
// trust_declined says so.
func (s *DaemonServer) offerTrust() error {
	s.mu.Lock()
	sess, talk := s.activeSession, s.trustTalk
	if sess == nil || talk == nil {
		s.mu.Unlock()
		return errors.New("not paired with any device")
	}
	at := time.Now()
	s.trustOffered = at
	s.mu.Unlock()
	if err := sess.Send(talk.Offer()); err != nil {
		return err
	}
	time.AfterFunc(pairing.TrustAnswerTime, func() {
		s.mu.Lock()
		pending := s.trustOffered.Equal(at)
		if pending {
			s.trustOffered = time.Time{}
		}
		s.mu.Unlock()
		if pending {
			s.Broadcast(NewEvent("trust_declined", map[string]string{"reason": "no_answer"}))
		}
	})
	return nil
}

// handleTrustOffer offers mutual trust to the device of the active session (PAIR-10).
func (s *DaemonServer) handleTrustOffer(rs Responder, req RequestMessage) {
	if err := s.offerTrust(); err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
	}
}

// handleTrustAnswer answers the peer's trust_offer with the user's choice.
func (s *DaemonServer) handleTrustAnswer(rs Responder, req RequestMessage) {
	var p TrustAnswerPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid trust_answer payload"}, req.ID))
		return
	}
	s.mu.RLock()
	sess, talk := s.activeSession, s.trustTalk
	s.mu.RUnlock()
	if sess == nil || talk == nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "not paired with any device"}, req.ID))
		return
	}
	m, err := talk.Answer(p.Accept)
	if err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "no trust offer to answer (it may have expired)"}, req.ID))
		return
	}
	_ = sess.Send(m)
}

// handleTrustMessage takes a negotiation message of the active session.
func (s *DaemonServer) handleTrustMessage(msg session.Message) {
	s.mu.RLock()
	sess, talk, fp, name := s.activeSession, s.trustTalk, s.trustPeer, s.pairedDeviceName
	s.mu.RUnlock()
	if sess == nil || talk == nil {
		return
	}
	reply, ev, err := talk.Handle(msg)
	if err != nil {
		s.Broadcast(NewEvent("log", map[string]string{"level": "WARN", "message": "could not save the trusted device: " + err.Error()}))
		return
	}
	if reply != nil {
		_ = sess.Send(*reply)
	}
	switch ev {
	case pairing.TrustAsked:
		s.Broadcast(NewEvent("trust_offered", map[string]any{
			"device_name": name,
			"fingerprint": fp.Short(),
			"timeout_s":   int(pairing.TrustAnswerTime.Seconds()),
		}))
	case pairing.TrustEstablished:
		s.mu.Lock()
		s.trustOffered = time.Time{}
		s.mu.Unlock()
		s.Broadcast(NewEvent("trust_established", map[string]string{"device_name": name, "fingerprint": fp.Short()}))
	case pairing.TrustDeclined:
		s.mu.Lock()
		s.trustOffered = time.Time{}
		s.mu.Unlock()
		s.Broadcast(NewEvent("trust_declined", map[string]string{"reason": "declined"}))
	}
}

// showOnDemand is the screen of the PINs drawn on demand (PAIR-11): pin_requested tells the
// user which PIN to read out, and to whom. The PIN never reaches the journal (chapter 13).
func (s *DaemonServer) showOnDemand(code string, expires time.Time) {
	s.mu.RLock()
	id, from := s.identity, s.pinDemandFrom
	s.mu.RUnlock()
	if code == "" && expires.IsZero() { // too many wrong PINs: no more PINs (#105)
		s.Broadcast(NewEvent("pin_requested", map[string]any{"pin": "", "stopped": true}))
		return
	}
	shown := ""
	if code != "" {
		shown = pairing.FormatPIN(code)
		diag.Secrets.Add("PIN", code, shown)
	}
	fp := ""
	if id != nil {
		fp = id.Fingerprint.Short()
	}
	s.Broadcast(NewEvent("pin_requested", map[string]any{
		"pin":         shown,
		"expires":     expires.Unix(),
		"from":        from, // the address of the device that asked
		"fingerprint": fp,
		"instance":    s.instance(),
	}))
}
