package pairing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pake"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
)

// Message types of the exchange, inside the TLS channel.
const (
	TypeSecret = "pair_secret" // scanner -> shower: {secret, device_name}
	TypeHello  = "pair_hello"  // scanner without the QR code -> shower: {device_name}; the shower's user decides
	TypeAccept = "pair_accept" // shower -> scanner: {device_name}, and the offer of a one-shot send
	TypeReject = "pair_reject" // shower -> scanner: neutral reason

	// The PIN path (bible §6.2, D3 of the J5 plan): the initiator types the PIN the responder
	// shows. Names travel only once both confirmations passed (PAIR-07).
	TypePIN        = "pair_pin"         // initiator -> responder: {suite, share}
	TypePINReply   = "pair_pin_reply"   // responder -> initiator: {share, confirm}
	TypePINConfirm = "pair_pin_confirm" // initiator -> responder: {confirm, device_name}
)

// PINSuite is the PAKE of the PIN path (gate G1). pair_pin names it: another suite is another
// name, which a responder that does not know it refuses (D9).
const PINSuite = pake.SuiteCPaceRistretto255

// pinService opens the channel identifier: CI = lv_cat(pinService, fp_i, fp_r) (bible §6.2).
const pinService = "medXfer-pair-v1"

func pinCI(initiator, responder identity.Fingerprint) []byte {
	return pake.LVCat([]byte(pinService), initiator[:], responder[:])
}

// replyTimeout bounds the wait for pair_accept after pair_secret: no user is involved.
const replyTimeout = 10 * time.Second

// ErrDeclined: this side's user did not accept the other device's key (Ask).
var ErrDeclined = errors.New("pairing: the other device's key was not accepted")

// Result is the device a pairing succeeded with.
type Result struct {
	Fingerprint identity.Fingerprint
	Name        string
	Address     string // IP the peer was seen at
	// Offer is, on the scanner's side, the shower's pair_accept message: for a one-shot send it
	// carries the transfer (file, size, batch, data port).
	Offer session.Message
}

// Options of both sides.
type Options struct {
	Identity *identity.Identity
	Trust    *identity.TrustStore
	// Once keeps the trust to this session: nothing is written in trusted.json (PAIR-08).
	Once bool
	// DeviceName is sent to the other side, which shows it and uses it as the alias.
	DeviceName string
	// Offer (shower) is sent with pair_accept: the transfer a one-shot send offers, and Follow
	// right after it: the pages of an offer too big for one message (session.OfferMessages).
	Offer  session.Message
	Follow []session.Message
	// Confirm (shower) decides a pair_hello, sent by a device that has no QR code: provisional
	// trust, confirmed by the user (WP-3.3, decision D3). Nil refuses every pair_hello. Calls
	// come one at a time; ctx ends when the wait is over.
	Confirm func(ctx context.Context, r Result) bool
	// BeforeAccept (shower), when set, runs once the scanner is accepted and before pair_accept
	// is sent: a one-shot send registers there the transfer the scanner will join on the data
	// connections, so that its first DATA_JOIN finds it. An error refuses the pairing.
	BeforeAccept func(r Result) error
	// PIN (shower), when set, also accepts the PIN path: a device without a camera types the
	// PIN shown (D6). Serve closes it when it returns: a PIN serves one pairing (PAIR-02).
	PIN *PIN
}

func remember(o Options, r Result) error {
	if o.Once || o.Trust == nil {
		return nil
	}
	return o.Trust.Add(identity.Peer{Fingerprint: r.Fingerprint, Alias: r.Name, LastAddress: r.Address})
}

// Serve is the shower's side: it waits on ln until a scanner presents the invitation's secret,
// or one without it is accepted by o.Confirm, or a device types o.PIN, or all of them expire.
// inv may be nil when o.PIN is set. Refusals are neutral and the wait goes on. On success the
// scanner's key is trusted (unless o.Once) and returned. ln may be the control listener of a
// pkg/transfer.Hub, whose connections are already secure.
func Serve(ctx context.Context, ln net.Listener, inv *Invitation, o Options) (Result, error) {
	if inv == nil && o.PIN == nil {
		return Result{}, errors.New("pairing: nothing to serve")
	}
	if o.PIN != nil {
		defer o.PIN.Close()
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var stopped <-chan struct{} // nil without a PIN: never ready
	if o.PIN != nil {
		stopped = o.PIN.stopped
	}
	go func() {
		defer ln.Close()
		for {
			t := time.NewTimer(time.Until(serveDeadline(inv, o.PIN)))
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-stopped: // a guesser at work: stop waiting, QR code included (#105)
				t.Stop()
				cancel(ErrTooManyPINs)
				return
			case <-t.C:
				// A PIN destroyed and drawn again moves the deadline (PAIR-04).
				if !time.Now().Before(serveDeadline(inv, o.PIN)) {
					cancel(ErrExpired)
					return
				}
			}
		}
	}()

	gate := securechan.NewGate()
	var confirmMu sync.Mutex
	results := make(chan Result, 1)
	for {
		raw, err := ln.Accept()
		if err != nil {
			select {
			case r := <-results:
				return r, nil
			default:
			}
			if errors.Is(context.Cause(ctx), ErrTooManyPINs) {
				return Result{}, ErrTooManyPINs
			}
			if errors.Is(context.Cause(ctx), ErrExpired) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return Result{}, ErrExpired
			}
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}
			return Result{}, err
		}
		go func() {
			r, err := serveOne(ctx, raw, inv, o, gate, &confirmMu)
			if err == nil {
				select {
				case results <- r:
					cancel(nil) // stop accepting: the invitation is used
				default:
				}
			}
		}()
	}
}

// serveDeadline is when everything served has expired.
func serveDeadline(inv *Invitation, pin *PIN) time.Time {
	var d time.Time
	if inv != nil {
		d = inv.Expires()
	}
	if pin != nil {
		if pd := pin.deadline(); pd.After(d) {
			d = pd
		}
	}
	return d
}

func serveOne(ctx context.Context, raw net.Conn, inv *Invitation, o Options, gate *securechan.Gate, confirmMu *sync.Mutex) (Result, error) {
	// A connection routed by the device's single port (pkg/transfer.Hub) is already secure.
	conn, ok := raw.(*securechan.Conn)
	if !ok {
		var err error
		conn, err = securechan.Server(ctx, raw, securechan.Config{Identity: o.Identity, Trust: o.Trust, AllowUnknown: true, Gate: gate})
		if err != nil {
			return Result{}, err
		}
	}
	ch := session.NewChannel(conn)
	defer ch.Close()
	_ = conn.SetReadDeadline(time.Now().Add(replyTimeout))
	msg, err := ch.Read()
	if err != nil {
		return Result{}, err
	}
	reject := func() (Result, error) {
		_ = ch.Send(session.Message{Type: TypeReject, FileName: ErrRefused.Error()}) // PAIR-07: same answer
		return Result{}, ErrRefused
	}
	r := Result{Fingerprint: conn.PeerFingerprint(), Name: msg.DeviceName, Address: ch.RemoteIP()}
	switch msg.Type {
	case TypeSecret:
		if inv == nil {
			return reject()
		}
		if err := inv.Redeem(DecodeSecret(msg.Secret), time.Now()); err != nil {
			_, _ = reject()
			return Result{}, err
		}
	case TypeHello:
		if o.Confirm == nil {
			return reject()
		}
		confirmMu.Lock()
		ok := ctx.Err() == nil && o.Confirm(ctx, r)
		confirmMu.Unlock()
		if !ok || ctx.Err() != nil {
			return reject()
		}
	case TypePIN:
		name, err := RespondPIN(ch, conn, msg, o.PIN, o.Identity.Fingerprint)
		if err != nil {
			_, _ = reject()
			return Result{}, err
		}
		r.Name = name
	default:
		return reject()
	}
	conn.Authenticate() // also lifts the Unauth deadline, which the user's answer may have passed
	if err := remember(o, r); err != nil {
		return Result{}, fmt.Errorf("saving the trusted device: %w", err)
	}
	if o.BeforeAccept != nil {
		if err := o.BeforeAccept(r); err != nil {
			_, _ = reject()
			return Result{}, err
		}
	}
	accept := o.Offer
	accept.Type, accept.DeviceName = TypeAccept, o.DeviceName
	if err := ch.Send(accept); err != nil {
		return Result{}, err
	}
	for _, m := range o.Follow {
		if err := ch.Send(m); err != nil {
			return Result{}, err
		}
	}
	return r, nil
}

// RespondPIN is the responder's side of the PIN path (bible §6.2), on a connection whose first
// message, msg, is pair_pin; self is this device's key. It returns the initiator's name, which
// comes once the initiator has confirmed the key. The caller then moves the connection to AUTH
// and sends pair_accept, or pair_reject on an error. The attempt counts from the reply on
// (PIN.begin). With no attempt left, it answers on a decoy PIN: the initiator sees the same
// exchange and the same refusal (PAIR-07).
func RespondPIN(ch *session.Channel, conn *securechan.Conn, msg session.Message, pin *PIN, self identity.Fingerprint) (string, error) {
	if pin == nil || !slices.Contains(pake.Suites(), msg.Suite) {
		return "", ErrRefused
	}
	code, counted := pin.begin()
	paired := false
	if counted {
		defer func() { pin.end(paired) }()
	} else {
		var err error
		if code, err = drawPIN(pin.cfg.Rand, pin.cfg.Digits); err != nil {
			return "", err
		}
	}
	ekm, err := conn.ChannelBinding()
	if err != nil {
		return "", err
	}
	ex, err := pake.New(msg.Suite, pake.Params{Password: code, CI: pinCI(conn.PeerFingerprint(), self),
		SID: ekm, Role: pake.Responder})
	if err != nil {
		return "", err
	}
	s, err := ex.Finish(msg.Share)
	if err != nil {
		return "", ErrRefused
	}
	if err := ch.Send(session.Message{Type: TypePINReply, Share: ex.Share(), Confirm: s.Tag()}); err != nil {
		return "", err
	}
	_ = conn.SetReadDeadline(time.Now().Add(replyTimeout))
	m, err := ch.Read()
	if err != nil {
		return "", err
	}
	if m.Type != TypePINConfirm || !s.Verify(m.Confirm) || !counted {
		return "", ErrRefused
	}
	paired = true
	return m.DeviceName, nil
}

// JoinPIN is the initiator's side of the PIN path (bible §6.2): it connects to addr, whose key
// it does not know yet, and runs the PAKE with the PIN the user typed, bound to this TLS
// session and to both keys (PAIR-03). Only once the responder's confirmation passes does it
// confirm in turn and give its name (PAIR-05, PAIR-07); then it trusts the responder (unless
// o.Once). A wrong PIN, an expired one, or a relay between the two devices gives ErrRefused.
// A known device that shows another key is the caller's to refuse (ID-05).
func JoinPIN(ctx context.Context, addr, pin string, o Options) (Result, error) {
	if _, err := NormalizePIN(pin); err != nil {
		return Result{}, err // nothing is sent for a PIN mistyped
	}
	conn, ch, host, err := dial(ctx, addr, securechan.Config{Identity: o.Identity, Trust: o.Trust, AllowUnknown: true})
	if err != nil {
		return Result{}, err
	}
	defer ch.Close()
	if err := InitiatePIN(ch, conn, pin, o.Identity.Fingerprint, o.DeviceName); err != nil {
		return Result{}, err
	}
	return awaitAccept(ch, conn, conn.PeerFingerprint(), host, replyTimeout, o)
}

// InitiatePIN is the initiator's side of the PIN path on an open connection, up to its own
// confirmation; self is this device's key, name the one sent once the responder has proved it
// holds the PIN. On success the connection is in AUTH and the caller reads pair_accept. A
// wrong PIN, or a relay, gives ErrRefused.
func InitiatePIN(ch *session.Channel, conn *securechan.Conn, pin string, self identity.Fingerprint, name string) error {
	code, err := NormalizePIN(pin)
	if err != nil {
		return err
	}
	ekm, err := conn.ChannelBinding()
	if err != nil {
		return err
	}
	ex, err := pake.New(PINSuite, pake.Params{Password: code, CI: pinCI(self, conn.PeerFingerprint()), SID: ekm, Role: pake.Initiator})
	if err != nil {
		return err
	}
	if err := ch.Send(session.Message{Type: TypePIN, Suite: PINSuite, Share: ex.Share()}); err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(replyTimeout))
	reply, err := ch.Read()
	if err != nil {
		return err
	}
	if reply.Type != TypePINReply {
		return ErrRefused
	}
	s, err := ex.Finish(reply.Share)
	if err != nil || !s.Verify(reply.Confirm) {
		return ErrRefused
	}
	conn.Authenticate() // the responder holds the PIN: its offer may pass the Unauth limit
	return ch.Send(session.Message{Type: TypePINConfirm, Confirm: s.Tag(), DeviceName: name})
}

// Join is the scanner's side: it connects to the URI's address pinning the URI's key, sends
// the secret, and on acceptance trusts the shower (unless o.Once).
func Join(ctx context.Context, u URI, o Options) (Result, error) {
	if !time.Now().Before(u.Expires) {
		return Result{}, ErrExpired
	}
	msg := session.Message{Type: TypeSecret, Secret: EncodeSecret(u.Secret), DeviceName: o.DeviceName}
	return exchange(ctx, u.Addr(), u.Fingerprint, nil, msg, replyTimeout, o)
}

// Ask is the scanner's side without a QR code. The shower's key is not known in advance (the
// discovery that found the shower carries no key, DISC-06): Ask reads it in the TLS handshake
// and passes it to decide, which shows it to the user (or finds it among the trusted devices,
// or refuses it under ID-05). Only if decide accepts does Ask send pair_hello, then it waits up
// to wait for the shower's user. A refusal by decide returns ErrDeclined; nothing is sent.
func Ask(ctx context.Context, addr string, decide func(identity.Fingerprint) bool, wait time.Duration, o Options) (Result, error) {
	return exchange(ctx, addr, identity.Fingerprint{}, decide, session.Message{Type: TypeHello, DeviceName: o.DeviceName}, wait, o)
}

// exchange connects to addr, pinning fp when it is set, otherwise asking decide about the key
// presented, then sends msg and waits up to wait for pair_accept.
func exchange(ctx context.Context, addr string, fp identity.Fingerprint, decide func(identity.Fingerprint) bool,
	msg session.Message, wait time.Duration, o Options) (Result, error) {
	cfg := securechan.Config{Identity: o.Identity, Expected: fp}
	if fp.IsZero() {
		cfg.Trust, cfg.AllowUnknown = o.Trust, true
	}
	conn, ch, host, err := dial(ctx, addr, cfg)
	if err != nil {
		return Result{}, err
	}
	defer ch.Close()
	if fp.IsZero() {
		fp = conn.PeerFingerprint()
		// The user may take its time: the Unauth deadline must not cut the connection.
		_ = conn.SetDeadline(time.Now().Add(wait))
		if decide == nil || !decide(fp) {
			return Result{}, ErrDeclined
		}
	}
	if err := ch.Send(msg); err != nil {
		return Result{}, err
	}
	return awaitAccept(ch, conn, fp, host, wait, o)
}

// dial opens the secure channel of a pairing to addr.
func dial(ctx context.Context, addr string, cfg securechan.Config) (*securechan.Conn, *session.Channel, string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, nil, "", err
	}
	d := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}
	raw, err := d.DialContext(ctx, "tcp4", addr)
	if err != nil {
		return nil, nil, "", err
	}
	conn, err := securechan.Client(ctx, raw, cfg)
	if err != nil {
		return nil, nil, "", err
	}
	return conn, session.NewChannel(conn), host, nil
}

// awaitAccept waits up to wait for pair_accept and its offer, then trusts the shower (unless
// o.Once).
func awaitAccept(ch *session.Channel, conn *securechan.Conn, fp identity.Fingerprint, host string, wait time.Duration, o Options) (Result, error) {
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	resp, err := ch.Read()
	if err != nil {
		return Result{}, err
	}
	if resp.Type != TypeAccept {
		return Result{}, ErrRefused
	}
	if resp.Offer != nil { // a one-shot send: the offer, with its pages (D2), checked (LIM-05)
		offer, err := session.ReadOffer(ch, resp)
		if err != nil {
			return Result{}, err
		}
		resp.Offer = &offer
	}
	r := Result{Fingerprint: fp, Name: resp.DeviceName, Address: host, Offer: resp}
	if err := remember(o, r); err != nil {
		return Result{}, fmt.Errorf("saving the trusted device: %w", err)
	}
	return r, nil
}
