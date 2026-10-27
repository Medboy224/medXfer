package api

// A control session lost without a word (the network gone a while, WP-B.8) came back only when
// the user pressed Connect again, after the 15 s grace period: the data of a transfer could
// come back alone, but not the session, and with it neither the receiver's progress nor the
// transfer's end. The side that dialed the session now dials it again, as long as the other is
// a trusted device: pinned to its key, without a code. A device that no longer takes this one
// without a code ends the attempts; it is never asked for a PIN.

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/session"
)

var (
	redialEvery = 3 * time.Second  // between two attempts: well within LIM-01's 60 a minute
	redialFor   = 10 * time.Minute // then the user decides
)

// dialedFor remembers target as the address of the session ch, which this side dialed.
func (s *DaemonServer) dialedFor(ch *session.Channel, target string) {
	s.mu.Lock()
	if s.activeSession == ch {
		s.redialTo = target
	}
	s.mu.Unlock()
}

// stopRedial stops dialing a lost session again, and forgets where it was. s.mu is held.
func (s *DaemonServer) stopRedial() {
	if s.redialCancel != nil {
		s.redialCancel()
		s.redialCancel = nil
	}
	s.redialTo = ""
}

// redial dials the lost session with fp at target again, every redialEvery for redialFor, until
// it is back or ctx ends (another session, a disconnect, Stop).
func (s *DaemonServer) redial(ctx context.Context, target string, fp identity.Fingerprint) {
	end := time.Now().Add(redialFor)
	for time.Now().Before(end) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(redialEvery):
		}
		err := s.redialOnce(ctx, target, fp)
		var rej *rejectedError
		if err == nil || errors.As(err, &rej) {
			return
		}
	}
}

// redialOnce opens the session with fp at target again, if none came back meanwhile.
func (s *DaemonServer) redialOnce(ctx context.Context, target string, fp identity.Fingerprint) error {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	cfg := s.pairingChanConfig()
	cfg.Expected, cfg.AllowUnknown = fp, false
	conn, err := dialSecure(ctx, target, cfg)
	if err != nil {
		return err
	}
	s.mu.RLock()
	name, back := s.config.DeviceName, s.activeSession != nil
	s.mu.RUnlock()
	if back || ctx.Err() != nil { // the peer, or the user, brought it back first
		conn.Close()
		return nil
	}
	ch := session.NewChannel(conn)
	if err := ch.Send(session.Message{Type: "pair_request", DeviceName: name, TrustsYou: s.declares(fp)}); err != nil {
		ch.Close()
		return err
	}
	sess, err := s.finishOutgoing(ch, conn, fp, host, false, "")
	if err != nil {
		return err
	}
	s.dialedFor(sess, target)
	return nil
}
