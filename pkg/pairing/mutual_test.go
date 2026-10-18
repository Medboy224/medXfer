package pairing

import (
	"errors"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/session"
)

// talks returns the negotiations of a session between a and b, on a clock the test moves.
func talks(a, b Options, clock *time.Time) (ta, tb *TrustTalk) {
	ta = NewTrustTalk(a.Trust, a.Identity.Fingerprint, identity.Peer{Fingerprint: b.Identity.Fingerprint, Alias: "b", LastAddress: "10.0.0.2"})
	tb = NewTrustTalk(b.Trust, b.Identity.Fingerprint, identity.Peer{Fingerprint: a.Identity.Fingerprint, Alias: "a", LastAddress: "10.0.0.1"})
	ta.now = func() time.Time { return *clock }
	tb.now = func() time.Time { return *clock }
	return ta, tb
}

// handle passes m to t and checks what it meant.
func handle(t *testing.T, tt *TrustTalk, m session.Message, want TrustEvent) *session.Message {
	t.Helper()
	reply, ev, err := tt.Handle(m)
	if err != nil {
		t.Fatal(err)
	}
	if ev != want {
		t.Fatalf("%s meant %d, want %d", m.Type, ev, want)
	}
	return reply
}

// mutual reports whether each side sees the other as a device of mutual trust: its own list,
// and what the other declares (PAIR-09).
func mutual(a, b Options) (fromA, fromB bool) {
	fpA, fpB := a.Identity.Fingerprint, b.Identity.Fingerprint
	return Mutual(a.Trust, fpB, Declares(b.Trust, fpA)), Mutual(b.Trust, fpA, Declares(a.Trust, fpB))
}

// PAIR-10: both say yes, each writes the other's key, and the trust is mutual on both sides.
func TestTrustNeedsBothYes(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	clock := time.Now()
	ta, tb := talks(a, b, &clock)

	handle(t, tb, ta.Offer(), TrustAsked)
	if a.Trust.Contains(b.Identity.Fingerprint) || b.Trust.Contains(a.Identity.Fingerprint) {
		t.Fatal("written before any answer")
	}
	yes, err := tb.Answer(true)
	if err != nil {
		t.Fatal(err)
	}
	commit := handle(t, ta, yes, TrustEstablished)
	if commit == nil || commit.Type != TypeTrustCommit {
		t.Fatalf("after the yes: %+v, want trust_commit", commit)
	}
	handle(t, tb, *commit, TrustEstablished)
	if fromA, fromB := mutual(a, b); !fromA || !fromB {
		t.Fatalf("mutual: %v %v", fromA, fromB)
	}
	if p, _ := a.Trust.Get(b.Identity.Fingerprint); p.Alias != "b" || p.LastAddress != "10.0.0.2" {
		t.Fatalf("written as %+v", p)
	}
}

// A no, or no answer within TrustAnswerTime, writes nothing, on either side.
func TestTrustDeclinedOrUnansweredWritesNothing(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	clock := time.Now()
	ta, tb := talks(a, b, &clock)

	handle(t, tb, ta.Offer(), TrustAsked)
	no, err := tb.Answer(false)
	if err != nil {
		t.Fatal(err)
	}
	handle(t, ta, no, TrustDeclined)

	handle(t, tb, ta.Offer(), TrustAsked)
	clock = clock.Add(TrustAnswerTime + time.Second)
	if _, err := tb.Answer(true); !errors.Is(err, ErrNoOffer) {
		t.Fatalf("late answer: %v, want ErrNoOffer", err)
	}
	// A yes sent late, as a peer might: the offer is over, nothing is committed.
	if reply := handle(t, ta, session.Message{Type: TypeTrustAccept}, TrustNothing); reply != nil {
		t.Fatalf("late yes committed: %+v", reply)
	}
	if a.Trust.Contains(b.Identity.Fingerprint) || b.Trust.Contains(a.Identity.Fingerprint) {
		t.Fatal("written without both yes in time")
	}
}

// The peer cannot make this side write its key: an accept without our offer, or a commit
// without our yes, is ignored.
func TestTrustCannotBeForced(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	clock := time.Now()
	ta, _ := talks(a, b, &clock)
	for _, typ := range []string{TypeTrustAccept, TypeTrustCommit, TypeTrustDecline} {
		if reply := handle(t, ta, session.Message{Type: typ}, TrustNothing); reply != nil {
			t.Fatalf("unasked %s answered %+v", typ, reply)
		}
	}
	if a.Trust.Contains(b.Identity.Fingerprint) {
		t.Fatal("written without our yes")
	}
	// Our user's no to an offer, then the peer's commit: still nothing.
	handle(t, ta, session.Message{Type: TypeTrustOffer}, TrustAsked)
	if _, err := ta.Answer(false); err != nil {
		t.Fatal(err)
	}
	handle(t, ta, session.Message{Type: TypeTrustCommit}, TrustNothing)
	if a.Trust.Contains(b.Identity.Fingerprint) {
		t.Fatal("written after our no")
	}
}

// D5: a trust_commit lost on the way leaves the key on the offerer's side only, which gives
// no privilege on either side.
func TestLostCommitLeavesAnInertEntry(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	clock := time.Now()
	ta, tb := talks(a, b, &clock)
	handle(t, tb, ta.Offer(), TrustAsked)
	yes, _ := tb.Answer(true)
	handle(t, ta, yes, TrustEstablished) // the commit never reaches b
	if !a.Trust.Contains(b.Identity.Fingerprint) || b.Trust.Contains(a.Identity.Fingerprint) {
		t.Fatal("expected a's entry only")
	}
	if fromA, fromB := mutual(a, b); fromA || fromB {
		t.Fatalf("a one-sided entry gives privileges: %v %v", fromA, fromB)
	}
}

// Both offer at once (--trust on both sides): each offer is the other's yes, and each side
// reports the trust once.
func TestBothOfferAtOnce(t *testing.T) {
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
	if fromA, fromB := mutual(a, b); !fromA || !fromB {
		t.Fatalf("mutual: %v %v", fromA, fromB)
	}
}

// D4: forgetting the other on one side ends the privileges on both.
func TestForgetOnOneSideEndsBoth(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	clock := time.Now()
	ta, tb := talks(a, b, &clock)
	handle(t, tb, ta.Offer(), TrustAsked)
	yes, _ := tb.Answer(true)
	commit := handle(t, ta, yes, TrustEstablished)
	handle(t, tb, *commit, TrustEstablished)
	if _, err := b.Trust.Forget(a.Identity.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if fromA, fromB := mutual(a, b); fromA || fromB {
		t.Fatalf("after b forgot a: %v %v", fromA, fromB)
	}
	if Mutual(a.Trust, b.Identity.Fingerprint, false) || Mutual(nil, b.Identity.Fingerprint, true) {
		t.Fatal("Mutual without a declaration or a list")
	}
}

// A trust written again keeps what the user set on the entry (auto-accept, date).
func TestTrustKeepsTheUsersSettings(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	added := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := a.Trust.Add(identity.Peer{Fingerprint: b.Identity.Fingerprint, Alias: "old", AutoAccept: true, AddedAt: added}); err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	ta, tb := talks(a, b, &clock)
	handle(t, tb, ta.Offer(), TrustAsked)
	yes, _ := tb.Answer(true)
	handle(t, ta, yes, TrustEstablished)
	p, _ := a.Trust.Get(b.Identity.Fingerprint)
	if !p.AutoAccept || !p.AddedAt.Equal(added) || p.Alias != "b" {
		t.Fatalf("entry %+v", p)
	}
}
