//go:build devui

package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #137: files to send are passed by path, never copied. There is no upload route any more, even
// with the control token, and the copies older dashboards left (13 GB on a phone) are removed
// when the dashboard starts.
func TestDashboardNeverCopiesFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	left := filepath.Join(legacyStaging(), "stage_1", "video.mp4")
	if err := os.MkdirAll(filepath.Dir(left), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(left, []byte("copy"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, addr := startGuardServer(t, false)
	good := map[string]string{"Authorization": "Bearer " + s.ControlToken()}
	if code, _ := guardDo(t, "POST", "http://"+addr+"/api/upload", good); code != http.StatusNotFound {
		t.Fatalf("POST /api/upload = %d; want 404: nothing is copied", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(legacyStaging()); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the old copies are still there")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
