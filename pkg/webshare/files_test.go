package webshare

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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

// Files added to a running session (real test of 10 November 2026: the host had no way to add
// any once started): listed after the others, under the next IDs, downloaded by them; the open
// ones closed at the end, or at once when the session ended already.
func TestAddDuringSession(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "premier.txt")
	_ = os.WriteFile(first, []byte("un"), 0o644)
	s, g, _ := startGuest(t, Options{Paths: []string{first}})
	later := filepath.Join(dir, "ajout.txt")
	_ = os.WriteFile(later, []byte("deux"), 0o644)
	open, err := os.Open(later)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add([]string{later}, []OpenFile{{File: open, Name: "ouvert.txt"}}); err != nil {
		t.Fatal(err)
	}
	var items []Item
	if err := json.NewDecoder(g.do("GET", "/api/items", s.Token(), nil).Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.ID+":"+it.Name)
	}
	if len(got) != 3 || got[0] != "0:premier.txt" || got[1] != "1:ajout.txt" || got[2] != "2:ouvert.txt" {
		t.Fatalf("items %v", got)
	}
	if body := readAll(t, g.do("GET", "/api/items/2", s.Token(), nil).Body); string(body) != "deux" {
		t.Fatalf("the open file added: %q", body)
	}
	if err := s.Add([]string{filepath.Join(dir, "absent")}, nil); err == nil {
		t.Fatal("a missing path added")
	}
	s.Close()
	waitDone(t, s)
	if _, err := open.Stat(); err == nil {
		t.Fatal("the open file added is still open after the session")
	}
	late, _ := os.Open(later)
	if err := s.Add(nil, []OpenFile{{File: late, Name: "tard.txt"}}); err == nil {
		t.Fatal("added to an ended session")
	}
	if _, err := late.Stat(); err == nil {
		t.Fatal("a file refused stays open")
	}
}

// The host withdraws an item (real test of 10 November 2026: the selection could not be
// changed): gone from the guest's list, its download and its link refused, its open file closed;
// the next one added never takes its ID.
func TestRemoveDuringSession(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	_ = os.WriteFile(a, []byte("a"), 0o644)
	f, err := os.Open(a)
	if err != nil {
		t.Fatal(err)
	}
	s, g, _ := startGuest(t, Options{Paths: []string{a}, Files: []OpenFile{{File: f, Name: "ouvert.txt"}}})
	var link map[string]string
	_ = json.NewDecoder(g.do("POST", "/api/items/1/link", s.Token(), nil).Body).Decode(&link)
	if err := s.Remove("1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Stat(); err == nil {
		t.Fatal("the file withdrawn is still open")
	}
	if resp := g.do("GET", "/api/items/1", s.Token(), nil); resp.StatusCode != 404 {
		t.Fatalf("download of an item withdrawn: %d", resp.StatusCode)
	}
	if resp := g.do("GET", link["url"], "", nil); resp.StatusCode != 404 {
		t.Fatalf("its link: %d", resp.StatusCode)
	}
	if err := s.Remove("1"); err == nil {
		t.Fatal("withdrawn twice")
	}
	if err := s.Add([]string{a}, nil); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range s.Items() {
		ids = append(ids, it.ID)
	}
	if len(ids) != 2 || ids[0] != "0" || ids[1] != "2" {
		t.Fatalf("IDs %v: an ID given again", ids)
	}
}

// A download the guest gave up is not done (real test of 10 November 2026: 7 MB of 1 GiB, and the
// host was told it was done); one that went to the end is, a range too.
func TestDownloadDoneOnlyWhenComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gros.bin")
	if err := os.WriteFile(path, bytes.Repeat([]byte{7}, 32<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	s, g, rec := startGuest(t, Options{Paths: []string{path}})
	done := func() int {
		n := 0
		for _, k := range rec.kinds() {
			if k == "download_done" {
				n++
			}
		}
		return n
	}
	idle := func() {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			s.mu.Lock()
			busy := s.busy
			s.mu.Unlock()
			if busy == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("the download did not end")
			}
		}
	}

	resp := g.do("GET", "/api/items/0", s.Token(), nil)
	if _, err := io.ReadFull(resp.Body, make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() // the guest leaves
	idle()
	if n := done(); n != 0 {
		t.Fatalf("a download given up counted as done: %d", n)
	}

	if got := readAll(t, g.do("GET", "/api/items/0", s.Token(), nil).Body); len(got) != 32<<20 {
		t.Fatalf("download: %d bytes", len(got))
	}
	part := g.do("GET", "/api/items/0", s.Token(), nil, "Range", "bytes=100-199")
	if got := readAll(t, part.Body); part.StatusCode != http.StatusPartialContent || len(got) != 100 {
		t.Fatalf("range: %d, %d bytes", part.StatusCode, len(got))
	}
	idle()
	if n := done(); n != 2 {
		t.Fatalf("downloads done: %d, want 2", n)
	}
}
