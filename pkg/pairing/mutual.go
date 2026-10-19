package pairing

import (
	"bytes"
	"crypto/rand"
	"errors"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/session"
)

// Mutual trust (bible §6.5; D4 and D5 of the trust plan). An entry in trusted.json is one
// side's decision only: a device can always keep the other's public key. So the privileges of
// a trusted device (path P3: no PIN, no QR code) hold only while both lists have the other,
// which each side declares in its first message of a session (TrustsYou, PAIR-09). An entry on
// one side alone does nothing, and forgetting a device on one side ends the privileges of both.

// Mutual reports whether peer is a device of mutual trust: it is in this side's list, and it
// declared that this side is in its own.
func Mutual(store *identity.TrustStore, peer identity.Fingerprint, declared bool) bool {
	return declared && store != nil && store.Contains(peer)
}

// Declares is what this side tells peer in TrustsYou: whether peer is in its list.
func Declares(store *identity.TrustStore, peer identity.Fingerprint) bool {
	return store != nil && store.Contains(peer)
}

// The trust negotiation (PAIR-10), inside a session opened by a PIN or a QR code, or between
// devices of mutual trust: one side offers, the other's user answers, the offerer commits.
// Each side writes the other's key only once both said yes: the offerer as it sends
// trust_commit, the other as it receives it. A lost trust_commit leaves an entry on one side,
// which Mutual makes inert. trust_commit carries the pair's recognition key (DISC-11), drawn by
// the side that commits; when both commit (both offered), both keep the key of the device with
// the smaller fingerprint.
const (
	TypeTrustOffer   = "trust_offer"
	TypeTrustAccept  = "trust_accept"
	TypeTrustDecline = "trust_decline"
	TypeTrustCommit  = "trust_commit"
)

// TrustAnswerTime is how long an offer waits for the other user's answer: past it, the answer
// is no (D5).
const TrustAnswerTime = 60 * time.Second

// IsTrustMessage reports whether a session message belongs to the trust negotiation.
func IsTrustMessage(typ string) bool {
	switch typ {
	case TypeTrustOffer, TypeTrustAccept, TypeTrustDecline, TypeTrustCommit:
		return true
	}
	return false
}

// TrustEvent is what a negotiation message meant for this side.
type TrustEvent int

const (
	TrustNothing     TrustEvent = iota // nothing to tell the user (late, unasked or repeated)
	TrustAsked                         // the peer offers: ask the user, then call Answer
	TrustEstablished                   // the peer's key is now written on this side
	TrustDeclined                      // the peer's user said no
)

// ErrNoOffer: Answer was called with no offer of the peer waiting (none, or expired).
var ErrNoOffer = errors.New("pairing: no trust offer to answer")

// TrustTalk is one session's trust negotiation with peer. It is safe for concurrent use.
type TrustTalk struct {
	store *identity.TrustStore
	self  identity.Fingerprint
	peer  identity.Peer // the key, and the alias and address to write
	now   func() time.Time

	mu          sync.Mutex
	offeredAt   time.Time // our offer, waiting for the peer's answer
	askedAt     time.Time // the peer's offer, waiting for our user's answer
	acceptedAt  time.Time // our yes, waiting for the peer's trust_commit
	established bool
	tagKey      []byte               // the recognition key kept
	tagOwner    identity.Fingerprint // the device that drew it
}

// NewTrustTalk returns the negotiation of a session between self and peer, whose key it writes
// in store.
func NewTrustTalk(store *identity.TrustStore, self identity.Fingerprint, peer identity.Peer) *TrustTalk {
	return &TrustTalk{store: store, self: self, peer: peer, now: time.Now}
}

// keepTagKey keeps key, drawn by owner, unless the key already kept was drawn by a device with
// a smaller fingerprint: both sides end with the same key.
func (t *TrustTalk) keepTagKey(key []byte, owner identity.Fingerprint) {
	if len(key) != tagKeySize {
		return
	}
	if t.tagKey == nil || bytes.Compare(owner[:], t.tagOwner[:]) < 0 {
		t.tagKey, t.tagOwner = append([]byte(nil), key...), owner
	}
}

func (t *TrustTalk) live(at time.Time) bool {
	return !at.IsZero() && t.now().Sub(at) < TrustAnswerTime
}

// Offer returns the message that offers mutual trust to the peer.
func (t *TrustTalk) Offer() session.Message {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.offeredAt = t.now()
	return session.Message{Type: TypeTrustOffer}
}

// Answer returns our user's answer to the peer's offer. ErrNoOffer when none waits.
func (t *TrustTalk) Answer(yes bool) (session.Message, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.live(t.askedAt) {
		t.askedAt = time.Time{}
		return session.Message{}, ErrNoOffer
	}
	t.askedAt = time.Time{}
	if !yes {
		return session.Message{Type: TypeTrustDecline}, nil
	}
	t.acceptedAt = t.now()
	return session.Message{Type: TypeTrustAccept}, nil
}

// Handle takes a negotiation message from the peer. It returns the reply to send, if any, what
// it meant, and an error if the key could not be written.
func (t *TrustTalk) Handle(m session.Message) (*session.Message, TrustEvent, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch m.Type {
	case TypeTrustOffer:
		if t.live(t.offeredAt) { // both offered: each one's offer is the other's yes
			t.acceptedAt = t.now()
			return &session.Message{Type: TypeTrustAccept}, TrustNothing, nil
		}
		t.askedAt = t.now()
		return nil, TrustAsked, nil
	case TypeTrustAccept:
		if !t.live(t.offeredAt) {
			return nil, TrustNothing, nil // unasked, or after our user was told there was no answer
		}
		t.offeredAt = time.Time{}
		key := make([]byte, tagKeySize)
		if _, err := rand.Read(key); err != nil {
			return nil, TrustNothing, err
		}
		t.keepTagKey(key, t.self)
		if err := t.writeLocked(); err != nil {
			return nil, TrustNothing, err
		}
		return &session.Message{Type: TypeTrustCommit, TagKey: key}, t.establishedLocked(), nil
	case TypeTrustDecline:
		if !t.live(t.offeredAt) {
			return nil, TrustNothing, nil
		}
		t.offeredAt = time.Time{}
		return nil, TrustDeclined, nil
	case TypeTrustCommit:
		if !t.live(t.acceptedAt) { // never written without our own yes
			return nil, TrustNothing, nil
		}
		t.acceptedAt = time.Time{}
		t.keepTagKey(m.TagKey, t.peer.Fingerprint)
		if err := t.writeLocked(); err != nil {
			return nil, TrustNothing, err
		}
		return nil, t.establishedLocked(), nil
	}
	return nil, TrustNothing, nil
}

// AwaitsCommit reports whether this side said yes and still waits for the peer's trust_commit:
// after a double offer, this side's own commit already established the trust, but the key
// both keep may be the peer's (keepTagKey). Reading on until it comes keeps one key (#170).
func (t *TrustTalk) AwaitsCommit() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.live(t.acceptedAt)
}

// establishedLocked reports the trust once, though both commits of a double offer write it.
func (t *TrustTalk) establishedLocked() TrustEvent {
	if t.established {
		return TrustNothing
	}
	t.established = true
	return TrustEstablished
}

// writeLocked writes the peer's key, keeping what the user set on an entry already there.
func (t *TrustTalk) writeLocked() error {
	if t.store == nil {
		return errors.New("pairing: no trusted list")
	}
	p := t.peer
	p.TagKey = t.tagKey
	if old, ok := t.store.Get(p.Fingerprint); ok {
		p.AutoAccept, p.AddedAt, p.Renamed = old.AutoAccept, old.AddedAt, old.Renamed
		if p.Alias == "" || old.Renamed {
			p.Alias = old.Alias
		}
	}
	return t.store.Add(p)
}
