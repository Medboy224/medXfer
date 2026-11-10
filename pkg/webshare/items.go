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
)

// Item is a file or a folder the host offers. The guest names it by ID only, never by a name
// it supplies (WEB-07).
type Item struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Folder bool   `json:"folder"`
	path   string
}

// itemsOf lists the paths offered, each with its ID: its place in the list. A folder's size is
// that of its regular files.
func itemsOf(paths []string) ([]Item, error) {
	items := make([]Item, 0, len(paths))
	for i, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("webshare: %w", err)
		}
		it := Item{ID: strconv.Itoa(i), Name: filepath.Base(abs), path: abs, Folder: fi.IsDir()}
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
	return items, nil
}

func (s *Session) item(id string) (Item, bool) {
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
		f, err := os.Open(it.path)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		w.Header().Set("Content-Disposition", attachment(it.Name))
		http.ServeContent(w, r, it.Name, fi.ModTime(), f)
		s.emit(Event{Kind: "download_done", Data: map[string]any{"id": it.ID, "name": it.Name}})
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

// attachment is a Content-Disposition that keeps any name, accents included (RFC 6266).
func attachment(name string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": name})
}
