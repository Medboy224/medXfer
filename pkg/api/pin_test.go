package api

import (
	"strings"
	"testing"
)

type pinShownEvent struct {
	PIN     string `json:"pin"`
	Expires int64  `json:"expires"`
	Address string `json:"address"`
}

// Two daemons: one shows a PIN (pair_pin_show), the other types it (pair_pin); both end up
// paired and trusting each other (bible §6.2, WP-5.4).
func TestPairPINBetweenDaemons(t *testing.T) {
	a, na := startOwnIdentity(t, "Shower")
	b, nb := startOwnIdentity(t, "Typist")
	na.Request("pair_pin_show", nil)
	var shown pinShownEvent
	if err := na.WaitEvent("pair_pin_shown", 0).Decode(&shown); err != nil {
		t.Fatal(err)
	}
	if len(strings.ReplaceAll(shown.PIN, " ", "")) != 6 || shown.Expires == 0 {
		t.Fatalf("pair_pin_shown: %+v", shown)
	}
	nb.Request("pair_pin", map[string]any{"target": na.PeerAddr(), "pin": shown.PIN})
	nb.WaitEvent("paired", 0)
	na.WaitEvent("paired", 0)
	if !a.trust.Contains(b.identity.Fingerprint) || !b.trust.Contains(a.identity.Fingerprint) {
		t.Fatal("the two daemons do not trust each other")
	}
}

// A wrong PIN is a pair_failed on both sides, without detail (PAIR-07), and trusts nothing.
func TestPairPINWrongFails(t *testing.T) {
	a, na := startOwnIdentity(t, "Shower")
	b, nb := startOwnIdentity(t, "Typist")
	na.Request("pair_pin_show", nil)
	var shown pinShownEvent
	if err := na.WaitEvent("pair_pin_shown", 0).Decode(&shown); err != nil {
		t.Fatal(err)
	}
	code := []byte(strings.ReplaceAll(shown.PIN, " ", ""))
	code[0] = '0' + (code[0]-'0'+1)%10
	nb.Request("pair_pin", map[string]any{"target": na.PeerAddr(), "pin": string(code)})
	var failed map[string]any
	if err := nb.WaitEvent("pair_failed", 0).Decode(&failed); err != nil {
		t.Fatal(err)
	}
	if len(failed) != 0 {
		t.Fatalf("pair_failed carries details: %v", failed)
	}
	na.WaitEvent("pair_failed", 0)
	if a.trust.Contains(b.identity.Fingerprint) || b.trust.Contains(a.identity.Fingerprint) {
		t.Fatal("a wrong PIN left a trust behind")
	}
}

// A PIN that is not 6 to 8 digits is refused before anything is sent.
func TestPairPINBadPayload(t *testing.T) {
	_, n := startOwnIdentity(t, "Typist")
	for _, p := range []map[string]any{{"target": "127.0.0.1:1", "pin": "12"}, {"pin": "123456"}} {
		n.Request("pair_pin", p)
		n.WaitEvent("action_error", 0)
	}
}
