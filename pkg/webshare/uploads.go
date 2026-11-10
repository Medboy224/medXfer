package webshare

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
)

// The guest's uploads (WEB-06, WEB-07, bible §11.4): the guest announces its files, the host
// accepts or refuses them all, then each file comes in chunks, each checked against what was
// announced. A file is written to a part file, then renamed under a free name: never over a
// file already there.

// Defaults of the quotas (WEB-06), when Options leaves them at 0.
const (
	DefaultMaxBytes = 64 << 30 // per session
	DefaultMaxFiles = 1000     // per session
	// MaxChunk bounds one PUT's body; ChunkSize is the size the page sends.
	MaxChunk  = 16 << 20
	ChunkSize = 8 << 20
	// spaceMargin is left free on the receiving disk beyond the files announced.
	spaceMargin = 64 << 20
	// maxAnnounce bounds the JSON of a request: up to DefaultMaxFiles names.
	maxAnnounce = 1 << 20
)

// The states of an upload, as GET /api/uploads/{ticket} says them.
const (
	upPending   = "pending"
	upAccepted  = "accepted"
	upRefused   = "refused"
	upDone      = "done"
	upCancelled = "cancelled"
)

type upload struct {
	mu     sync.Mutex // one chunk at a time
	ticket string
	state  string
	files  []*upFile
	total  int64
	said   time.Time // the last upload_progress
}

// counting writes a chunk to its file, and counts what is written as it goes. The upload's
// lock is held.
type counting struct {
	s  *Session
	up *upload
	f  *upFile
}

func (c *counting) Write(p []byte) (int, error) {
	n, err := c.f.f.Write(p)
	c.f.received += int64(n)
	c.s.progress(c.up)
	return n, err
}

// progressGap is the least time between two upload_progress events.
const progressGap = 250 * time.Millisecond

// progress tells the host what came, ProgressGap apart at most. Its lock is held.
func (s *Session) progress(up *upload) {
	now := time.Now()
	if now.Sub(up.said) < progressGap {
		return
	}
	up.said = now
	var received int64
	for _, f := range up.files {
		received += f.received
	}
	s.emit(Event{Kind: "upload_progress", Data: map[string]any{"ticket": up.ticket, "received": received, "total": up.total}})
}

type upFile struct {
	Name     string // the base name kept (WEB-07)
	Size     int64
	received int64
	part     string   // the part file, while it comes
	f        *os.File // open while it comes
	final    string   // the name it got once complete
}

// UploadFile is an announced file, as the host sees it.
type UploadFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func (s *Session) maxBytes() int64 {
	if s.opts.MaxBytes > 0 {
		return s.opts.MaxBytes
	}
	return DefaultMaxBytes
}

func (s *Session) maxFiles() int {
	if s.opts.MaxFiles > 0 {
		return s.opts.MaxFiles
	}
	return DefaultMaxFiles
}

// handleUploadRequest: POST /api/uploads {files: [{name, size}]}.
func (s *Session) handleUploadRequest(w http.ResponseWriter, r *http.Request) {
	if s.opts.ReceiveDir == "" {
		writeError(w, http.StatusForbidden, "uploads_off")
		return
	}
	var body struct {
		Files []UploadFile `json:"files"`
	}
	if !decode(w, r, &body, maxAnnounce) {
		return
	}
	if len(body.Files) == 0 {
		writeError(w, http.StatusBadRequest, "no_files")
		return
	}
	up := &upload{state: upPending}
	for _, f := range body.Files {
		name, err := baseName(f.Name)
		if err != nil || f.Size < 0 {
			writeError(w, http.StatusBadRequest, "bad_name")
			return
		}
		up.files = append(up.files, &upFile{Name: name, Size: f.Size})
		up.total += f.Size
	}
	ticket, err := randomToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	up.ticket = ticket[:16]

	s.mu.Lock()
	switch {
	case s.active != nil:
		s.mu.Unlock()
		writeError(w, http.StatusConflict, "busy") // one transfer at a time (WEB-06)
		return
	case s.usedFiles+len(up.files) > s.maxFiles() || s.usedBytes+up.total > s.maxBytes():
		s.mu.Unlock()
		writeError(w, http.StatusRequestEntityTooLarge, "quota")
		return
	}
	if free, err := engine.FreeSpace(s.opts.ReceiveDir); err == nil && free < up.total+spaceMargin {
		s.mu.Unlock()
		writeError(w, http.StatusInsufficientStorage, "no_space")
		return
	}
	s.active = up
	s.usedFiles += len(up.files)
	s.usedBytes += up.total
	s.uploads[up.ticket] = up
	s.busy++ // the session does not expire while the host decides, nor while files come
	s.mu.Unlock()

	files := make([]UploadFile, len(up.files))
	for i, f := range up.files {
		files[i] = UploadFile{Name: f.Name, Size: f.Size}
	}
	s.emit(Event{Kind: "upload_request", Data: map[string]any{"ticket": up.ticket, "files": files, "total": up.total, "auto": s.opts.AutoAccept}})
	state := upPending
	if s.opts.AutoAccept {
		if err := s.Answer(up.ticket, true); err != nil {
			writeError(w, http.StatusInternalServerError, "internal")
			return
		}
		state = upAccepted
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ticket": up.ticket, "state": state, "chunk": ChunkSize})
}

// baseName keeps a name the guest gives only once it is a safe relative path, and then only
// its base name (WEB-07).
func baseName(name string) (string, error) {
	safe, err := engine.SafeRelPath(name)
	if err != nil {
		return "", err
	}
	base := path.Base(safe)
	if base == "." || base == "/" || base == "" {
		return "", errors.New("webshare: no name")
	}
	return base, nil
}

// Answer is the host's answer to an upload: accepted, its part files are made and the guest
// may send; refused, the guest is told and the quota given back.
func (s *Session) Answer(ticket string, accept bool) error {
	s.mu.Lock()
	up := s.uploads[ticket]
	s.mu.Unlock()
	if up == nil {
		return fmt.Errorf("webshare: no upload %q", ticket)
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if up.state != upPending {
		return fmt.Errorf("webshare: upload %q is %s", ticket, up.state)
	}
	if !accept {
		up.state = upRefused
		s.release(up, true)
		s.emit(Event{Kind: "upload_refused", Data: map[string]any{"ticket": ticket}})
		return nil
	}
	if err := os.MkdirAll(s.opts.ReceiveDir, 0o755); err != nil {
		return err
	}
	for i, f := range up.files {
		f.part = filepath.Join(s.opts.ReceiveDir, fmt.Sprintf(".medxfer-%s-%d.part", ticket, i))
		file, err := os.OpenFile(f.part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			s.drop(up)
			up.state = upCancelled
			s.release(up, true)
			return err
		}
		f.f = file
	}
	up.state = upAccepted
	s.emit(Event{Kind: "upload_accepted", Data: map[string]any{"ticket": ticket}})
	for i, f := range up.files {
		if f.Size == 0 {
			s.complete(up, i)
		}
	}
	return nil
}

// CancelUpload ends an upload, from the host or the guest: the incomplete files are removed;
// the complete ones, accepted by the host, stay.
func (s *Session) CancelUpload(ticket string) error {
	s.mu.Lock()
	up := s.uploads[ticket]
	s.mu.Unlock()
	if up == nil {
		return fmt.Errorf("webshare: no upload %q", ticket)
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if up.state != upPending && up.state != upAccepted {
		return nil
	}
	s.drop(up)
	up.state = upCancelled
	s.release(up, false)
	s.emit(Event{Kind: "upload_cancelled", Data: map[string]any{"ticket": ticket}})
	return nil
}

// drop closes and removes the part files of an upload. Its lock is held.
func (s *Session) drop(up *upload) {
	for _, f := range up.files {
		if f.f != nil {
			_ = f.f.Close()
			f.f = nil
		}
		if f.part != "" && f.final == "" {
			_ = os.Remove(f.part)
		}
	}
}

// release ends an upload's hold on the session: no more the active one, the session free to
// expire; refund gives the quota back (nothing was written).
func (s *Session) release(up *upload, refund bool) {
	s.mu.Lock()
	if s.active == up {
		s.active = nil
	}
	if refund {
		s.usedFiles -= len(up.files)
		s.usedBytes -= up.total
	}
	s.mu.Unlock()
	s.finish()
}

// handleUploadState: GET /api/uploads/{ticket}.
func (s *Session) handleUploadState(w http.ResponseWriter, r *http.Request) {
	up := s.upload(r.PathValue("ticket"))
	if up == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	up.mu.Lock()
	files := make([]map[string]any, len(up.files))
	for i, f := range up.files {
		files[i] = map[string]any{"index": i, "received": f.received}
	}
	state := up.state
	up.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"state": state, "files": files})
}

// handleChunk: PUT /api/uploads/{ticket}/{index}?offset=N. Each chunk is checked against the
// file announced: its index, its position, its size (#157, 2b).
func (s *Session) handleChunk(w http.ResponseWriter, r *http.Request) {
	up := s.upload(r.PathValue("ticket"))
	if up == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	offset, err2 := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || err2 != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if up.state != upAccepted {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "not_accepted", "state": up.state})
		return
	}
	if index < 0 || index >= len(up.files) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	f := up.files[index]
	if f.f == nil || offset != f.received {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "offset", "received": f.received})
		return
	}
	// Never past the size announced: one byte more read says the chunk is too big.
	body := http.MaxBytesReader(w, r.Body, MaxChunk)
	// Counted as it is written, the host's bar moving during a chunk, not once per chunk (real
	// test of 10 November 2026: an 8 MiB chunk took seconds on a slow network).
	_, err = io.Copy(&counting{s: s, up: up, f: f}, io.LimitReader(body, f.Size-f.received+1))
	if f.received > f.Size {
		s.drop(up)
		up.state = upCancelled
		s.release(up, false)
		s.emit(Event{Kind: "upload_cancelled", Data: map[string]any{"ticket": up.ticket}})
		writeError(w, http.StatusRequestEntityTooLarge, "too_large")
		return
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "chunk_too_large", "received": f.received})
			return
		}
		// The guest's connection broke: it sends again from what was received.
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "interrupted", "received": f.received})
		return
	}
	s.progress(up)
	if f.received == f.Size {
		if err := s.complete(up, index); err != nil {
			writeError(w, http.StatusInternalServerError, "write_failed")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"received": f.received})
}

// complete closes a file received in full and gives it a free name: "name.ext", else
// "name (1).ext" and so on, never over a file already there (#157, 2a). The last file ends the
// upload (#157, 2c: the ticket lives until then). Its lock is held.
func (s *Session) complete(up *upload, index int) error {
	f := up.files[index]
	if err := f.f.Close(); err != nil {
		return err
	}
	f.f = nil
	_, base, ext := engine.SplitNameAndExt(f.Name)
	for n := 0; ; n++ {
		final := filepath.Join(s.opts.ReceiveDir, engine.FormatCandidateName("", base, n, ext))
		// Taken first, then replaced by the part file: a name another writer takes meanwhile
		// is never overwritten.
		hold, err := os.OpenFile(final, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		_ = hold.Close()
		if err := os.Rename(f.part, final); err != nil {
			_ = os.Remove(final)
			return err
		}
		f.final = final
		break
	}
	s.emit(Event{Kind: "upload_file_done", Data: map[string]any{"ticket": up.ticket, "index": index, "name": filepath.Base(f.final), "path": f.final, "size": f.Size}})
	for _, other := range up.files {
		if other.final == "" {
			return nil
		}
	}
	up.state = upDone
	s.release(up, false)
	s.emit(Event{Kind: "upload_done", Data: map[string]any{"ticket": up.ticket}})
	return nil
}

// handleUploadCancel: DELETE /api/uploads/{ticket}.
func (s *Session) handleUploadCancel(w http.ResponseWriter, r *http.Request) {
	if s.upload(r.PathValue("ticket")) == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	_ = s.CancelUpload(r.PathValue("ticket"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Session) upload(ticket string) *upload {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.uploads[ticket]
}

// dropUploads cancels what still comes when the session ends: no part file left behind.
func (s *Session) dropUploads() {
	s.mu.Lock()
	ups := make([]*upload, 0, len(s.uploads))
	for _, up := range s.uploads {
		ups = append(ups, up)
	}
	s.mu.Unlock()
	for _, up := range ups {
		_ = s.CancelUpload(up.ticket)
	}
}
