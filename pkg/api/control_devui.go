//go:build devui

package api

// The control surface of the development daemon (API-04): /status and the control WebSocket
// of the web dashboard. Only devui builds have it (WP-6.6 of the J6a plan): the published
// binary drives the core in-process (pkg/core, cmd/libmedxfer) and listens to no control
// socket (API-01).

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/websocket"
)

// wsResponder answers a request that came over the dashboard's WebSocket.
type wsResponder struct {
	s    *DaemonServer
	conn *websocket.Conn
}

func (r wsResponder) Reply(evt EventMessage) { r.s.sendTo(r.conn, evt) }

func (s *DaemonServer) handleHTTPStatus(w http.ResponseWriter, r *http.Request) {
	// The status carries the Web Share PIN/token and the pairing code: never serve it to the LAN.
	if !isLoopbackRequest(r) {
		http.Error(w, "Forbidden: status is restricted to localhost", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.getStatus())
}

func (s *DaemonServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		http.Error(w, "Forbidden: WebSocket control channel is restricted to localhost", http.StatusForbidden)
		return
	}

	upgrader := websocket.Upgrader{
		// control() already rejected foreign origins; checked again as defense in depth.
		CheckOrigin:  func(r *http.Request) bool { o := r.Header.Get("Origin"); return o == "" || s.originAllowed(o) },
		Subprotocols: []string{"medxfer.v1"}, // never echo the "token.<hex>" protocol
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	s.clientsMu.Lock()
	s.clients[conn] = true
	s.clientsMu.Unlock()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, conn)
		s.clientsMu.Unlock()
		conn.Close()
	}()

	// Send initial status immediately upon connection
	s.sendTo(conn, NewEvent("status", s.getStatus()))

	for {
		var req RequestMessage
		if err := conn.ReadJSON(&req); err != nil {
			break
		}
		s.dispatch(wsResponder{s, conn}, req)
	}
}
