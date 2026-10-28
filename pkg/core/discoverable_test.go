package core

import (
	"encoding/json"
	"testing"

	"github.com/Medboy224/medXfer/pkg/api"
	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/netbind"
)

func statusOf(t *testing.T, r Response) map[string]any {
	t.Helper()
	if !r.OK || len(r.Events) == 0 || r.Events[0].Event != "status" {
		t.Fatalf("response %+v", r)
	}
	b, _ := json.Marshal(r.Events[0].Data)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// DISC-05: a core starts silent; set_discoverable turns the announcement on and off. DISC-07:
// the status gives the generic name the other devices see.
func TestDiscoverableOnDemand(t *testing.T) {
	c := newCore(t, "Quiet")
	st := statusOf(t, c.Call(api.RequestMessage{Action: "get_status"}))
	if st["discoverable"] != false {
		t.Fatalf("a core announces itself without being asked: %v", st["discoverable"])
	}
	if name, _ := st["instance"].(string); len(name) != len("medXfer-xxxx") {
		t.Fatalf("instance %q", st["instance"])
	}
	on := c.Call(api.RequestMessage{Action: "set_discoverable", Payload: json.RawMessage(`{"on":true}`)})
	if statusOf(t, on)["discoverable"] != true {
		t.Fatal("set_discoverable on did nothing")
	}
	off := c.Call(api.RequestMessage{Action: "set_discoverable", Payload: json.RawMessage(`{"on":false}`)})
	if statusOf(t, off)["discoverable"] != false {
		t.Fatal("set_discoverable off did nothing")
	}
	if r := c.Call(api.RequestMessage{Action: "set_discoverable", Payload: json.RawMessage(`nope`)}); r.OK {
		t.Fatal("a bad payload was accepted")
	}
}

// DISC-10: set_network_info takes the platform's interfaces, and refuses a bad list.
func TestSetNetworkInfoAction(t *testing.T) {
	t.Cleanup(func() { _ = discovery.SetNetworkInfo(nil) })
	c := newCore(t, "Phone")
	ok := c.Call(api.RequestMessage{Action: "set_network_info",
		Payload: json.RawMessage(`{"interfaces":[{"name":"wlan0","index":50,"addrs":["192.168.1.20/24"]}]}`)})
	if st := statusOf(t, ok); st["local_ip"] != "192.168.1.20" {
		t.Fatalf("local_ip %v, want the platform's address", st["local_ip"])
	}
	for _, bad := range []string{`{}`, `{"interfaces":[{"name":"wlan0","addrs":["nope"]}]}`, `[]`,
		`{"interfaces":[{"name":"wlan0","addrs":["192.168.1.20/24"]}],"enumerate":true}`} {
		if r := c.Call(api.RequestMessage{Action: "set_network_info", Payload: json.RawMessage(bad)}); r.OK {
			t.Fatalf("accepted %s", bad)
		}
	}
	// J6c: Android gives the network it joined (a QR code's); the core's sockets go through it.
	joined := c.Call(api.RequestMessage{Action: "set_network_info",
		Payload: json.RawMessage(`{"interfaces":[{"name":"wlan0","index":46,"addrs":["169.254.121.149/16"]}],"network":643355824141}`)})
	if statusOf(t, joined); netbind.Network() != 643355824141 {
		t.Fatalf("network %d, want the one joined", netbind.Network())
	}
	// WP-C.2: a desktop whose own network stopped gives the enumeration back to the core.
	back := c.Call(api.RequestMessage{Action: "set_network_info", Payload: json.RawMessage(`{"enumerate":true}`)})
	if st := statusOf(t, back); st["local_ip"] == "192.168.1.20" {
		t.Fatal("the platform's address stayed after enumerate")
	}
	if netbind.Network() != 0 {
		t.Fatal("the sockets stay tied to a network after enumerate")
	}
}
