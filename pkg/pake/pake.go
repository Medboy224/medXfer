// Package pake is the password-authenticated key exchange of pairing by PIN (bible §6.2, path
// P2): two devices that know the same short PIN prove it to each other without revealing it,
// and an attacker gets one guess per connection, none offline.
//
// The rest of medXfer only sees this file (D9 of the J5 plan): a suite is chosen by its name,
// which the pairing messages carry, so that another PAKE can be added beside CPace without
// touching the callers. Today's only suite is CPACE-RISTR255-SHA512 (gate G1).
package pake

import (
	"crypto/hmac"
	"errors"
	"io"
)

// Role of a side of the exchange: the initiator types the PIN, the responder shows it.
type Role int

const (
	Initiator Role = iota
	Responder
)

// Params of one side. Both sides must give the same Password, CI and SID, or the run fails.
type Params struct {
	Password []byte // the PIN, as typed
	CI       []byte // channel identifier: who pairs with whom, initiator first (PAIR-03)
	SID      []byte // session identifier: the TLS channel's exported key (ekm, PAIR-03)
	Role     Role
}

// Exchange is one side of a run: Share is the message to send to the peer; Finish takes the
// peer's share and returns the session. A side is used for one run only.
type Exchange interface {
	Share() []byte
	Finish(peerShare []byte) (*Session, error)
}

// Session is a finished run. Each side sends Tag and checks the peer's with Verify (key
// confirmation in both directions, PAIR-05): only then do both know the other had the PIN.
type Session struct {
	isk       []byte // the intermediate session key (never exposed: confirmation only)
	macKey    []byte
	own, peer []byte // each side's message, lv_cat(Y, AD)
}

// Tag is this side's confirmation of the run.
func (s *Session) Tag() []byte { return tag(s.macKey, s.own) }

// Verify reports whether peerTag is the peer's confirmation of the same run, in constant time.
func (s *Session) Verify(peerTag []byte) bool { return hmac.Equal(peerTag, tag(s.macKey, s.peer)) }

// SuiteCPaceRistretto255 is CPace over ristretto255 with SHA-512 (draft-irtf-cfrg-cpace-21).
const SuiteCPaceRistretto255 = "CPACE-RISTR255-SHA512"

// suites are the PAKEs this build knows, by name. rand is nil for crypto/rand.
var suites = map[string]func(p Params, rand io.Reader) (Exchange, error){
	SuiteCPaceRistretto255: newCPace,
}

// ErrUnknownSuite: the peer asks for a PAKE this build does not know.
var ErrUnknownSuite = errors.New("pake: unknown suite")

// New starts one side of a run of suite.
func New(suite string, p Params) (Exchange, error) {
	f, ok := suites[suite]
	if !ok {
		return nil, ErrUnknownSuite
	}
	return f(p, nil)
}

// Suites lists the suites this build knows, the preferred first.
func Suites() []string { return []string{SuiteCPaceRistretto255} }
