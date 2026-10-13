package pake

import (
	_ "embed"
	"encoding/json"
	"testing"
)

// Outputs of github.com/the-sarge/cpace v0.1.3 (CPACE-RISTR255-SHA512, draft 21), computed
// once by its own internal functions on 32 random inputs (WP-5.5, decision D7 of the J5
// plan): an independent implementation, compared once, never a dependency.
//
//go:embed testdata/xcheck-the-sarge-cpace-v0.1.3.json
var xcheckJSON []byte

// The confirmation tags of B.3's inputs (ADa = "ADa", ADb = "ADb"). The draft gives none; these
// come from the same implementation (testdata/draft21-ristretto255-confirmation-tags.json).
const (
	xcheckB3TagA = "30478d28d83ae3b214295e91f4a3eb62999a8ed94575075db4a7d3bc3e4d26f16c51adba12dc7771372637a46b5134e0937cddd7d820bfdd83be335e339eb57a"
	xcheckB3TagB = "e927a7da291676f8878caae9cb70407d2f14117e6f9ff9adcfda797c4bcf2bd2fefd2ca3f2ddcce81b7386996c895b2f359010cc92069d5ff4ad49a977de79a6"
)

// Another implementation, on random inputs: same generator, shares, K, ISK and confirmation
// tags, byte for byte. It covers what the draft's single vector cannot: other lengths of PRS,
// CI, sid and AD (empty AD too), and the tags of §10.4.
func TestCrossCheckTheSargeCPace(t *testing.T) {
	var recs []struct {
		PRS, CI, SID, ADa, ADb string
		ScalarA, ScalarB       string
		Generator              string
		Ya, Yb, K, ISK         string
		TagA, TagB             string
	}
	if err := json.Unmarshal(xcheckJSON, &recs); err != nil {
		t.Fatal(err)
	}
	if len(recs) != 32 {
		t.Fatalf("%d records", len(recs))
	}
	for i, r := range recs {
		p := Params{Password: unhex(t, r.PRS), CI: unhex(t, r.CI), SID: unhex(t, r.SID), Role: Initiator}
		g, err := calculateGenerator(p.Password, p.CI, p.SID)
		if err != nil {
			t.Fatal(err)
		}
		eqHex(t, "generator", g.Bytes(), r.Generator)
		ya, err := scalarFromBytes(unhex(t, r.ScalarA))
		if err != nil {
			t.Fatal(err)
		}
		yb, err := scalarFromBytes(unhex(t, r.ScalarB))
		if err != nil {
			t.Fatal(err)
		}
		a, err := newCPaceWith(p, unhex(t, r.ADa), unhex(t, r.ADb), ya)
		if err != nil {
			t.Fatal(err)
		}
		pb := p
		pb.Role = Responder
		b, err := newCPaceWith(pb, unhex(t, r.ADb), unhex(t, r.ADa), yb)
		if err != nil {
			t.Fatal(err)
		}
		eqHex(t, "Ya", a.Share(), r.Ya)
		eqHex(t, "Yb", b.Share(), r.Yb)
		eqHex(t, "K", scalarMultVfy(ya, b.Share()), r.K)
		sa, err := a.Finish(b.Share())
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		sb, err := b.Finish(a.Share())
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		eqHex(t, "ISK at A", sa.isk, r.ISK)
		eqHex(t, "ISK at B", sb.isk, r.ISK)
		eqHex(t, "Ta", sa.Tag(), r.TagA)
		eqHex(t, "Tb", sb.Tag(), r.TagB)
	}
}

// B.3's inputs: our tags equal the other implementation's.
func TestCrossCheckB3Tags(t *testing.T) {
	ya, err := scalarFromBytes(unhex(t, b3ScalarA))
	if err != nil {
		t.Fatal(err)
	}
	yb, err := scalarFromBytes(unhex(t, b3ScalarB))
	if err != nil {
		t.Fatal(err)
	}
	a, err := newCPaceWith(b3Params(t, Initiator), []byte("ADa"), []byte("ADb"), ya)
	if err != nil {
		t.Fatal(err)
	}
	b, err := newCPaceWith(b3Params(t, Responder), []byte("ADb"), []byte("ADa"), yb)
	if err != nil {
		t.Fatal(err)
	}
	sa, err := a.Finish(b.Share())
	if err != nil {
		t.Fatal(err)
	}
	sb, err := b.Finish(a.Share())
	if err != nil {
		t.Fatal(err)
	}
	eqHex(t, "Ta", sa.Tag(), xcheckB3TagA)
	eqHex(t, "Tb", sb.Tag(), xcheckB3TagB)
}
