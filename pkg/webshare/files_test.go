package webshare

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// STO-16: Android offers files open already, not paths. They are offered under the name given,
// read by position (two downloads at once never move each other), and closed at the session's
// end; a session that does not start closes them at once.
func TestOpenFilesOffered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "content-1234")
	data := bytes.Repeat([]byte("0123456789"), 100_000)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s, g, _ := startGuest(t, Options{Files: []OpenFile{{File: f, Name: "Vidéo.mp4"}}})
	var items []Item
	if err := json.NewDecoder(g.do("GET", "/api/items", s.Token(), nil).Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "Vidéo.mp4" || items[0].Size != int64(len(data)) {
		t.Fatalf("items %+v", items)
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := g.do("GET", "/api/items/0", s.Token(), nil)
			if got := readAll(t, resp.Body); !bytes.Equal(got, data) {
				t.Errorf("download: %d bytes", len(got))
			}
		}()
	}
	wg.Wait()
	s.Close()
	waitDone(t, s)
	if _, err := f.Stat(); err == nil {
		t.Fatal("the file is still open after the session")
	}

	g2, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Start(Options{Addr: "0.0.0.0", Files: []OpenFile{{File: g2, Name: "x"}}}); err == nil {
		t.Fatal("started on every interface")
	}
	if _, err := g2.Stat(); err == nil {
		t.Fatal("the file is still open after a session that did not start")
	}
}
