package pairing

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"slices"
	"strings"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
)

// Recognition tags (DISC-11, lot « Noms des appareils de confiance »). Two devices of mutual
// trust share a key, K_AB, drawn when the trust was established (trust_commit). Each announces,
// for each of its trusted devices, a tag made of that key and the current period; only the
// other device can compute it, and it changes every TagPeriod, so a stranger can neither link
// two announcements nor follow a device. The scanning device then shows the name it recorded,
// which never travels on the network (DISC-06).

// TagPeriod is how long a tag lasts (N3).
const TagPeriod = 10 * time.Minute

// MaxTags bounds the tags of one announcement: the most recently trusted devices (N2).
const MaxTags = 16

// tagLabel opens the HMAC input: another use of the key gives other values.
const tagLabel = "medXfer-tag-v1"

// tagKeySize is the size of a recognition key, drawn with crypto/rand.
const tagKeySize = 32

var tagEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// TagPeriodOf is the period t falls in.
func TagPeriodOf(t time.Time) int64 { return t.Unix() / int64(TagPeriod/time.Second) }

// Tag is the tag a device of key fingerprint announcer announces for the device it shares key
// with, during period: HMAC-SHA-256(key, label ‖ announcer ‖ period), cut to 8 bytes, base32.
func Tag(key []byte, announcer identity.Fingerprint, period int64) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(tagLabel))
	m.Write(announcer[:])
	var p [8]byte
	binary.BigEndian.PutUint64(p[:], uint64(period))
	m.Write(p[:])
	return strings.ToLower(tagEncoding.EncodeToString(m.Sum(nil)[:8]))
}

// ValidTag reports whether s has the shape of a tag: what an announcement carries is checked
// before use.
func ValidTag(s string) bool {
	if len(s) != 13 {
		return false
	}
	_, err := tagEncoding.DecodeString(strings.ToUpper(s))
	return err == nil
}

// tagged are the trusted devices that have a recognition key, the most recently trusted first.
func tagged(store *identity.TrustStore) []identity.Peer {
	if store == nil {
		return nil
	}
	var out []identity.Peer
	for _, p := range store.List() {
		if len(p.TagKey) == tagKeySize {
			out = append(out, p)
		}
	}
	slices.SortStableFunc(out, func(a, b identity.Peer) int { return b.AddedAt.Compare(a.AddedAt) })
	return out
}

// AnnounceTags are the tags this device (self) announces at now: always MaxTags, one per trusted
// device with a recognition key (the most recently trusted first, MaxTags at most), completed by
// tags of the same shape that no device can recognize, then sorted. Neither their number nor their
// order tells how many devices this one trusts (#308).
func AnnounceTags(store *identity.TrustStore, self *identity.Identity, now time.Time) []string {
	period := TagPeriodOf(now)
	tags := make([]string, 0, MaxTags)
	for _, p := range tagged(store) {
		if len(tags) == MaxTags {
			break
		}
		tags = append(tags, Tag(p.TagKey, self.Fingerprint, period))
	}
	key := padKeyOf(self)
	for i := 0; len(tags) < MaxTags; i++ {
		if pad := padTag(key, period, i); !slices.Contains(tags, pad) {
			tags = append(tags, pad)
		}
	}
	slices.Sort(tags)
	return tags
}

// padLabel names the key of the completing tags among the keys drawn from the device's.
const padLabel = "medXfer-pad-v1"

// padKeyOf is the key of self's completing tags. Drawn from its private key, it is secret and the
// same at every launch: a restart within a period changes no tag, so the tags that stay are not
// told apart as the real ones. Without it, a key of this call only.
func padKeyOf(self *identity.Identity) []byte {
	if k, err := self.DerivedKey(padLabel); err == nil {
		return k
	}
	k := make([]byte, tagKeySize)
	_, _ = rand.Read(k) // never fails (crypto/rand)
	return k
}

// padTag is the i-th completing tag during period, under key.
func padTag(key []byte, period int64, i int) string {
	m := hmac.New(sha256.New, key)
	var p [16]byte
	binary.BigEndian.PutUint64(p[:8], uint64(period))
	binary.BigEndian.PutUint64(p[8:], uint64(i))
	m.Write(p[:])
	return strings.ToLower(tagEncoding.EncodeToString(m.Sum(nil)[:8]))
}

// Recognize returns the trusted device that announced one of tags, at now, in the period before
// or in the one after (clocks a little apart, N3).
func Recognize(store *identity.TrustStore, tags []string, now time.Time) (identity.Peer, bool) {
	if len(tags) == 0 {
		return identity.Peer{}, false
	}
	period := TagPeriodOf(now)
	for _, p := range tagged(store) {
		for _, at := range []int64{period, period - 1, period + 1} {
			if slices.Contains(tags, Tag(p.TagKey, p.Fingerprint, at)) {
				return p, true
			}
		}
	}
	return identity.Peer{}, false
}
