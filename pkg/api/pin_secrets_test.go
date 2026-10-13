package api

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/testkit"
)

// blob matches what a share, an ISK or an ekm would look like if a bug wrote one: 32 bytes or
// more in base64 (43+ characters) or hex (64+).
var blob = regexp.MustCompile(`[A-Za-z0-9+/_-]{43,}={0,2}|[0-9a-fA-F]{64,}`)

// Bible chapter 13 and WP-5.5: a PIN pairing between two daemons, with a wrong attempt first,
// leaves neither the PIN nor any key material in the journal or in the events. Only
// pair_pin_shown carries the PIN, to the client that asked for it, and it is not journaled.
func TestPINPairingLeavesNoSecret(t *testing.T) {
	journalDir := t.TempDir()
	j, err := diag.OpenJournal(journalDir, diag.JournalBudget, nil)
	if err != nil {
		t.Fatal(err)
	}
	SetJournal(j)
	log.SetOutput(io.MultiWriter(os.Stderr, j))
	t.Cleanup(func() { SetJournal(nil); log.SetOutput(os.Stderr) })

	a, na := startOwnIdentity(t, "Shower")
	b, nb := startOwnIdentity(t, "Typist")
	na.Request("pair_pin_show", nil)
	var shown pinShownEvent
	if err := na.WaitEvent("pair_pin_shown", 0).Decode(&shown); err != nil {
		t.Fatal(err)
	}
	code := strings.ReplaceAll(shown.PIN, " ", "")
	bad := []byte(code)
	bad[0] = '0' + (bad[0]-'0'+1)%10
	nb.Request("pair_pin", map[string]any{"target": na.PeerAddr(), "pin": string(bad)})
	nb.WaitEvent("pair_failed", 0)
	nb.Request("pair_pin", map[string]any{"target": na.PeerAddr(), "pin": shown.PIN})
	nb.WaitEvent("paired", 0)
	na.WaitEvent("paired", 0)

	// A careless log line, like a future bug could add: the redactor must still catch it.
	log.Printf("debug: pair pin=%s, typed %s", code, shown.PIN)
	time.Sleep(50 * time.Millisecond) // let the last broadcast reach the journal
	_ = j.Close()

	public := map[string]bool{}
	for _, s := range []*DaemonServer{a, b} {
		public[s.identity.Fingerprint.String()] = true
	}
	pinForms := regexp.MustCompile(`(^|\D)(` + regexp.QuoteMeta(code) + `|` + regexp.QuoteMeta(shown.PIN) + `)(\D|$)`)
	check := func(where, text string) {
		t.Helper()
		if pinForms.MatchString(text) {
			t.Errorf("%s holds the PIN", where)
		}
		for _, m := range blob.FindAllString(text, -1) {
			if _, err := os.Stat(m); err == nil {
				continue // a folder of the test (status's receive folder: "/tmp/Test.../003" matches base64)
			}
			if !public[strings.TrimRight(m, "=")] && !strings.HasPrefix(m, "iVBORw0KGgo") { // a PNG: status's QR code images
				t.Errorf("%s holds a key-sized blob %q", where, m)
			}
		}
	}

	var journalText string
	files, _ := filepath.Glob(filepath.Join(journalDir, "*"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		journalText += string(data)
	}
	check("the journal", journalText)
	if strings.Contains(journalText, "pair_pin_shown") {
		t.Error("pair_pin_shown reached the journal")
	}
	for _, want := range []string{"event pair_failed", "event paired", "debug: pair pin="} {
		if !strings.Contains(journalText, want) {
			t.Errorf("journal lacks %q:\n%s", want, journalText)
		}
	}

	for name, n := range map[string]*testkit.Node{"shower": na, "typist": nb} {
		for _, e := range n.Client.Events() {
			if e.Event == "pair_pin_shown" {
				if name != "shower" {
					t.Errorf("the %s's client got pair_pin_shown", name)
				}
				continue
			}
			check(name+" event "+e.Event, string(e.Data))
		}
	}
}
