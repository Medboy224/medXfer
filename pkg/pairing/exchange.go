package pairing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
)

// Message types of the exchange, inside the TLS channel.
const (
	TypeSecret = "pair_secret" // scanner -> shower: {secret, device_name}
	TypeAccept = "pair_accept" // shower -> scanner: {device_name}
	TypeReject = "pair_reject" // shower -> scanner: neutral reason
)

// Result is the device a pairing succeeded with.
type Result struct {
	Fingerprint identity.Fingerprint
	Name        string
	Address     string // IP the peer was seen at
}

// Options of both sides.
type Options struct {
	Identity *identity.Identity
	Trust    *identity.TrustStore
	// Once keeps the trust to this session: nothing is written in trusted.json (PAIR-08).
	Once bool
	// DeviceName is sent to the other side, which shows it and uses it as the alias.
	DeviceName string
}

func remember(o Options, r Result) error {
	if o.Once || o.Trust == nil {
		return nil
	}
	return o.Trust.Add(identity.Peer{Fingerprint: r.Fingerprint, Alias: r.Name, LastAddress: r.Address})
}

// Serve is the shower's side: it waits on ln until a scanner presents the invitation's secret,
// or the invitation expires. Wrong secrets get a neutral refusal and the wait goes on. On
// success the scanner's key is trusted (unless o.Once) and returned.
func Serve(ctx context.Context, ln net.Listener, inv *Invitation, o Options) (Result, error) {
	ctx, cancel := context.WithDeadline(ctx, inv.Expires())
	defer cancel()
	go func() { <-ctx.Done(); ln.Close() }()

	gate := securechan.NewGate()
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
			r, err := serveOne(ctx, raw, inv, o, gate)
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

func serveOne(ctx context.Context, raw net.Conn, inv *Invitation, o Options, gate *securechan.Gate) (Result, error) {
	conn, err := securechan.Server(ctx, raw, securechan.Config{Identity: o.Identity, Trust: o.Trust, AllowUnknown: true, Gate: gate})
	if err != nil {
		return Result{}, err
	}
	ch := session.NewChannel(conn)
	defer ch.Close()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	msg, err := ch.Read()
	if err != nil {
		return Result{}, err
	}
	if msg.Type != TypeSecret {
		_ = ch.Send(session.Message{Type: TypeReject, FileName: ErrRefused.Error()})
		return Result{}, ErrRefused
	}
	if err := inv.Redeem(DecodeSecret(msg.Secret), time.Now()); err != nil {
		_ = ch.Send(session.Message{Type: TypeReject, FileName: ErrRefused.Error()}) // PAIR-07: same answer
		return Result{}, err
	}
	conn.Authenticate()
	r := Result{Fingerprint: conn.PeerFingerprint(), Name: msg.DeviceName, Address: ch.RemoteIP()}
	if err := remember(o, r); err != nil {
		return Result{}, fmt.Errorf("saving the trusted device: %w", err)
	}
	if err := ch.Send(session.Message{Type: TypeAccept, DeviceName: o.DeviceName}); err != nil {
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
	d := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}
	raw, err := d.DialContext(ctx, "tcp4", u.Addr())
	if err != nil {
		return Result{}, err
	}
	conn, err := securechan.Client(ctx, raw, securechan.Config{Identity: o.Identity, Expected: u.Fingerprint})
	if err != nil {
		return Result{}, err
	}
	ch := session.NewChannel(conn)
	defer ch.Close()
	if err := ch.Send(session.Message{Type: TypeSecret, Secret: EncodeSecret(u.Secret), DeviceName: o.DeviceName}); err != nil {
		return Result{}, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	resp, err := ch.Read()
	if err != nil {
		return Result{}, err
	}
	if resp.Type != TypeAccept {
		return Result{}, ErrRefused
	}
	r := Result{Fingerprint: u.Fingerprint, Name: resp.DeviceName, Address: u.Host}
	if err := remember(o, r); err != nil {
		return Result{}, fmt.Errorf("saving the trusted device: %w", err)
	}
	return r, nil
}
