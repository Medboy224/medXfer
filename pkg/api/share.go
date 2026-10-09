package api

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
)

// checkAuth checks if the request supplies either:
// 1. A valid 128-bit crypto token (from QR code scan or persistent cookie)
// 2. A valid PIN (from manual input, query, header, or cookie)
// Comparisons are constant-time; the attempted value is never returned so it cannot be logged (WEB-04).
func (s *DaemonServer) checkAuth(r *http.Request) (valid bool, attempted bool) {
	s.mu.RLock()
	enabled := s.webShareEnabled
	expectedPIN := s.webSharePIN
	expectedToken := s.webShareToken
	s.mu.RUnlock()

	if !enabled {
		return false, false
	}
	if expectedPIN == "" && expectedToken == "" {
		return true, false
	}
	tokenOK := func(v string) bool { return expectedToken != "" && secureEq(v, expectedToken) }
	pinOK := func(v string) bool { return expectedPIN != "" && secureEq(v, expectedPIN) }

	// 1. Check high-entropy crypto token
	if token := r.URL.Query().Get("token"); token != "" && tokenOK(token) {
		return true, true
	}
	if token := r.Header.Get("X-Share-Token"); token != "" && tokenOK(token) {
		return true, true
	}
	if cookie, err := r.Cookie("medxfer_token"); err == nil && cookie.Value != "" && tokenOK(cookie.Value) {
		return true, true
	}

	// 2. Check PIN
	if pin := r.URL.Query().Get("pin"); pin != "" {
		return pinOK(pin), true
	}
	if pin := r.Header.Get("X-Share-PIN"); pin != "" {
		return pinOK(pin), true
	}
	if cookie, err := r.Cookie("medxfer_pin"); err == nil && cookie.Value != "" {
		return pinOK(cookie.Value), true
	}

	return false, false
}

// shareHeaders sets security headers on every Web Share route (WEB-05, partial: the CSP
// needs the portal's inline JavaScript moved out, phase 5). The URL can carry the token,
// so no Referer may leak it.
func shareHeaders(next http.HandlerFunc, noStore bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		if noStore {
			h.Set("Cache-Control", "no-store")
		}
		next(w, r)
	}
}

// verifyWebShareAccess performs lockout checks, token/PIN authentication,
// enforces 500ms tarpit delay on failure, and writes appropriate HTTP response (401 or 429).
func (s *DaemonServer) verifyWebShareAccess(w http.ResponseWriter, r *http.Request) bool {
	s.mu.RLock()
	enabled := s.webShareEnabled
	s.mu.RUnlock()

	if !enabled {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":        "web_share_disabled",
			"message":      "Web Share is currently disabled on the host device.",
			"pin_required": false,
		})
		return false
	}

	clientIP := r.RemoteAddr
	if host, _, err := netSplitHost(clientIP); err == nil {
		clientIP = host
	}

	// 1. Check if IP is currently locked out
	if locked, remainingSec := s.CheckLockout(clientIP); locked {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", strconv.Itoa(remainingSec))
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":        "locked_out",
			"retry_after":  remainingSec,
			"message":      fmt.Sprintf("Too many failed attempts. Device temporarily locked for %d seconds.", remainingSec),
			"pin_required": true,
		})
		return false
	}

	// 2. Validate token or PIN
	valid, attempted := s.checkAuth(r)
	if valid {
		s.RecordAuthSuccess(clientIP)
		return true
	}

	// 3. Failed attempt -> enforce 500ms tarpit delay
	if attempted {
		time.Sleep(500 * time.Millisecond)

		locked, remainingSec, _ := s.RecordAuthFailure(clientIP)
		if locked {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", strconv.Itoa(remainingSec))
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":        "locked_out",
				"retry_after":  remainingSec,
				"message":      fmt.Sprintf("Too many failed attempts. Device temporarily locked for %d seconds.", remainingSec),
				"pin_required": true,
			})
			return false
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":        "invalid_pin",
			"pin_required": true,
		})
		return false
	}

	// Unauthenticated visitor (no credentials sent)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":        "unauthorized",
		"pin_required": true,
	})
	return false
}

// validatePIN checks if the request supplies a valid PIN or token, enforcing lockout
func (s *DaemonServer) validatePIN(r *http.Request) bool {
	clientIP := r.RemoteAddr
	if host, _, err := netSplitHost(clientIP); err == nil {
		clientIP = host
	}
	if locked, _ := s.CheckLockout(clientIP); locked {
		return false
	}
	valid, attempted := s.checkAuth(r)
	if valid {
		s.RecordAuthSuccess(clientIP)
		return true
	}
	if attempted {
		time.Sleep(500 * time.Millisecond)
		s.RecordAuthFailure(clientIP)
	}
	return false
}

// handleSharePortal serves the mobile zero-install download & upload web page
func (s *DaemonServer) handleSharePortal(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	enabled := s.webShareEnabled
	expectedPIN := s.webSharePIN
	expectedToken := s.webShareToken
	s.mu.RUnlock()

	if !enabled {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(ShareDisabledHTML))
		return
	}

	// 1. Persist valid token
	if token := r.URL.Query().Get("token"); token != "" && token == expectedToken {
		http.SetCookie(w, &http.Cookie{
			Name:     "medxfer_token",
			Value:    token,
			Path:     "/",
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   86400,
		})
	}

	// 2. Persist valid PIN
	if pin := r.URL.Query().Get("pin"); pin != "" {
		if pin == expectedPIN {
			http.SetCookie(w, &http.Cookie{
				Name:     "medxfer_pin",
				Value:    pin,
				Path:     "/",
				HttpOnly: false,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   86400,
			})
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(SharePortalHTML))
}

// SharedItem represents a single file entry in the web share portal
type SharedItem struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"url"`
}

// handleShareList returns the list of currently shared files as JSON
func (s *DaemonServer) handleShareList(w http.ResponseWriter, r *http.Request) {
	if !s.verifyWebShareAccess(w, r) {
		return
	}

	s.mu.RLock()
	m := s.webSharedManifest
	singleFile := s.webSharedFile
	devName := s.config.DeviceName
	s.mu.RUnlock()

	// URLs carry no credential: the portal adds its own token/PIN when downloading
	// (startOrResumeDownload). Names are escaped so "&", "#" or "%" cannot break the link.
	var items []SharedItem
	var totalBytes int64
	if m != nil {
		totalBytes = m.TotalBytes
		for _, it := range m.Items {
			items = append(items, SharedItem{
				Name: it.RelPath,
				Size: it.Size,
				URL:  "/api/share/download?file=" + url.QueryEscape(it.RelPath),
			})
		}
	} else if singleFile != "" {
		fi, err := os.Stat(singleFile)
		if err == nil {
			totalBytes = fi.Size()
			items = append(items, SharedItem{
				Name: filepath.Base(singleFile),
				Size: fi.Size(),
				URL:  "/api/share/download?file=" + url.QueryEscape(filepath.Base(singleFile)),
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"device_name": devName,
		"items":       items,
		"total_count": len(items),
		"total_bytes": totalBytes,
	})
}

// progressResponseWriter intercepts writes to emit live WebSocket download telemetry
type progressResponseWriter struct {
	http.ResponseWriter
	server       *DaemonServer
	clientIP     string
	fileName     string
	totalBytes   int64
	transferred  int64
	startOffset  int64
	ctx          context.Context
	startTime    time.Time
	lastUpdate   time.Time
	lastBytes    int64
	currentSpeed float64
}

func (pw *progressResponseWriter) Write(p []byte) (int, error) {
	if pw.ctx != nil {
		if err := pw.server.CheckWebSharePause(pw.ctx); err != nil {
			return 0, err
		}
	}
	n, err := pw.ResponseWriter.Write(p)
	pw.transferred += int64(n)
	now := time.Now()
	elapsed := now.Sub(pw.lastUpdate).Seconds()
	currentTotal := pw.startOffset + pw.transferred
	if elapsed >= 0.25 || (pw.totalBytes > 0 && currentTotal >= pw.totalBytes) {
		if elapsed > 0 {
			deltaBytes := pw.transferred - pw.lastBytes
			instantSpeed := (float64(deltaBytes) / (1024 * 1024)) / elapsed
			if pw.currentSpeed == 0 {
				pw.currentSpeed = instantSpeed
			} else {
				pw.currentSpeed = 0.7*pw.currentSpeed + 0.3*instantSpeed
			}
		}
		pw.lastUpdate = now
		pw.lastBytes = pw.transferred

		pct := 0
		if pw.totalBytes > 0 {
			pct = int((currentTotal * 100) / pw.totalBytes)
			if pct > 100 {
				pct = 100
			}
		}
		pw.server.Broadcast(NewEvent("web_share_progress", map[string]interface{}{
			"direction":   "download",
			"client_ip":   pw.clientIP,
			"file":        pw.fileName,
			"bytes":       currentTotal,
			"total_bytes": pw.totalBytes,
			"percent":     pct,
			"speed_mbps":  fmt.Sprintf("%.1f", pw.currentSpeed),
		}))
	}
	return n, err
}

// handleShareDownload streams the requested file or full zip on-the-fly with progress
func (s *DaemonServer) handleShareDownload(w http.ResponseWriter, r *http.Request) {
	if !s.verifyWebShareAccess(w, r) {
		return
	}

	fileName := r.URL.Query().Get("file")
	allZip := r.URL.Query().Get("zip") == "true"

	clientIP := r.RemoteAddr
	if host, _, err := netSplitHost(clientIP); err == nil {
		clientIP = host
	}

	s.mu.RLock()
	m := s.webSharedManifest
	singleFile := s.webSharedFile
	s.mu.RUnlock()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if allZip && m != nil {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.zip\"", m.RootName))
		w.Header().Set("X-Total-Bytes", strconv.FormatInt(m.TotalBytes, 10))
		w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, X-Total-Bytes")

		s.RegisterWebShareTransfer("download", clientIP, m.RootName+".zip", cancel, "")
		defer s.UnregisterWebShareTransfer()

		pw := &progressResponseWriter{
			ResponseWriter: w,
			server:         s,
			clientIP:       clientIP,
			fileName:       m.RootName + ".zip",
			totalBytes:     m.TotalBytes,
			ctx:            ctx,
			startTime:      time.Now(),
			lastUpdate:     time.Now(),
		}

		zw := zip.NewWriter(pw)
		for _, it := range m.Items {
			if ctx.Err() != nil {
				break
			}
			f, err := os.Open(it.FullPath)
			if err != nil {
				continue
			}
			header := &zip.FileHeader{
				Name:     filepath.ToSlash(it.RelPath),
				Method:   zip.Store, // Zero CPU compression: raw byte streaming at line-speed
				Modified: time.Now(),
			}
			zf, err := zw.CreateHeader(header)
			if err == nil {
				_, _ = io.Copy(zf, f)
			}
			f.Close()
		}
		_ = zw.Close()

		if ctx.Err() != nil {
			s.Broadcast(NewEvent("web_share_canceled", map[string]interface{}{
				"direction": "download",
				"client_ip": clientIP,
				"file":      m.RootName + ".zip",
				"message":   "Download canceled or aborted",
			}))
		} else {
			s.Broadcast(NewEvent("web_share_complete", map[string]interface{}{
				"direction": "download",
				"client_ip": clientIP,
				"file":      m.RootName + ".zip",
			}))
		}
		return
	}

	// Single file download
	var targetPath string
	if m != nil {
		for _, it := range m.Items {
			if it.RelPath == fileName || filepath.Base(it.RelPath) == fileName {
				targetPath = it.FullPath
				break
			}
		}
	} else if singleFile != "" && (filepath.Base(singleFile) == fileName || fileName == "") {
		targetPath = singleFile
	}

	if targetPath == "" {
		http.Error(w, "File not found or no files currently shared", http.StatusNotFound)
		return
	}

	f, err := os.Open(targetPath)
	if err != nil {
		http.Error(w, "Failed to open file", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	fi, _ := f.Stat()
	totalSize := fi.Size()
	cleanName := filepath.Base(targetPath)

	s.RegisterWebShareTransfer("download", clientIP, cleanName, cancel, "")
	defer s.UnregisterWebShareTransfer()

	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", cleanName))

	rangeHdr := r.Header.Get("Range")
	var startOffset int64 = 0
	var endOffset int64 = totalSize - 1
	isRange := false

	if rangeHdr != "" && strings.HasPrefix(rangeHdr, "bytes=") {
		parts := strings.Split(strings.TrimPrefix(rangeHdr, "bytes="), "-")
		if len(parts) >= 1 && parts[0] != "" {
			if sVal, err := strconv.ParseInt(parts[0], 10, 64); err == nil && sVal >= 0 && sVal < totalSize {
				startOffset = sVal
				isRange = true
			}
		}
		if len(parts) >= 2 && parts[1] != "" {
			if eVal, err := strconv.ParseInt(parts[1], 10, 64); err == nil && eVal >= startOffset && eVal < totalSize {
				endOffset = eVal
			}
		}
	}

	if isRange {
		contentLen := endOffset - startOffset + 1
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", startOffset, endOffset, totalSize))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", contentLen))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = f.Seek(startOffset, io.SeekStart)
	} else {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", totalSize))
	}

	pw := &progressResponseWriter{
		ResponseWriter: w,
		server:         s,
		clientIP:       clientIP,
		fileName:       cleanName,
		totalBytes:     totalSize,
		startOffset:    startOffset,
		ctx:            ctx,
		startTime:      time.Now(),
		lastUpdate:     time.Now(),
	}

	buf := make([]byte, 1024*1024)
	var copyErr error
	if isRange {
		_, copyErr = io.CopyBuffer(pw, io.LimitReader(f, endOffset-startOffset+1), buf)
	} else {
		_, copyErr = io.CopyBuffer(pw, f, buf)
	}

	if ctx.Err() != nil || copyErr != nil {
		s.Broadcast(NewEvent("web_share_canceled", map[string]interface{}{
			"direction": "download",
			"client_ip": clientIP,
			"file":      cleanName,
			"message":   "Download canceled or connection closed",
		}))
	} else {
		s.Broadcast(NewEvent("web_share_complete", map[string]interface{}{
			"direction": "download",
			"client_ip": clientIP,
			"file":      cleanName,
		}))
	}
}

func netSplitHost(remote string) (string, string, error) {
	idx := strings.LastIndex(remote, ":")
	if idx == -1 {
		return remote, "", nil
	}
	return remote[:idx], remote[idx+1:], nil
}

// handleShareRequestUpload receives upload file metadata from mobile, emits an approval event, and waits for host response
func (s *DaemonServer) handleShareRequestUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.verifyWebShareAccess(w, r) {
		return
	}

	clientIP := r.RemoteAddr
	if host, _, err := netSplitHost(clientIP); err == nil {
		clientIP = host
	}

	var reqBody struct {
		Files []WebUploadFile `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil || len(reqBody.Files) == 0 {
		http.Error(w, "Invalid upload metadata", http.StatusBadRequest)
		return
	}

	var totalBytes int64
	for _, f := range reqBody.Files {
		totalBytes += f.Size
	}

	s.mu.Lock()
	autoAccept := s.webShareAutoAccept
	ticketSuffix, err := randomHex(16)
	if err != nil {
		s.mu.Unlock()
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	ticketID := "ticket_" + ticketSuffix // unguessable: a ticket authorizes an upload
	doneChan := make(chan bool, 1)

	ticket := &WebUploadRequest{
		ID:        ticketID,
		ClientIP:  clientIP,
		Files:     reqBody.Files,
		Approved:  autoAccept,
		ExpiresAt: time.Now().Add(60 * time.Second),
		DoneChan:  doneChan,
	}
	s.pendingWebUploads[ticketID] = ticket
	s.mu.Unlock()

	if autoAccept {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"approved": true,
			"ticket":   ticketID,
		})
		return
	}

	// Broadcast approval prompt event to Flutter and Web UI
	s.Broadcast(NewEvent("web_share_upload_request", map[string]interface{}{
		"ticket":      ticketID,
		"client_ip":   clientIP,
		"files":       reqBody.Files,
		"file_count":  len(reqBody.Files),
		"total_bytes": totalBytes,
	}))

	// Wait for host user decision or 45s timeout
	select {
	case approved := <-doneChan:
		if approved {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"approved": true,
				"ticket":   ticketID,
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"approved": false,
			"error":    "Upload was declined by the host device.",
		})
		return
	case <-time.After(45 * time.Second):
		s.mu.Lock()
		delete(s.pendingWebUploads, ticketID)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestTimeout)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"approved": false,
			"error":    "Upload request timed out waiting for approval on the host device.",
		})
		return
	}
}

// handleShareUpload allows mobile browsers to upload photos and files to the PC with real-time streaming progress
func (s *DaemonServer) handleShareUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.verifyWebShareAccess(w, r) {
		return
	}

	ticketID := r.URL.Query().Get("ticket")
	if ticketID == "" {
		ticketID = r.Header.Get("X-Upload-Ticket")
	}

	s.mu.Lock()
	autoAccept := s.webShareAutoAccept
	var approvedTicket bool
	if ticketID != "" {
		if t, ok := s.pendingWebUploads[ticketID]; ok && t != nil && t.Approved && time.Now().Before(t.ExpiresAt) {
			approvedTicket = true
			delete(s.pendingWebUploads, ticketID) // single-use token consumed
		}
	}
	s.mu.Unlock()

	if !autoAccept && !approvedTicket {
		http.Error(w, "Upload unauthorized. Host approval required before uploading.", http.StatusForbidden)
		return
	}

	clientIP := r.RemoteAddr
	if host, _, err := netSplitHost(clientIP); err == nil {
		clientIP = host
	}

	reader, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "Failed to read multipart stream: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	outDir := s.config.DownloadDir
	s.mu.RUnlock()

	if outDir == "" {
		outDir = "."
	}
	_ = os.MkdirAll(outDir, 0755)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	totalExpected := r.ContentLength
	var totalReceived int64
	var savedNames []string
	lastProgressTime := time.Now()
	var lastProgressBytes int64
	var smoothSpeed float64

	for {
		if err := s.CheckWebSharePause(ctx); err != nil {
			http.Error(w, "Upload canceled", http.StatusRequestTimeout)
			return
		}

		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		fileName := part.FileName()
		if fileName == "" {
			continue
		}

		cleanName := filepath.Base(fileName)
		if cleanName == "" || cleanName == "." || cleanName == "/" || cleanName == "\\" {
			cleanName = fmt.Sprintf("upload_%d", time.Now().UnixNano())
		}
		destPath := filepath.Join(outDir, cleanName)

		// Auto-rename if file already exists to prevent lock/permission failures
		if _, err := os.Stat(destPath); err == nil {
			dir, base, ext := engine.SplitNameAndExt(cleanName)
			for count := 1; count < 10000; count++ {
				candidate := fmt.Sprintf("%s (%d)%s", base, count, ext)
				if dir != "" {
					candidate = filepath.Join(dir, candidate)
				}
				candPath := filepath.Join(outDir, candidate)
				if _, err := os.Stat(candPath); os.IsNotExist(err) {
					cleanName = candidate
					destPath = candPath
					break
				}
			}
		}

		s.RegisterWebShareTransfer("upload", clientIP, cleanName, cancel, destPath)

		dst, err := os.Create(destPath)
		if err != nil {
			continue
		}

		buf := make([]byte, 1024*1024) // 1 MB high-speed streaming buffer
		var aborted bool
		for {
			if err := s.CheckWebSharePause(ctx); err != nil {
				aborted = true
				break
			}

			nr, readErr := part.Read(buf)
			if nr > 0 {
				_, _ = dst.Write(buf[:nr])
				totalReceived += int64(nr)
				now := time.Now()
				elapsed := now.Sub(lastProgressTime).Seconds()
				if elapsed >= 0.25 || (totalExpected > 0 && totalReceived >= totalExpected) {
					if elapsed > 0 {
						delta := totalReceived - lastProgressBytes
						instant := (float64(delta) / (1024 * 1024)) / elapsed
						if smoothSpeed == 0 {
							smoothSpeed = instant
						} else {
							smoothSpeed = 0.7*smoothSpeed + 0.3*instant
						}
					}
					lastProgressTime = now
					lastProgressBytes = totalReceived

					pct := 0
					if totalExpected > 0 {
						pct = int((totalReceived * 100) / totalExpected)
						if pct > 100 {
							pct = 100
						}
					}
					s.Broadcast(NewEvent("web_share_progress", map[string]interface{}{
						"direction":   "upload",
						"client_ip":   clientIP,
						"file":        cleanName,
						"bytes":       totalReceived,
						"total_bytes": totalExpected,
						"percent":     pct,
						"speed_mbps":  fmt.Sprintf("%.1f", smoothSpeed),
					}))
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					aborted = true
				}
				break
			}
		}
		dst.Close()

		if aborted || ctx.Err() != nil {
			_ = os.Remove(destPath)
			s.UnregisterWebShareTransfer()
			s.Broadcast(NewEvent("web_share_canceled", map[string]interface{}{
				"direction": "upload",
				"client_ip": clientIP,
				"file":      cleanName,
				"message":   "Upload canceled or aborted",
			}))
			http.Error(w, "Upload canceled", http.StatusRequestTimeout)
			return
		}

		savedNames = append(savedNames, cleanName)
	}

	s.UnregisterWebShareTransfer()

	if len(savedNames) == 0 {
		http.Error(w, "No files uploaded", http.StatusBadRequest)
		return
	}

	s.Broadcast(NewEvent("web_share_progress", map[string]interface{}{
		"direction":   "upload",
		"client_ip":   clientIP,
		"file":        fmt.Sprintf("%d file(s)", len(savedNames)),
		"bytes":       totalReceived,
		"total_bytes": totalReceived,
		"percent":     100,
		"speed_mbps":  "0.0",
	}))

	s.Broadcast(NewEvent("web_share_complete", map[string]interface{}{
		"direction": "upload",
		"client_ip": clientIP,
		"files":     savedNames,
		"count":     len(savedNames),
	}))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"saved":   savedNames,
	})
}

// handleShareUploadChunk handles chunked slice uploads from mobile browsers (allowing pause/resume)
func (s *DaemonServer) handleShareUploadChunk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.verifyWebShareAccess(w, r) {
		return
	}

	ticketID := r.URL.Query().Get("ticket")
	if ticketID == "" {
		ticketID = r.Header.Get("X-Upload-Ticket")
	}

	s.mu.Lock()
	autoAccept := s.webShareAutoAccept
	var approvedTicket bool
	if ticketID != "" {
		if t, ok := s.pendingWebUploads[ticketID]; ok && t != nil && t.Approved && time.Now().Before(t.ExpiresAt) {
			approvedTicket = true
		}
	}
	s.mu.Unlock()

	if !autoAccept && !approvedTicket {
		http.Error(w, "Upload unauthorized. Host approval required before uploading.", http.StatusForbidden)
		return
	}

	clientIP := r.RemoteAddr
	if host, _, err := netSplitHost(clientIP); err == nil {
		clientIP = host
	}

	fileName := r.URL.Query().Get("name")
	if fileName == "" {
		fileName = "upload_chunked.bin"
	}
	cleanName := filepath.Base(fileName)

	chunkIdxStr := r.URL.Query().Get("chunk_index")
	chunkIdx, _ := strconv.Atoi(chunkIdxStr)

	totalChunksStr := r.URL.Query().Get("total_chunks")
	totalChunks, _ := strconv.Atoi(totalChunksStr)
	if totalChunks <= 0 {
		totalChunks = 1
	}

	offsetStr := r.URL.Query().Get("offset")
	offset, _ := strconv.ParseInt(offsetStr, 10, 64)

	totalSizeStr := r.URL.Query().Get("total_size")
	totalSize, _ := strconv.ParseInt(totalSizeStr, 10, 64)

	s.mu.RLock()
	outDir := s.config.DownloadDir
	s.mu.RUnlock()
	if outDir == "" {
		outDir = "."
	}
	_ = os.MkdirAll(outDir, 0755)

	destPath := filepath.Join(outDir, cleanName)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	s.RegisterWebShareTransfer("upload", clientIP, cleanName, cancel, destPath)

	if err := s.CheckWebSharePause(ctx); err != nil {
		s.Broadcast(NewEvent("web_share_canceled", map[string]interface{}{
			"direction": "upload",
			"client_ip": clientIP,
			"file":      cleanName,
			"message":   "Upload paused or canceled",
		}))
		http.Error(w, "Upload paused or canceled", http.StatusRequestTimeout)
		return
	}

	var f *os.File
	var err error
	if chunkIdx == 0 && offset == 0 {
		f, err = os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	} else {
		f, err = os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY, 0644)
	}
	if err != nil {
		http.Error(w, "Failed to open destination file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			http.Error(w, "Failed to seek destination file: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	written, err := io.Copy(f, r.Body)
	if err != nil {
		if ctx.Err() != nil {
			s.Broadcast(NewEvent("web_share_canceled", map[string]interface{}{
				"direction": "upload",
				"client_ip": clientIP,
				"file":      cleanName,
				"message":   "Upload canceled by client",
			}))
		}
		http.Error(w, "Failed to write chunk: "+err.Error(), http.StatusInternalServerError)
		return
	}

	currentBytes := offset + written
	pct := 0
	if totalSize > 0 {
		pct = int((currentBytes * 100) / totalSize)
		if pct > 100 {
			pct = 100
		}
	}

	s.webShareTransferMu.Lock()
	now := time.Now()
	elapsed := now.Sub(s.webShareTransferLastTime).Seconds()
	if elapsed >= 0.2 || (totalSize > 0 && currentBytes >= totalSize) {
		// Windows' monotonic clock advances in ~15.6 ms ticks: two fast chunks can read
		// the same instant. Floor the interval so the speed is not stuck at 0 (#15).
		if elapsed < 0.001 {
			elapsed = 0.001
		}
		if currentBytes > s.webShareTransferLastBytes {
			delta := currentBytes - s.webShareTransferLastBytes
			instant := (float64(delta) / (1024 * 1024)) / elapsed
			if s.webShareTransferSpeed == 0 {
				s.webShareTransferSpeed = instant
			} else {
				s.webShareTransferSpeed = 0.7*s.webShareTransferSpeed + 0.3*instant
			}
		}
		s.webShareTransferLastBytes = currentBytes
		s.webShareTransferLastTime = now
	}
	speedVal := s.webShareTransferSpeed
	s.webShareTransferMu.Unlock()

	speedStr := fmt.Sprintf("%.1f", speedVal)
	if speedVal > 0 && speedStr == "0.0" {
		speedStr = "0.1"
	}

	s.Broadcast(NewEvent("web_share_progress", map[string]interface{}{
		"direction":   "upload",
		"client_ip":   clientIP,
		"file":        cleanName,
		"bytes":       currentBytes,
		"total_bytes": totalSize,
		"percent":     pct,
		"speed_mbps":  speedStr,
	}))

	isLast := (chunkIdx+1 >= totalChunks) || (totalSize > 0 && currentBytes >= totalSize)
	if isLast {
		s.UnregisterWebShareTransfer()
		s.mu.Lock()
		delete(s.pendingWebUploads, ticketID)
		s.mu.Unlock()

		s.Broadcast(NewEvent("web_share_complete", map[string]interface{}{
			"direction": "upload",
			"client_ip": clientIP,
			"files":     []string{cleanName},
			"count":     1,
		}))
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"chunk_index": chunkIdx,
		"written":     written,
		"completed":   isLast,
	})
}

func (s *DaemonServer) handleSharePause(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.verifyWebShareAccess(w, r) {
		return
	}
	ok := s.PauseWebShareTransfer()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": ok, "paused": true})
}

func (s *DaemonServer) handleShareResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.verifyWebShareAccess(w, r) {
		return
	}
	ok := s.ResumeWebShareTransfer()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": ok, "paused": false})
}

func (s *DaemonServer) handleShareCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.verifyWebShareAccess(w, r) {
		return
	}
	ok := s.CancelWebShareTransfer()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": ok, "canceled": true})
}

const SharePortalHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no">
  <title>medXfer Mobile Share</title>
  <style>
    :root {
      --bg: #0f172a;
      --card: #1e293b;
      --border: #334155;
      --primary: #3b82f6;
      --primary-hover: #2563eb;
      --text: #f8fafc;
      --text-muted: #94a3b8;
      --success: #10b981;
      --danger: #ef4444;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
    body { background: var(--bg); color: var(--text); padding: 1.25rem; max-width: 600px; margin: 0 auto; }
    .header { text-align: center; margin-bottom: 1.5rem; }
    .header h1 { font-size: 1.4rem; font-weight: 700; color: #60a5fa; }
    .header p { font-size: 0.85rem; color: var(--text-muted); margin-top: 0.25rem; }
    .card { background: var(--card); border: 1px solid var(--border); border-radius: 12px; padding: 1.25rem; margin-bottom: 1.25rem; }
    .card-title { font-size: 1rem; font-weight: 600; margin-bottom: 0.75rem; display: flex; justify-content: space-between; align-items: center; }
    .btn { display: inline-block; width: 100%; padding: 0.75rem; background: var(--primary); color: white; text-align: center; border-radius: 8px; font-weight: 600; font-size: 0.95rem; text-decoration: none; border: none; cursor: pointer; transition: background 0.2s; }
    .btn:active { background: var(--primary-hover); }
    .btn-success { background: var(--success); }
    .btn:disabled { opacity: 0.5; pointer-events: none; }
    .file-list { list-style: none; max-height: 280px; overflow-y: auto; margin-bottom: 1rem; }
    .file-item { display: flex; justify-content: space-between; align-items: center; padding: 0.6rem 0; border-bottom: 1px solid var(--border); font-size: 0.85rem; }
    .file-item:last-child { border-bottom: none; }
    .file-name { font-weight: 500; word-break: break-all; padding-right: 0.5rem; }
    .file-size { color: var(--text-muted); font-size: 0.75rem; white-space: nowrap; }
    .file-dl-btn { background: #334155; color: #60a5fa; padding: 0.4rem 0.75rem; border-radius: 6px; font-size: 0.8rem; text-decoration: none; font-weight: 600; border: none; cursor: pointer; }
    .file-dl-btn:active { background: #2563eb; color: white; }
    .upload-zone { border: 2px dashed var(--border); border-radius: 8px; padding: 1.25rem; text-align: center; cursor: pointer; }
    .upload-zone input { display: none; }
    .upload-text { font-size: 0.85rem; color: var(--text-muted); }
    .pin-input { font-size: 2rem; letter-spacing: 0.5rem; text-align: center; width: 160px; padding: 0.5rem; background: #0b1120; border: 1px solid var(--border); border-radius: 8px; color: white; font-weight: 700; margin: 1rem auto; display: block; }
    @keyframes shake {
      0%, 100% { transform: translateX(0); }
      20%, 60% { transform: translateX(-8px); }
      40%, 80% { transform: translateX(8px); }
    }
    .shake {
      animation: shake 0.4s cubic-bezier(0.36, 0.07, 0.19, 0.97) both;
      border-color: var(--danger) !important;
    }
  </style>
</head>
<body>
  <div class="header">
    <h1>⚡ medXfer Mobile Share</h1>
    <p id="hostDeviceName">Connecting to host...</p>
  </div>

  <!-- Lockout Card (Displayed when too many failed attempts) -->
  <div class="card" id="lockoutCard" style="display: none; text-align: center; border-color: #ef4444; background: #2d0606;">
    <div class="card-title" style="justify-content: center; font-size: 1.1rem; color: #f87171;">🚨 Access Temporarily Blocked</div>
    <p style="font-size: 0.85rem; color: #fca5a5; margin-top: 0.5rem;">
      Too many incorrect PIN attempts. This device has been locked out for security.
    </p>
    <div style="font-size: 2.2rem; font-weight: 700; color: #ef4444; margin: 1rem auto;">
      ⏳ <span id="lockoutCountdown">60</span>s
    </div>
    <p style="font-size: 0.75rem; color: #94a3b8;">
      Please wait until the countdown finishes or check the PIN on your host device.
    </p>
  </div>

  <!-- PIN Authentication Card -->
  <div class="card" id="pinCard" style="display: none; text-align: center; border-color: #3b82f6; transition: border-color 0.2s;">
    <div class="card-title" style="justify-content: center; font-size: 1.1rem;">🔒 Security PIN Required</div>
    <p style="font-size: 0.85rem; color: var(--text-muted); margin-top: 0.5rem;">
      Enter the PIN shown on the host device screen to unlock Web Share:
    </p>
    <input type="tel" id="pinInput" maxlength="8" placeholder="------" class="pin-input" style="width: 200px;" onkeyup="if(event.key==='Enter'||this.value.length>=6)submitPIN();">
    <button class="btn" id="pinBtn" onclick="submitPIN()">Unlock</button>
    <div id="pinError" style="color: var(--danger); font-size: 0.85rem; margin-top: 0.75rem; font-weight: 600; display: none;">Invalid PIN. Please check the code on the host device.</div>
  </div>

  <!-- Real-Time Download Progress Card -->
  <div class="card" id="dlProgressCard" style="display: none; border-color: #3b82f6; background: #0b1120;">
    <div class="card-title">
      <div style="display: flex; flex-direction: column; overflow: hidden; max-width: 65%;">
        <span id="dlFileName" style="font-size: 0.9rem; color: #60a5fa; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">Downloading file...</span>
        <span id="dlFileIndex" style="font-size: 0.72rem; color: var(--text-muted); display: none;">File 1 of 1</span>
      </div>
      <div style="display: flex; align-items: center; gap: 8px;">
        <span id="dlSpeed" style="font-size: 0.85rem; color: #34d399; font-weight: 600;">0.0 MB/s</span>
        <button type="button" onclick="dismissDlProgress()" style="background: none; border: none; color: var(--text-muted); font-size: 1.1rem; cursor: pointer; padding: 0 4px;" title="Close">✕</button>
      </div>
    </div>
    <div style="background: #1e293b; border-radius: 6px; height: 10px; overflow: hidden; margin-bottom: 6px;">
      <div id="dlProgressBar" style="width: 0%; height: 100%; background: #3b82f6; transition: width 0.15s;"></div>
    </div>
    <div style="display: flex; justify-content: space-between; font-size: 0.75rem; color: var(--text-muted); margin-bottom: 8px;">
      <span id="dlBytes">0 MB / 0 MB</span>
      <span id="dlPercent" style="font-weight: 600; color: #60a5fa;">0%</span>
    </div>

    <!-- Total Batch Progress (Shown during multi-file batch download) -->
    <div id="dlBatchProgressBox" style="display: none; margin-top: 10px; padding-top: 10px; border-top: 1px dashed #334155;">
      <div style="display: flex; justify-content: space-between; font-size: 0.8rem; font-weight: 600; color: #34d399; margin-bottom: 4px;">
        <span>📁 Total Batch Progress</span>
        <span id="dlBatchPercent">0%</span>
      </div>
      <div style="background: #1e293b; border-radius: 6px; height: 10px; overflow: hidden; margin-bottom: 6px;">
        <div id="dlBatchProgressBar" style="width: 0%; height: 100%; background: linear-gradient(90deg, #10b981, #34d399); transition: width 0.15s;"></div>
      </div>
      <div style="display: flex; justify-content: space-between; font-size: 0.72rem; color: var(--text-muted); margin-bottom: 8px;">
        <span id="dlBatchBytes">0 MB / 0 MB</span>
        <span id="dlBatchRemaining">Remaining: 0 MB</span>
      </div>

      <!-- Batch File Queue (Interactive advance skip list) -->
      <div id="dlBatchQueueBox" style="margin-top: 8px; padding-top: 8px; border-top: 1px dashed #334155;">
        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 6px;">
          <span style="font-size: 0.75rem; font-weight: 600; color: #93c5fd;">📋 Batch Queue (<span id="dlBatchQueueCount">0</span> files)</span>
          <span style="font-size: 0.68rem; color: var(--text-muted);">Tap ✕ to skip pending</span>
        </div>
        <div id="dlBatchQueueList" style="background: #070b14; border: 1px solid #1e293b; border-radius: 8px; max-height: 140px; overflow-y: auto; padding: 4px; font-size: 0.72rem;"></div>
      </div>
    </div>

    <div style="display: flex; justify-content: flex-end; gap: 8px; margin-top: 8px;">
      <button type="button" id="dlSkipBtn" class="btn" style="display: none; width: auto; padding: 4px 12px; font-size: 0.75rem; background: #475569; border-radius: 6px; color: #fff;" onclick="skipCurrentBatchFile()" title="Skip this file and download next">⏭️ Skip File</button>
      <button type="button" id="dlPauseBtn" class="btn" style="width: auto; padding: 4px 14px; font-size: 0.75rem; background: #334155; border-radius: 6px;" onclick="toggleDlPause()">⏸️ Pause</button>
      <button type="button" id="dlStopBtn" class="btn btn-danger" style="width: auto; padding: 4px 14px; font-size: 0.75rem; background: #ef4444; border-radius: 6px;" onclick="stopDlTransfer()">⏹️ Stop</button>
    </div>
  </div>

  <!-- Real-Time Upload Progress Card -->
  <div class="card" id="ulProgressCard" style="display: none; border-color: #10b981; background: #0b1120;">
    <div class="card-title">
      <span id="ulFileName" style="font-size: 0.9rem; color: #34d399;">Uploading to Host...</span>
      <div style="display: flex; align-items: center; gap: 8px;">
        <span id="ulSpeed" style="font-size: 0.85rem; color: #34d399; font-weight: 600;">0.0 MB/s</span>
        <button type="button" onclick="dismissUlProgress()" style="background: none; border: none; color: var(--text-muted); font-size: 1.1rem; cursor: pointer; padding: 0 4px;" title="Close">✕</button>
      </div>
    </div>
    <div style="background: #1e293b; border-radius: 6px; height: 10px; overflow: hidden; margin-bottom: 6px;">
      <div id="ulProgressBar" style="width: 0%; height: 100%; background: #10b981; transition: width 0.15s;"></div>
    </div>
    <div style="display: flex; justify-content: space-between; font-size: 0.75rem; color: var(--text-muted); margin-bottom: 8px;">
      <span id="ulBytes">0 MB / 0 MB</span>
      <span id="ulPercent" style="font-weight: 600; color: #34d399;">0%</span>
    </div>
    <div style="display: flex; justify-content: flex-end; gap: 8px;">
      <button type="button" id="ulPauseBtn" class="btn" style="width: auto; padding: 4px 14px; font-size: 0.75rem; background: #334155; border-radius: 6px;" onclick="toggleUlPause()">⏸️ Pause</button>
      <button type="button" id="ulStopBtn" class="btn btn-danger" style="width: auto; padding: 4px 14px; font-size: 0.75rem; background: #ef4444; border-radius: 6px;" onclick="stopUlTransfer()">⏹️ Stop</button>
    </div>
  </div>

  <!-- Shared Files from Host -->
  <div class="card" id="fileListCard" style="display: none;">
    <div class="card-title">
      <span>Files from Host</span>
      <span id="fileCountBadge" style="font-size: 0.75rem; background: #334155; padding: 2px 8px; border-radius: 12px;">0 files</span>
    </div>
    <div id="batchDlBox" style="display: none; margin-bottom: 0.85rem;">
      <button class="btn" id="dlBatchBtn" style="background: #2563eb; font-weight: 600; padding: 10px 14px; width: 100%;" onclick="downloadAllBatch()">
        ⬇️ Download All (Automatic)
      </button>
    </div>
    <ul class="file-list" id="fileList">
      <li style="text-align: center; color: var(--text-muted); padding: 1rem;">No files currently shared by host.</li>
    </ul>
  </div>

  <!-- Send to Host -->
  <div class="card" id="uploadCard" style="display: none;">
    <div class="card-title">Send to Host</div>
    <div class="upload-zone" onclick="document.getElementById('mobileUpload').click()">
      <input type="file" multiple id="mobileUpload" onchange="uploadFiles(this.files)">
      <div style="font-size: 1.5rem; margin-bottom: 0.25rem;">📤</div>
      <div class="upload-text">Tap to select Photos, Videos, or Files to send to Host</div>
    </div>
  </div>

  <script>
    let isAuthenticated = false;
    let pollInterval = null;
    let isDownloading = false;
    let isUploading = false;
    let dlTimeout = null;
    let ulTimeout = null;
    let lockoutInterval = null;

    function formatBytes(bytes) {
      if (!bytes || bytes <= 0) return '0 B';
      const k = 1024;
      const dm = 1;
      const sizes = ['B', 'KB', 'MB', 'GB'];
      const i = Math.floor(Math.log(bytes) / Math.log(k));
      return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
    }

    function formatSpeed(bytesPerSec) {
      if (!bytesPerSec || bytesPerSec <= 0) return '0.0 MB/s';
      const mbps = bytesPerSec / (1024 * 1024);
      if (mbps >= 1.0) {
        return mbps.toFixed(1) + ' MB/s';
      } else if (mbps >= 0.1) {
        return mbps.toFixed(2) + ' MB/s';
      } else {
        return (bytesPerSec / 1024).toFixed(0) + ' KB/s';
      }
    }

    function getToken() {
      const urlParam = new URLSearchParams(window.location.search).get('token');
      if (urlParam) {
        setToken(urlParam);
        return urlParam;
      }
      const stored = sessionStorage.getItem('medxfer_token');
      if (stored) return stored;
      const match = document.cookie.match(new RegExp('(^| )medxfer_token=([^;]+)'));
      if (match) return decodeURIComponent(match[2]);
      return '';
    }

    function setToken(token) {
      sessionStorage.setItem('medxfer_token', token);
      document.cookie = "medxfer_token=" + encodeURIComponent(token) + "; path=/; max-age=86400";
    }

    function getPIN() {
      const urlParam = new URLSearchParams(window.location.search).get('pin');
      if (urlParam) {
        setPIN(urlParam);
        return urlParam;
      }
      const stored = sessionStorage.getItem('medxfer_pin');
      if (stored) return stored;
      const match = document.cookie.match(new RegExp('(^| )medxfer_pin=([^;]+)'));
      if (match) return decodeURIComponent(match[2]);
      return '';
    }

    function setPIN(pin) {
      sessionStorage.setItem('medxfer_pin', pin);
      document.cookie = "medxfer_pin=" + encodeURIComponent(pin) + "; path=/; max-age=86400";
    }

    function clearCredentials() {
      sessionStorage.removeItem('medxfer_pin');
      sessionStorage.removeItem('medxfer_token');
      document.cookie = "medxfer_pin=; path=/; max-age=0";
      document.cookie = "medxfer_token=; path=/; max-age=0";
      try {
        if (window.location.search) {
          const url = new URL(window.location);
          if (url.searchParams.has('pin') || url.searchParams.has('token')) {
            url.searchParams.delete('pin');
            url.searchParams.delete('token');
            const cleanQuery = url.searchParams.toString();
            const newUrl = url.pathname + (cleanQuery ? ('?' + cleanQuery) : '');
            window.history.replaceState({}, document.title, newUrl);
          }
        }
      } catch (e) {}
      const pinIn = document.getElementById('pinInput');
      if (pinIn) pinIn.value = '';
    }

    function stopPolling() {
      if (pollInterval) {
        clearInterval(pollInterval);
        pollInterval = null;
      }
    }

    function startPolling() {
      if (!pollInterval) {
        pollInterval = setInterval(() => {
          if (isAuthenticated && !isDownloading && !lockoutInterval) {
            loadSharedFiles();
          }
        }, 2000);
      }
    }

    function showLockoutCountdown(seconds) {
      stopPolling();
      isAuthenticated = false;
      clearCredentials();
      if (lockoutInterval) clearInterval(lockoutInterval);

      document.getElementById('pinCard').style.display = 'none';
      document.getElementById('fileListCard').style.display = 'none';
      document.getElementById('uploadCard').style.display = 'none';

      const card = document.getElementById('lockoutCard');
      const countEl = document.getElementById('lockoutCountdown');
      card.style.display = 'block';

      let remaining = seconds || 60;
      countEl.innerText = remaining;

      lockoutInterval = setInterval(() => {
        remaining--;
        if (remaining <= 0) {
          clearInterval(lockoutInterval);
          lockoutInterval = null;
          card.style.display = 'none';
          document.getElementById('pinCard').style.display = 'block';
          const pinIn = document.getElementById('pinInput');
          if (pinIn) { pinIn.value = ''; pinIn.focus(); }
        } else {
          countEl.innerText = remaining;
        }
      }, 1000);
    }

    function triggerPinShake(msg) {
      stopPolling();
      isAuthenticated = false;
      clearCredentials();

      const pinCard = document.getElementById('pinCard');
      const errEl = document.getElementById('pinError');
      if (errEl) {
        errEl.innerText = msg || 'Invalid PIN. Please check the code on the host device.';
        errEl.style.display = 'block';
      }
      if (pinCard) {
        pinCard.classList.remove('shake');
        void pinCard.offsetWidth;
        pinCard.classList.add('shake');
      }
      const pinIn = document.getElementById('pinInput');
      if (pinIn) {
        pinIn.value = '';
        pinIn.focus();
      }
    }

    function submitPIN() {
      const pin = document.getElementById('pinInput').value.trim();
      if (!pin || pin.length < 4) {
        triggerPinShake('Please enter your PIN (at least 4 digits).');
        return;
      }
      setPIN(pin);
      const errEl = document.getElementById('pinError');
      if (errEl) errEl.style.display = 'none';
      loadSharedFiles();
    }

    function setButtonsDisabled(disabled, activeBtn) {
      const btns = document.querySelectorAll('.file-dl-btn, #dlBatchBtn');
      btns.forEach(b => {
        b.disabled = disabled;
        b.style.opacity = disabled ? '0.5' : '1';
        b.style.pointerEvents = disabled ? 'none' : 'auto';
      });
      if (activeBtn) {
        if (disabled) {
          activeBtn.dataset.orig = activeBtn.innerText;
          activeBtn.innerText = '⏳ Loading...';
        } else if (activeBtn.dataset.orig) {
          activeBtn.innerText = activeBtn.dataset.orig;
        }
      }
    }

    let currentSharedItems = [];

    async function loadSharedFiles() {
      if (isDownloading || lockoutInterval) return;
      const pin = getPIN();
      const token = getToken();

      if (!isAuthenticated && !pin && !token) {
        document.getElementById('pinCard').style.display = 'block';
        if (document.getElementById('lockoutCard')) document.getElementById('lockoutCard').style.display = 'none';
        document.getElementById('fileListCard').style.display = 'none';
        document.getElementById('uploadCard').style.display = 'none';
        document.getElementById('hostDeviceName').innerText = '🔒 Protected with PIN';
        return;
      }

      try {
        let q = [];
        if (token) q.push('token=' + encodeURIComponent(token));
        if (pin) q.push('pin=' + encodeURIComponent(pin));
        const url = '/api/share/list' + (q.length ? ('?' + q.join('&')) : '');
        const res = await fetch(url, {
          headers: {
            'X-Share-Token': token,
            'X-Share-PIN': pin
          }
        });

        if (res.status === 429) {
          const data = await res.json().catch(() => ({}));
          showLockoutCountdown(data.retry_after || 60);
          return;
        }

        if (res.status === 401) {
          stopPolling();
          isAuthenticated = false;
          const errData = await res.json().catch(() => ({}));
          document.getElementById('pinCard').style.display = 'block';
          if (document.getElementById('lockoutCard')) document.getElementById('lockoutCard').style.display = 'none';
          document.getElementById('fileListCard').style.display = 'none';
          document.getElementById('uploadCard').style.display = 'none';
          document.getElementById('hostDeviceName').innerText = '🔒 Protected with PIN';

          if (errData.error === 'invalid_pin' || pin || token) {
            triggerPinShake('❌ Invalid PIN or expired session. Please check the code on the host device.');
          }
          return;
        }

        if (!res.ok) {
          return;
        }

        isAuthenticated = true;
        document.getElementById('pinCard').style.display = 'none';
        if (document.getElementById('lockoutCard')) document.getElementById('lockoutCard').style.display = 'none';
        document.getElementById('fileListCard').style.display = 'block';
        document.getElementById('uploadCard').style.display = 'block';
        startPolling();

        const data = await res.json();
        document.getElementById('hostDeviceName').innerText = 'Hosted by ' + (data.device_name || 'Host Device');

        const listEl = document.getElementById('fileList');
        const countBadge = document.getElementById('fileCountBadge');

        const previousStatus = {};
        if (currentSharedItems && currentSharedItems.length > 0) {
          currentSharedItems.forEach(it => {
            if (it.index !== undefined && it.status) previousStatus[it.index] = it.status;
          });
        }

        currentSharedItems = (data.items || []).map((it, idx) => ({
          ...it,
          index: idx,
          status: previousStatus[idx] || 'idle'
        }));

        const batchBox = document.getElementById('batchDlBox');
        if (batchBox) {
          batchBox.style.display = currentSharedItems.length > 1 ? 'flex' : 'none';
        }

        if (!data.items || data.items.length === 0) {
          countBadge.innerText = '0 files';
          listEl.innerHTML = '<li style="text-align: center; color: var(--text-muted); padding: 1rem;">No files currently shared by host.</li>';
          return;
        }

        countBadge.innerText = data.items.length + (data.items.length === 1 ? ' file (' : ' files (') + formatBytes(data.total_bytes) + ')';
        renderFileList();
      } catch (err) {
        console.error(err);
      }
    }

    function renderFileList() {
      const listEl = document.getElementById('fileList');
      if (!listEl) return;
      if (!currentSharedItems || currentSharedItems.length === 0) {
        listEl.innerHTML = '<li style="text-align: center; color: var(--text-muted); padding: 1rem;">No files currently shared by host.</li>';
        return;
      }
      listEl.innerHTML = '';
      currentSharedItems.forEach(it => {
        const li = document.createElement('li');
        li.className = 'file-item';
        const clean = it.name.split('/').pop().split('\\').pop();

        let statusBadge = '';
        let skipToggleBtn = '';
        if (it.status === 'completed') {
          statusBadge = '<span style="color: #34d399; font-size: 0.72rem; font-weight: 600;">✓ Saved</span>';
        } else if (it.status === 'downloading') {
          statusBadge = '<span style="color: #60a5fa; font-size: 0.72rem; font-weight: 600;">⚡ Active</span>';
          skipToggleBtn = '<button type="button" class="btn" style="width: auto; padding: 2px 7px; font-size: 0.72rem; background: #e11d48; color: #fff; border-radius: 4px;" onclick="toggleFileSkip(' + it.index + ')">✕ Skip</button>';
        } else if (it.status === 'skipped') {
          statusBadge = '<span style="color: #94a3b8; font-size: 0.72rem; text-decoration: line-through;">⊘ Skipped</span>';
          skipToggleBtn = '<button type="button" class="btn" style="width: auto; padding: 2px 7px; font-size: 0.72rem; background: #334155; color: #93c5fd; border-radius: 4px;" onclick="toggleFileSkip(' + it.index + ')">↩ Keep</button>';
        } else if (it.status === 'pending') {
          statusBadge = '<span style="color: var(--text-muted); font-size: 0.72rem;">⏳ Queued</span>';
          skipToggleBtn = '<button type="button" class="btn" style="width: auto; padding: 2px 7px; font-size: 0.72rem; background: #334155; color: #fca5a5; border-radius: 4px;" onclick="toggleFileSkip(' + it.index + ')">✕ Skip</button>';
        } else {
          skipToggleBtn = '<button type="button" class="btn" style="width: auto; padding: 2px 7px; font-size: 0.72rem; background: #1e293b; color: #94a3b8; border: 1px solid #334155; border-radius: 4px;" onclick="toggleFileSkip(' + it.index + ')" title="Exclude from batch download">✕ Skip</button>';
        }

        const nameStyle = it.status === 'skipped' ? 'text-decoration: line-through; opacity: 0.55;' : '';

        li.innerHTML =
          '<span class="file-name" style="' + nameStyle + '">' + clean + '</span>' +
          '<div style="display:flex;align-items:center;gap:6px;">' +
            '<span class="file-size">' + formatBytes(it.size) + '</span>' +
            statusBadge +
            skipToggleBtn +
            '<button class="file-dl-btn" onclick="downloadWithProgress(\'' + it.url + '\', \'' + clean + '\', this, null, ' + (it.size || 0) + ')">Get</button>' +
          '</div>';
        listEl.appendChild(li);
      });
    }

    function renderBatchQueue() {
      const qList = document.getElementById("dlBatchQueueList");
      const qCount = document.getElementById("dlBatchQueueCount");
      if (!qList) return;
      if (!currentSharedItems || currentSharedItems.length === 0) {
        qList.innerHTML = '';
        return;
      }
      if (qCount) qCount.innerText = currentSharedItems.length;

      qList.innerHTML = '';
      currentSharedItems.forEach(it => {
        const row = document.createElement("div");
        row.style = "display: flex; justify-content: space-between; align-items: center; padding: 4px 6px; border-bottom: 1px solid rgba(255,255,255,0.05);";

        const clean = it.name.split('/').pop().split('\\').pop();
        let statusPill = '<span style="color:var(--text-muted); font-size:0.68rem;">⏳ Pending</span>';
        let actionBtn = '<button type="button" class="btn" style="width:auto; padding:2px 6px; font-size:0.68rem; background:#334155; color:#fca5a5; border-radius:4px;" onclick="toggleFileSkip(' + it.index + ')">✕ Skip</button>';

        if (it.status === 'completed') {
          statusPill = '<span style="color:#34d399; font-size:0.68rem; font-weight:600;">✓ Done</span>';
          actionBtn = '';
        } else if (it.status === 'downloading') {
          statusPill = '<span style="color:#60a5fa; font-weight:600; font-size:0.68rem;">⚡ Active</span>';
          actionBtn = '<button type="button" class="btn" style="width:auto; padding:2px 6px; font-size:0.68rem; background:#e11d48; color:#fff; border-radius:4px;" onclick="skipCurrentBatchFile()">✕ Skip</button>';
        } else if (it.status === 'skipped') {
          statusPill = '<span style="color:#94a3b8; text-decoration:line-through; font-size:0.68rem;">⊘ Skipped</span>';
          actionBtn = '<button type="button" class="btn" style="width:auto; padding:2px 6px; font-size:0.68rem; background:#1e293b; color:#93c5fd; border:1px solid #334155; border-radius:4px;" onclick="toggleFileSkip(' + it.index + ')">↩ Keep</button>';
        }

        const nameStyle = it.status === 'skipped' ? 'text-decoration: line-through; opacity: 0.55;' : '';

        row.innerHTML =
          '<div style="overflow:hidden; text-overflow:ellipsis; white-space:nowrap; max-width:60%; ' + nameStyle + '">' +
            '<strong>#' + (it.index + 1) + '</strong> ' + clean + ' <small style="color:var(--text-muted)">(' + formatBytes(it.size) + ')</small>' +
          '</div>' +
          '<div style="display:flex; gap:6px; align-items:center;">' +
            statusPill +
            actionBtn +
          '</div>';
        qList.appendChild(row);
      });
    }

    function updateBatchProgressStats() {
      if (!currentSharedItems) return;
      batchTotalBytes = 0;
      batchCompletedBytes = 0;

      currentSharedItems.forEach((it, idx) => {
        batchTotalBytes += (it.size || 0);
        if (it.status === 'completed' || it.status === 'skipped') {
          batchCompletedBytes += (it.size || 0);
        } else if (idx === batchCurrentFileIdx && isDownloading) {
          batchCompletedBytes += dlReceivedBytes;
        }
      });

      const bBar = document.getElementById("dlBatchProgressBar");
      const bPctText = document.getElementById("dlBatchPercent");
      const bBytesText = document.getElementById("dlBatchBytes");
      const bRemText = document.getElementById("dlBatchRemaining");

      const bPct = batchTotalBytes > 0 ? Math.min(100, Math.round((batchCompletedBytes / batchTotalBytes) * 100)) : 100;
      if (bBar) bBar.style.width = bPct + "%";
      if (bPctText) bPctText.innerText = bPct + "%";
      if (bBytesText) bBytesText.innerText = formatBytes(batchCompletedBytes) + " / " + formatBytes(batchTotalBytes);
      if (bRemText) {
        const rem = Math.max(0, batchTotalBytes - batchCompletedBytes);
        bRemText.innerText = "Remaining: " + formatBytes(rem);
      }
    }

    function toggleFileSkip(index) {
      if (!currentSharedItems || !currentSharedItems[index]) return;
      const item = currentSharedItems[index];

      if (item.status === 'downloading') {
        skipCurrentBatchFile();
        return;
      }

      if (item.status === 'skipped') {
        item.status = isBatchDownloading ? 'pending' : 'idle';
      } else {
        item.status = 'skipped';
      }

      renderFileList();
      renderBatchQueue();

      if (isBatchDownloading) {
        updateBatchProgressStats();
      }
    }

    function dismissDlProgress() {
      if (dlTimeout) { clearTimeout(dlTimeout); dlTimeout = null; }
      const card = document.getElementById("dlProgressCard");
      if (card) card.style.display = "none";
      const bar = document.getElementById("dlProgressBar");
      if (bar) bar.style.width = "0%";
      const pct = document.getElementById("dlPercent");
      if (pct) pct.innerText = "0%";
      const batchBox = document.getElementById("dlBatchProgressBox");
      if (batchBox) batchBox.style.display = "none";
      const skipBtn = document.getElementById("dlSkipBtn");
      if (skipBtn) skipBtn.style.display = "none";
      const fileIdx = document.getElementById("dlFileIndex");
      if (fileIdx) fileIdx.style.display = "none";
    }

    function dismissUlProgress() {
      if (ulTimeout) { clearTimeout(ulTimeout); ulTimeout = null; }
      const card = document.getElementById("ulProgressCard");
      if (card) card.style.display = "none";
      const bar = document.getElementById("ulProgressBar");
      if (bar) bar.style.width = "0%";
      const pct = document.getElementById("ulPercent");
      if (pct) pct.innerText = "0%";
    }

    let dlAbortController = null;
    let dlPaused = false;
    let dlCurrentUrl = "";
    let dlCurrentFilename = "";
    let dlReceivedBytes = 0;
    let dlTotalBytes = 0;
    let dlExpectedBytes = 0;
    let dlChunks = [];
    let dlActiveBtn = null;
    let dlResolvePromise = null;

    let isBatchDownloading = false;
    let batchCancelRequested = false;
    let batchSkipCurrentFile = false;
    let batchCurrentFileIdx = -1;
    let batchTotalBytes = 0;
    let batchCompletedBytes = 0;

    let ulAbortController = null;
    let ulPaused = false;
    let ulFilesQueue = [];
    let ulCurrentFileIdx = 0;
    let ulCurrentChunkIdx = 0;
    let ulTotalBytes = 0;
    let ulCurrentTicket = "";

    function downloadWithProgress(url, filename, btnEl, label, expectedTotalBytes) {
      if (isDownloading) return Promise.resolve(false);
      isDownloading = true;
      dlPaused = false;
      dlCurrentUrl = url;
      dlCurrentFilename = filename;
      dlActiveBtn = btnEl;
      dlReceivedBytes = 0;
      dlTotalBytes = 0;
      dlExpectedBytes = expectedTotalBytes || 0;
      dlChunks = [];
      setButtonsDisabled(true, btnEl);
      if (dlTimeout) { clearTimeout(dlTimeout); dlTimeout = null; }

      const card = document.getElementById("dlProgressCard");
      const nameText = document.getElementById("dlFileName");
      const bar = document.getElementById("dlProgressBar");
      const pctText = document.getElementById("dlPercent");
      const bytesText = document.getElementById("dlBytes");
      const speedText = document.getElementById("dlSpeed");
      const pBtn = document.getElementById("dlPauseBtn");

      card.style.display = "block";
      nameText.innerText = label || ("Downloading " + filename + "...");
      bar.style.width = "0%";
      pctText.innerText = "0%";
      bytesText.innerText = dlExpectedBytes > 0 ? ("0 B / " + formatBytes(dlExpectedBytes)) : "0 B";
      speedText.innerText = "0.0 MB/s";
      if (pBtn) {
        pBtn.innerText = "⏸️ Pause";
        pBtn.style.background = "#334155";
      }

      return new Promise((resolve) => {
        dlResolvePromise = resolve;
        startOrResumeDownload();
      });
    }

    function skipCurrentBatchFile() {
      if (!isBatchDownloading || !isDownloading) return;
      batchSkipCurrentFile = true;
      if (dlAbortController) {
        dlAbortController.abort();
      }
    }

    async function downloadAllBatch() {
      if (isDownloading || isBatchDownloading) {
        alert("A download is already in progress!");
        return;
      }
      if (!currentSharedItems || currentSharedItems.length === 0) return;

      isBatchDownloading = true;
      batchCancelRequested = false;
      batchSkipCurrentFile = false;
      batchCurrentFileIdx = -1;

      // Mark all non-skipped files as pending
      currentSharedItems.forEach(it => {
        if (it.status !== 'skipped' && it.status !== 'completed') {
          it.status = 'pending';
        }
      });

      const batchBtn = document.getElementById("dlBatchBtn");
      if (batchBtn) {
        batchBtn.disabled = true;
        batchBtn.innerText = "⏳ Downloading batch...";
      }

      const batchBox = document.getElementById("dlBatchProgressBox");
      const skipBtn = document.getElementById("dlSkipBtn");
      const fileIdxSpan = document.getElementById("dlFileIndex");
      if (batchBox) batchBox.style.display = "block";
      if (skipBtn) skipBtn.style.display = "inline-block";
      if (fileIdxSpan) fileIdxSpan.style.display = "inline";

      renderBatchQueue();
      renderFileList();
      updateBatchProgressStats();

      try {
        for (let i = 0; i < currentSharedItems.length; i++) {
          if (!isBatchDownloading || batchCancelRequested) break;
          batchCurrentFileIdx = i;
          const item = currentSharedItems[i];
          const clean = item.name.split('/').pop().split('\\').pop();

          if (item.status === 'skipped') {
            updateBatchProgressStats();
            renderBatchQueue();
            renderFileList();
            continue;
          }

          item.status = 'downloading';
          renderBatchQueue();
          renderFileList();

          if (fileIdxSpan) fileIdxSpan.innerText = "File " + (i + 1) + " of " + currentSharedItems.length;

          const progressLabel = "File " + (i + 1) + " of " + currentSharedItems.length + ": " + clean;
          const result = await downloadWithProgress(item.url, clean, null, progressLabel, item.size || 0);

          if (batchCancelRequested) {
            break;
          }

          if (result && result.skipped) {
            item.status = 'skipped';
          } else if (result && result.ok) {
            item.status = 'completed';
          }

          renderBatchQueue();
          renderFileList();
          updateBatchProgressStats();

          if (i < currentSharedItems.length - 1 && isBatchDownloading && !batchCancelRequested) {
            await new Promise(r => setTimeout(r, 350));
          }
        }

        if (isBatchDownloading && !batchCancelRequested) {
          const nameText = document.getElementById("dlFileName");
          const speedText = document.getElementById("dlSpeed");
          const completedCount = currentSharedItems.filter(it => it.status === 'completed').length;
          const skippedCount = currentSharedItems.filter(it => it.status === 'skipped').length;
          if (nameText) nameText.innerText = "✓ Finished: " + completedCount + " saved" + (skippedCount > 0 ? (", " + skippedCount + " skipped") : "");
          if (speedText) speedText.innerText = "✓ Done";
          const bBar = document.getElementById("dlBatchProgressBar");
          const bPctText = document.getElementById("dlBatchPercent");
          if (bBar) bBar.style.width = "100%";
          if (bPctText) bPctText.innerText = "100%";
          dlTimeout = setTimeout(dismissDlProgress, 4000);
        }
      } finally {
        isBatchDownloading = false;
        if (batchBtn) {
          batchBtn.disabled = false;
          batchBtn.innerText = "⬇️ Download All (Automatic)";
        }
        if (skipBtn) skipBtn.style.display = "none";
        renderFileList();
      }
    }

    async function startOrResumeDownload() {
      const pin = getPIN();
      const token = getToken();
      let q = [];
      if (token) q.push('token=' + encodeURIComponent(token));
      if (pin) q.push('pin=' + encodeURIComponent(pin));
      const downloadUrl = dlCurrentUrl + (dlCurrentUrl.includes('?') ? '&' : '?') + q.join('&');

      const bar = document.getElementById("dlProgressBar");
      const bytesText = document.getElementById("dlBytes");
      const pctText = document.getElementById("dlPercent");
      const speedText = document.getElementById("dlSpeed");
      const nameText = document.getElementById("dlFileName");

      dlAbortController = new AbortController();
      let lastDlBytes = dlReceivedBytes;
      let lastDlTime = Date.now();
      let dlSmoothedSpeed = 0;

      try {
        const headers = {
          'X-Share-PIN': pin,
          'X-Share-Token': token
        };
        if (dlReceivedBytes > 0) {
          headers['Range'] = 'bytes=' + dlReceivedBytes + '-';
        }

        const res = await fetch(downloadUrl, {
          headers: headers,
          signal: dlAbortController.signal
        });

        if (!res.ok) {
          if (res.status === 429) {
            const data = await res.json().catch(() => ({}));
            showLockoutCountdown(data.retry_after || 60);
            throw new Error("Too many attempts. Locked out.");
          }
          if (res.status === 401) {
            triggerPinShake('❌ Session expired or invalid PIN. Please enter PIN again.');
            throw new Error("Invalid or missing PIN");
          }
          if (res.status === 404) {
            throw new Error("File not found or no files currently shared");
          }
          throw new Error("HTTP " + res.status);
        }

        if (dlReceivedBytes === 0) {
          const totalHeader = res.headers.get("X-Total-Bytes") || res.headers.get("Content-Length");
          dlTotalBytes = parseInt(totalHeader || "0", 10);
          if ((!dlTotalBytes || isNaN(dlTotalBytes)) && dlExpectedBytes > 0) {
            dlTotalBytes = dlExpectedBytes;
          }
        } else if (res.status === 206) {
          const cr = res.headers.get("Content-Range");
          if (cr && cr.includes('/')) {
            dlTotalBytes = parseInt(cr.split('/')[1] || "0", 10);
          }
        }

        const reader = res.body.getReader();
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          dlChunks.push(value);
          dlReceivedBytes += value.length;

          const now = Date.now();
          const timeDelta = (now - lastDlTime) / 1000;
          if (timeDelta >= 0.25 || (dlTotalBytes > 0 && dlReceivedBytes >= dlTotalBytes)) {
            const bytesDelta = dlReceivedBytes - lastDlBytes;
            if (timeDelta > 0 && bytesDelta > 0) {
              const instantBytesPerSec = bytesDelta / timeDelta;
              dlSmoothedSpeed = dlSmoothedSpeed === 0 ? instantBytesPerSec : (0.7 * dlSmoothedSpeed + 0.3 * instantBytesPerSec);
              speedText.innerText = formatSpeed(dlSmoothedSpeed);
            }
            lastDlBytes = dlReceivedBytes;
            lastDlTime = now;

            if (dlTotalBytes > 0) {
              const pct = Math.min(100, Math.round((dlReceivedBytes / dlTotalBytes) * 100));
              bar.style.width = pct + "%";
              pctText.innerText = pct + "%";
              bytesText.innerText = formatBytes(dlReceivedBytes) + " / " + formatBytes(dlTotalBytes);
            } else {
              bytesText.innerText = formatBytes(dlReceivedBytes);
            }

            if (isBatchDownloading) {
              updateBatchProgressStats();
            }
          }
        }

        // Complete!
        const blob = new Blob(dlChunks);
        const blobUrl = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = blobUrl;
        a.download = dlCurrentFilename;
        document.body.appendChild(a);
        a.click();
        a.remove();
        setTimeout(() => URL.revokeObjectURL(blobUrl), 10000);

        bar.style.width = "100%";
        pctText.innerText = "✓ 100%";
        nameText.innerText = "✓ Saved " + dlCurrentFilename + "!";
        speedText.innerText = "✓ Done";
        isDownloading = false;
        setButtonsDisabled(false, dlActiveBtn);
        if (!isBatchDownloading) {
          dlTimeout = setTimeout(dismissDlProgress, 2500);
        }
        if (dlResolvePromise) {
          const res = dlResolvePromise;
          dlResolvePromise = null;
          res({ ok: true, bytes: dlReceivedBytes });
        }
      } catch (err) {
        if (batchSkipCurrentFile) {
          batchSkipCurrentFile = false;
          nameText.innerText = "⏭️ Skipped " + dlCurrentFilename;
          speedText.innerText = "⏭️ Skipped";
          isDownloading = false;
          setButtonsDisabled(false, dlActiveBtn);
          if (dlResolvePromise) {
            const res = dlResolvePromise;
            dlResolvePromise = null;
            res({ skipped: true, bytes: dlReceivedBytes });
          }
          return;
        }
        if (err.name === 'AbortError' || dlPaused) {
          return;
        }
        nameText.innerText = "⚠️ Download error: " + err.message;
        isDownloading = false;
        setButtonsDisabled(false, dlActiveBtn);
        if (!isBatchDownloading) {
          dlTimeout = setTimeout(dismissDlProgress, 3500);
        }
        if (dlResolvePromise) {
          const res = dlResolvePromise;
          dlResolvePromise = null;
          res({ ok: false, error: err.message });
        }
      }
    }

    function toggleDlPause() {
      if (!isDownloading) return;
      const pin = getPIN();
      const token = getToken();
      let q = [];
      if (token) q.push('token=' + encodeURIComponent(token));
      if (pin) q.push('pin=' + encodeURIComponent(pin));
      const authQ = q.length ? ('?' + q.join('&')) : '';

      const pBtn = document.getElementById("dlPauseBtn");
      const speedText = document.getElementById("dlSpeed");
      const nameText = document.getElementById("dlFileName");

      if (!dlPaused) {
        dlPaused = true;
        if (dlAbortController) dlAbortController.abort();
        if (pBtn) {
          pBtn.innerText = "▶️ Resume";
          pBtn.style.background = "#10b981";
        }
        speedText.innerText = "⏸️ Paused";
        nameText.innerText = "⏸️ Paused: " + dlCurrentFilename;
        fetch('/api/share/pause' + authQ, { method: 'POST' }).catch(() => {});
      } else {
        dlPaused = false;
        if (pBtn) {
          pBtn.innerText = "⏸️ Pause";
          pBtn.style.background = "#334155";
        }
        speedText.innerText = "Resuming...";
        nameText.innerText = "Downloading " + dlCurrentFilename + "...";
        fetch('/api/share/resume' + authQ, { method: 'POST' }).catch(() => {});
        startOrResumeDownload();
      }
    }

    function stopDlTransfer() {
      isBatchDownloading = false;
      batchCancelRequested = true;
      batchSkipCurrentFile = false;
      batchCurrentFileIdx = -1;
      const batchBtn = document.getElementById("dlBatchBtn");
      if (batchBtn) {
        batchBtn.disabled = false;
        batchBtn.innerText = "⬇️ Download All (Automatic)";
      }
      const skipBtn = document.getElementById("dlSkipBtn");
      if (skipBtn) skipBtn.style.display = "none";
      const fileIdx = document.getElementById("dlFileIndex");
      if (fileIdx) fileIdx.style.display = "none";

      const pin = getPIN();
      const token = getToken();
      let q = [];
      if (token) q.push('token=' + encodeURIComponent(token));
      if (pin) q.push('pin=' + encodeURIComponent(pin));
      const authQ = q.length ? ('?' + q.join('&')) : '';

      dlPaused = false;
      fetch('/api/share/cancel' + authQ, {
        method: 'POST',
        headers: {
          'X-Share-Token': token,
          'X-Share-PIN': pin
        }
      }).catch(() => {});
      if (dlAbortController) dlAbortController.abort();
      dlChunks = [];
      dlReceivedBytes = 0;
      isDownloading = false;
      setButtonsDisabled(false, dlActiveBtn);
      dismissDlProgress();
      if (dlResolvePromise) {
        const res = dlResolvePromise;
        dlResolvePromise = null;
        res({ ok: false, canceled: true });
      }
      renderFileList();
    }

    async function uploadFiles(files) {
      if (!files || files.length === 0) return;
      if (isUploading) {
        alert("An upload is already in progress!");
        return;
      }
      isUploading = true;
      ulPaused = false;
      if (ulTimeout) { clearTimeout(ulTimeout); ulTimeout = null; }

      const card = document.getElementById("ulProgressCard");
      const bar = document.getElementById("ulProgressBar");
      const bytesText = document.getElementById("ulBytes");
      const pctText = document.getElementById("ulPercent");
      const speedText = document.getElementById("ulSpeed");
      const nameText = document.getElementById("ulFileName");
      const pBtn = document.getElementById("ulPauseBtn");

      card.style.display = "block";
      nameText.innerText = "⏳ Asking permission on host device...";
      bar.style.width = "0%";
      pctText.innerText = "Waiting...";
      bytesText.innerText = "0 MB";
      speedText.innerText = "";
      if (pBtn) {
        pBtn.innerText = "⏸️ Pause";
        pBtn.style.background = "#334155";
      }

      const pin = getPIN();
      const token = getToken();
      let q = [];
      if (token) q.push('token=' + encodeURIComponent(token));
      if (pin) q.push('pin=' + encodeURIComponent(pin));
      const reqUrl = '/api/share/request_upload?' + q.join('&');

      ulFilesQueue = Array.from(files);
      ulCurrentFileIdx = 0;
      ulCurrentChunkIdx = 0;
      ulTotalBytes = 0;
      ulFilesQueue.forEach(f => { ulTotalBytes += f.size; });
      ulLastSpeedTime = Date.now();
      ulLastSpeedBytes = 0;
      ulSmoothedSpeed = 0;

      const metaFiles = ulFilesQueue.map(f => ({ name: f.name, size: f.size }));

      try {
        const reqRes = await fetch(reqUrl, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'X-Share-PIN': pin,
            'X-Share-Token': token
          },
          body: JSON.stringify({ files: metaFiles })
        });

        if (!reqRes.ok) {
          if (reqRes.status === 429) {
            const data = await reqRes.json().catch(() => ({}));
            showLockoutCountdown(data.retry_after || 60);
          } else if (reqRes.status === 401) {
            triggerPinShake('❌ Session expired or invalid PIN. Please enter PIN again.');
          }
          const errData = await reqRes.json().catch(() => ({}));
          throw new Error(errData.error || ("Host refused upload (HTTP " + reqRes.status + ")"));
        }

        const approveData = await reqRes.json();
        ulCurrentTicket = approveData.ticket || "";
      } catch (err) {
        isUploading = false;
        const uploadInput = document.getElementById("mobileUpload");
        if (uploadInput) uploadInput.value = "";
        nameText.innerText = "❌ " + err.message;
        ulTimeout = setTimeout(dismissUlProgress, 4000);
        return;
      }

      nameText.innerText = "Uploading " + ulFilesQueue.length + " file(s) to host...";
      pctText.innerText = "0%";
      await uploadNextChunk();
    }

    async function uploadNextChunk() {
      if (!isUploading || ulPaused) return;

      if (ulCurrentFileIdx >= ulFilesQueue.length) {
        // Finished all files!
        isUploading = false;
        const uploadInput = document.getElementById("mobileUpload");
        if (uploadInput) uploadInput.value = "";
        const bar = document.getElementById("ulProgressBar");
        const pctText = document.getElementById("ulPercent");
        const nameText = document.getElementById("ulFileName");
        bar.style.width = "100%";
        pctText.innerText = "✓ 100%";
        nameText.innerText = "✓ Successfully sent " + ulFilesQueue.length + " file(s) to host!";
        ulTimeout = setTimeout(dismissUlProgress, 2500);
        return;
      }

      const file = ulFilesQueue[ulCurrentFileIdx];
      const CHUNK_SIZE = 2 * 1024 * 1024; // 2 MB slice per chunk
      const totalChunks = Math.max(1, Math.ceil(file.size / CHUNK_SIZE));
      const start = ulCurrentChunkIdx * CHUNK_SIZE;
      const end = Math.min(file.size, start + CHUNK_SIZE);
      const chunkBlob = file.slice(start, end);

      const pin = getPIN();
      const token = getToken();
      let q = [];
      if (token) q.push('token=' + encodeURIComponent(token));
      if (pin) q.push('pin=' + encodeURIComponent(pin));
      q.push('ticket=' + encodeURIComponent(ulCurrentTicket));
      q.push('name=' + encodeURIComponent(file.name));
      q.push('chunk_index=' + ulCurrentChunkIdx);
      q.push('total_chunks=' + totalChunks);
      q.push('offset=' + start);
      q.push('total_size=' + file.size);

      const chunkUrl = '/api/share/upload_chunk?' + q.join('&');
      ulAbortController = new AbortController();

      try {
        const res = await fetch(chunkUrl, {
          method: 'POST',
          headers: {
            'X-Upload-Ticket': ulCurrentTicket,
            'X-Share-PIN': pin,
            'X-Share-Token': token,
            'Content-Type': 'application/octet-stream'
          },
          body: chunkBlob,
          signal: ulAbortController.signal
        });

        if (!res.ok) {
          throw new Error("Chunk upload failed (HTTP " + res.status + ")");
        }

        const bar = document.getElementById("ulProgressBar");
        const bytesText = document.getElementById("ulBytes");
        const pctText = document.getElementById("ulPercent");
        const speedText = document.getElementById("ulSpeed");

        let overallUploaded = 0;
        for (let i = 0; i < ulCurrentFileIdx; i++) {
          overallUploaded += ulFilesQueue[i].size;
        }
        overallUploaded += end;

        const pct = ulTotalBytes > 0 ? Math.min(99, Math.round((overallUploaded / ulTotalBytes) * 100)) : 0;
        bar.style.width = pct + "%";
        pctText.innerText = pct + "%";
        bytesText.innerText = formatBytes(overallUploaded) + " / " + formatBytes(ulTotalBytes);

        const now = Date.now();
        const timeDelta = (now - ulLastSpeedTime) / 1000;
        const bytesDelta = overallUploaded - ulLastSpeedBytes;
        if (timeDelta > 0 && bytesDelta > 0) {
          const instantBytesPerSec = bytesDelta / timeDelta;
          ulSmoothedSpeed = ulSmoothedSpeed === 0 ? instantBytesPerSec : (0.7 * ulSmoothedSpeed + 0.3 * instantBytesPerSec);
          speedText.innerText = formatSpeed(ulSmoothedSpeed);
        }
        ulLastSpeedTime = now;
        ulLastSpeedBytes = overallUploaded;

        if (end >= file.size) {
          // File completed!
          ulCurrentFileIdx++;
          ulCurrentChunkIdx = 0;
        } else {
          ulCurrentChunkIdx++;
        }

        if (!ulPaused) {
          setTimeout(uploadNextChunk, 10);
        }
      } catch (err) {
        if (err.name === 'AbortError' || ulPaused) {
          return;
        }
        isUploading = false;
        const nameText = document.getElementById("ulFileName");
        nameText.innerText = "⚠️ Upload error: " + err.message;
        ulTimeout = setTimeout(dismissUlProgress, 3500);
      }
    }

    function toggleUlPause() {
      if (!isUploading) return;
      const pin = getPIN();
      const token = getToken();
      let q = [];
      if (token) q.push('token=' + encodeURIComponent(token));
      if (pin) q.push('pin=' + encodeURIComponent(pin));
      const authQ = q.length ? ('?' + q.join('&')) : '';

      const pBtn = document.getElementById("ulPauseBtn");
      const speedText = document.getElementById("ulSpeed");
      const nameText = document.getElementById("ulFileName");

      if (!ulPaused) {
        ulPaused = true;
        if (ulAbortController) ulAbortController.abort();
        if (pBtn) {
          pBtn.innerText = "▶️ Resume";
          pBtn.style.background = "#10b981";
        }
        speedText.innerText = "⏸️ Paused";
        nameText.innerText = "⏸️ Upload Paused";
        fetch('/api/share/pause' + authQ, { method: 'POST' }).catch(() => {});
      } else {
        ulPaused = false;
        if (pBtn) {
          pBtn.innerText = "⏸️ Pause";
          pBtn.style.background = "#334155";
        }
        speedText.innerText = "Resuming...";
        nameText.innerText = "Uploading to host...";
        fetch('/api/share/resume' + authQ, { method: 'POST' }).catch(() => {});
        uploadNextChunk();
      }
    }

    function stopUlTransfer() {
      const pin = getPIN();
      const token = getToken();
      let q = [];
      if (token) q.push('token=' + encodeURIComponent(token));
      if (pin) q.push('pin=' + encodeURIComponent(pin));
      const authQ = q.length ? ('?' + q.join('&')) : '';

      ulPaused = false;
      isUploading = false;
      fetch('/api/share/cancel' + authQ, {
        method: 'POST',
        headers: {
          'X-Share-Token': token,
          'X-Share-PIN': pin
        }
      }).catch(() => {});
      if (ulAbortController) ulAbortController.abort();
      const uploadInput = document.getElementById("mobileUpload");
      if (uploadInput) uploadInput.value = "";
      dismissUlProgress();
    }

    loadSharedFiles();
  </script>
</body>
</html>
`

const ShareDisabledHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>medXfer - Web Share Disabled</title>
  <style>
    :root {
      --bg: #0b0f19;
      --card: #131d31;
      --border: #1e293b;
      --danger: #ef4444;
      --text: #f8fafc;
      --muted: #94a3b8;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
    body {
      background: var(--bg);
      color: var(--text);
      min-height: 100vh;
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 1.5rem;
      text-align: center;
    }
    .card {
      background: var(--card);
      border: 1px solid var(--border);
      border-radius: 16px;
      padding: 2.5rem 1.5rem;
      max-width: 440px;
      width: 100%;
      box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.5);
    }
    .badge {
      display: inline-block;
      background: #2d0606;
      color: #f87171;
      border: 1px solid #ef4444;
      padding: 4px 14px;
      border-radius: 9999px;
      font-size: 0.75rem;
      font-weight: 700;
      letter-spacing: 0.05em;
      margin-bottom: 1.25rem;
    }
    h1 {
      font-size: 1.35rem;
      margin-bottom: 0.75rem;
      color: #f8fafc;
    }
    p {
      color: var(--muted);
      font-size: 0.9rem;
      line-height: 1.5;
      margin-bottom: 1.5rem;
    }
    .icon {
      font-size: 3rem;
      margin-bottom: 0.5rem;
    }
    .refresh-btn {
      background: #1e293b;
      color: #93c5fd;
      border: 1px solid #3b82f6;
      padding: 8px 18px;
      font-size: 0.85rem;
      border-radius: 8px;
      font-weight: 600;
      cursor: pointer;
      text-decoration: none;
      display: inline-block;
      transition: all 0.2s;
    }
    .refresh-btn:hover {
      background: #2563eb;
      color: white;
    }
  </style>
</head>
<body>
  <div class="card">
    <div class="icon">🔒</div>
    <div class="badge">ACCESS DISABLED</div>
    <h1>Web Share is Turned Off</h1>
    <p>Web Share is currently disabled on the host device. Ask the host to enable Web Share from the medXfer dashboard or mobile app to access files.</p>
    <button class="refresh-btn" onclick="window.location.reload()">🔄 Refresh Page</button>
  </div>
</body>
</html>
`
