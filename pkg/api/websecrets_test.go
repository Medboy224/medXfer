package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRandomPIN(t *testing.T) {
	firstDigits := make(map[byte]bool)
	prev, increasing, constant := "", true, true
	for i := 0; i < 10000; i++ {
		pin, err := randomPIN(6)
		if err != nil {
			t.Fatal(err)
		}
		if len(pin) != 6 || strings.Trim(pin, "0123456789") != "" {
			t.Fatalf("randomPIN(6) = %q; want 6 digits", pin)
		}
		if prev != "" {
			constant = constant && pin == prev
			increasing = increasing && pin > prev
		}
		prev = pin
		firstDigits[pin[0]] = true
	}
	if constant || increasing {
		t.Fatalf("PIN sequence is predictable (constant=%v, increasing=%v)", constant, increasing)
	}
	// The old generator never produced a leading 0: all ten digits must appear.
	if len(firstDigits) != 10 {
		t.Fatalf("first digits seen: %d of 10", len(firstDigits))
	}
	if pin, _ := randomPIN(4); len(pin) != 4 {
		t.Fatalf("randomPIN(4) = %q", pin)
	}
}

// WEB-04: no PIN (attempted or current) and no token in the logs (bible ch. 13).
func TestNoSecretsInLogs(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	server := NewDaemonServer(0, t.TempDir(), "LogHost")
	server.SetWebShareEnabled(true)
	secrets := []string{server.webSharePIN, server.webShareToken}

	attempted := []string{"314159", "271828", "161803", "141421", "173205", "223606"}
	for _, pin := range attempted {
		req := httptest.NewRequest("GET", "/api/share/list?pin="+pin, nil)
		req.RemoteAddr = "192.168.1.50:4000"
		server.handleShareList(httptest.NewRecorder(), req)
	}
	server.regeneratePINInternal(6)
	secrets = append(secrets, server.webSharePIN, server.webShareToken)

	logs := buf.String()
	if !strings.Contains(logs, "failed attempt") {
		t.Fatalf("expected failure logs, got:\n%s", logs)
	}
	for _, s := range append(secrets, attempted...) {
		if s != "" && strings.Contains(logs, s) {
			t.Fatalf("secret %q found in logs:\n%s", s, logs)
		}
	}
}

func sharedFileServer(t *testing.T, name string, content []byte) (*DaemonServer, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	server := NewDaemonServer(0, t.TempDir(), "ShareHost")
	server.SetWebShareEnabled(true)
	if err := server.SharePaths([]string{p}); err != nil {
		t.Fatal(err)
	}
	return server, server.webShareToken
}

func listItems(t *testing.T, server *DaemonServer, token string) []SharedItem {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/share/list", nil)
	req.Header.Set("X-Share-Token", token)
	w := httptest.NewRecorder()
	server.handleShareList(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/share/list = %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Items []SharedItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Items
}

func TestShareListNoCredentialsInURLs(t *testing.T) {
	server, token := sharedFileServer(t, "doc.txt", []byte("hello"))
	items := listItems(t, server, token)
	if len(items) != 1 {
		t.Fatalf("items = %v", items)
	}
	for _, it := range items {
		if strings.Contains(it.URL, "pin=") || strings.Contains(it.URL, "token=") ||
			strings.Contains(it.URL, token) || strings.Contains(it.URL, server.webSharePIN) {
			t.Fatalf("download URL carries credentials: %s", it.URL)
		}
	}
}

func TestShareListEncodesNames(t *testing.T) {
	content := []byte("special name content")
	server, token := sharedFileServer(t, "a&b#c%d.txt", content)
	items := listItems(t, server, token)
	if len(items) != 1 {
		t.Fatalf("items = %v", items)
	}
	req := httptest.NewRequest("GET", items[0].URL, nil)
	req.Header.Set("X-Share-Token", token)
	w := httptest.NewRecorder()
	server.handleShareDownload(w, req)
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), content) {
		t.Fatalf("download via %s = %d, %q", items[0].URL, w.Code, w.Body.String())
	}
}

func TestShareHeaders(t *testing.T) {
	server, token := sharedFileServer(t, "doc.txt", []byte("hello"))
	ln, err := server.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(ln) }()
	defer server.Stop()
	base := fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)

	for _, path := range []string{"/share", "/api/share/list", "/api/share/download?file=doc.txt"} {
		req, _ := http.NewRequest("GET", base+path, nil)
		req.Header.Set("X-Share-Token", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		for h, want := range map[string]string{
			"Referrer-Policy":        "no-referrer",
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":        "DENY",
		} {
			if got := resp.Header.Get(h); got != want {
				t.Errorf("%s: %s = %q; want %q", path, h, got, want)
			}
		}
		if path != "/api/share/download?file=doc.txt" && resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control = %q; want no-store", path, resp.Header.Get("Cache-Control"))
		}
	}
}

func TestUploadTicketUnguessable(t *testing.T) {
	server := NewDaemonServer(0, t.TempDir(), "TicketHost")
	server.SetWebShareEnabled(true)
	server.webShareAutoAccept = true
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/api/share/request_upload",
			strings.NewReader(`{"files":[{"name":"a.txt","size":3}]}`))
		req.Header.Set("X-Share-Token", server.webShareToken)
		w := httptest.NewRecorder()
		server.handleShareRequestUpload(w, req)
		var body struct {
			Ticket string `json:"ticket"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if !strings.HasPrefix(body.Ticket, "ticket_") || len(body.Ticket) != len("ticket_")+32 {
			t.Fatalf("ticket = %q (%d %s); want ticket_ + 32 hex chars", body.Ticket, w.Code, w.Body.String())
		}
		if seen[body.Ticket] {
			t.Fatalf("ticket reused: %s", body.Ticket)
		}
		seen[body.Ticket] = true
	}
}
