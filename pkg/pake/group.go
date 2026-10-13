package pake

// The group of the suite CPACE-RISTR255-SHA512: ristretto255 (RFC 9496), over
// github.com/gtank/ristretto255. This file is the only one that knows the library (D9 of the
// J5 plan): another library, or one in Go itself some day, replaces it here alone.

import (
	"errors"
	"io"

	"github.com/gtank/ristretto255"
)

type (
	element = ristretto255.Element
	scalar  = ristretto255.Scalar
)

// elementLen is the size of an encoded element (draft §8.3: field_size_bytes).
const elementLen = 32

var errInvalidElement = errors.New("pake: invalid group element")

// identityBytes is G.I: the encoding of the neutral element.
var identityBytes = ristretto255.NewIdentityElement().Bytes()

// deriveElement is the element derivation of RFC 9496 from 64 uniform bytes.
func deriveElement(uniform []byte) (*element, error) {
	return ristretto255.NewIdentityElement().SetUniformBytes(uniform)
}

// scalarFromBytes reads a scalar from its 32-byte little-endian encoding.
func scalarFromBytes(b []byte) (*scalar, error) {
	return ristretto255.NewScalar().SetCanonicalBytes(b)
}

// sampleScalar is the draft's recommended sampling (§8.3): 32 random bytes with the bits above
// the 252nd cleared, read in little-endian; a zero scalar is drawn again.
func sampleScalar(rand io.Reader) (*scalar, error) {
	var b [32]byte
	for {
		if _, err := io.ReadFull(rand, b[:]); err != nil {
			return nil, err
		}
		b[31] &= 0x0f
		s, err := scalarFromBytes(b[:])
		if err != nil {
			return nil, err
		}
		if s.Equal(ristretto255.NewScalar()) == 0 {
			return s, nil
		}
	}
}

// scalarMult is G.scalar_mult: the encoding of y·g.
func scalarMult(y *scalar, g *element) []byte {
	return ristretto255.NewIdentityElement().ScalarMult(y, g).Bytes()
}

// scalarMultVfy is G.scalar_mult_vfy: the encoding of y·X, or G.I if X does not decode.
func scalarMultVfy(y *scalar, x []byte) []byte {
	p, err := ristretto255.NewIdentityElement().SetCanonicalBytes(x)
	if err != nil {
		return identityBytes
	}
	return ristretto255.NewIdentityElement().ScalarMult(y, p).Bytes()
}

// decodeElement decodes a canonical encoding (the test vector X of draft B.3.10).
func decodeElement(b []byte) (*element, error) {
	return ristretto255.NewIdentityElement().SetCanonicalBytes(b)
}
