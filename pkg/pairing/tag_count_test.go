package pairing

import (
	"slices"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
)

// #308, DISC-11: an announcement always carries MaxTags tags, whatever the number of trusted
// devices: the real ones among others of the same shape, sorted, so neither their number nor
// their order tells anything. Within a period they stay the same; in the next one, all change.
func TestAnnounceTagsAlwaysMaxTags(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 1, 0, 0, time.UTC)
	for _, n := range []int{0, 1, 3, MaxTags} {
		a := device(t, "a")
		var real []string
		for i := 0; i < n; i++ {
			other := device(t, "o")
			key := make([]byte, tagKeySize)
			key[0], key[1] = byte(i+1), 0x5a
			if err := a.Trust.Add(identity.Peer{Fingerprint: other.Identity.Fingerprint, TagKey: key}); err != nil {
				t.Fatal(err)
			}
			real = append(real, Tag(key, a.Identity.Fingerprint, TagPeriodOf(now)))
		}
		tags := AnnounceTags(a.Trust, a.Identity.Fingerprint, now)
		if len(tags) != MaxTags {
			t.Fatalf("%d trusted devices: %d tags, want %d", n, len(tags), MaxTags)
		}
		seen := map[string]bool{}
		for _, tag := range tags {
			if !ValidTag(tag) || seen[tag] {
				t.Fatalf("%d trusted devices: tag %q invalid or repeated", n, tag)
			}
			seen[tag] = true
		}
		for _, r := range real {
			if !seen[r] {
				t.Fatalf("%d trusted devices: a real tag is missing", n)
			}
		}
		if !slices.IsSorted(tags) {
			t.Fatalf("%d trusted devices: not sorted, the order could tell the real ones", n)
		}
		if again := AnnounceTags(a.Trust, a.Identity.Fingerprint, now.Add(time.Minute)); !slices.Equal(again, tags) {
			t.Fatalf("%d trusted devices: changed within the period", n)
		}
		for _, tag := range AnnounceTags(a.Trust, a.Identity.Fingerprint, now.Add(TagPeriod)) {
			if seen[tag] {
				t.Fatalf("%d trusted devices: a tag kept into the next period", n)
			}
		}
	}
}

// #308: an announcer whose clock is a little ahead, already in the next period, is recognized.
func TestRecognizeNextPeriod(t *testing.T) {
	a, b := device(t, "a"), device(t, "b")
	trustThrough(t, a, b)
	now := time.Now()
	ahead := AnnounceTags(a.Trust, a.Identity.Fingerprint, now.Add(TagPeriod))
	if p, ok := Recognize(b.Trust, ahead, now); !ok || p.Fingerprint != a.Identity.Fingerprint {
		t.Fatal("an announcer one period ahead is not recognized")
	}
}
