package webshare

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"
)

// The page: HTML, JavaScript and CSS in files of their own, for the CSP to forbid any inline
// script (WEB-05).
//
//go:embed assets
var assets embed.FS

// CSP is the policy of every response (WEB-05).
const CSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'"

// maxJSON bounds a request's JSON body (WEB-08).
const maxJSON = 16 << 10

func (s *Session) handler() http.Handler {
	mux := http.NewServeMux()
	page, _ := fs.Sub(assets, "assets")
	files := http.FileServer(http.FS(page))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/s", http.StatusFound)
	})
	mux.HandleFunc("GET /s", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	})
	mux.Handle("GET /s/", http.StripPrefix("/s", files))
	mux.HandleFunc("POST /api/unlock", s.handleUnlock)
	mux.HandleFunc("GET /api/session", s.authed(s.handleSession))
	mux.HandleFunc("GET /api/items", s.authed(s.handleItems))
	mux.HandleFunc("GET /api/items/{id}", s.authed(s.handleItem))
	mux.HandleFunc("POST /api/uploads", s.authed(s.handleUploadRequest))
	mux.HandleFunc("GET /api/uploads/{ticket}", s.authed(s.handleUploadState))
	mux.HandleFunc("PUT /api/uploads/{ticket}/{index}", s.authed(s.handleChunk))
	mux.HandleFunc("DELETE /api/uploads/{ticket}", s.authed(s.handleUploadCancel))
	return s.headers(mux)
}

// headers sets the security headers on every response (WEB-05) and refuses everything once
// the session ended (WEB-10). Each route bounds its own body (WEB-08): decode, handleChunk; the
// others read none.
func (s *Session) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", CSP)
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		if s.Closed() != "" {
			writeError(w, http.StatusGone, "session_closed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authed lets through a request with the session's token in Authorization (WEB-02): never in
// the URL, never in a cookie.
func (s *Session) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !secureEq(got, s.token) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !s.allowIP(r) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next(w, r)
	}
}

// allowIP: with PinIP, the first address authenticated is the only one let in (WEB-09).
func (s *Session) allowIP(r *http.Request) bool {
	if !s.opts.PinIP {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.guestIP == "" {
		s.guestIP = host
	}
	return s.guestIP == host
}

// handleUnlock gives the token for the PIN (WEB-03): MaxPINTries per session, not per address,
// counted before the comparison so that requests in parallel cannot try more; a wait after
// each wrong one; the session ended after the last, the host told.
func (s *Session) handleUnlock(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PIN string `json:"pin"`
	}
	if !decode(w, r, &body, maxJSON) {
		return
	}
	s.mu.Lock()
	if s.closed != "" || s.tries >= MaxPINTries {
		s.mu.Unlock()
		writeError(w, http.StatusGone, "session_closed")
		return
	}
	s.tries++
	try := s.tries
	s.mu.Unlock()

	if secureEq(strings.TrimSpace(body.PIN), s.pin) && s.allowIP(r) {
		s.mu.Lock()
		s.tries-- // a right PIN costs no try
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"token": s.token})
		return
	}
	time.Sleep(s.failWait)
	if try >= MaxPINTries {
		s.end(ClosedPINAttempts)
		writeError(w, http.StatusGone, "session_closed")
		return
	}
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_pin", "tries_left": MaxPINTries - try})
}

// handleSession says who the host is (its generic name, DISC-07) and until when.
func (s *Session) handleSession(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	bytesLeft, filesLeft := s.maxBytes()-s.usedBytes, s.maxFiles()-s.usedFiles
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"name":       s.opts.Name,
		"expires":    s.Expires().UTC().Format(time.RFC3339),
		"receive":    s.opts.ReceiveDir != "",
		"bytes_left": bytesLeft,
		"files_left": filesLeft,
		"chunk":      ChunkSize,
	})
}

func (s *Session) handleItems(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.items)
}

func (s *Session) handleItem(w http.ResponseWriter, r *http.Request) {
	it, ok := s.item(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	s.serveItem(w, r, it)
}

// decode reads a JSON body: 413 beyond limit (WEB-08), 400 when it is not JSON.
func decode(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v)
	var tooBig *http.MaxBytesError
	switch {
	case err == nil:
		return true
	case errors.As(err, &tooBig):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large")
	default:
		writeError(w, http.StatusBadRequest, "bad_request")
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
