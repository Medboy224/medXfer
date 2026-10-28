package pairing

import (
	"context"
	"errors"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/netbind"
	"github.com/Medboy224/medXfer/pkg/pake"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
)

// Message types of the exchange, inside the TLS channel.
const (
	TypeSecret = "pair_secret" // scanner -> shower: {secret, device_name}
	TypeHello  = "pair_hello"  // a device of mutual trust -> shower: {device_name, trusts_you} (P3, PAIR-09)
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

// CodePINRequired is the pair_reject code of a device that is not of mutual trust: a PIN or a
// QR code is needed (PAIR-12).
const CodePINRequired = "pin_required"

// ErrPINRequired: the other device is not of mutual trust; pair with a PIN or a QR code.
var ErrPINRequired = errors.New("pairing: a PIN or a QR code is needed")

// Result is the device a pairing succeeded with.
type Result struct {
	Fingerprint identity.Fingerprint
	Name        string
	Address     string // IP the peer was seen at
	// Offer is, on the scanner's side, the shower's pair_accept message: for a one-shot send it
	// carries the transfer (file, size, batch, data port).
	Offer session.Message
	// Mutual: the devices were already of mutual trust (P3). Trusted: they became so during
	// this pairing, both users having said yes (PAIR-10). Otherwise nothing was written
	// (PAIR-08).
	Mutual, Trusted bool
}

// Options of both sides.
type Options struct {
	Identity *identity.Identity
	// Trust is this device's list: read for mutual trust (P3), written only by a trust
	// negotiation both users accepted (PAIR-10).
	Trust *identity.TrustStore
	// OfferTrust: this side wants mutual trust (--trust): it says so in its pairing message,
	// and the negotiation follows pair_accept on the same connection.
	OfferTrust bool
	// AskTrust asks this side's user whether to accept the other's offer of mutual trust;
	// ctx ends at TrustAnswerTime. Nil answers no.
	AskTrust func(ctx context.Context, peer Result) bool
	// DeviceName is sent to the other side, which shows it and uses it as the alias.
	DeviceName string
	// Offer (shower) is sent with pair_accept: the transfer a one-shot send offers, and Follow
	// right after it: the pages of an offer too big for one message (session.OfferMessages).
	Offer  session.Message
	Follow []session.Message
	// BeforeAccept (shower), when set, runs once the scanner is accepted and before pair_accept
	// is sent: a one-shot send registers there the transfer the scanner will join on the data
	// connections, so that its first DATA_JOIN finds it. An error refuses the pairing.
	BeforeAccept func(r Result) error
	// PIN (shower), when set, also accepts the PIN path: a device without a camera types the
	// PIN shown (D6). Serve closes it when it returns: a PIN serves one pairing (PAIR-02).
	PIN *PIN
}

// Serve is the shower's side: it waits on ln until a scanner presents the invitation's secret,
// or a device types o.PIN (shown at once, or asked for on demand), or a device of mutual trust
// says hello, or all of them expire. inv may be nil when o.PIN is set. Refusals are neutral and
// the wait goes on. Nothing is written, unless both users accept mutual trust (PAIR-08,
// PAIR-10). ln may be the control listener of a pkg/transfer.Hub, whose connections are
// already secure.
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
	// The first device accepted claims the pairing: from then on nothing expires and nobody
	// else is accepted, so the trust question keeps its time (audit 10, #156).
	var claimMu sync.Mutex
	claimed := make(chan struct{})
	claim := func() bool {
		claimMu.Lock()
		defer claimMu.Unlock()
		select {
		case <-claimed:
			return false
		default:
		}
		if ctx.Err() != nil {
			return false
		}
		close(claimed)
		return true
	}
	end := func(cause error) { // unless claimed
		claimMu.Lock()
		defer claimMu.Unlock()
		select {
		case <-claimed:
		default:
			cancel(cause)
		}
	}
	go func() {
		defer ln.Close()
		for {
			t := time.NewTimer(time.Until(serveDeadline(inv, o.PIN)))
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-claimed: // stop accepting
				t.Stop()
				return
			case <-stopped: // a guesser at work: stop waiting, QR code included (#105)
				t.Stop()
				end(ErrTooManyPINs)
				return
			case <-t.C:
				// A PIN destroyed and drawn again moves the deadline (PAIR-04).
				if !time.Now().Before(serveDeadline(inv, o.PIN)) {
					end(ErrExpired)
					return
				}
			}
		}
	}()

	gate := securechan.NewGate()
	outcome := make(chan claimOutcome, 1) // of the pairing that claimed
	for {
		raw, err := ln.Accept()
		if err != nil {
			select {
			case <-claimed:
				out := <-outcome
				return out.r, out.err
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
			won := false
			r, err := serveOne(ctx, raw, inv, o, gate, func() bool { won = claim(); return won })
			if won {
				outcome <- claimOutcome{r, err}
			}
		}()
	}
}

// claimOutcome is the outcome of the pairing that claimed Serve.
type claimOutcome struct {
	r   Result
	err error
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

// serveOne serves one connection. claim, once the device is accepted, reports whether it is
// the one that pairs: false when another device came first or the wait ended.
func serveOne(ctx context.Context, raw net.Conn, inv *Invitation, o Options, gate *securechan.Gate, claim func() bool) (Result, error) {
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
	theyOffer := msg.TrustOffer
	switch msg.Type {
	case TypeSecret:
		if inv == nil {
			return reject()
		}
		if err := inv.Redeem(DecodeSecret(msg.Secret), time.Now()); err != nil {
			_, _ = reject()
			return Result{}, err
		}
	case TypeHello: // P3 only: no question, no code (PAIR-09, PAIR-12)
		if !Mutual(o.Trust, r.Fingerprint, msg.TrustsYou) {
			_ = ch.Send(session.Message{Type: TypeReject, FileName: ErrPINRequired.Error(), Code: CodePINRequired})
			return Result{}, ErrPINRequired
		}
		r.Mutual = true
	case TypePIN:
		c, err := respondPIN(ch, conn, msg, o.PIN, o.Identity.Fingerprint)
		if err != nil {
			_, _ = reject()
			return Result{}, err
		}
		r.Name, theyOffer = c.DeviceName, c.TrustOffer
	case TypePINWanted: // a device picked this one in a list: the PIN on screen answers it
		c, err := answerPINWanted(ch, conn, func() (*PIN, error) { return o.PIN, nil }, o.Identity.Fingerprint)
		if err != nil {
			_, _ = reject()
			return Result{}, err
		}
		r.Name, theyOffer = c.DeviceName, c.TrustOffer
	default:
		return reject()
	}
	conn.Authenticate() // also lifts the Unauth deadline, which the user's answer may have passed
	if !claim() {
		return reject()
	}
	if o.BeforeAccept != nil {
		if err := o.BeforeAccept(r); err != nil {
			_, _ = reject()
			return Result{}, err
		}
	}
	accept := o.Offer
	accept.Type, accept.DeviceName = TypeAccept, o.DeviceName
	accept.TrustsYou = Declares(o.Trust, r.Fingerprint)
	accept.TrustOffer = o.OfferTrust && !r.Mutual
	if err := ch.Send(accept); err != nil {
		return Result{}, err
	}
	for _, m := range o.Follow {
		if err := ch.Send(m); err != nil {
			return Result{}, err
		}
	}
	if !r.Mutual && (o.OfferTrust || theyOffer) {
		r.Trusted = negotiate(ctx, ch, conn, o, r)
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
	c, err := respondPIN(ch, conn, msg, pin, self)
	return c.DeviceName, err
}

// respondPIN is RespondPIN returning the initiator's pair_pin_confirm: its name, and whether it
// offers mutual trust.
func respondPIN(ch *session.Channel, conn *securechan.Conn, msg session.Message, pin *PIN, self identity.Fingerprint) (session.Message, error) {
	if pin == nil || !slices.Contains(pake.Suites(), msg.Suite) {
		return session.Message{}, ErrRefused
	}
	code, counted := pin.begin()
	paired := false
	if counted {
		defer func() { pin.end(paired) }()
	} else {
		var err error
		if code, err = drawPIN(pin.cfg.Rand, pin.cfg.Digits); err != nil {
			return session.Message{}, err
		}
	}
	ekm, err := conn.ChannelBinding()
	if err != nil {
		return session.Message{}, err
	}
	ex, err := pake.New(msg.Suite, pake.Params{Password: code, CI: pinCI(conn.PeerFingerprint(), self),
		SID: ekm, Role: pake.Responder})
	if err != nil {
		return session.Message{}, err
	}
	s, err := ex.Finish(msg.Share)
	if err != nil {
		return session.Message{}, ErrRefused
	}
	if err := ch.Send(session.Message{Type: TypePINReply, Share: ex.Share(), Confirm: s.Tag()}); err != nil {
		return session.Message{}, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(replyTimeout))
	m, err := ch.Read()
	if err != nil {
		return session.Message{}, err
	}
	if m.Type != TypePINConfirm || !s.Verify(m.Confirm) || !counted {
		return session.Message{}, ErrRefused
	}
	paired = true
	return m, nil
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
	if err := initiatePIN(ch, conn, pin, o.Identity.Fingerprint, o.DeviceName, o.OfferTrust); err != nil {
		return Result{}, err
	}
	return awaitAccept(ctx, ch, conn, conn.PeerFingerprint(), host, replyTimeout, o)
}

// InitiatePIN is the initiator's side of the PIN path on an open connection, up to its own
// confirmation; self is this device's key, name the one sent once the responder has proved it
// holds the PIN. On success the connection is in AUTH and the caller reads pair_accept. A
// wrong PIN, or a relay, gives ErrRefused.
func InitiatePIN(ch *session.Channel, conn *securechan.Conn, pin string, self identity.Fingerprint, name string) error {
	return initiatePIN(ch, conn, pin, self, name, false)
}

// initiatePIN is InitiatePIN that also says, in pair_pin_confirm, whether this side offers
// mutual trust.
func initiatePIN(ch *session.Channel, conn *securechan.Conn, pin string, self identity.Fingerprint, name string, offer bool) error {
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
	return ch.Send(session.Message{Type: TypePINConfirm, Confirm: s.Tag(), DeviceName: name, TrustOffer: offer})
}

// Join is the scanner's side: it connects to the URI's address pinning the URI's key, sends
// the secret, and waits for pair_accept.
func Join(ctx context.Context, u URI, o Options) (Result, error) {
	if !time.Now().Before(u.Expires) {
		return Result{}, ErrExpired
	}
	conn, ch, host, err := dial(ctx, u.Addr(), securechan.Config{Identity: o.Identity, Expected: u.Fingerprint})
	if err != nil {
		return Result{}, err
	}
	defer ch.Close()
	msg := session.Message{Type: TypeSecret, Secret: EncodeSecret(u.Secret), DeviceName: o.DeviceName,
		TrustsYou: Declares(o.Trust, u.Fingerprint), TrustOffer: o.OfferTrust}
	if err := ch.Send(msg); err != nil {
		return Result{}, err
	}
	return awaitAccept(ctx, ch, conn, u.Fingerprint, host, replyTimeout, o)
}

// JoinTrusted reaches the device at addr without a code, as a device of mutual trust (P3):
// only if its key is in this side's list, and only if it has this side in its own. Otherwise
// it returns ErrPINRequired, with the key seen in the Result (for ID-05): pair by a PIN on
// demand (JoinPINOnDemand) or a QR code then.
func JoinTrusted(ctx context.Context, addr string, o Options) (Result, error) {
	conn, ch, host, err := dial(ctx, addr, securechan.Config{Identity: o.Identity, Trust: o.Trust, AllowUnknown: true})
	if err != nil {
		return Result{}, err
	}
	defer ch.Close()
	fp := conn.PeerFingerprint()
	if !Declares(o.Trust, fp) {
		return Result{Fingerprint: fp, Address: host}, ErrPINRequired // nothing is sent
	}
	if err := ch.Send(session.Message{Type: TypeHello, DeviceName: o.DeviceName, TrustsYou: true}); err != nil {
		return Result{}, err
	}
	r, err := awaitAccept(ctx, ch, conn, fp, host, replyTimeout, Options{Identity: o.Identity, Trust: o.Trust})
	if errors.Is(err, ErrPINRequired) {
		r.Fingerprint, r.Address = fp, host
	}
	r.Mutual = err == nil
	return r, err
}

// dial opens the secure channel of a pairing to addr.
func dial(ctx context.Context, addr string, cfg securechan.Config) (*securechan.Conn, *session.Channel, string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, nil, "", err
	}
	d := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second, Control: netbind.Control}
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

// awaitAccept waits up to wait for pair_accept and its offer; then, if either side offers
// mutual trust, the negotiation runs on the same connection.
func awaitAccept(ctx context.Context, ch *session.Channel, conn *securechan.Conn, fp identity.Fingerprint, host string, wait time.Duration, o Options) (Result, error) {
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	resp, err := ch.Read()
	if err != nil {
		return Result{}, err
	}
	if resp.Type == TypeReject && resp.Code == CodePINRequired {
		return Result{}, ErrPINRequired
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
	if o.OfferTrust || resp.TrustOffer {
		r.Trusted = negotiate(ctx, ch, conn, o, r)
	}
	return r, nil
}

// negotiate runs the trust negotiation of a one-shot pairing, on its connection (PAIR-10):
// the sides that offer send trust_offer; a side asked answers with its user's choice
// (o.AskTrust, TrustAnswerTime at most). It reports whether mutual trust was written on this
// side. The transfer does not depend on it.
func negotiate(ctx context.Context, ch *session.Channel, conn *securechan.Conn, o Options, r Result) bool {
	if o.Trust == nil {
		return false
	}
	talk := NewTrustTalk(o.Trust, o.Identity.Fingerprint, identity.Peer{Fingerprint: r.Fingerprint, Alias: r.Name, LastAddress: r.Address})
	_ = conn.SetDeadline(time.Now().Add(TrustAnswerTime + replyTimeout))
	defer func() { _ = conn.SetDeadline(time.Time{}) }()
	// The caller giving up ends the negotiation at once, the wait for the peer included.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	if o.OfferTrust {
		if err := ch.Send(talk.Offer()); err != nil {
			return false
		}
	}
	established := false
	for {
		m, err := ch.Read()
		if err != nil || !IsTrustMessage(m.Type) {
			return established
		}
		reply, ev, err := talk.Handle(m)
		if err != nil {
			return established
		}
		if reply != nil && ch.Send(*reply) != nil {
			return established
		}
		if established && !talk.AwaitsCommit() {
			return true // the peer's commit came: both keep the same key
		}
		switch ev {
		case TrustAsked:
			yes := false
			if o.AskTrust != nil {
				actx, cancel := context.WithTimeout(ctx, TrustAnswerTime)
				yes = o.AskTrust(actx, r)
				cancel()
			}
			answer, err := talk.Answer(yes)
			if err != nil || ch.Send(answer) != nil || !yes {
				return false
			}
		case TrustEstablished:
			// Both offered: our commit is sent, the peer's is on its way. Without it, each side
			// could keep its own recognition key (DISC-11, #170).
			if !talk.AwaitsCommit() {
				return true
			}
			established = true
			_ = conn.SetDeadline(time.Now().Add(replyTimeout))
		case TrustDeclined:
			return false
		}
	}
}
