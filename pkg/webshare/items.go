package webshare

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Item is a file or a folder the host offers. The guest names it by ID only, never by a name
// it supplies (WEB-07).
type Item struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Folder bool   `json:"folder"`
	path   string
	file   *os.File // an open file offered (Options.Files): read by position, never moved
}

// OpenFile is a file offered open already, and the name the guest sees.
type OpenFile struct {
	File *os.File
	Name string
}

func closeFiles(files []OpenFile) {
	for _, f := range files {
		if f.File != nil {
			_ = f.File.Close()
		}
	}
}

// itemsOf lists the paths and the open files offered, each with its ID: its place in the
// session's list, from first on. A folder's size is that of its regular files.
func itemsOf(first int, paths []string, files []OpenFile) ([]Item, error) {
	items := make([]Item, 0, len(paths)+len(files))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("webshare: %w", err)
		}
		it := Item{ID: strconv.Itoa(first + len(items)), Name: filepath.Base(abs), path: abs, Folder: fi.IsDir()}
		if it.Folder {
			_ = filepath.WalkDir(abs, func(_ string, d fs.DirEntry, err error) error {
				if err == nil && d.Type().IsRegular() {
					if info, err := d.Info(); err == nil {
						it.Size += info.Size()
					}
				}
				return nil
			})
		} else {
			it.Size = fi.Size()
		}
		items = append(items, it)
	}
	for _, f := range files {
		if f.File == nil {
			return nil, fmt.Errorf("webshare: no file for %q", f.Name)
		}
		fi, err := f.File.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("webshare: %q is not a regular file", f.Name)
		}
		name := filepath.Base(f.Name)
		if f.Name == "" || name == "." || name == string(filepath.Separator) {
			name = "file"
		}
		items = append(items, Item{ID: strconv.Itoa(first + len(items)), Name: name, Size: fi.Size(), file: f.File})
	}
	return items, nil
}

// LinkTTL is how long a download link lasts.
const LinkTTL = 10 * time.Minute

// link is a download link: a browser saves a file through an address, which carries no
// Authorization header, and a file read whole by the page's script would not fit in a phone's
// memory. So the page asks, with the token, for a link to one item: a key of its own, 128
// bits, never the session's token (WEB-04), valid LinkTTL, used again to resume, gone with the
// session.
type link struct {
	item    string
	expires time.Time
}

// handleLink: POST /api/items/{id}/link.
func (s *Session) handleLink(w http.ResponseWriter, r *http.Request) {
	it, ok := s.item(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	key, err := randomToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.mu.Lock()
	now := s.now()
	for k, l := range s.links {
		if now.After(l.expires) {
			delete(s.links, k)
		}
	}
	s.links[key] = link{item: it.ID, expires: now.Add(LinkTTL)}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"url": "/dl/" + key})
}

// handleDownload: GET /dl/{key}, the link's item.
func (s *Session) handleDownload(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	l, ok := s.links[r.PathValue("key")]
	expired := s.now().After(l.expires)
	s.mu.Unlock()
	if !ok || expired {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	it, ok := s.item(l.item)
	if !ok { // withdrawn by the host since
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	s.serveItem(w, r, it)
}

// Add offers more, the session running: the guest's page lists them at its next look. The open
// files are the session's from the call on, closed with it, or at once if they cannot be added.
func (s *Session) Add(paths []string, files []OpenFile) error {
	// One addition at a time, its folders walked outside mu: the guest's requests go on meanwhile.
	s.adding.Lock()
	defer s.adding.Unlock()
	s.mu.Lock()
	first, closed := s.nextID, s.closed != ""
	s.mu.Unlock()
	if closed {
		closeFiles(files)
		return fmt.Errorf("webshare: session closed")
	}
	items, err := itemsOf(first, paths, files)
	if err != nil {
		closeFiles(files)
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed != "" { // ended meanwhile: its files are closed already
		closeFiles(files)
		return fmt.Errorf("webshare: session closed")
	}
	s.items = append(s.items, items...)
	s.files = append(s.files, files...)
	s.nextID += len(items)
	return nil
}

// Remove withdraws an item: gone from the guest's list, its downloads refused from now on, its
// file closed when the session held it open. An ID is never given again.
func (s *Session) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, it := range s.items {
		if it.ID != id {
			continue
		}
		s.items = append(s.items[:i:i], s.items[i+1:]...)
		if it.file != nil {
			for j, f := range s.files {
				if f.File == it.file {
					s.files = append(s.files[:j:j], s.files[j+1:]...)
					break
				}
			}
			_ = it.file.Close()
		}
		return nil
	}
	return fmt.Errorf("webshare: no item %q", id)
}

// Items is what the session offers.
func (s *Session) Items() []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Item(nil), s.items...)
}

func (s *Session) item(id string) (Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.items {
		if it.ID == id {
			return it, true
		}
	}
	return Item{}, false
}

// serveItem sends a file, ranges included (a download resumed), or a folder as a zip made on
// the fly, stored without compression: the network, not the processor, is the limit.
func (s *Session) serveItem(w http.ResponseWriter, r *http.Request, it Item) {
	s.begin()
	defer s.finish()
	s.emit(Event{Kind: "download_started", Data: map[string]any{"id": it.ID, "name": it.Name}})
	if !it.Folder {
		f := it.file
		if f == nil {
			opened, err := os.Open(it.path)
			if err != nil {
				writeError(w, http.StatusNotFound, "not_found")
				return
			}
			defer opened.Close()
			f = opened
		}
		fi, err := f.Stat()
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		w.Header().Set("Content-Disposition", attachment(it.Name))
		// By position: two downloads of an open file at once never move each other.
		sw := &sentWriter{ResponseWriter: w}
		http.ServeContent(sw, r, it.Name, fi.ModTime(), io.NewSectionReader(f, 0, fi.Size()))
		if sw.complete(r) {
			s.emit(Event{Kind: "download_done", Data: map[string]any{"id": it.ID, "name": it.Name}})
		}
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", attachment(it.Name+".zip"))
	zw := zip.NewWriter(w)
	err := filepath.WalkDir(it.path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil // unreadable, or not a regular file (a link among others): left out
		}
		rel, err := filepath.Rel(filepath.Dir(it.path), p)
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return nil
		}
		h.Name = filepath.ToSlash(rel)
		h.Method = zip.Store
		fw, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		_, err = io.Copy(fw, f)
		return err // the guest left: stop
	})
	if err == nil && zw.Close() == nil {
		s.emit(Event{Kind: "download_done", Data: map[string]any{"id": it.ID, "name": it.Name}})
	}
}

// sentWriter counts what reaches the guest. A download is done once all of it went: real test
// of 10 November 2026, the guest's browser gave up after 7 MB of 1 GiB, and the host was told
// the download was done.
type sentWriter struct {
	http.ResponseWriter
	status int
	sent   int64
}

func (w *sentWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *sentWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.sent += int64(n)
	return n, err
}

// complete: the whole body announced went, for a GET answered 200 or 206 (a range: a download
// resumed, or one of the parts a browser fetches at once).
func (w *sentWriter) complete(r *http.Request) bool {
	if r.Method != http.MethodGet || (w.status != http.StatusOK && w.status != http.StatusPartialContent) {
		return false
	}
	want, err := strconv.ParseInt(w.Header().Get("Content-Length"), 10, 64)
	return err == nil && w.sent == want
}

// attachment is a Content-Disposition that keeps any name, accents included (RFC 6266).
func attachment(name string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": name})
}
