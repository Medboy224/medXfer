package api

// Pairing by PIN (bible §6.2, path P2; WP-5.4): pair_pin_show shows a PIN on this device,
// pair_pin types the one another device shows. The PIN goes to the requesting client only
// (sendTo): Broadcast writes events to the journal, which never holds a PIN (chapter 13).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
)

// PairPINShowPayload asks for a PIN; once keeps the trust to the session (PAIR-08), digits is
// 6 (default) or 8.
type PairPINShowPayload struct {
	Once   bool `json:"once,omitempty"`
	Digits int  `json:"digits,omitempty"`
}

// PairPINPayload pairs with the device at target, by the PIN it shows.
type PairPINPayload struct {
	Target string `json:"target"`
	PIN    string `json:"pin"`
	Once   bool   `json:"once,omitempty"`
}

// handlePairPINShow draws a PIN for this daemon's control listener and sends it in
// pair_pin_shown events: the first one, a new one after 3 wrong ones, and "" while the delay
// after PINs destroyed runs (expires is then when the next one comes). A new PIN replaces the
// previous one.
func (s *DaemonServer) handlePairPINShow(conn *websocket.Conn, req RequestMessage) {
	var p PairPINShowPayload
	if len(req.Payload) > 0 {
		_ = json.Unmarshal(req.Payload, &p)
	}
	s.mu.RLock()
	id, ln := s.identity, s.nodeLn
	s.mu.RUnlock()
	if id == nil || ln == nil {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "pairing listener not started"}, req.ID))
		return
	}
	addr := net.JoinHostPort(discovery.GetPrimaryLocalIP(), strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	pin, err := pairing.NewPIN(pairing.PINConfig{Digits: p.Digits, Show: func(code string, expires time.Time) {
		if code == "" && expires.IsZero() { // too many wrong PINs: stopped (#105)
			s.sendTo(conn, NewEvent("pair_pin_shown", map[string]any{"pin": "", "stopped": true, "address": addr,
				"fingerprint": id.Fingerprint.Short()}, req.ID))
			return
		}
		shown := ""
		if code != "" {
			shown = pairing.FormatPIN(code)
			diag.Secrets.Add("PIN", code, shown) // no journal line or bundle may reveal it
		}
		s.sendTo(conn, NewEvent("pair_pin_shown", map[string]any{
			"pin":         shown,
			"expires":     expires.Unix(),
			"address":     addr,
			"fingerprint": id.Fingerprint.Short(),
		}, req.ID))
	}})
	if err != nil {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
		return
	}
	s.mu.Lock()
	old := s.pin
	s.pin, s.pinOnce = pin, p.Once
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
}

// handlePairPIN pairs with the device that shows a PIN. A refusal (wrong PIN, PIN destroyed,
// relay) is a pair_failed event without detail (PAIR-07).
func (s *DaemonServer) handlePairPIN(conn *websocket.Conn, req RequestMessage) {
	var p PairPINPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil || strings.TrimSpace(p.Target) == "" {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": "invalid pair_pin payload"}, req.ID))
		return
	}
	if _, err := pairing.NormalizePIN(p.PIN); err != nil {
		s.sendTo(conn, NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
		return
	}
	go func() {
		_, err := s.pairWithPIN(strings.TrimSpace(p.Target), p.PIN, p.Once, req.ID)
		switch {
		case errors.Is(err, pairing.ErrRefused):
			s.Broadcast(NewEvent("pair_failed", map[string]string{}, req.ID))
		case err != nil:
			s.Broadcast(NewEvent("action_error", map[string]string{"error": fmt.Sprintf("failed to pair with %s: %v", p.Target, err)}, req.ID))
		}
	}()
}

// pairWithPIN opens a control session with target and runs the initiator's side of the PIN
// path; on success the session becomes the active one. A known device that shows another key
// is refused first (ID-05).
func (s *DaemonServer) pairWithPIN(target, pin string, once bool, reqID string) (*session.Channel, error) {
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
	if known, changed := s.keyChanged(host, fp); changed {
		connPeer.Close()
		return nil, fmt.Errorf("REFUSED: %s is the address of '%s' (%s), but the device there presents another key (%s)",
			host, known.Alias, known.Fingerprint.Short(), fp.Short())
	}
	s.mu.RLock()
	name, self := s.config.DeviceName, s.identity.Fingerprint
	s.mu.RUnlock()
	ch := session.NewChannel(connPeer)
	if err := pairing.InitiatePIN(ch, connPeer, pin, self, name); err != nil {
		ch.Close()
		return nil, err
	}
	decision := trustAlways
	if once {
		decision = trustOnce
	}
	return s.finishOutgoing(ch, connPeer, fp, host, decision, reqID)
}

// acceptPIN answers an incoming pair_pin with the PIN of pair_pin_show: ID-05 first, then the
// PAKE. It returns the initiator's name once both sides confirmed, after pair_accept. Any
// failure gets the same neutral pair_reject (PAIR-07) and a pair_failed event.
func (s *DaemonServer) acceptPIN(conn *securechan.Conn, ch *session.Channel, msg session.Message, devName string) (string, bool) {
	ip, fp := ch.RemoteIP(), conn.PeerFingerprint()
	refuse := func() (string, bool) {
		_ = ch.Send(session.Message{Type: pairing.TypeReject, FileName: pairing.ErrRefused.Error()})
		s.Broadcast(NewEvent("pair_failed", map[string]string{}))
		return "", false
	}
	if known, changed := s.keyChanged(ip, fp); changed {
		s.Broadcast(NewEvent("log", map[string]string{"level": "WARN", "message": fmt.Sprintf(
			"REFUSED: %s is the address of '%s' (%s), but the device there presents another key (%s)",
			ip, known.Alias, known.Fingerprint.Short(), fp.Short())}))
		return refuse()
	}
	s.mu.RLock()
	pin, once, self := s.pin, s.pinOnce, s.identity.Fingerprint
	s.mu.RUnlock()
	name, err := pairing.RespondPIN(ch, conn, msg, pin, self)
	if err != nil {
		return refuse()
	}
	decision := trustAlways
	if once {
		decision = trustOnce
	}
	if err := s.rememberTrust(fp, name, ip, decision); err != nil {
		s.Broadcast(NewEvent("log", map[string]string{"level": "WARN", "message": "could not save the trusted device: " + err.Error()}))
	}
	ch.Authenticate()
	_ = conn.SetDeadline(time.Time{})
	_ = ch.Send(session.Message{Type: pairing.TypeAccept, DeviceName: devName})
	return name, true
}
