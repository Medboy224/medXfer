package pairing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
)

// Message types of the exchange, inside the TLS channel.
const (
	TypeSecret = "pair_secret" // scanner -> shower: {secret, device_name}
	TypeHello  = "pair_hello"  // scanner without the QR code -> shower: {device_name}; the shower's user decides
	TypeAccept = "pair_accept" // shower -> scanner: {device_name}, and the offer of a one-shot send
	TypeReject = "pair_reject" // shower -> scanner: neutral reason
)

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
	// Offer (shower) is sent with pair_accept: the transfer a one-shot send offers.
	Offer session.Message
	// Confirm (shower) decides a pair_hello, sent by a device that has no QR code: provisional
	// trust, confirmed by the user (WP-3.3, decision D3). Nil refuses every pair_hello. Calls
	// come one at a time; ctx ends when the wait is over.
	Confirm func(ctx context.Context, r Result) bool
	// BeforeAccept (shower), when set, runs once the scanner is accepted and before pair_accept
	// is sent: a one-shot send registers there the transfer the scanner will join on the data
	// connections, so that its first DATA_JOIN finds it. An error refuses the pairing.
	BeforeAccept func(r Result) error
}

func remember(o Options, r Result) error {
	if o.Once || o.Trust == nil {
		return nil
	}
	return o.Trust.Add(identity.Peer{Fingerprint: r.Fingerprint, Alias: r.Name, LastAddress: r.Address})
}

// Serve is the shower's side: it waits on ln until a scanner presents the invitation's secret,
// or one without it is accepted by o.Confirm, or the invitation expires. Refusals are neutral
// and the wait goes on. On success the scanner's key is trusted (unless o.Once) and returned.
// ln may be the control listener of a pkg/transfer.Hub, whose connections are already secure.
func Serve(ctx context.Context, ln net.Listener, inv *Invitation, o Options) (Result, error) {
	ctx, cancel := context.WithDeadline(ctx, inv.Expires())
	defer cancel()
	go func() { <-ctx.Done(); ln.Close() }()

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
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
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
					cancel() // stop accepting: the invitation is used
				default:
				}
			}
		}()
	}
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
	return r, nil
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
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return Result{}, err
	}
	d := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}
	raw, err := d.DialContext(ctx, "tcp4", addr)
	if err != nil {
		return Result{}, err
	}
	cfg := securechan.Config{Identity: o.Identity, Expected: fp}
	if fp.IsZero() {
		cfg.Trust, cfg.AllowUnknown = o.Trust, true
	}
	conn, err := securechan.Client(ctx, raw, cfg)
	if err != nil {
		return Result{}, err
	}
	ch := session.NewChannel(conn)
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
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	resp, err := ch.Read()
	if err != nil {
		return Result{}, err
	}
	if resp.Type != TypeAccept {
		return Result{}, ErrRefused
	}
	r := Result{Fingerprint: fp, Name: resp.DeviceName, Address: host, Offer: resp}
	if err := remember(o, r); err != nil {
		return Result{}, fmt.Errorf("saving the trusted device: %w", err)
	}
	return r, nil
}
