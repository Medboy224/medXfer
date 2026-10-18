package pairing

import (
	"context"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
)

// The PIN on demand (bible §6.2, PAIR-11; D2 of the trust plan): a device picked in a list
// shows a PIN when asked, and the one that picked it types it. A device seen for the first
// time is reached by a PIN or a QR code, never by comparing fingerprints by eye (PAIR-12).
const (
	TypePINWanted = "pair_pin_wanted" // initiator -> responder: show a PIN
	TypePINReady  = "pair_pin_ready"  // responder -> initiator: a PIN is on screen, type it
)

// typingTime bounds how long a responder waits for the PIN typed after pair_pin_ready: no more
// than a PIN lives (PAIR-02).
const typingTime = TTL

// reusable tells what a PINOnDemand does with p: answer with it while it is valid or waiting
// out a delay (its attempts and delays then hold, PAIR-04), or draw another once it paired,
// expired or was closed. stopped: too many PINs were destroyed, no other may be drawn (#105).
func (p *PIN) reusable() (reuse, stopped bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.stopped:
		return false, true
	default:
	}
	if p.closed || p.paired || !time.Now().Before(p.expires) {
		return false, false
	}
	return true, false
}

// PINOnDemand draws a PIN when a device asks for one, and keeps it while it is valid: every
// demand meanwhile gets the same PIN, so asking again gives a guesser no new attempts
// (PAIR-04). Once MaxSeries series of PINs were destroyed, it draws no more (#105): the user
// starts over with Reset. It is safe for concurrent use.
type PINOnDemand struct {
	cfg PINConfig

	mu      sync.Mutex
	pin     *PIN
	stopped bool
}

// NewPINOnDemand returns a source of PINs drawn with c; none is drawn yet.
func NewPINOnDemand(c PINConfig) *PINOnDemand { return &PINOnDemand{cfg: c} }

// Get returns the PIN to answer a demand with, drawing (and showing) one when none is valid.
func (d *PINOnDemand) Get() (*PIN, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return nil, ErrTooManyPINs
	}
	if d.pin != nil {
		reuse, stopped := d.pin.reusable()
		if stopped {
			d.stopped = true
			return nil, ErrTooManyPINs
		}
		if reuse {
			return d.pin, nil
		}
		d.pin.Close()
	}
	p, err := NewPIN(d.cfg)
	if err != nil {
		return nil, err
	}
	d.pin = p
	return p, nil
}

// Reset forgets the current PIN and a stop: the user asked to pair again.
func (d *PINOnDemand) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pin != nil {
		d.pin.Close()
	}
	d.pin, d.stopped = nil, false
}

// Close forgets the current PIN.
func (d *PINOnDemand) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pin != nil {
		d.pin.Close()
		d.pin = nil
	}
}

// AnswerPINWanted is the responder's side of a PIN on demand, on a connection whose first
// message was pair_pin_wanted: it takes a PIN from get (and shows it), says so, then waits for
// the PIN typed and runs the PIN path (RespondPIN). It returns the initiator's name. Any error
// gets the caller's neutral pair_reject (PAIR-07); get failing (no PIN may be drawn) is
// ErrRefused.
func AnswerPINWanted(ch *session.Channel, conn *securechan.Conn, get func() (*PIN, error), self identity.Fingerprint) (string, error) {
	p, err := get()
	if err != nil || p == nil {
		return "", ErrRefused
	}
	if err := ch.Send(session.Message{Type: TypePINReady}); err != nil {
		return "", err
	}
	// The user reads the PIN on this screen and types it there: the Unauth deadline of the
	// handshake would cut the connection meanwhile.
	_ = conn.SetDeadline(time.Now().Add(typingTime))
	m, err := ch.Read()
	if err != nil {
		return "", err
	}
	if m.Type != TypePIN {
		return "", ErrRefused
	}
	return RespondPIN(ch, conn, m, p, self)
}

// RequestPIN is the initiator's side: it asks the responder to show a PIN and waits until it
// says one is on screen. A refusal is ErrRefused.
func RequestPIN(ch *session.Channel, conn *securechan.Conn) error {
	if err := ch.Send(session.Message{Type: TypePINWanted}); err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(replyTimeout))
	m, err := ch.Read()
	if err != nil {
		return err
	}
	if m.Type != TypePINReady {
		return ErrRefused
	}
	return nil
}

// JoinPINOnDemand pairs with the device at addr by a PIN it shows on demand: it asks for the
// PIN, gets the one the user typed from typed, and runs the PIN path, then waits for
// pair_accept. typed may take up to the PIN's life; its error ends the pairing.
func JoinPINOnDemand(ctx context.Context, addr string, typed func() (string, error), o Options) (Result, error) {
	conn, ch, host, err := dial(ctx, addr, securechan.Config{Identity: o.Identity, Trust: o.Trust, AllowUnknown: true})
	if err != nil {
		return Result{}, err
	}
	defer ch.Close()
	if err := RequestPIN(ch, conn); err != nil {
		return Result{}, err
	}
	_ = conn.SetDeadline(time.Now().Add(typingTime))
	pin, err := typed()
	if err != nil {
		return Result{}, err
	}
	if err := InitiatePIN(ch, conn, pin, o.Identity.Fingerprint, o.DeviceName); err != nil {
		return Result{}, err
	}
	return awaitAccept(ch, conn, conn.PeerFingerprint(), host, replyTimeout, o)
}
