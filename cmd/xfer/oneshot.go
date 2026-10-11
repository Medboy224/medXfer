package main

// One-shot send and receive with mutual authentication (WP-3.5b). The sender shows a QR code
// (path P1): the receiver that redeems it, or one without it that the sender's user accepts
// (decision D3), becomes the only key let in on the data connections. The receiver pins the
// sender's key, taken from the QR code or confirmed by its user. Nothing is written in
// trusted.json, unless the user answers "always" (PAIR-08).

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
)

// sendQRTTL is how long the QR code of `xfer send` stays valid; a new one is shown after it.
// Longer than pairing.TTL: the sender waits for someone to walk up with the other device.
const sendQRTTL = 10 * time.Minute

// errNoReceiver refuses data connections that come before any receiver is paired.
var errNoReceiver = errors.New("no receiver paired yet")

// oneShotPeer is the receiver of a one-shot send, once paired.
type oneShotPeer struct {
	mu sync.Mutex
	fp identity.Fingerprint
}

func (p *oneShotPeer) set(fp identity.Fingerprint) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fp = fp
}

func (p *oneShotPeer) get() (identity.Fingerprint, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fp, !p.fp.IsZero()
}

// secure is the SecureFunc of the sender's data listener: everything is refused until a
// receiver is paired, then only its key gets through.
func (p *oneShotPeer) secure(id *identity.Identity) engine.SecureFunc {
	return func(ctx context.Context, c net.Conn) (net.Conn, error) {
		fp, ok := p.get()
		if !ok {
			c.Close()
			return nil, errNoReceiver
		}
		return securechan.ServerFunc(securechan.Config{Identity: id, Expected: fp})(ctx, c)
	}
}

// serveOneShotPairing waits until a receiver redeems the QR code, or one without it is
// accepted by confirm. shown gets each URI: the first one, then a new one each time the
// previous expires. The pairing port changes with it.
func serveOneShotPairing(ctx context.Context, id *identity.Identity, host string, ttl time.Duration,
	offer session.Message, confirm func(context.Context, pairing.Result) bool, shown func(pairing.URI)) (pairing.Result, error) {
	name, _ := os.Hostname()
	o := pairing.Options{Identity: id, Once: true, DeviceName: name, Offer: offer, Confirm: confirm}
	for {
		ln, err := net.Listen("tcp4", "0.0.0.0:0")
		if err != nil {
			return pairing.Result{}, err
		}
		inv, err := pairing.NewInvitation(ttl)
		if err != nil {
			ln.Close()
			return pairing.Result{}, err
		}
		shown(inv.URI(id.Fingerprint, host, ln.Addr().(*net.TCPAddr).Port))
		r, err := pairing.Serve(ctx, ln, inv, o)
		ln.Close()
		if errors.Is(err, pairing.ErrExpired) {
			continue
		}
		return r, err
	}
}

// startOneShotPairing runs serveOneShotPairing for `xfer send` alongside the data listener:
// it prints the receive command and the QR code, keeps the discovery offer's pairing port up
// to date, and lets the paired receiver in on the data connections.
func startOneShotPairing(ctx context.Context, peer *oneShotPeer, disc *discovery.DiscoveryServer,
	offer discovery.TransferOffer, data session.Message, trust *identity.TrustStore) {
	first := true
	ready := make(chan struct{})
	var readyOnce sync.Once
	shown := func(u pairing.URI) {
		o := offer
		o.PairPort = u.Port
		disc.SetOffer(&o)
		if !first {
			fmt.Println("\n[*] The QR code has expired: here is a new one.")
		}
		first = false
		printReceiveCommand(os.Stdout, u)
		readyOnce.Do(func() { close(ready) })
	}
	confirm := receiverConfirm(os.Stdout, askLine, trust)
	go func() {
		r, err := serveOneShotPairing(ctx, localID, getLocalIP(), sendQRTTL, data, confirm, shown)
		readyOnce.Do(func() { close(ready) })
		if err != nil {
			if ctx.Err() == nil {
				fmt.Printf("[-] Pairing stopped: %v\n", err)
			}
			return
		}
		peer.set(r.Fingerprint)
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
func offerMessage(o discovery.TransferOffer, dataPort int) session.Message {
	return session.Message{FileName: o.FileName, FileSize: o.FileSize, FileID: o.FileID, Batch: o.Batch, DataPort: dataPort}
}
