package main

// One-shot send and receive with mutual authentication (WP-3.5b), on the sender's single port
// (WP-4.2). The sender shows a QR code (path P1) and a PIN (path P2, typed by a receiver
// without a camera, or one that picks the sender in its list); a device of mutual trust needs
// neither (P3). The receiver paired becomes the only key that may join the transfer on the data
// connections (CHAN-06). Nothing is written in trusted.json, unless both users accept mutual
// trust (--trust, PAIR-08, PAIR-10).

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
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

// oneShotPairing is what xfer send serves on its single port (bible §6.1, §6.2): its QR code
// and its PIN, both at once by default (D2 of the trust plan), and mutual trust when asked
// (PAIR-10). A device of mutual trust gets in without either (P3).
type oneShotPairing struct {
	hub    *transfer.Hub
	id     *identity.Identity
	trust  *identity.TrustStore
	host   string
	port   int
	ttl    time.Duration // of each QR code; with a PIN, no longer than a PIN lives (PAIR-02)
	offer  session.Message
	follow []session.Message
	before func(pairing.Result) error // once the receiver is accepted, before pair_accept
	shown  func(pairing.URI)          // each QR code: the first, then one per renewal; nil: none (--pin)
	pin    func(code string, expires time.Time)
	// offerTrust: --trust; askTrust asks the user about the receiver's offer.
	offerTrust bool
	askTrust   func(context.Context, pairing.Result) bool
}

// serve waits on the hub's control connections until a receiver pairs: it renews the QR code
// and the PIN as they expire (same port, new secret).
func (p oneShotPairing) serve(ctx context.Context) (pairing.Result, error) {
	name, _ := os.Hostname()
	o := pairing.Options{Identity: p.id, Trust: p.trust, DeviceName: name, Offer: p.offer, Follow: p.follow,
		BeforeAccept: p.before, OfferTrust: p.offerTrust, AskTrust: p.askTrust}
	ttl := p.ttl
	if p.pin != nil {
		ttl = min(ttl, pairing.TTL)
	}
	for {
		ln := p.hub.Control()
		var inv *pairing.Invitation
		var err error
		o.PIN = nil
		if p.pin != nil { // for a receiver without a camera, or one that picks this device in its list
			o.PIN, err = pairing.NewPIN(pairing.PINConfig{TTL: ttl, Show: p.pin})
		}
		if err == nil && p.shown != nil {
			if inv, err = pairing.NewInvitation(ttl); err == nil {
				p.shown(inv.URI(p.id.Fingerprint, p.host, p.port))
			}
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

// startOneShotPairing serves the pairing of `xfer send`: it prints the QR code and the PIN
// (only the PIN with usePIN), and registers the transfer for the paired receiver.
func startOneShotPairing(ctx context.Context, send *oneShotSend, port int, data session.Message, follow []session.Message,
	trust *identity.TrustStore, usePIN, offerTrust bool) {
	first := true
	ready := make(chan struct{})
	var readyOnce sync.Once
	var shown func(pairing.URI)
	if !usePIN {
		shown = func(u pairing.URI) {
			if !first {
				fmt.Println("\n[*] The QR code and the PIN have expired: here are new ones.")
			}
			first = false
			printReceiveCommand(os.Stdout, u)
		}
	}
	addr := net.JoinHostPort(getLocalIP(), strconv.Itoa(port))
	screen := pinScreen(os.Stdout, fmt.Sprintf("xfer recv, then pick this device; or: xfer recv --pin %s", addr))
	pin := func(code string, expires time.Time) {
		screen(code, expires)
		readyOnce.Do(func() { close(ready) })
	}
	p := oneShotPairing{hub: send.hub, id: localID, trust: trust, host: getLocalIP(), port: port, ttl: sendQRTTL,
		offer: data, follow: follow, before: send.register, shown: shown, pin: pin,
		offerTrust: offerTrust, askTrust: askTrustFrom(os.Stdout, stdinLines())}
	go func() {
		r, err := p.serve(ctx)
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
		how := "proved by the QR code or the PIN"
		switch {
		case r.Mutual:
			how = "a device of mutual trust"
		case !r.Trusted:
			how += ", for this transfer only"
		}
		fmt.Printf("\n[+] Receiver '%s' (%s) paired, key %s: %s.\n", name, r.Address, r.Fingerprint.Short(), how)
		trustOutcome(os.Stdout, r, offerTrust)
	}()
	<-ready // the QR code and the PIN are on screen before the sender says it is waiting
}

// trustOutcome says how a trust negotiation ended, if one ran (PAIR-10).
func trustOutcome(w io.Writer, r pairing.Result, offered bool) {
	switch {
	case r.Trusted:
		fmt.Fprintf(w, "[+] '%s' is now a trusted device, on both sides: next time, no PIN (xfer trust list).\n", r.Name)
	case offered && !r.Mutual:
		fmt.Fprintln(w, "[*] No mutual trust: nothing was saved, this session stays temporary.")
	}
}

// askTrustFrom asks the user whether to accept the other device's offer of mutual trust: an
// explicit yes only, within the time ctx leaves (PAIR-10).
func askTrustFrom(w io.Writer, lines <-chan string) func(context.Context, pairing.Result) bool {
	return func(ctx context.Context, r pairing.Result) bool {
		name := r.Name
		if name == "" {
			name = r.Address
		}
		fmt.Fprintf(w, "\n[?] '%s' (key %s) offers mutual trust: both devices would then reconnect without a PIN.\n"+
			"    Accept? [y/N]: ", name, r.Fingerprint.Short())
		select {
		case l := <-lines:
			if a := strings.ToLower(strings.TrimSpace(l)); a == "y" || a == "yes" || a == "o" || a == "oui" {
				return true
			}
		case <-ctx.Done():
			fmt.Fprintln(w, "\n[-] No answer: declined.")
		}
		return false
	}
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

// stdinLines are the lines typed on the terminal, read by one goroutine for the rest of the
// program (xfer send, xfer recv): several readers of os.Stdin, each with its own buffer, would
// steal each other's lines. It starts at the first call: xfer node and xfer ctl read os.Stdin
// their own way.
var stdinLines = sync.OnceValue(func() <-chan string {
	lines := make(chan string)
	go func() {
		r := bufio.NewReader(os.Stdin)
		for {
			l, err := r.ReadString('\n')
			if l != "" {
				lines <- l
			}
			if err != nil {
				close(lines)
				return
			}
		}
	}()
	return lines
})

// askLine reads the user's answer, or "" (no) when ctx ends, after trustPromptTimeout, or
// without a terminal.
func askLine(ctx context.Context) string {
	select {
	case s := <-stdinLines():
		return s
	case <-ctx.Done():
	case <-time.After(trustPromptTimeout):
		fmt.Println("\n[-] No answer: refused.")
	}
	return ""
}
