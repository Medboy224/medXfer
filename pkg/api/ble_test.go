package api

import (
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/testkit"
)

// The path by BLE through the API (bible §6.6, WP-D.2): two daemons, the radio left out, the
// messages carried from one to the other as the platform would.

// bleStep sends one step of ble_server or ble_client and returns its reply's data.
func bleStep(t *testing.T, n *testkit.Node, action string, payload map[string]any) map[string]any {
	t.Helper()
	ev := n.WaitReply(n.Request(action, payload), 0)
	if ev.Event != action {
		t.Fatalf("%s %v: %s %s", action, payload["step"], ev.Event, ev.Data)
	}
	return ev.Fields()
}

// askPINByBLE runs the client's pin_wanted to the server, and returns the PIN the server shows.
func askPINByBLE(t *testing.T, na, nb *testkit.Node) string {
	t.Helper()
	out := bleStep(t, nb, "ble_client", map[string]any{"conn": "c1", "step": "pin_wanted"})
	id := na.Request("ble_server", map[string]any{"conn": "s1", "step": "message", "data": out["data"], "create": 3, "join": 3})
	var asked struct {
		PIN  string `json:"pin"`
		From string `json:"from"`
	}
	if err := na.WaitEvent("pin_requested", 0).Decode(&asked); err != nil { // shown before the reply
		t.Fatal(err)
	}
	in := na.WaitReply(id, 0).Fields()
	if !strings.HasPrefix(asked.From, "medXfer-") || asked.PIN == "" {
		t.Fatalf("pin_requested %+v", asked)
	}
	if r := bleStep(t, nb, "ble_client", map[string]any{"conn": "c1", "step": "pin_ready", "data": in["data"]}); r["ready"] != true {
		t.Fatalf("pin_ready %v", r)
	}
	return asked.PIN
}

// BLE-06, BLE-07, BLE-10, chapter 13: an unknown device asks by BLE for the PIN the phone shows;
// the phone creates the network and gives it encrypted; on the network, pair_ble opens the
// session with the key BLE authenticated. Nothing is written (PAIR-08), and the network's
// passphrase never reaches the journal.
func TestBLEByPINThenPairBLE(t *testing.T) {
	journalDir := t.TempDir()
	j, err := diag.OpenJournal(journalDir, diag.JournalBudget, nil)
	if err != nil {
		t.Fatal(err)
	}
	SetJournal(j)
	log.SetOutput(io.MultiWriter(os.Stderr, j))
	t.Cleanup(func() { SetJournal(nil); log.SetOutput(os.Stderr); _ = j.Close() })

	a, na := startOwnIdentity(t, "Phone") // touched: it creates the network
	b, nb := startOwnIdentity(t, "PC")    // touches: it joins
	pin := askPINByBLE(t, na, nb)
	out := bleStep(t, nb, "ble_client", map[string]any{"conn": "c1", "step": "pin", "pin": pin})
	in := bleStep(t, na, "ble_server", map[string]any{"conn": "s1", "step": "message", "data": out["data"], "create": 3, "join": 3})
	out = bleStep(t, nb, "ble_client", map[string]any{"conn": "c1", "step": "pin_reply", "data": in["data"], "create": 1, "join": 3})
	in = bleStep(t, na, "ble_server", map[string]any{"conn": "s1", "step": "message", "data": out["data"], "create": 3, "join": 3})
	req, _ := in["request"].(map[string]any)
	if req == nil || req["fingerprint"] != b.identity.Fingerprint.String() || req["trusted"] != false || req["outcome"] != float64(1) {
		t.Fatalf("request %v", in)
	}

	_, p, _ := net.SplitHostPort(na.PeerAddr())
	port, _ := strconv.Atoi(p)
	const passphrase = "K3PQ8Z2MXA7D"
	in = bleStep(t, na, "ble_server", map[string]any{"conn": "s1", "step": "answer", "outcome": 1, "network": map[string]any{
		"ssid": "DIRECT-ZA-medXfer", "passphrase": passphrase, "security": 1, "band": 2, "addrs": []string{"127.0.0.1"}}})
	out = bleStep(t, nb, "ble_client", map[string]any{"conn": "c1", "step": "reply", "data": in["data"]})
	network, _ := out["network"].(map[string]any)
	if network == nil || network["passphrase"] != passphrase || network["port"] != float64(port) || out["fingerprint"] != a.identity.Fingerprint.String() {
		t.Fatalf("reply %v", out)
	}

	// B joined the network (here, the loopback), and connects with A's key pinned.
	nb.Request("pair_ble", map[string]any{"target": na.PeerAddr(), "fingerprint": out["fingerprint"]})
	nb.WaitEvent("paired", 0)
	na.WaitEvent("paired", 0)
	if a.trust.Contains(b.identity.Fingerprint) || b.trust.Contains(a.identity.Fingerprint) {
		t.Fatal("a pairing by BLE wrote a trusted device")
	}

	log.Printf("debug: joined with %s", passphrase) // a careless line: the redactor catches it
	time.Sleep(50 * time.Millisecond)
	_ = j.Close()
	files, _ := filepath.Glob(filepath.Join(journalDir, "*"))
	var journal string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		journal += string(data)
	}
	if strings.Contains(journal, passphrase) || strings.Contains(journal, strings.ReplaceAll(pin, " ", "")) {
		t.Fatal("the journal holds the network's passphrase or the PIN")
	}
	if !strings.Contains(journal, "debug: joined with") {
		t.Fatal("the journal lacks the careless line: nothing was checked")
	}
}

// BLE-10: pair_ble without a key BLE authenticated is refused, without detail.
func TestPairBLEWithoutBLEIsRefused(t *testing.T) {
	a, na := startOwnIdentity(t, "Phone")
	_, nb := startOwnIdentity(t, "PC")
	nb.Request("pair_ble", map[string]any{"target": na.PeerAddr(), "fingerprint": a.identity.Fingerprint.String()})
	nb.WaitEvent("pair_failed", 0)
	na.WaitEvent("pair_failed", 0)
	nb.ExpectNoEvent("paired", 200*time.Millisecond)
}

// BLE-07: "Refuser" on the request of a PIN by BLE: its next message is refused.
func TestBLEPINRefused(t *testing.T) {
	_, na := startOwnIdentity(t, "Phone")
	_, nb := startOwnIdentity(t, "PC")
	pin := askPINByBLE(t, na, nb)
	na.WaitReply(na.Request("pin_refuse", nil), 0) // the user's "Refuser"
	out := bleStep(t, nb, "ble_client", map[string]any{"conn": "c1", "step": "pin", "pin": pin})
	in := bleStep(t, na, "ble_server", map[string]any{"conn": "s1", "step": "message", "data": out["data"], "create": 3, "join": 3})
	if in["refused"] != true {
		t.Fatalf("a request refused went on: %v", in)
	}
}

// BLE-04: two strangers: the recognition gives the generic name's suffix, nothing else.
func TestBLERecognizeAStranger(t *testing.T) {
	a, na := startOwnIdentity(t, "Phone")
	_, nb := startOwnIdentity(t, "PC")
	out := bleStep(t, nb, "ble_client", map[string]any{"conn": "c1", "step": "hello"})
	in := bleStep(t, na, "ble_server", map[string]any{"conn": "s1", "step": "message", "data": out["data"]})
	peer := bleStep(t, nb, "ble_client", map[string]any{"conn": "c1", "step": "recognize", "data": in["data"]})
	// The suffix as the generic name has it: the list joins a device seen by BLE and on the network.
	if peer["suffix"] == "" || !strings.HasSuffix(a.instance(), "-"+peer["suffix"].(string)) || peer["trusted"] != false || peer["fingerprint"] != nil {
		t.Fatalf("recognized %v, the instance %s", peer, a.instance())
	}
}
