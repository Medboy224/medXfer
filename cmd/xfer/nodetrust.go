package main

// Trust on the command line (bible chapter 6, lot « Modèle de confiance »): a device seen for
// the first time pairs by a PIN or a QR code, never on fingerprints compared by eye (PAIR-12);
// a session writes nothing (PAIR-08); mutual trust is offered and accepted on both sides
// (PAIR-10). A trusted device's address that shows up with another key is refused and
// reported (ID-05).

import (
	"fmt"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/session"
)

// trustPromptTimeout bounds how long a question waits for the user.
const trustPromptTimeout = 60 * time.Second

// trustedAt is " - trusted 'alias'" when ip is the last address of a trusted device: it is
// recognized in a discovery list before any pairing, on this device only (the alias never goes
// out, DISC-06). It is a hint: the key and the mutual trust are still checked when connecting
// (ID-05, PAIR-09).
func trustedAt(ts *identity.TrustStore, ip string) string {
	if ts == nil {
		return ""
	}
	if p, ok := ts.ByAddress(ip); ok && p.Alias != "" {
		return fmt.Sprintf(" - trusted '%s'", p.Alias)
	}
	return ""
}

// keyChanged implements ID-05: if ip is the last address of a trusted device and the key
// presented now is another one, it returns that device.
func keyChanged(ts *identity.TrustStore, ip string, fp identity.Fingerprint) (identity.Peer, bool) {
	if ts == nil || ip == "" {
		return identity.Peer{}, false
	}
	p, ok := ts.ByAddress(ip)
	if !ok || p.Fingerprint == fp {
		return identity.Peer{}, false
	}
	return p, true
}

// keyChangedAlert is the message shown when keyChanged reports a device.
func keyChangedAlert(known identity.Peer, ip string, fp identity.Fingerprint) string {
	name := known.Alias
	if name == "" {
		name = "a trusted device"
	}
	return fmt.Sprintf("[!] REFUSED: %s is the address of '%s' (%s), but the device there presents\n"+
		"    another key (%s). This is what an interception looks like.\n"+
		"    If '%s' was really reinstalled, forget it first: xfer trust forget %s",
		ip, name, known.Fingerprint.Short(), fp.Short(), name, known.Fingerprint.Short())
}

// touchPeer records where a trusted device was seen, and its name. It never adds a device:
// only a trust negotiation both users accepted does (PAIR-08, PAIR-10).
func touchPeer(ts *identity.TrustStore, fp identity.Fingerprint, name, ip string) error {
	if ts == nil {
		return nil
	}
	p, ok := ts.Get(fp)
	if !ok || (p.LastAddress == ip && (name == "" || p.Alias == name)) {
		return nil
	}
	p.LastAddress = ip
	if name != "" {
		p.Alias = name
	}
	return ts.Add(p)
}

// nodeInvitation is the QR code shown by `pair --show` in xfer node (WP-3.6b): its secret
// admits one device, for the session; with --trust, mutual trust is offered once paired.
type nodeInvitation struct {
	mu    sync.Mutex
	inv   *pairing.Invitation
	trust bool
}

// renew replaces the QR code with a new one, valid pairing.TTL.
func (n *nodeInvitation) renew(fp identity.Fingerprint, host string, port int, trust bool) (pairing.URI, error) {
	inv, err := pairing.NewInvitation(pairing.TTL)
	if err != nil {
		return pairing.URI{}, err
	}
	n.mu.Lock()
	n.inv, n.trust = inv, trust
	n.mu.Unlock()
	return inv.URI(fp, host, port), nil
}

// redeem checks a scanner's secret: single use, before expiry, in constant time. offer: the
// QR code was shown with --trust.
func (n *nodeInvitation) redeem(secret string) (offer, ok bool) {
	n.mu.Lock()
	inv, trust := n.inv, n.trust
	n.mu.Unlock()
	if inv == nil || inv.Redeem(pairing.DecodeSecret(secret), time.Now()) != nil {
		return false, false
	}
	return trust, true
}

// localID is this device's identity once a command has loaded it (node, send, recv).
var localID *identity.Identity

// sessionPeer is the verified key of the paired device, or the zero value (then refused).
func sessionPeer(s *session.Channel) identity.Fingerprint {
	if s == nil {
		return identity.Fingerprint{}
	}
	return s.PeerFingerprint()
}
