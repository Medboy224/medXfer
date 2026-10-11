// Package pairing is path P1 of the bible (§6.1): pairing by QR code. The QR code carries
// the shower's key fingerprint, its address and a 128-bit secret for single use. The scanner
// connects pinning that key (the camera is the authenticated channel), then proves it saw the
// QR code by sending the secret inside the TLS channel. No PAKE is needed: the secret is
// strong and the key comes from the QR code, so a man in the middle has nothing to work with.
package pairing

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
)

// TTL is how long a QR code stays valid by default (§6.1).
const TTL = 120 * time.Second

// SecretSize is 16 bytes: 128 bits of entropy.
const SecretSize = 16

// Secret is the single-use secret of a QR code.
type Secret [SecretSize]byte

var (
	ErrNotPairingURI = errors.New("pairing: not a medxfer://v1/pair URI")
	ErrExpired       = errors.New("pairing: the QR code has expired")
	ErrRefused       = errors.New("pairing: pairing failed") // deliberately says nothing more (PAIR-07)
)

// URI is the content of the QR code:
// medxfer://v1/pair?fp=<fingerprint>&a=<ip>&p=<port>&s=<secret base64url>&exp=<unix time>
type URI struct {
	Fingerprint identity.Fingerprint
	Host        string
	Port        int
	Secret      Secret
	Expires     time.Time
}

// String encodes u in the order of the bible (§6.1).
func (u URI) String() string {
	return fmt.Sprintf("medxfer://v1/pair?fp=%s&a=%s&p=%d&s=%s&exp=%d",
		u.Fingerprint, url.QueryEscape(u.Host), u.Port,
		base64.RawURLEncoding.EncodeToString(u.Secret[:]), u.Expires.Unix())
}

// Addr is the host:port to connect to.
func (u URI) Addr() string { return net.JoinHostPort(u.Host, strconv.Itoa(u.Port)) }

// Parse reads a pairing URI and checks it has not expired at now.
func Parse(s string, now time.Time) (URI, error) {
	var u URI
	p, err := url.Parse(s)
	if err != nil || p.Scheme != "medxfer" || p.Host != "v1" || p.Path != "/pair" {
		return u, ErrNotPairingURI
	}
	q := p.Query()
	if u.Fingerprint, err = identity.ParseFingerprint(q.Get("fp")); err != nil {
		return u, fmt.Errorf("%w: %v", ErrNotPairingURI, err)
	}
	if ip := net.ParseIP(q.Get("a")); ip == nil {
		return u, fmt.Errorf("%w: address %q is not an IP address", ErrNotPairingURI, q.Get("a"))
	}
	u.Host = q.Get("a")
	if u.Port, err = strconv.Atoi(q.Get("p")); err != nil || u.Port < 1 || u.Port > 65535 {
		return u, fmt.Errorf("%w: invalid port %q", ErrNotPairingURI, q.Get("p"))
	}
	secret, err := base64.RawURLEncoding.DecodeString(q.Get("s"))
	if err != nil || len(secret) != SecretSize {
		return u, fmt.Errorf("%w: the secret must be %d bytes", ErrNotPairingURI, SecretSize)
	}
	copy(u.Secret[:], secret)
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	if err != nil {
		return u, fmt.Errorf("%w: invalid expiry %q", ErrNotPairingURI, q.Get("exp"))
	}
	u.Expires = time.Unix(exp, 0)
	if !now.Before(u.Expires) {
		return u, ErrExpired
	}
	return u, nil
}

// EncodeSecret and DecodeSecret carry the secret in a JSON message.
func EncodeSecret(s Secret) string { return base64.RawURLEncoding.EncodeToString(s[:]) }

func DecodeSecret(s string) []byte {
	b, _ := base64.RawURLEncoding.DecodeString(s)
	return b
}

// Invitation is the shower's side of a QR code: its secret, valid until Expires and for a
// single successful pairing.
type Invitation struct {
	mu      sync.Mutex
	secret  Secret
	expires time.Time
	used    bool
}

// NewInvitation draws a fresh secret with crypto/rand, valid for ttl.
func NewInvitation(ttl time.Duration) (*Invitation, error) {
	inv := &Invitation{expires: time.Now().Add(ttl)}
	if _, err := rand.Read(inv.secret[:]); err != nil {
		return nil, err
	}
	return inv, nil
}

// URI is the QR code content for this invitation.
func (inv *Invitation) URI(fp identity.Fingerprint, host string, port int) URI {
	return URI{Fingerprint: fp, Host: host, Port: port, Secret: inv.secret, Expires: inv.expires}
}

// Expires is when the invitation stops being valid.
func (inv *Invitation) Expires() time.Time { return inv.expires }

// Redeem consumes the invitation if secret is its secret, compared in constant time, before
// expiry, and only once. A failure returns ErrRefused or ErrExpired; callers must not tell the
// peer which one (PAIR-07).
func (inv *Invitation) Redeem(secret []byte, now time.Time) error {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	if !now.Before(inv.expires) {
		return ErrExpired
	}
	if inv.used || len(secret) != SecretSize || subtle.ConstantTimeCompare(secret, inv.secret[:]) != 1 {
		return ErrRefused
	}
	inv.used = true
	return nil
}
