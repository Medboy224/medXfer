package pairing

import (
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
)

// trustThrough establishes mutual trust between a and b by the negotiation itself (PAIR-10), so
// that both get the pair's recognition key from trust_commit.
func trustThrough(t *testing.T, a, b Options) {
	t.Helper()
	clock := time.Now()
	ta, tb := talks(a, b, &clock)
	handle(t, tb, ta.Offer(), TrustAsked)
	yes, err := tb.Answer(true)
	if err != nil {
		t.Fatal(err)
	}
	commit := handle(t, ta, yes, TrustEstablished)
	handle(t, tb, *commit, TrustEstablished)
}

func keyOf(t *testing.T, o Options, peer identity.Fingerprint) []byte {
	t.Helper()
	p, ok := o.Trust.Get(peer)
	if !ok {
		t.Fatal("not trusted")
	}
	return p.TagKey
}

// DISC-11: a tag is the HMAC of the pair's key, the announcer's key and the period: stable within
// a period, different from one period, one announcer or one key to the next, of a fixed shape.
func TestTagShape(t *testing.T) {
	key := make([]byte, tagKeySize)
	other := make([]byte, tagKeySize)
	other[0] = 1
	a, b := device(t, "a"), device(t, "b")
	tag := Tag(key, a.Identity.Fingerprint, 100)
	if !ValidTag(tag) || tag != Tag(key, a.Identity.Fingerprint, 100) {
		t.Fatalf("tag %q", tag)
	}
	for _, x := range []string{Tag(key, a.Identity.Fingerprint, 101), Tag(key, b.Identity.Fingerprint, 100), Tag(other, a.Identity.Fingerprint, 100)} {
		if x == tag {
			t.Fatal("two tags agree that should not")
		}
	}
	for _, bad := range []string{"", "abc", "ABCDEFGHIJKLM!", "0000000000000"} {
		if ValidTag(bad) {
			t.Errorf("%q taken for a tag", bad)
		}
	}
	if TagPeriodOf(time.Unix(1200, 0)) != 2 || TagPeriodOf(time.Unix(1199, 0)) != 1 {
		t.Fatal("periods of 10 minutes")
	}
}

// N1, N2, N3: after mutual trust, each recognizes the other by its tags, in the period and the
// one after; a stranger recognizes nothing; forgetting, on either side, ends the recognition.
func TestRecognizeAfterMutualTrust(t *testing.T) {
	a, b, stranger := device(t, "laptop"), device(t, "phone"), device(t, "stranger")
	trustThrough(t, a, b)
	ka, kb := keyOf(t, a, b.Identity.Fingerprint), keyOf(t, b, a.Identity.Fingerprint)
	if len(ka) != tagKeySize || string(ka) != string(kb) {
		t.Fatal("the two sides do not share one recognition key")
	}

	now := time.Now()
	tagsA := AnnounceTags(a.Trust, a.Identity.Fingerprint, now)
	if len(tagsA) != 1 || !ValidTag(tagsA[0]) {
		t.Fatalf("a announces %v", tagsA)
	}
	if p, ok := Recognize(b.Trust, tagsA, now); !ok || p.Fingerprint != a.Identity.Fingerprint || p.Alias != "a" {
		t.Fatalf("b did not recognize a: %+v %v", p, ok)
	}
	if _, ok := Recognize(b.Trust, tagsA, now.Add(TagPeriod)); !ok {
		t.Fatal("not recognized in the next period (clocks a little apart)")
	}
	if _, ok := Recognize(b.Trust, tagsA, now.Add(2*TagPeriod)); ok {
		t.Fatal("a tag still valid two periods later")
	}
	if _, ok := Recognize(stranger.Trust, tagsA, now); ok {
		t.Fatal("a stranger recognized a")
	}
	if _, ok := Recognize(a.Trust, tagsA, now); ok {
		t.Fatal("a recognized its own tag as b's")
	}

	// a forgets b: it no longer announces b's tag.
	if _, err := a.Trust.Forget(b.Identity.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if tags := AnnounceTags(a.Trust, a.Identity.Fingerprint, now); len(tags) != 0 {
		t.Fatalf("a still announces %v", tags)
	}
	// b forgets a: it no longer recognizes it, even from old tags.
	if _, err := b.Trust.Forget(a.Identity.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, ok := Recognize(b.Trust, tagsA, now); ok {
		t.Fatal("b still recognizes a")
	}
}

// When both offer, both commit with their own key: both keep the one drawn by the smaller
// fingerprint, so that they still share one key.
func TestBothOfferShareOneTagKey(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	clock := time.Now()
	ta, tb := talks(a, b, &clock)
	offerA, offerB := ta.Offer(), tb.Offer()
	acceptB := handle(t, tb, offerA, TrustNothing)
	acceptA := handle(t, ta, offerB, TrustNothing)
	commitA := handle(t, ta, *acceptB, TrustEstablished)
	commitB := handle(t, tb, *acceptA, TrustEstablished)
	handle(t, ta, *commitB, TrustNothing)
	handle(t, tb, *commitA, TrustNothing)
	ka, kb := keyOf(t, a, b.Identity.Fingerprint), keyOf(t, b, a.Identity.Fingerprint)
	if len(ka) != tagKeySize || string(ka) != string(kb) {
		t.Fatal("after a double offer, the keys differ")
	}
}

// An announcement carries MaxTags tags at most, for the most recently trusted devices; an entry
// without a key (written before DISC-11) gives no tag.
func TestAnnounceTagsBound(t *testing.T) {
	a := device(t, "a")
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var newest identity.Fingerprint
	for i := 0; i < MaxTags+4; i++ {
		other := device(t, "o")
		key := make([]byte, tagKeySize)
		key[0] = byte(i + 1)
		if err := a.Trust.Add(identity.Peer{Fingerprint: other.Identity.Fingerprint, TagKey: key, AddedAt: base.Add(time.Duration(i) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
		newest = other.Identity.Fingerprint
	}
	old := device(t, "old")
	if err := a.Trust.Add(identity.Peer{Fingerprint: old.Identity.Fingerprint, AddedAt: base.Add(100 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tags := AnnounceTags(a.Trust, a.Identity.Fingerprint, now)
	if len(tags) != MaxTags {
		t.Fatalf("%d tags, want %d", len(tags), MaxTags)
	}
	p, _ := a.Trust.Get(newest)
	if tags[0] != Tag(p.TagKey, a.Identity.Fingerprint, TagPeriodOf(now)) {
		t.Fatal("the most recently trusted device does not come first")
	}
}

// N6: an entry the user renamed keeps its name when the trust is established again.
func TestTrustAgainKeepsTheUsersName(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	if err := a.Trust.Add(identity.Peer{Fingerprint: b.Identity.Fingerprint, Alias: "Phone of Fatou", Renamed: true}); err != nil {
		t.Fatal(err)
	}
	trustThrough(t, a, b)
	if p, _ := a.Trust.Get(b.Identity.Fingerprint); p.Alias != "Phone of Fatou" || !p.Renamed || len(p.TagKey) != tagKeySize {
		t.Fatalf("entry %+v", p)
	}
}
