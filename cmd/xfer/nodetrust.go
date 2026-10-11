package main

// Provisional trust for the command line (WP-3.3, ID-05): the first time a device with an
// unknown key connects, the user sees both short fingerprints and decides; never a silent
// trust. A trusted device's address that shows up with another key is refused and reported.

import (
	"fmt"
	"strings"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/session"
)

// trustAnswer is the user's decision about an unknown key.
type trustAnswer int

const (
	trustRefused trustAnswer = iota // default: anything but an explicit yes
	trustOnce                       // this session only
	trustAlways                     // saved in trusted.json
)

// trustPromptTimeout bounds how long an incoming pairing waits for the user.
const trustPromptTimeout = 60 * time.Second

// parseTrustAnswer reads the user's reply. Only an explicit yes trusts: the default is no.
func parseTrustAnswer(s string) trustAnswer {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes", "o", "oui":
		return trustOnce
	case "a", "always", "t", "toujours":
		return trustAlways
	}
	return trustRefused
}

// trustQuestion shows both short fingerprints, to compare with the other screen.
func trustQuestion(peerName string, peer, self identity.Fingerprint) string {
	if peerName == "" {
		peerName = "unknown device"
	}
	return fmt.Sprintf("[?] New device '%s'.\n"+
		"    Its fingerprint:  %s\n"+
		"    Ours:             %s\n"+
		"    Check that the other screen shows the same two fingerprints.\n"+
		"    Trust it? [y]es for this session / [a]lways / [N]o: ",
		peerName, peer.Short(), self.Short())
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

// rememberPeer records the decision: a new trusted device (trustAlways), or the last address
// of an already trusted one.
func rememberPeer(ts *identity.TrustStore, fp identity.Fingerprint, name, ip string, answer trustAnswer) error {
	if ts == nil {
		return nil
	}
	if p, ok := ts.Get(fp); ok {
		if p.LastAddress == ip && (name == "" || p.Alias == name) {
			return nil
		}
		p.LastAddress = ip
		if name != "" {
			p.Alias = name
		}
		return ts.Add(p)
	}
	if answer != trustAlways {
		return nil
	}
	return ts.Add(identity.Peer{Fingerprint: fp, Alias: name, LastAddress: ip})
}

// trustRequest is an incoming pairing waiting for the user's decision in the command loop.
type trustRequest struct {
	name    string
	ip      string
	fp      identity.Fingerprint
	expires time.Time
	reply   chan trustAnswer // buffered: the command loop never blocks on it
}

// localID is this device's identity once a command has loaded it (node, send, recv).
var localID *identity.Identity

// recvSecure secures the data connections of `xfer recv`, pinned to the sender's key.
var recvSecure engine.SecureFunc

// sessionPeer is the verified key of the paired device, or the zero value (then refused).
func sessionPeer(s *session.Channel) identity.Fingerprint {
	if s == nil {
		return identity.Fingerprint{}
	}
	return s.PeerFingerprint()
}
