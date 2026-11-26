package api

import (
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/session"
)

func TestCleanDeviceName(t *testing.T) {
	for in, want := range map[string]string{
		"  Naruto  ":                    "Naruto",
		"PC\x00 d'Amadou\n":             "PC d'Amadou",
		"\t\r\n":                        "",
		strings.Repeat("é", 80):         strings.Repeat("é", MaxDeviceNameLen),
		"Galaxy A57 de\u0007 Fatoumata": "Galaxy A57 de Fatoumata",
		// #309: bidi formatting reverses what follows it on screen.
		"Phone\u202Egnp.exe":                "Phonegnp.exe",
		"\u2066PC\u2069 \u200Fde\u200E Awa": "PC de Awa",
	} {
		if got := CleanDeviceName(in); got != want {
			t.Errorf("CleanDeviceName(%q) = %q, want %q", in, got, want)
		}
	}
}

// N5, #136: the default name is the machine's, except "localhost" (Android), replaced by the
// phone's maker and model; with neither, a neutral name.
func TestDefaultDeviceName(t *testing.T) {
	oldHost, oldModel := hostname, androidModel
	t.Cleanup(func() { hostname, androidModel = oldHost, oldModel })
	for _, c := range []struct{ host, model, want string }{
		{"Naruto", "Samsung SM-A576B", "Naruto"},
		{"localhost", "Samsung SM-A576B", "Samsung SM-A576B"},
		{"localhost.localdomain", "Samsung SM-A576B", "Samsung SM-A576B"},
		{"", "", "medXfer device"},
		{"localhost", "", "medXfer device"},
	} {
		hostname = func() string { return c.host }
		androidModel = func() string { return c.model }
		if got := DefaultDeviceName(); got != c.want {
			t.Errorf("host %q, model %q: %q, want %q", c.host, c.model, got, c.want)
		}
	}
}

// The name set with set_config is cleaned like the others.
func TestSetConfigCleansTheName(t *testing.T) {
	s, n := startOwnIdentity(t, "Daemon")
	n.WaitReply(n.Request("set_config", map[string]any{"device_name": "  PC\x00 d'Amadou  "}), 0)
	s.mu.RLock()
	got := s.config.DeviceName
	s.mu.RUnlock()
	if got != "PC d'Amadou" {
		t.Fatalf("device name %q", got)
	}
}

// #309: a name received from a peer is cleaned as this device's own is: at most 64 characters,
// without control or bidi formatting characters.
func TestReceivedNameIsCleaned(t *testing.T) {
	s, n := startOwnIdentity(t, "Daemon")
	n.Request("pair_show", nil)
	var shown struct {
		URI string `json:"uri"`
	}
	if err := n.WaitEvent("pair_show", 0).Decode(&shown); err != nil {
		t.Fatal(err)
	}
	u, err := pairing.Parse(shown.URI, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cfg := testPeerChan(t)
	cfg.Expected, cfg.AllowUnknown = u.Fingerprint, false
	dirty := "Phone\u202Egnp.exe\n" + strings.Repeat("x", 3000) // under LIM-03 (4 KiB before authentication)
	if got := <-pairRequest(t, n.PeerAddr(), cfg, session.Message{Secret: pairing.EncodeSecret(u.Secret), DeviceName: dirty}, 10*time.Second); got.Type != "pair_accept" {
		t.Fatalf("pairing: %+v", got)
	}
	var paired struct {
		DeviceName string `json:"device_name"`
	}
	if err := n.WaitEvent("paired", 0).Decode(&paired); err != nil {
		t.Fatal(err)
	}
	if want := CleanDeviceName(dirty); paired.DeviceName != want {
		t.Fatalf("name shown: %d characters %q..., want %q", len([]rune(paired.DeviceName)), first(paired.DeviceName, 24), want)
	}
	_ = s
}

func first(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
