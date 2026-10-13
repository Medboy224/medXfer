package pake

import (
	"bytes"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func unhex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func eqHex(t *testing.T, what string, got []byte, want string) {
	t.Helper()
	if h := hex.EncodeToString(got); h != want {
		t.Fatalf("%s:\n got  %s\n want %s", what, h, want)
	}
}

// Appendix A of draft-irtf-cfrg-cpace-21: the string functions.
func TestDraftAppendixA(t *testing.T) {
	r127, r128 := make([]byte, 127), make([]byte, 128)
	for i := range r128 {
		r128[i] = byte(i)
		if i < 127 {
			r127[i] = byte(i)
		}
	}
	eqHex(t, "prepend_len(b'')", prependLen(nil), "00")
	eqHex(t, "prepend_len(b'1234')", prependLen([]byte("1234")), "0431323334")
	eqHex(t, "prepend_len(range(127))", prependLen(r127), "7f"+hex.EncodeToString(r127))
	eqHex(t, "prepend_len(range(128))", prependLen(r128), "8001"+hex.EncodeToString(r128))
	eqHex(t, "lv_cat", lvCat([]byte("1234"), []byte("5"), nil, []byte("678")), "043132333401350003363738")
	for _, c := range []struct {
		a, b string
		want bool
	}{{"\x00", "\x00\x00", false}, {"\x01", "\x00\x00", true}, {"\x00\x00", "\x00", true},
		{"\x00\x00", "\x01", false}, {"\x00\x01", "\x01", false}, {"ABCD", "BCD", false}} {
		if got := lexLarger([]byte(c.a), []byte(c.b)); got != c.want {
			t.Fatalf("lexiographically_larger(%q, %q) = %v", c.a, c.b, got)
		}
	}
	eqHex(t, "o_cat(ABCD, BCD)", oCat([]byte("ABCD"), []byte("BCD")), "6f6342434441424344")
	eqHex(t, "o_cat(BCD, ABCDE)", oCat([]byte("BCD"), []byte("ABCDE")), "6f634243444142434445")
	b := func(s string) []byte { return []byte(s) }
	eqHex(t, "transcript_ir 1", transcriptIR(b("123"), b("PartyA"), b("234"), b("PartyB")), "03313233065061727479410332333406506172747942")
	eqHex(t, "transcript_ir 2", transcriptIR(b("3456"), b("PartyA"), b("2345"), b("PartyB")), "043334353606506172747941043233343506506172747942")
	eqHex(t, "transcript_oc 1", transcriptOC(b("123"), b("PartyA"), b("234"), b("PartyB")), "6f6303323334065061727479420331323306506172747941")
	eqHex(t, "transcript_oc 2", transcriptOC(b("3456"), b("PartyA"), b("2345"), b("PartyB")), "6f63043334353606506172747941043233343506506172747942")
}

// The inputs of Appendix B.3.
func b3Params(t testing.TB, role Role) Params {
	return Params{Password: []byte("Password"), CI: unhex(t, "0b415f696e69746961746f720b425f726573706f6e646572"),
		SID: unhex(t, "7e4b4791d6a8ef019b936c79fb7f2c57"), Role: role}
}

// Appendix B.3 of draft-irtf-cfrg-cpace-21 (CPACE-RISTR255-SHA512), byte for byte.
func TestDraftAppendixB3(t *testing.T) {
	pa := b3Params(t, Initiator)
	eqHex(t, "generator_string", generatorString([]byte(cpaceDSI), pa.Password, pa.CI, pa.SID, sha512Block), b3GenString)
	h := sha512.Sum512(unhex(t, b3GenString))
	eqHex(t, "generator hash", h[:], b3GenHash)
	g, err := calculateGenerator(pa.Password, pa.CI, pa.SID)
	if err != nil {
		t.Fatal(err)
	}
	eqHex(t, "generator", g.Bytes(), b3Generator)

	ya, err := scalarFromBytes(unhex(t, b3ScalarA))
	if err != nil {
		t.Fatal(err)
	}
	yb, err := scalarFromBytes(unhex(t, b3ScalarB))
	if err != nil {
		t.Fatal(err)
	}
	a, err := newCPaceWith(pa, []byte("ADa"), []byte("ADb"), ya)
	if err != nil {
		t.Fatal(err)
	}
	b, err := newCPaceWith(b3Params(t, Responder), []byte("ADb"), []byte("ADa"), yb)
	if err != nil {
		t.Fatal(err)
	}
	eqHex(t, "Ya", a.Share(), b3ShareA)
	eqHex(t, "Yb", b.Share(), b3ShareB)
	eqHex(t, "K at A", scalarMultVfy(ya, b.Share()), b3KA)
	eqHex(t, "K at B", scalarMultVfy(yb, a.Share()), b3KB)

	tir := transcriptIR(a.Share(), []byte("ADa"), b.Share(), []byte("ADb"))
	toc := transcriptOC(a.Share(), []byte("ADa"), b.Share(), []byte("ADb"))
	k := unhex(t, b3KA)
	eqHex(t, "transcript_ir", tir, b3TranscriptIR)
	eqHex(t, "ISK, initiator-responder", iskOf(pa.SID, k, tir), b3ISKIR)
	eqHex(t, "transcript_oc", toc, b3TranscriptOC)
	eqHex(t, "ISK, parallel", iskOf(pa.SID, k, toc), b3ISKOC)
	eqHex(t, "sid_output, initiator-responder", sidOutput(tir), b3SidOutputIR)
	eqHex(t, "sid_output, parallel", sidOutput(toc), b3SidOutputOC)

	sa, err := a.Finish(b.Share())
	if err != nil {
		t.Fatal(err)
	}
	sb, err := b.Finish(a.Share())
	if err != nil {
		t.Fatal(err)
	}
	eqHex(t, "ISK of the run at A", sa.isk, b3ISKIR)
	eqHex(t, "ISK of the run at B", sb.isk, b3ISKIR)
	if !sa.Verify(sb.Tag()) || !sb.Verify(sa.Tag()) {
		t.Fatal("the confirmations of a good run do not verify")
	}
}

// B.3.10 and B.3.11: scalar_mult with a valid input; invalid inputs give G.I and abort.
func TestDraftScalarMultVfy(t *testing.T) {
	s, err := scalarFromBytes(unhex(t, b3ValidScalar))
	if err != nil {
		t.Fatal(err)
	}
	x, err := decodeElement(unhex(t, b3ValidX))
	if err != nil {
		t.Fatal(err)
	}
	eqHex(t, "scalar_mult(s, decode(X))", scalarMult(s, x), b3ValidMult)
	eqHex(t, "scalar_mult_vfy(s, X)", scalarMultVfy(s, unhex(t, b3ValidX)), b3ValidMultVfy)
	for _, bad := range []string{b3InvalidY1, strings.Repeat("00", 32)} {
		if got := scalarMultVfy(s, unhex(t, bad)); !bytes.Equal(got, identityBytes) {
			t.Fatalf("scalar_mult_vfy(s, %s) = %x; want G.I", bad, got)
		}
		e, err := New(SuiteCPaceRistretto255, b3Params(t, Initiator))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Finish(unhex(t, bad)); !errors.Is(err, ErrAbort) {
			t.Fatalf("Finish(%s) = %v; want ErrAbort", bad, err)
		}
	}
}

// pair runs a whole exchange; it returns both sessions, or the first error.
func pair(t *testing.T, pi, pr Params) (*Session, *Session, error) {
	t.Helper()
	a, err := New(SuiteCPaceRistretto255, pi)
	if err != nil {
		return nil, nil, err
	}
	b, err := New(SuiteCPaceRistretto255, pr)
	if err != nil {
		return nil, nil, err
	}
	sb, err := b.Finish(a.Share())
	if err != nil {
		return nil, nil, err
	}
	sa, err := a.Finish(b.Share())
	return sa, sb, err
}

func params(pin, ci, sid string, role Role) Params {
	return Params{Password: []byte(pin), CI: []byte(ci), SID: []byte(sid), Role: role}
}

// PAIR-05: the same PIN on the same channel confirms in both directions; a wrong PIN, or
// another channel (a relay between two TLS sessions has two ekm), confirms in neither.
func TestConfirmation(t *testing.T) {
	sa, sb, err := pair(t, params("482913", "fp_i|fp_r", "ekm-1", Initiator), params("482913", "fp_i|fp_r", "ekm-1", Responder))
	if err != nil {
		t.Fatal(err)
	}
	if !sa.Verify(sb.Tag()) || !sb.Verify(sa.Tag()) {
		t.Fatal("a good run does not confirm")
	}
	if bytes.Equal(sa.Tag(), sb.Tag()) {
		t.Fatal("both sides send the same tag: a reflected tag would pass")
	}
	for name, pr := range map[string]Params{
		"wrong PIN":        params("482914", "fp_i|fp_r", "ekm-1", Responder),
		"other channel":    params("482913", "fp_i|fp_r", "ekm-2", Responder),
		"other identities": params("482913", "fp_i|fp_x", "ekm-1", Responder),
		"same role twice":  params("482913", "fp_i|fp_r", "ekm-1", Initiator),
	} {
		sa, sb, err := pair(t, params("482913", "fp_i|fp_r", "ekm-1", Initiator), pr)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if sa.Verify(sb.Tag()) || sb.Verify(sa.Tag()) {
			t.Fatalf("%s: the run confirmed", name)
		}
	}
}

// A share altered in transit, or too short, never confirms; a side runs once.
func TestBadShares(t *testing.T) {
	a, _ := New(SuiteCPaceRistretto255, params("123456", "ci", "sid", Initiator))
	b, _ := New(SuiteCPaceRistretto255, params("123456", "ci", "sid", Responder))
	bad := b.Share()
	bad[0] ^= 1
	if sa, err := a.Finish(bad); err == nil {
		sb, _ := b.Finish(a.Share())
		if sb != nil && (sa.Verify(sb.Tag()) || sb.Verify(sa.Tag())) {
			t.Fatal("an altered share confirmed")
		}
	} else if !errors.Is(err, ErrAbort) {
		t.Fatalf("altered share: %v", err)
	}
	if _, err := a.Finish(b.Share()); err == nil {
		t.Fatal("a side finished twice")
	}
	c, _ := New(SuiteCPaceRistretto255, params("123456", "ci", "sid", Initiator))
	if _, err := c.Finish(b.Share()[:31]); !errors.Is(err, ErrAbort) {
		t.Fatalf("short share: %v; want ErrAbort", err)
	}
	if _, err := New("SPAKE2-OF-THE-FUTURE", params("123456", "ci", "sid", Initiator)); !errors.Is(err, ErrUnknownSuite) {
		t.Fatalf("unknown suite: %v", err)
	}
}

// PAIR-07 and chapter 13: an error never carries the PIN.
func TestErrorsHoldNoSecret(t *testing.T) {
	const pin = "731905"
	a, _ := New(SuiteCPaceRistretto255, params(pin, "ci", "sid", Initiator))
	_, err := a.Finish(make([]byte, 32))
	if err == nil || strings.Contains(err.Error(), pin) {
		t.Fatalf("error %v", err)
	}
}

// Fuzzing of a peer's share: whatever the bytes, Finish never panics, and a share that was not
// made with the PIN never confirms.
func FuzzFinish(f *testing.F) {
	f.Add(make([]byte, 32))
	f.Add(bytes.Repeat([]byte{0xff}, 32))
	f.Add([]byte{1, 2, 3})
	f.Fuzz(func(t *testing.T, share []byte) {
		a, err := New(SuiteCPaceRistretto255, params("000000", "ci", "sid", Initiator))
		if err != nil {
			t.Fatal(err)
		}
		s, err := a.Finish(share)
		if err != nil {
			return
		}
		if s.Verify(make([]byte, 64)) {
			t.Fatal("a zero tag confirmed")
		}
	})
}
