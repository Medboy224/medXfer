// Package identity holds the device's long-term identity and the list of devices it trusts
// (bible chapter 4, ID-01 to ID-06). A device is recognised by the key it keeps forever, never
// by its IP address or its name.
package identity

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base32"
	"fmt"
	"strings"
)

// Fingerprint identifies a device: SHA-256 of the DER SubjectPublicKeyInfo of its certificate
// (ID-02). It depends on the key only, so renewing the certificate keeps it.
type Fingerprint [sha256.Size]byte

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// FingerprintOf returns the fingerprint of cert.
func FingerprintOf(cert *x509.Certificate) Fingerprint {
	return sha256.Sum256(cert.RawSubjectPublicKeyInfo)
}

// String is the full fingerprint in base32 (52 characters), as stored in trusted.json.
func (f Fingerprint) String() string { return b32.EncodeToString(f[:]) }

// Short is the form shown for comparison by eye (ID-03): the first 16 base32 characters
// (80 bits) in four groups, for example "ABCD-EFGH-IJKL-MNOP".
func (f Fingerprint) Short() string {
	s := f.String()[:16]
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16]
}

// IsZero reports whether f is the zero value (no fingerprint).
func (f Fingerprint) IsZero() bool { return f == Fingerprint{} }

// ParseFingerprint reads a full fingerprint, case-insensitive, with or without separators.
func ParseFingerprint(s string) (Fingerprint, error) {
	var f Fingerprint
	n := normalize(s)
	b, err := b32.DecodeString(n)
	if err != nil || len(b) != len(f) {
		return f, fmt.Errorf("invalid fingerprint %q: expected %d base32 characters", s, b32.EncodedLen(len(f)))
	}
	copy(f[:], b)
	return f, nil
}

// normalize removes separators and spaces and upper-cases a typed fingerprint or prefix.
func normalize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '-' || r == ' ' || r == ':' || r == '\t':
			return -1
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		}
		return r
	}, s)
}
