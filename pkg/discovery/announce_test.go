package discovery

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// DISC-06, #65: a beacon and a discovery reply, serialized, carry neither the host name nor any
// alias: only a generic instance name, drawn at each launch.
func TestAnnouncementNamesNoDevice(t *testing.T) {
	host, _ := os.Hostname()
	srv := NewDiscoveryServer("sender", 18888, &TransferOffer{FileName: "a.bin", IsBatch: true})
	srv.SetPairingCode("104-567")
	name := regexp.MustCompile(`^medXfer-[0-9a-f]{4}$`)
	for what, p := range map[string]Peer{
		"beacon":              srv.beacon("10.0.0.5"),
		"reply":               srv.answer(DiscoveryQuery{}, "10.0.0.5"),
		"reply with the code": srv.answer(DiscoveryQuery{PairingCode: "104-567"}, "10.0.0.5"),
	} {
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(host) >= 3 && strings.Contains(strings.ToLower(string(data)), strings.ToLower(host)) {
			t.Errorf("%s carries the host name %q: %s", what, host, data)
		}
		if !name.MatchString(p.DeviceName) || p.ID != p.DeviceName+"-18888" {
			t.Errorf("%s: name %q, id %q", what, p.DeviceName, p.ID)
		}
	}
}
