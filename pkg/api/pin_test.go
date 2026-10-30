package api

import (
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Medboy224/medXfer/pkg/discovery"
)

type pinShownEvent struct {
	PIN       string       `json:"pin"`
	Expires   int64        `json:"expires"`
	Address   string       `json:"address"`
	Addresses []pinAddress `json:"addresses"`
}

// DISC-13, WP-E.0 of the cable plan: a PC on a cable and on Wi-Fi showed its Wi-Fi address
// alone under the PIN, out of reach of the PC at the other end of the cable. Now one address
// per network, cable first, the primary one among them.
func TestPINShowsAnAddressPerNetwork(t *testing.T) {
	t.Cleanup(func() { _ = discovery.SetNetworkInfo(nil) })
	if err := discovery.SetNetworkInfo([]discovery.NetworkInterface{
		{Name: "wltest0", Addrs: []string{"192.0.2.174/24"}},
		{Name: "ethtest0", Addrs: []string{"169.254.185.66/16"}},
	}); err != nil {
		t.Fatal(err)
	}
	s, n := startOwnIdentity(t, "Shower")
	n.Request("pair_pin_show", nil)
	var shown pinShownEvent
	if err := n.WaitEvent("pair_pin_shown", 0).Decode(&shown); err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(s.nodeLn.Addr().(*net.TCPAddr).Port)
	want := []pinAddress{
		{Address: net.JoinHostPort("169.254.185.66", port), Kind: discovery.KindEthernet},
		{Address: net.JoinHostPort("192.0.2.174", port), Kind: discovery.KindWiFi},
	}
	if !slices.Equal(shown.Addresses, want) {
		t.Fatalf("addresses %+v, want %+v", shown.Addresses, want)
	}
	if !slices.ContainsFunc(shown.Addresses, func(a pinAddress) bool { return a.Address == shown.Address }) {
		t.Fatalf("the primary address %s is not among %+v", shown.Address, shown.Addresses)
	}
}

// Two daemons: one shows a PIN (pair_pin_show), the other types it (pair_pin); both end up
// paired, for this session only: nothing is written (bible §6.2, PAIR-08).
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
	if a.trust.Contains(b.identity.Fingerprint) || b.trust.Contains(a.identity.Fingerprint) {
		t.Fatal("a PIN pairing wrote a trusted device")
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
