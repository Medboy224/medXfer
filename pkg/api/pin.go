package api

// Pairing by PIN (bible §6.2, path P2; WP-5.4): pair_pin_show shows a PIN on this device,
// pair_pin types the one another device shows. The PIN goes to the requesting client only
// (sendTo): Broadcast writes events to the journal, which never holds a PIN (chapter 13).

import (
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
)

// PairPINShowPayload asks for a PIN; digits is 6 (default) or 8; trust offers mutual trust once
// paired (PAIR-10).
type PairPINShowPayload struct {
	Trust  bool `json:"trust,omitempty"`
	Digits int  `json:"digits,omitempty"`
}

// PairPINPayload pairs with the device at target, by the PIN it shows (pair_pin_show, or on
// demand after pin_needed).
type PairPINPayload struct {
	Target string `json:"target"`
	PIN    string `json:"pin"`
	Trust  bool   `json:"trust,omitempty"`
}

// handlePairPINShow draws a PIN for this daemon's control listener and sends it in
// pair_pin_shown events: the first one, a new one after 3 wrong ones, and "" while the delay
// after PINs destroyed runs (expires is then when the next one comes). A new PIN replaces the
// previous one.
func (s *DaemonServer) handlePairPINShow(rs Responder, req RequestMessage) {
	var p PairPINShowPayload
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
	addr := net.JoinHostPort(discovery.GetPrimaryLocalIP(), strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	pin, err := pairing.NewPIN(pairing.PINConfig{Digits: p.Digits, Show: func(code string, expires time.Time) {
		if code == "" && expires.IsZero() { // too many wrong PINs: stopped (#105)
			rs.Reply(NewEvent("pair_pin_shown", map[string]any{"pin": "", "stopped": true, "address": addr,
				"fingerprint": id.Fingerprint.Short(), "instance": s.instance()}, req.ID))
			return
		}
		shown := ""
		if code != "" {
			shown = pairing.FormatPIN(code)
			diag.Secrets.Add("PIN", code, shown) // no journal line or bundle may reveal it
		}
		rs.Reply(NewEvent("pair_pin_shown", map[string]any{
			"pin":         shown,
			"expires":     expires.Unix(),
			"address":     addr,
			"fingerprint": id.Fingerprint.Short(),
			"instance":    s.instance(), // the name in the other device's list (DISC-07)
		}, req.ID))
	}})
	if err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
		return
	}
	s.mu.Lock()
	old, demand := s.pin, s.pinDemand
	s.pin, s.pinTrust = pin, p.Trust
	s.mu.Unlock()
	if demand != nil {
		demand.Reset() // the user pairs again: PINs on demand may be drawn again (#105)
	}
	if old != nil {
		old.Close()
	}
}

// handlePINRefuse is this user's refusal of a PIN asked for (pin_requested, PAIR-11): the PIN on
// demand is destroyed, so the PIN read out no longer pairs. The device that asked learns it
// when it types the PIN: a pair_failed, as for a wrong one (PAIR-07).
func (s *DaemonServer) handlePINRefuse(rs Responder, req RequestMessage) {
	s.mu.Lock()
	demand := s.pinDemand
	s.mu.Unlock()
	if demand != nil {
		demand.Close()
	}
	rs.Reply(NewEvent("pin_refused", map[string]string{}, req.ID))
}

// handlePairPIN pairs with the device that shows a PIN. A refusal (wrong PIN, PIN destroyed,
// relay) is a pair_failed event without detail (PAIR-07).
func (s *DaemonServer) handlePairPIN(rs Responder, req RequestMessage) {
	var p PairPINPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil || strings.TrimSpace(p.Target) == "" {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid pair_pin payload"}, req.ID))
		return
	}
	if _, err := pairing.NormalizePIN(p.PIN); err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
		return
	}
	go func() {
		_, err := s.pairWithPIN(strings.TrimSpace(p.Target), p.PIN, p.Trust, req.ID)
		switch {
		case errors.Is(err, pairing.ErrRefused):
			s.Broadcast(NewEvent("pair_failed", map[string]string{}, req.ID))
		case err != nil:
			s.Broadcast(pairFailed(p.Target, err, req.ID))
		}
	}()
}

// pairWithPIN opens a control session with target and runs the initiator's side of the PIN
// path; on success the session becomes the active one. The PIN, typed by the user and checked by
// the PAKE, proves the user's authorisation: a trusted device's address is the new one's then
// (ID-05, #217).
func (s *DaemonServer) pairWithPIN(target, pin string, offer bool, reqID string) (*session.Channel, error) {
	if !strings.Contains(target, ":") {
		target = net.JoinHostPort(target, strconv.Itoa(18887))
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	connPeer, err := dialSecure(s.ctx, target, s.pairingChanConfig())
	if err != nil {
		return nil, err
	}
	fp := connPeer.PeerFingerprint()
	s.mu.RLock()
	name, self := s.config.DeviceName, s.identity.Fingerprint
	s.mu.RUnlock()
	ch := session.NewChannel(connPeer)
	if err := pairing.InitiatePIN(ch, connPeer, pin, self, name); err != nil {
		ch.Close()
		return nil, err
	}
	s.addressTaken(host, fp)
	sess, err := s.finishOutgoing(ch, connPeer, fp, host, offer, reqID)
	if err == nil {
		s.dialedFor(sess, target)
	}
	return sess, err
}

// acceptPIN answers an incoming pair_pin, or a pair_pin_wanted (PAIR-11), with the PIN shown
// by pair_pin_show or the one drawn on demand, by the PAKE. The PIN, shown on this device's
// screen only, proves the user's authorisation: a trusted device's address is the new one's then
// (ID-05, #217). It returns the
// initiator's name once both sides confirmed, after pair_accept, and whether this side asked to
// offer mutual trust (pair_pin_show with trust). Nothing is written (PAIR-08). Any failure gets
// the same neutral pair_reject (PAIR-07) and a pair_failed event.
func (s *DaemonServer) acceptPIN(conn *securechan.Conn, ch *session.Channel, msg session.Message, devName string) (string, bool, bool) {
	ip, fp := ch.RemoteIP(), conn.PeerFingerprint()
	refuse := func() (string, bool, bool) {
		_ = ch.Send(session.Message{Type: pairing.TypeReject, FileName: pairing.ErrRefused.Error()})
		s.Broadcast(NewEvent("pair_failed", map[string]string{}))
		return "", false, false
	}
	s.mu.Lock()
	shown, trust, self := s.pin, s.pinTrust, s.identity.Fingerprint
	if shown != nil && !shown.Live() {
		shown = nil
	}
	if s.pinDemand == nil {
		s.pinDemand = pairing.NewPINOnDemand(pairing.PINConfig{Show: s.showOnDemand})
	}
	demand := s.pinDemand
	if msg.Type == pairing.TypePINWanted {
		s.pinDemandFrom = ip
	}
	s.mu.Unlock()
	if shown == nil {
		trust = false
	}
	var name string
	var err error
	if msg.Type == pairing.TypePINWanted {
		// The PIN on screen answers, else one is drawn and shown (pin_requested).
		name, err = pairing.AnswerPINWanted(ch, conn, func() (*pairing.PIN, error) {
			if shown != nil {
				return shown, nil
			}
			return demand.Get()
		}, self)
	} else {
		pin := shown
		if pin == nil {
			pin = demand.Current() // asked for on another connection, typed on this one
		}
		name, err = pairing.RespondPIN(ch, conn, msg, pin, self)
	}
	if errors.Is(err, pairing.ErrNoPINTyped) {
		return "", false, false // to be typed on another connection: nothing failed
	}
	if err != nil {
		return refuse()
	}
	s.addressTaken(ip, fp)
	if shown == nil {
		demand.Close() // a PIN serves one pairing (PAIR-02)
	}
	if err := s.touchTrust(fp, name, ip); err != nil {
		s.Broadcast(NewEvent("log", map[string]string{"level": "WARN", "message": "could not save the trusted device: " + err.Error()}))
	}
	ch.Authenticate()
	_ = conn.SetDeadline(time.Time{})
	_ = ch.Send(session.Message{Type: pairing.TypeAccept, DeviceName: devName, TrustsYou: s.declares(fp)})
	return name, trust, true
}
