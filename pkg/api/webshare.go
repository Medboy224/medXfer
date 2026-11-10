package api

import (
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/webshare"
)

// Web Share v2 (bible chapter 11, J7): the host's interface starts a guest session, shows its
// QR code and PIN, answers the guest's uploads and stops it. One session at a time; it has its
// own listener (pkg/webshare, WEB-01), never this server's.

// WebShareStartPayload is web_share_start's.
type WebShareStartPayload struct {
	Paths []string `json:"paths"` // what the guest may download
	// Items: files open already, as send takes them (Android, STO-16). The core owns their
	// descriptors from the call on, and closes them when the session ends.
	Items      []SendItem `json:"items,omitempty"`
	Receive    bool       `json:"receive"`     // the guest may send, into the download folder
	ReceiveDir string     `json:"receive_dir"` // another folder than the download folder
	AutoAccept bool       `json:"auto_accept"` // the guest's uploads without asking (WEB-06)
	Addr       string     `json:"addr"`        // the interface's address; the one status shows when ""
	Port       int        `json:"port"`        // 19999 when 0 (decision D5 of the J7 plan)
}

// WebShareTicketPayload is web_share_answer's and web_share_cancel's.
type WebShareTicketPayload struct {
	Ticket string `json:"ticket"`
	Accept bool   `json:"accept"`
}

type webShareHost struct {
	mu   sync.Mutex
	sess *webshare.Session
}

func (s *DaemonServer) webShareSession() *webshare.Session {
	s.webShare.mu.Lock()
	defer s.webShare.mu.Unlock()
	return s.webShare.sess
}

// handleWebShareStart: a running session ends first. The answer, web_share_started, carries the
// token and the PIN for the host's screen only (WEB-04): both are masked in the journal from
// their drawing on.
func (s *DaemonServer) handleWebShareStart(rs Responder, req RequestMessage) {
	var p WebShareStartPayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid web_share_start payload"}, req.ID))
			return
		}
	}
	var files []webshare.OpenFile
	for _, it := range p.Items {
		if it.FD < 0 {
			continue
		}
		files = append(files, webshare.OpenFile{File: os.NewFile(uintptr(it.FD), it.Name), Name: it.Name})
	}
	s.stopWebShare()
	if p.Addr == "" {
		p.Addr = statusAddress()
	}
	dir := p.ReceiveDir
	if dir == "" && p.Receive {
		s.mu.RLock()
		dir = s.config.DownloadDir
		s.mu.RUnlock()
	}
	name := s.instance()
	if name == "" {
		name = "medXfer"
	}
	sess, err := webshare.Start(webshare.Options{
		Addr:       p.Addr,
		Port:       p.Port,
		Name:       name,
		Paths:      p.Paths,
		Files:      files,
		ReceiveDir: dir,
		AutoAccept: p.AutoAccept,
		Events:     s.relayWebShare,
		Logf:       log.Printf,
	})
	if err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
		return
	}
	s.webShare.mu.Lock()
	s.webShare.sess = sess
	s.webShare.mu.Unlock()
	rs.Reply(NewEvent("web_share_started", map[string]any{
		"url":         sess.URL(),
		"token":       sess.Token(),
		"pin":         sess.PIN(),
		"fingerprint": sess.Fingerprint(),
		"addr":        p.Addr,
		"port":        sess.Port(),
		"receive":     dir != "",
		"expires":     sess.Expires().UTC().Format(time.RFC3339),
	}, req.ID))
}

// handleWebShareStop: web_share_closed follows, reason "stopped".
func (s *DaemonServer) handleWebShareStop(rs Responder, req RequestMessage) {
	s.stopWebShare()
}

func (s *DaemonServer) stopWebShare() {
	s.webShare.mu.Lock()
	sess := s.webShare.sess
	s.webShare.sess = nil
	s.webShare.mu.Unlock()
	if sess != nil {
		sess.Close()
	}
}

// handleWebShareAnswer: the host accepts or refuses the guest's upload.
func (s *DaemonServer) handleWebShareAnswer(rs Responder, req RequestMessage) {
	var p WebShareTicketPayload
	sess := s.webShareSession()
	if err := json.Unmarshal(req.Payload, &p); err != nil || p.Ticket == "" || sess == nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "no such Web Share upload"}, req.ID))
		return
	}
	if err := sess.Answer(p.Ticket, p.Accept); err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
	}
}

// handleWebShareCancel: the host stops the guest's upload; the incomplete files are removed.
func (s *DaemonServer) handleWebShareCancel(rs Responder, req RequestMessage) {
	var p WebShareTicketPayload
	sess := s.webShareSession()
	if err := json.Unmarshal(req.Payload, &p); err != nil || p.Ticket == "" || sess == nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "no such Web Share upload"}, req.ID))
		return
	}
	if err := sess.CancelUpload(p.Ticket); err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
	}
}

// relayWebShare gives the session's events to every interface, as web_share_* (never the token
// nor the PIN: pkg/webshare puts none in them). Its end lets the next session start.
func (s *DaemonServer) relayWebShare(e webshare.Event) {
	switch e.Kind {
	case "session_closed":
		s.webShare.mu.Lock()
		if s.webShare.sess != nil && s.webShare.sess.Closed() != "" {
			s.webShare.sess = nil
		}
		s.webShare.mu.Unlock()
		s.Broadcast(NewEvent("web_share_closed", e.Data))
	case "download_started":
		s.Broadcast(NewEvent("web_share_download_started", e.Data))
	case "download_done":
		s.Broadcast(NewEvent("web_share_download_done", e.Data))
	case "upload_request":
		s.Broadcast(NewEvent("web_share_upload_request", e.Data))
	case "upload_accepted":
		s.Broadcast(NewEvent("web_share_upload_accepted", e.Data))
	case "upload_refused":
		s.Broadcast(NewEvent("web_share_upload_refused", e.Data))
	case "upload_progress":
		s.Broadcast(NewEvent("web_share_upload_progress", e.Data))
	case "upload_file_done":
		s.Broadcast(NewEvent("web_share_upload_file_done", e.Data))
	case "upload_done":
		s.Broadcast(NewEvent("web_share_upload_done", e.Data))
	case "upload_cancelled":
		s.Broadcast(NewEvent("web_share_upload_cancelled", e.Data))
	}
}
