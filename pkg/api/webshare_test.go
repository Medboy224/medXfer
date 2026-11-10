package api

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/testkit"
)

// WP-W.4 of the J7 plan: the host's interface starts the guest session, sees the guest's
// upload, answers it, and stops the session; a second start replaces the first.
func TestWebShareActions(t *testing.T) {
	recv := t.TempDir()
	shared := filepath.Join(t.TempDir(), "offert.txt")
	if err := os.WriteFile(shared, []byte("bonjour"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewDaemonServerAt(t.TempDir(), 0, recv, "Hôte")
	n := testkit.StartNode(t, s)

	start := func(replacing bool) (struct{ URL, Token, PIN, Fingerprint string }, testkit.Event) {
		t.Helper()
		id := n.Request("web_share_start", map[string]any{"paths": []string{shared}, "receive": true, "addr": "127.0.0.1", "port": -1})
		if replacing { // the session running ends first
			if closed := n.WaitEvent("web_share_closed", 0); !strings.Contains(string(closed.Data), `"stopped"`) {
				t.Fatalf("the first session: %s", closed.Data)
			}
		}
		e := n.WaitReply(id, 0)
		var got struct{ URL, Token, PIN, Fingerprint string }
		if e.Event != "web_share_started" || e.Decode(&got) != nil {
			t.Fatalf("web_share_start: %s %s", e.Event, e.Data)
		}
		return got, e
	}
	first, _ := start(false)
	second, e := start(true)
	if second.Token == first.Token || !strings.HasSuffix(second.URL, "#t="+second.Token) || len(second.PIN) != 6 || len(second.Fingerprint) != 95 {
		t.Fatalf("second session %s", e.Data)
	}

	// The guest: it trusts any certificate here; pkg/webshare tests the certificate itself.
	u, err := url.Parse(second.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	do := func(method, path string, body []byte) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, "https://"+u.Host+path, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+second.Token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	if resp := do("GET", "/api/items", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("items: %d", resp.StatusCode)
	}
	resp := do("POST", "/api/uploads", []byte(`{"files":[{"name":"photo.jpg","size":5}]}`))
	var up struct{ Ticket string }
	_ = json.NewDecoder(resp.Body).Decode(&up)
	if resp.StatusCode != http.StatusCreated || up.Ticket == "" {
		t.Fatalf("upload: %d", resp.StatusCode)
	}
	if req := n.WaitEvent("web_share_upload_request", 0); !strings.Contains(string(req.Data), "photo.jpg") {
		t.Fatalf("the host's request: %s", req.Data)
	}
	n.Request("web_share_answer", map[string]any{"ticket": up.Ticket, "accept": true})
	n.WaitEvent("web_share_upload_accepted", 0)
	if resp := do("PUT", "/api/uploads/"+up.Ticket+"/0?offset=0", []byte("12345")); resp.StatusCode != http.StatusOK {
		t.Fatalf("chunk: %d", resp.StatusCode)
	}
	n.WaitEvent("web_share_upload_done", 0)
	if got, err := os.ReadFile(filepath.Join(recv, "photo.jpg")); err != nil || string(got) != "12345" {
		t.Fatalf("received %q %v", got, err)
	}

	n.Request("web_share_stop", nil)
	if closed := n.WaitEvent("web_share_closed", 0); !strings.Contains(string(closed.Data), `"stopped"`) {
		t.Fatalf("stopped: %s", closed.Data)
	}
	if e := n.WaitReply(n.Request("web_share_answer", map[string]any{"ticket": up.Ticket, "accept": true}), 0); e.Event != "action_error" {
		t.Fatalf("an answer without a session: %s", e.Event)
	}
	if e := n.WaitReply(n.Request("web_share_start", map[string]any{"addr": "0.0.0.0"}), 0); e.Event != "action_error" {
		t.Fatalf("every interface at once: %s", e.Event)
	}
}
