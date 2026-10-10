package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	r := NewRedactor()
	r.Add("PIN", "482913")
	r.Add("TOKEN", "0123456789abcdef0123456789abcdef")
	r.Add("X", "ab") // too short to mask safely: ignored

	cases := map[string]string{
		"Web Share PIN 482913 ready":                 "Web Share PIN [PIN] ready",
		"tok 0123456789abcdef0123456789abcdef!":      "tok [TOKEN]!",
		`saved to "C:\Users\amadou\Videos\film.mkv"`: `saved to "[PATH]"`,
		"open /home/amadou/Documents/cv.pdf failed":  "open [PATH] failed",
		"Termux /data/data/com.termux/files/home/x":  "Termux [PATH]",
		"Authorization: Bearer abc.def":              "Authorization: [TOKEN]",
		"GET /api/share/list?pin=1234&token=zz":      "GET /api/share/list?pin=[REDACTED]&token=[REDACTED]",
		"subprotocol token.deadbeef01":               "subprotocol token.[TOKEN]",
		"file id 7016a9737cb180df239bdc0ab18f627a":   "file id [HEX]",
		"pairing 123-456 ok":                         "pairing [CODE] ok",
		"crab speed 42.5 MB/s, 3 retries":            "crab speed 42.5 MB/s, 3 retries",
	}
	for in, want := range cases {
		if got := r.Redact(in); got != want {
			t.Errorf("Redact(%q)\n  = %q\nwant %q", in, got, want)
		}
	}
}

func TestRedactIdentity(t *testing.T) {
	ids := identity()
	if len(ids) == 0 {
		t.Skip("no user or host name on this machine")
	}
	got := NewRedactor().Redact("user " + strings.ToUpper(ids[0]) + " here")
	if strings.Contains(strings.ToLower(got), strings.ToLower(ids[0])) {
		t.Fatalf("identity %q not masked: %q", ids[0], got)
	}
}

func TestRedactorKeepsNewestSecrets(t *testing.T) {
	r := NewRedactor()
	for i := 0; i < maxRedactorSecrets+10; i++ {
		r.Add("PIN", strings.Repeat("z", 4)+string(rune('A'+i%26))+strings.Repeat("y", i/26))
	}
	if len(r.order) != maxRedactorSecrets || len(r.secrets) != maxRedactorSecrets {
		t.Fatalf("redactor holds %d/%d secrets; want %d", len(r.order), len(r.secrets), maxRedactorSecrets)
	}
}

// The journal masks what it writes, completes split lines, and never exceeds its budget.
func TestJournal(t *testing.T) {
	dir := t.TempDir()
	r := NewRedactor()
	r.Add("PIN", "482913")
	const budget = 8 * 1024
	j, err := OpenJournal(dir, budget, r)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = j.Write([]byte("half a line with PIN 4829"))
	_, _ = j.Write([]byte("13 and its end\n"))
	j.Event("transfer_complete")
	for i := 0; i < 500; i++ {
		j.Printf("filler line %d with some text to pass the budget", i)
	}
	j.Printf("last line")
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}

	var total int64
	var all string
	for _, p := range JournalFiles(dir) {
		st, _ := os.Stat(p)
		total += st.Size()
		data, _ := os.ReadFile(p)
		all += string(data)
	}
	if total > budget {
		t.Fatalf("journal uses %d bytes; budget %d", total, budget)
	}
	if !strings.Contains(all, "last line") || !strings.Contains(all, "filler line 499") {
		t.Fatal("the newest lines are missing")
	}
	if strings.Contains(all, "482913") {
		t.Fatal("a PIN split across two writes was not masked")
	}
	if files := JournalFiles(dir); len(files) != 2 || filepath.Base(files[0]) != "journal.log.1" {
		t.Fatalf("journal files %v; want journal.log.1 then journal.log", files)
	}

	// Reopening continues the same journal.
	j2, err := OpenJournal(dir, budget, r)
	if err != nil {
		t.Fatal(err)
	}
	j2.Printf("after restart")
	_ = j2.Close()
	data, _ := os.ReadFile(filepath.Join(dir, "journal.log"))
	if !strings.Contains(string(data), "last line") || !strings.Contains(string(data), "after restart") {
		t.Fatal("reopening the journal lost lines")
	}
}
