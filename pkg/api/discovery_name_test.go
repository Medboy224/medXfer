package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// DISC-06, #65: the daemon's announcement carries neither its configured name nor a new one
// set by set_config; the name travels only after authentication (pair_accept).
func TestDaemonAnnouncesNoName(t *testing.T) {
	s, n := startOwnIdentity(t, "AliasSecret-Q7")
	check := func(alias string) {
		t.Helper()
		data, err := json.Marshal(s.discSrv.GetPeer())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), alias) {
			t.Errorf("the announcement carries %q: %s", alias, data)
		}
	}
	check("AliasSecret-Q7")
	n.Request("set_config", map[string]any{"device_name": "OtherAlias-Z9"})
	n.WaitEvent("status", 0)
	check("OtherAlias-Z9")
}
