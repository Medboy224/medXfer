//go:build devui

package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func upload(t *testing.T, s *DaemonServer, name string) int {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormField(name) // no file name: the handler takes the field's
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("content"))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	s.handleUpload(w, req)
	return w.Code
}

func stages(t *testing.T) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(stagingRoot(), "stage_*"))
	return m
}

// The dashboard's copies of picked files do not pile up (13 GB found on a phone): removed when
// the daemon stops, at once when the upload fails, after a day when a daemon was killed. A
// name that climbs out of the staging folder is refused.
func TestStagingIsCleanedUp(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	s := NewDaemonServer(0, t.TempDir(), "StageTest")

	old := filepath.Join(stagingRoot(), "stage_1")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-stagingMaxAge - time.Hour)
	_ = os.Chtimes(old, past, past)

	if code := upload(t, s, "../escape.txt"); code != http.StatusBadRequest {
		t.Fatalf("../escape.txt: %d", code)
	}
	if _, err := os.Stat(filepath.Join(stagingRoot(), "escape.txt")); err == nil {
		t.Fatal("written outside its stage folder")
	}
	if got := stages(t); len(got) != 0 {
		t.Fatalf("after a refused upload and a stale one: %v", got)
	}

	if code := upload(t, s, "video.mp4"); code != http.StatusOK {
		t.Fatalf("upload: %d", code)
	}
	if got := stages(t); len(got) != 1 {
		t.Fatalf("stages %v, want the upload's", got)
	}
	s.Stop()
	if got := stages(t); len(got) != 0 {
		t.Fatalf("left after Stop: %v", got)
	}
}
