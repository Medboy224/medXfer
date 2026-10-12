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
// pairing (QR code, or both users' confirmation) and the data connections, which only the
// paired receiver may join (CHAN-06). A single file is the transfer base; item i of a folder
// is base.Item(i), as the receiver derives it.
type oneShotSend struct {
	hub   *transfer.Hub
	base  transfer.ID
	items int // 0 for a single file, the item count of a folder

	once      sync.Once
	paired    chan struct{} // closed once the receiver is paired and its transfers registered
	listeners []*transfer.Listener
}

func newOneShotSend(hub *transfer.Hub, base transfer.ID, items int) *oneShotSend {
	return &oneShotSend{hub: hub, base: base, items: items, paired: make(chan struct{})}
}

// count is the number of transfers to register.
func (o *oneShotSend) count() int {
	if o.items == 0 {
		return 1
	}
	return o.items
}

// id is the transfer id of item i (0 for a single file).
func (o *oneShotSend) id(i int) transfer.ID {
	if o.items == 0 {
		return o.base
	}
	return o.base.Item(i)
}

// register makes the transfers of this send joinable by the paired receiver. The pairing calls
// it before pair_accept, so the receiver's first DATA_JOIN finds them. Only one receiver is
// ever registered.
func (o *oneShotSend) register(r pairing.Result) error {
	err := errors.New("a receiver is already paired")
	o.once.Do(func() {
		ls := make([]*transfer.Listener, 0, o.count())
		for i := 0; i < o.count(); i++ {
			l, rerr := o.hub.Register(o.id(i), r.Fingerprint)
			if rerr != nil {
				for _, done := range ls {
					done.Close()
				}
				err = rerr
				return
			}
			ls = append(ls, l)
		}
		o.listeners, err = ls, nil
		close(o.paired)
	})
	return err
}

// listener waits for the pairing and returns the data listener of item i, or nil if ctx ends
// first.
func (o *oneShotSend) listener(ctx context.Context, i int) *transfer.Listener {
	select {
	case <-o.paired:
		return o.listeners[i]
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

// serveOneShotPairing waits on the hub's control connections until a receiver redeems the QR
// code, or one without it is accepted by confirm. shown gets each URI: the first one, then a
// new one each time the previous expires (same port, new secret). before runs once the
// receiver is accepted, before pair_accept.
func serveOneShotPairing(ctx context.Context, hub *transfer.Hub, id *identity.Identity, host string, port int,
	ttl time.Duration, offer session.Message, confirm func(context.Context, pairing.Result) bool,
	before func(pairing.Result) error, shown func(pairing.URI)) (pairing.Result, error) {
	name, _ := os.Hostname()
	o := pairing.Options{Identity: id, Once: true, DeviceName: name, Offer: offer, Confirm: confirm, BeforeAccept: before}
	for {
		ln := hub.Control()
		inv, err := pairing.NewInvitation(ttl)
		if err != nil {
			ln.Close()
			return pairing.Result{}, err
		}
		shown(inv.URI(id.Fingerprint, host, port))
		r, err := pairing.Serve(ctx, ln, inv, o)
		ln.Close()
		if errors.Is(err, pairing.ErrExpired) {
			continue
		}
		return r, err
	}
}

// startOneShotPairing runs serveOneShotPairing for `xfer send`: it prints the receive command
// and the QR code, and registers the transfers for the paired receiver.
func startOneShotPairing(ctx context.Context, send *oneShotSend, port int, data session.Message, trust *identity.TrustStore) {
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
	confirm := receiverConfirm(os.Stdout, askLine, trust)
	go func() {
		r, err := serveOneShotPairing(ctx, send.hub, localID, getLocalIP(), port, sendQRTTL, data, confirm, send.register, shown)
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

// offerMessage is what a one-shot send tells its receiver with pair_accept.
func offerMessage(o discovery.TransferOffer, port int, base transfer.ID) session.Message {
	return session.Message{FileName: o.FileName, FileSize: o.FileSize, FileID: o.FileID, Batch: o.Batch,
		DataPort: port, TransferID: base.String()}
}
