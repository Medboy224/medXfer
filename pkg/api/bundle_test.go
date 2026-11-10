package api

import (
	"archive/zip"
	"bytes"
	"io"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/testkit"
)

// Bible chapter 13 and DEV-11: after a complete session, neither the journal nor the
// diagnostic bundle holds a PIN, a token, a pairing code, a file name, a path or the user name.
func TestBundleHoldsNoSecret(t *testing.T) {
	configMu.RLock()
	previous := customConfigDir
	configMu.RUnlock()
	SetCustomConfigDir(t.TempDir())
	t.Cleanup(func() { SetCustomConfigDir(previous) })

	j, err := diag.OpenJournal(JournalDir(), diag.JournalBudget, nil)
	if err != nil {
		t.Fatal(err)
	}
	SetJournal(j)
	log.SetOutput(io.MultiWriter(os.Stderr, j))
	t.Cleanup(func() { SetJournal(nil); log.SetOutput(os.Stderr) })

	// A full session: a transfer whose file name must not leak, then Web Share with a PIN rotation.
	sendDir := t.TempDir()
	folder := filepath.Join(sendDir, "Dossier-medical-confidentiel")
	_ = os.MkdirAll(folder, 0o755)
	secretFile := filepath.Join(folder, "Bulletin-salaire-secret.pdf")
	_ = os.WriteFile(secretFile, bytes.Repeat([]byte("contenu confidentiel "), 2000), 0o644)

	sender := NewDaemonServer(0, sendDir, "SenderNode")
	receiver := NewDaemonServer(0, t.TempDir(), "ReceiverNode")
	pair := testkit.StartPair(t, sender, receiver)
	pair.Send([]string{folder}, testkit.SendOptions{})
	pair.AcceptOffer(0)
	pair.WaitTransferDone(0)

	// A Web Share session (J7), its token and PIN shown to the host only (WEB-04).
	started := pair.Receiver.WaitReply(pair.Receiver.Request("web_share_start", map[string]any{"addr": "127.0.0.1", "port": -1, "receive": true}), 0)
	var share struct{ Token, PIN, URL string }
	if started.Event != "web_share_started" || started.Decode(&share) != nil || share.Token == "" || share.PIN == "" {
		t.Fatalf("web_share_start: %s %s", started.Event, started.Data)
	}
	pair.Receiver.Request("web_share_stop", nil)
	pair.Receiver.WaitEvent("web_share_closed", 0)

	var forbidden []string
	for _, s := range []*DaemonServer{sender, receiver} {
		forbidden = append(forbidden, s.GetStatus().PairingCode)
	}
	forbidden = append(forbidden, share.Token, share.PIN)

	// A careless log line, like a future bug could add: the redactor must still catch it.
	log.Printf("debug: pin=%s token %s url %s file %s", share.PIN, share.Token, share.URL, secretFile)
	time.Sleep(50 * time.Millisecond) // let the last broadcast reach the journal
	_ = j.Close()

	// The application's log, as careless: masked the same way.
	appLog := "ble: started, 70 ms\nhotspot: failed refused (aborted)\ndebug: pin=" + share.PIN +
		" file " + secretFile + "\n"

	var buf bytes.Buffer
	if err := diag.WriteBundle(&buf, diag.BundleOptions{
		ReportsDir: ReportsDir(), JournalDir: JournalDir(), Config: DiagnosticConfig(), AppLog: appLog,
	}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}

	forbidden = append(forbidden, "Bulletin-salaire-secret", "Dossier-medical-confidentiel", sendDir)
	if u, err := user.Current(); err == nil {
		name := u.Username
		if i := strings.LastIndexAny(name, `\/`); i >= 0 {
			name = name[i+1:]
		}
		if len(name) >= 3 {
			forbidden = append(forbidden, name)
		}
	}

	var journalText, appText string
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		text := string(data)
		if strings.HasPrefix(f.Name, "journal/") {
			journalText += text
		}
		if f.Name == "app.log" {
			appText = text
		}
		for _, secret := range forbidden {
			if secret == "" {
				continue
			}
			// Digits-only secrets must stand alone, so a byte count cannot match by chance.
			pattern := regexp.QuoteMeta(secret)
			if regexp.MustCompile(`^\d+$`).MatchString(secret) {
				pattern = `(^|\D)` + pattern + `(\D|$)`
			}
			if regexp.MustCompile("(?i)" + pattern).MatchString(text) {
				t.Errorf("bundle file %s contains the secret %q", f.Name, secret)
			}
		}
	}

	// The bundle is not empty: it holds what a diagnosis needs.
	for _, want := range []string{"manifest.json", "config.json", "journal/journal.log", "app.log"} {
		if !names[want] {
			t.Errorf("bundle lacks %s (has %v)", want, names)
		}
	}
	for _, want := range []string{"event incoming_offer", "event transfer_complete", "debug: pin="} {
		if !strings.Contains(journalText, want) {
			t.Errorf("journal lacks %q:\n%s", want, journalText)
		}
	}
	if !strings.Contains(appText, "hotspot: failed refused") {
		t.Errorf("app.log lacks the network's failure:\n%s", appText)
	}
}
