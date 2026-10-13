package pake

// CPace, suite CPACE-RISTR255-SHA512 (draft-irtf-cfrg-cpace-21), in the initiator-responder
// setting: the initiator is A, the responder B. Only the protocol is written here (PAIR-06 as
// reformulated, D2 of the J5 plan); the group comes from group.go.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"errors"
	"io"
)

const (
	cpaceDSI       = "CPaceRistretto255"
	sha512Block    = 128 // H.s_in_bytes of SHA-512
	sidOutputLabel = "CPaceSidOutput"
	macLabel       = "CPaceMac"
)

// prependLen is the LEB128 encoding of len(data) followed by data (draft A.1.1).
func prependLen(data []byte) []byte {
	var out []byte
	n := len(data)
	for {
		if n < 128 {
			out = append(out, byte(n))
		} else {
			out = append(out, byte(n&0x7f)|0x80)
		}
		n >>= 7
		if n == 0 {
			break
		}
	}
	return append(out, data...)
}

// lvCat is the length-value concatenation of parts (draft A.1.3).
func lvCat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, prependLen(p)...)
	}
	return out
}

// lexLarger reports whether a > b in lexicographical order (draft A.3.1).
func lexLarger(a, b []byte) bool {
	for i := 0; i < min(len(a), len(b)); i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return len(a) > len(b)
}

// oCat is the ordered concatenation (draft A.3.2).
func oCat(a, b []byte) []byte {
	if lexLarger(a, b) {
		return append(append([]byte("oc"), a...), b...)
	}
	return append(append([]byte("oc"), b...), a...)
}

// transcriptIR is the transcript of the initiator-responder setting (draft A.3.4).
func transcriptIR(ya, ada, yb, adb []byte) []byte {
	return append(lvCat(ya, ada), lvCat(yb, adb)...)
}

// transcriptOC is the transcript without roles (draft A.3.6); medXfer uses roles.
func transcriptOC(ya, ada, yb, adb []byte) []byte {
	return oCat(lvCat(ya, ada), lvCat(yb, adb))
}

// generatorString is lv_cat(DSI, PRS, zero padding, CI, sid) (draft A.2).
func generatorString(dsi, prs, ci, sid []byte, sInBytes int) []byte {
	zpad := max(0, sInBytes-1-len(prependLen(prs))-len(prependLen(dsi)))
	return lvCat(dsi, prs, make([]byte, zpad), ci, sid)
}

// calculateGenerator derives the generator from the password, CI and sid (draft §8.3).
func calculateGenerator(prs, ci, sid []byte) (*element, error) {
	h := sha512.Sum512(generatorString([]byte(cpaceDSI), prs, ci, sid, sha512Block))
	return deriveElement(h[:])
}

// iskOf is H.hash(lv_cat(DSI || "_ISK", sid, K) || transcript) (draft §7.2).
func iskOf(sid, k, transcript []byte) []byte {
	h := sha512.New()
	h.Write(lvCat([]byte(cpaceDSI+"_ISK"), sid, k))
	h.Write(transcript)
	return h.Sum(nil)
}

// sidOutput is the public session identifier of draft §10.6.
func sidOutput(transcript []byte) []byte {
	h := sha512.Sum512(append([]byte(sidOutputLabel), transcript...))
	return h[:]
}

// ErrAbort: the peer's share is not a valid element, or the shared point is the neutral element
// (draft §7.2: MUST abort). It says nothing of the password (PAIR-07).
var ErrAbort = errors.New("pake: the exchange failed")

// cpace is one side of a CPace run.
type cpace struct {
	role     Role
	sid      []byte
	y        *scalar
	share    []byte // Ya or Yb
	ad       []byte // this side's associated data (ADa or ADb): empty in medXfer
	peerAD   []byte // the peer's: empty in medXfer
	finished bool
}

func newCPace(p Params, rnd io.Reader) (Exchange, error) {
	if rnd == nil {
		rnd = rand.Reader
	}
	y, err := sampleScalar(rnd)
	if err != nil {
		return nil, err
	}
	return newCPaceWith(p, nil, nil, y)
}

// newCPaceWith starts a run with the scalar y and the associated data of both sides (the test
// vectors use them).
func newCPaceWith(p Params, ad, peerAD []byte, y *scalar) (*cpace, error) {
	g, err := calculateGenerator(p.Password, p.CI, p.SID)
	if err != nil {
		return nil, err
	}
	share := scalarMult(y, g)
	if subtle.ConstantTimeCompare(share, identityBytes) == 1 {
		return nil, ErrAbort
	}
	return &cpace{role: p.Role, sid: append([]byte(nil), p.SID...), y: y, share: share, ad: ad, peerAD: peerAD}, nil
}

func (c *cpace) Share() []byte { return append([]byte(nil), c.share...) }

func (c *cpace) Finish(peer []byte) (*Session, error) {
	if c.finished {
		return nil, errors.New("pake: the exchange is already finished")
	}
	c.finished = true
	if len(peer) != elementLen {
		return nil, ErrAbort
	}
	k := scalarMultVfy(c.y, peer)
	if subtle.ConstantTimeCompare(k, identityBytes) == 1 {
		return nil, ErrAbort
	}
	ownMsg, peerMsg := lvCat(c.share, c.ad), lvCat(peer, c.peerAD)
	t := append(append([]byte(nil), ownMsg...), peerMsg...) // transcript_ir: A's message first
	if c.role == Responder {
		t = append(append([]byte(nil), peerMsg...), ownMsg...)
	}
	isk := iskOf(c.sid, k, t)
	mac := sha512.New()
	mac.Write([]byte(macLabel))
	mac.Write(c.sid)
	mac.Write(isk)
	return &Session{isk: isk, macKey: mac.Sum(nil), own: ownMsg, peer: peerMsg}, nil
}

// tag is HMAC-SHA-512(mac_key, msg) (draft §10.4).
func tag(key, msg []byte) []byte {
	m := hmac.New(sha512.New, key)
	m.Write(msg)
	return m.Sum(nil)
}
