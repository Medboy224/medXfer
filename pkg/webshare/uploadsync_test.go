package webshare

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// #313, as pkg/store does (STO-01): a file the guest sent is made durable before it gets its
// name, so that after a power loss a file shown as received is never empty.
func TestUploadSyncedBeforeItsName(t *testing.T) {
	dir := t.TempDir()
	var synced []string
	saved := syncFile
	syncFile = func(f *os.File) error {
		if _, err := os.Stat(filepath.Join(dir, "a.txt")); err == nil {
			t.Error("synced after the file got its name")
		}
		synced = append(synced, f.Name())
		return saved(f)
	}
	t.Cleanup(func() { syncFile = saved })

	s, g, _ := startGuest(t, Options{ReceiveDir: dir})
	code, m := g.announce(UploadFile{"a.txt", 10})
	if code != http.StatusCreated {
		t.Fatalf("announce: %d %v", code, m)
	}
	ticket := m["ticket"].(string)
	if err := s.Answer(ticket, true); err != nil {
		t.Fatal(err)
	}
	if code, m := g.chunk(ticket, 0, 0, []byte("0123456789")); code != http.StatusOK {
		t.Fatalf("chunk: %d %v", code, m)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(got) != "0123456789" {
		t.Fatalf("a.txt: %q", got)
	}
	if len(synced) != 1 {
		t.Fatalf("%d fsyncs for the file received; want one, before its rename", len(synced))
	}
}
