package identity

import (
	"crypto/ecdsa"
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
)

// DerivedKeySize is the size of a key DerivedKey gives.
const DerivedKeySize = 32

// DerivedKey is a key for label drawn from this device's private key by HKDF-SHA-256: secret like
// the private key (chapter 13), the same at every launch, new with a new identity (ID-06). Each
// label gives another key, and none tells anything of the private key.
func (id *Identity) DerivedKey(label string) ([]byte, error) {
	key, ok := id.Cert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("identity: not an ECDSA key")
	}
	ek, err := key.ECDH()
	if err != nil {
		return nil, err
	}
	return hkdf.Key(sha256.New, ek.Bytes(), nil, label, DerivedKeySize)
}
