package webshare

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func (g *guest) json(method, path string, body any) (int, map[string]any) {
	g.t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	resp := g.do(method, path, g.s.Token(), r)
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func (g *guest) announce(files ...UploadFile) (int, map[string]any) {
	g.t.Helper()
	return g.json("POST", "/api/uploads", map[string]any{"files": files})
}

func (g *guest) chunk(ticket string, index int, offset int64, data []byte) (int, map[string]any) {
	g.t.Helper()
	resp := g.do("PUT", fmt.Sprintf("/api/uploads/%s/%d?offset=%d", ticket, index, offset), g.s.Token(), bytes.NewReader(data))
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// WEB-06, WEB-07, #157 (2a, 2b, 2c): the guest announces its files, the host accepts, each chunk
// is checked against the announce, a name taken gets another, the ticket lives until the last
// file, and no part file stays.
func TestUploadAccepted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("the host's own"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, g, rec := startGuest(t, Options{ReceiveDir: dir})
	code, m := g.announce(UploadFile{"a.txt", 10}, UploadFile{"sous/dossier/b.bin", 20}, UploadFile{"vide.txt", 0})
	if code != http.StatusCreated || m["state"] != upPending || m["chunk"] != float64(ChunkSize) {
		t.Fatalf("announce: %d %v", code, m)
	}
	ticket := m["ticket"].(string)
	if !strings.Contains(rec.text(), "upload_request map[auto:false files:[{a.txt 10} {b.bin 20} {vide.txt 0}]") {
		t.Fatalf("the host's request:\n%s", rec.text())
	}
	if code, m := g.chunk(ticket, 0, 0, []byte("0123456789")); code != http.StatusConflict || m["error"] != "not_accepted" {
		t.Fatalf("before the host's answer: %d %v", code, m)
	}

	if err := s.Answer(ticket, true); err != nil {
		t.Fatal(err)
	}
	if code, m := g.chunk(ticket, 0, 0, []byte("0123456789")); code != http.StatusOK || m["received"] != float64(10) {
		t.Fatalf("a.txt: %d %v", code, m)
	}
	if code, m := g.chunk(ticket, 1, 5, []byte("x")); code != http.StatusConflict || m["error"] != "offset" || m["received"] != float64(0) {
		t.Fatalf("a chunk out of place: %d %v", code, m)
	}
	if code, m := g.chunk(ticket, 9, 0, []byte("x")); code != http.StatusNotFound {
		t.Fatalf("a file not announced: %d %v", code, m)
	}
	if code, m := g.chunk(ticket, 1, 0, bytes.Repeat([]byte("b"), 8)); code != http.StatusOK || m["received"] != float64(8) {
		t.Fatalf("b.bin, first chunk: %d %v", code, m)
	}
	if code, m := g.json("GET", "/api/uploads/"+ticket, nil); code != http.StatusOK || m["state"] != upAccepted {
		t.Fatalf("state: %d %v", code, m)
	}
	if code, m := g.chunk(ticket, 1, 8, bytes.Repeat([]byte("b"), 12)); code != http.StatusOK || m["received"] != float64(20) {
		t.Fatalf("b.bin, last chunk: %d %v", code, m)
	}
	if code, m := g.json("GET", "/api/uploads/"+ticket, nil); code != http.StatusOK || m["state"] != upDone {
		t.Fatalf("state at the end: %d %v", code, m)
	}

	if got := names(t, dir); strings.Join(got, ",") != "a (1).txt,a.txt,b.bin,vide.txt" {
		t.Fatalf("files %v", got)
	}
	if own, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(own) != "the host's own" {
		t.Fatalf("the host's file overwritten: %q", own)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a (1).txt")); string(got) != "0123456789" {
		t.Fatalf("a (1).txt: %q", got)
	}
	kinds := strings.Join(rec.kinds(), ",")
	if strings.Count(kinds, "upload_file_done") != 3 || !strings.HasSuffix(kinds, "upload_done") {
		t.Fatalf("events %s", kinds)
	}
	// Free again: another upload may come.
	if code, _ := g.announce(UploadFile{"c.txt", 1}); code != http.StatusCreated {
		t.Fatalf("the next upload: %d", code)
	}
}

// WEB-06: refused, the guest is told and the quota is given back.
func TestUploadRefused(t *testing.T) {
	dir := t.TempDir()
	s, g, rec := startGuest(t, Options{ReceiveDir: dir, MaxBytes: 100})
	_, m := g.announce(UploadFile{"a.txt", 60})
	ticket := m["ticket"].(string)
	if err := s.Answer(ticket, false); err != nil {
		t.Fatal(err)
	}
	if _, m := g.json("GET", "/api/uploads/"+ticket, nil); m["state"] != upRefused {
		t.Fatalf("state %v", m)
	}
	if code, m := g.chunk(ticket, 0, 0, []byte("x")); code != http.StatusConflict || m["state"] != upRefused {
		t.Fatalf("a chunk after the refusal: %d %v", code, m)
	}
	if _, m := g.json("GET", "/api/session", nil); m["bytes_left"] != float64(100) || m["receive"] != true {
		t.Fatalf("session %v", m)
	}
	if err := s.Answer(ticket, true); err == nil {
		t.Fatal("answered twice")
	}
	if !strings.Contains(strings.Join(rec.kinds(), ","), "upload_refused") || len(names(t, dir)) != 0 {
		t.Fatalf("events %v, files %v", rec.kinds(), names(t, dir))
	}
}

// WEB-06, WEB-07: one upload at a time, within the quotas, under safe names; none without a
// receiving folder.
func TestUploadRules(t *testing.T) {
	dir := t.TempDir()
	s, g, _ := startGuest(t, Options{ReceiveDir: dir, MaxBytes: 100, MaxFiles: 2})
	for _, bad := range []string{"", "../x.txt", "a/../../x.txt", "/etc/passwd"} {
		if code, m := g.announce(UploadFile{bad, 1}); code != http.StatusBadRequest {
			t.Errorf("name %q: %d %v", bad, code, m)
		}
	}
	if code, _ := g.announce(UploadFile{"a", -1}); code != http.StatusBadRequest {
		t.Errorf("a negative size: %d", code)
	}
	if code, m := g.announce(); code != http.StatusBadRequest || m["error"] != "no_files" {
		t.Errorf("nothing announced: %d %v", code, m)
	}
	if code, m := g.announce(UploadFile{"a", 101}); code != http.StatusRequestEntityTooLarge || m["error"] != "quota" {
		t.Errorf("over MaxBytes: %d %v", code, m)
	}
	if code, m := g.announce(UploadFile{"a", 1}, UploadFile{"b", 1}, UploadFile{"c", 1}); code != http.StatusRequestEntityTooLarge || m["error"] != "quota" {
		t.Errorf("over MaxFiles: %d %v", code, m)
	}
	if code, _ := g.announce(UploadFile{"a", 1}); code != http.StatusCreated {
		t.Fatalf("within the quotas: %d", code)
	}
	if code, m := g.announce(UploadFile{"b", 1}); code != http.StatusConflict || m["error"] != "busy" {
		t.Errorf("a second at once: %d %v", code, m)
	}
	_ = s

	_, g2, _ := startGuest(t, Options{})
	if code, m := g2.announce(UploadFile{"a", 1}); code != http.StatusForbidden || m["error"] != "uploads_off" {
		t.Errorf("no receiving folder: %d %v", code, m)
	}
}

// #157 (2b): a chunk past the size announced cancels the upload; nothing incomplete stays.
func TestUploadTooBig(t *testing.T) {
	dir := t.TempDir()
	_, g, rec := startGuest(t, Options{ReceiveDir: dir, AutoAccept: true})
	_, m := g.announce(UploadFile{"a.txt", 4})
	if m["state"] != upAccepted {
		t.Fatalf("auto-accepted: %v", m)
	}
	ticket := m["ticket"].(string)
	if code, m := g.chunk(ticket, 0, 0, []byte("12345")); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("one byte too many: %d %v", code, m)
	}
	if got := names(t, dir); len(got) != 0 {
		t.Fatalf("files left %v", got)
	}
	if !strings.Contains(strings.Join(rec.kinds(), ","), "upload_cancelled") {
		t.Fatalf("events %v", rec.kinds())
	}
}

// The guest gives up, or the session ends: the incomplete files go, the complete ones stay.
func TestUploadCancelled(t *testing.T) {
	dir := t.TempDir()
	s, g, rec := startGuest(t, Options{ReceiveDir: dir, AutoAccept: true})
	_, m := g.announce(UploadFile{"done.txt", 2}, UploadFile{"half.txt", 4})
	ticket := m["ticket"].(string)
	g.chunk(ticket, 0, 0, []byte("ok"))
	g.chunk(ticket, 1, 0, []byte("ha"))
	if resp := g.do("DELETE", "/api/uploads/"+ticket, s.Token(), nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel: %d", resp.StatusCode)
	}
	if got := names(t, dir); strings.Join(got, ",") != "done.txt" {
		t.Fatalf("files %v", got)
	}
	if !strings.Contains(strings.Join(rec.kinds(), ","), "upload_cancelled") {
		t.Fatalf("events %v", rec.kinds())
	}

	_, m = g.announce(UploadFile{"late.txt", 4})
	g.chunk(m["ticket"].(string), 0, 0, []byte("la"))
	s.Close()
	waitDone(t, s)
	if got := names(t, dir); strings.Join(got, ",") != "done.txt" {
		t.Fatalf("after the session's end: %v", got)
	}
}

// WEB-10: an upload waiting for the host, or coming, keeps the session; its end lets it expire.
func TestUploadKeepsSession(t *testing.T) {
	dir := t.TempDir()
	s, g, _ := startGuest(t, Options{ReceiveDir: dir, TTL: 150 * time.Millisecond})
	s.mu.Lock()
	s.recheck = 50 * time.Millisecond
	s.mu.Unlock()
	_, m := g.announce(UploadFile{"a.txt", 1})
	time.Sleep(400 * time.Millisecond)
	if s.Closed() != "" {
		t.Fatal("expired while the host decides")
	}
	if err := s.Answer(m["ticket"].(string), false); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s)
	if s.Closed() != ClosedExpired {
		t.Fatalf("closed %q", s.Closed())
	}
}
