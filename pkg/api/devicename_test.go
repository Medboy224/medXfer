package api

import (
	"strings"
	"testing"
)

func TestCleanDeviceName(t *testing.T) {
	for in, want := range map[string]string{
		"  Naruto  ":                    "Naruto",
		"PC\x00 d'Amadou\n":             "PC d'Amadou",
		"\t\r\n":                        "",
		strings.Repeat("é", 80):         strings.Repeat("é", MaxDeviceNameLen),
		"Galaxy A57 de\u0007 Fatoumata": "Galaxy A57 de Fatoumata",
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
