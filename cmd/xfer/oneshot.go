package main

// One-shot send and receive with mutual authentication (WP-3.5b), on the sender's single port
// (WP-4.2). The sender shows a QR code (path P1): the receiver that redeems it, or one without
// it that the sender's user accepts (decision D3), becomes the only key that may join the
// transfer on the data connections (CHAN-06). The receiver pins the sender's key, taken from
// the QR code or confirmed by its user. Nothing is written in trusted.json, unless the user
// answers "always" (PAIR-08).

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

// sendQRTTL is how long the QR code of `xfer send` stays valid; a new one is shown after it.
// Longer than pairing.TTL: the sender waits for someone to walk up with the other device.
const sendQRTTL = 10 * time.Minute

// oneShotSend is the sender's side of `xfer send` on the device's single port (CHAN-01): the
// pairing (QR code, or both users' confirmation) and the data connections of the one transfer
// that carries the whole offer, which only the paired receiver may join (CHAN-06).
type oneShotSend struct {
	hub  *transfer.Hub
	base transfer.ID
	// authorize, when set (xfer send -i), asks the sender's user once the receiver is paired.
	authorize func(pairing.Result) bool

	once   sync.Once
	paired chan struct{} // closed once the receiver is paired and the transfer registered
	l      *transfer.Listener
}

func newOneShotSend(hub *transfer.Hub, base transfer.ID) *oneShotSend {
	return &oneShotSend{hub: hub, base: base, paired: make(chan struct{})}
}

// register makes the transfer joinable by the paired receiver. The pairing calls it before
// pair_accept, so the receiver's first DATA_JOIN finds it. Only one receiver is ever
// registered.
func (o *oneShotSend) register(r pairing.Result) error {
	err := errors.New("a receiver is already paired")
	o.once.Do(func() {
		if o.authorize != nil && !o.authorize(r) {
			err = errors.New("declined by the sender")
			return
		}
		var l *transfer.Listener
		if l, err = o.hub.Register(o.base, r.Fingerprint); err == nil {
			o.l = l
			close(o.paired)
		}
	})
	return err
}

// listener waits for the pairing and returns the transfer's data listener, or nil if ctx
// ends first.
func (o *oneShotSend) listener(ctx context.Context) *transfer.Listener {
	select {
	case <-o.paired:
		return o.l
	case <-ctx.Done():
		return nil
	}
}

// startOneShotHub opens the device's single port for a one-shot send, on bind (0.0.0.0 for
// the network; tests use the loopback).
func startOneShotHub(bind string, port int, trust *identity.TrustStore) (*transfer.Hub, net.Listener, error) {
	ln, err := net.Listen("tcp4", net.JoinHostPort(bind, strconv.Itoa(port)))
	if err != nil {
		return nil, nil, err
	}
	hub := transfer.NewHub(securechan.Config{Identity: localID, Trust: trust, AllowUnknown: true, Gate: securechan.NewGate()})
	hub.Wrap = engine.WrapAccepted // diag builds inject their faults on the network bytes
	go func() { _ = hub.Serve(ln) }()
	return hub, ln, nil
}

// startSendHub opens the one-shot hub on port; when it is taken and anyPort is set, on a free
// port instead, which discovery and the offer then announce (CHAN-01).
func startSendHub(bind string, port int, anyPort bool, trust *identity.TrustStore) (*transfer.Hub, net.Listener, error) {
	hub, ln, err := startOneShotHub(bind, port, trust)
	if err != nil && anyPort {
		if hub2, ln2, err2 := startOneShotHub(bind, 0, trust); err2 == nil {
			return hub2, ln2, nil
		}
	}
	return hub, ln, err
}

// serveOneShotPairing waits on the hub's control connections until a receiver redeems the QR
// code, or one without it is accepted by confirm. shown gets each URI: the first one, then a
// new one each time the previous expires (same port, new secret). before runs once the
// receiver is accepted, before pair_accept.
func serveOneShotPairing(ctx context.Context, hub *transfer.Hub, id *identity.Identity, host string, port int,
	ttl time.Duration, offer session.Message, follow []session.Message, confirm func(context.Context, pairing.Result) bool,
	before func(pairing.Result) error, shown func(pairing.URI), pin func(code string, expires time.Time)) (pairing.Result, error) {
	name, _ := os.Hostname()
	o := pairing.Options{Identity: id, Once: true, DeviceName: name, Offer: offer, Follow: follow, Confirm: confirm, BeforeAccept: before}
	for {
		ln := hub.Control()
		var inv *pairing.Invitation
		var err error
		if pin != nil { // a PIN instead of the QR code, for a receiver without a camera (WP-5.4)
			o.PIN, err = pairing.NewPIN(pairing.PINConfig{TTL: min(ttl, pairing.TTL), Show: pin}) // PAIR-02: 120 s at most
		} else if inv, err = pairing.NewInvitation(ttl); err == nil {
			shown(inv.URI(id.Fingerprint, host, port))
		}
		if err != nil {
			ln.Close()
			return pairing.Result{}, err
		}
		r, err := pairing.Serve(ctx, ln, inv, o)
		ln.Close()
		if errors.Is(err, pairing.ErrExpired) {
			continue
		}
		return r, err
	}
}

// startOneShotPairing runs serveOneShotPairing for `xfer send`: it prints the receive command
// and the QR code, or the PIN (usePIN), and registers the transfers for the paired receiver.
func startOneShotPairing(ctx context.Context, send *oneShotSend, port int, data session.Message, follow []session.Message,
	trust *identity.TrustStore, usePIN bool) {
	first := true
	ready := make(chan struct{})
	var readyOnce sync.Once
	shown := func(u pairing.URI) {
		if !first {
			fmt.Println("\n[*] The QR code has expired: here is a new one.")
		}
		first = false
		printReceiveCommand(os.Stdout, u)
		readyOnce.Do(func() { close(ready) })
	}
	var pin func(string, time.Time)
	if usePIN {
		screen := pinScreen(os.Stdout, fmt.Sprintf("xfer recv --pin %s", net.JoinHostPort(getLocalIP(), strconv.Itoa(port))))
		pin = func(code string, expires time.Time) {
			screen(code, expires)
			readyOnce.Do(func() { close(ready) })
		}
	}
	confirm := receiverConfirm(os.Stdout, askLine, trust)
	go func() {
		r, err := serveOneShotPairing(ctx, send.hub, localID, getLocalIP(), port, sendQRTTL, data, follow, confirm, send.register, shown, pin)
		readyOnce.Do(func() { close(ready) })
		if err != nil {
			if ctx.Err() == nil {
				fmt.Printf("[-] Pairing stopped: %v\n", err)
			}
			return
		}
		name := r.Name
		if name == "" {
			name = r.Address
		}
		fmt.Printf("\n[+] Receiver '%s' (%s) paired, key %s.\n", name, r.Address, r.Fingerprint.Short())
	}()
	<-ready // the QR code is on screen before the sender says it is waiting
}

// printReceiveCommand shows how to receive: the QR code, its URI, or the pairing code.
func printReceiveCommand(w io.Writer, u pairing.URI) {
	fmt.Fprintln(w, " On the receiving device, scan this QR code or run:")
	fmt.Fprintf(w, "   xfer recv '%s'\n", u)
	fmt.Fprintf(w, " (valid until %s)\n\n", u.Expires.Format("15:04:05"))
	if w == os.Stdout {
		discovery.PrintTerminalQR(u.String())
	}
}

// receiverConfirm decides about a receiver that has no QR code: a trusted device is let in,
// any other one is shown to the user, who compares both fingerprints (D3, WP-3.3). The answer
// defaults to no; ID-05 applies.
func receiverConfirm(w io.Writer, ask func(context.Context) string, trust *identity.TrustStore) func(context.Context, pairing.Result) bool {
	return func(ctx context.Context, r pairing.Result) bool {
		if known, changed := keyChanged(trust, r.Address, r.Fingerprint); changed {
			fmt.Fprintf(w, "\n%s\n", keyChangedAlert(known, r.Address, r.Fingerprint))
			return false
		}
		if p, ok := trustedPeer(trust, r.Fingerprint); ok {
			fmt.Fprintf(w, "\n[+] Trusted device '%s' connected from %s.\n", p.Alias, r.Address)
			_ = rememberPeer(trust, r.Fingerprint, r.Name, r.Address, trustOnce)
			return true
		}
		fmt.Fprintf(w, "\n\n[?] A receiver without the QR code is asking for the transfer, from %s.\n", r.Address)
		fmt.Fprint(w, trustQuestion(r.Name, r.Fingerprint, localID.Fingerprint))
		answer := parseTrustAnswer(ask(ctx))
		if answer == trustRefused {
			fmt.Fprintln(w, "[-] Receiver refused.")
			return false
		}
		if err := rememberPeer(trust, r.Fingerprint, r.Name, r.Address, answer); err != nil {
			fmt.Fprintf(w, "[-] Could not save the trusted device: %v\n", err)
		}
		return true
	}
}

func trustedPeer(trust *identity.TrustStore, fp identity.Fingerprint) (identity.Peer, bool) {
	if trust == nil {
		return identity.Peer{}, false
	}
	return trust.Get(fp)
}

// askLine reads the user's answer, or "" (no) when ctx ends, after trustPromptTimeout, or
// without a terminal.
func askLine(ctx context.Context) string {
	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		line <- s
	}()
	select {
	case s := <-line:
		return s
	case <-ctx.Done():
	case <-time.After(trustPromptTimeout):
		fmt.Println("\n[-] No answer: refused.")
	}
	return ""
}
