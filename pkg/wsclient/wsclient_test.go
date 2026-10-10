package wsclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeDaemon answers each request with an "ack" event carrying the request, after sending
// two unrelated events, and rejects connections without the right token.
func fakeDaemon(t *testing.T) string {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		_ = ws.WriteJSON(map[string]any{"event": "status"})
		for {
			var req map[string]any
			if err := ws.ReadJSON(&req); err != nil {
				return
			}
			_ = ws.WriteJSON(map[string]any{"event": "noise"})
			_ = ws.WriteJSON(map[string]any{"event": "noise"})
			_ = ws.WriteJSON(map[string]any{"event": "ack", "id": req["id"], "data": req})
		}
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestClient(t *testing.T) {
	addr := fakeDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := Dial(ctx, addr, "wrong"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("Dial with a wrong token = %v; want an HTTP 401 error", err)
	}
	c, err := Dial(ctx, addr, "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if e, err := c.Next(ctx); err != nil || e.Event != "status" {
		t.Fatalf("first event = %+v, %v; want status", e, err)
	}

	// Every payload form ends up as the same JSON object; generated ids are unique.
	var ids []string
	for _, payload := range []any{`{"n":1}`, json.RawMessage(`{"n":1}`), map[string]int{"n": 1}} {
		id, err := c.Send("", "test", payload)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if ids[0] == ids[1] || ids[1] == ids[2] {
		t.Fatalf("generated ids are not unique: %v", ids)
	}
	for _, id := range ids {
		e, err := c.WaitFor(ctx, func(e Event) bool { return e.Event == "ack" })
		if err != nil {
			t.Fatal(err)
		}
		var req struct {
			ID      string         `json:"id"`
			Action  string         `json:"action"`
			Payload map[string]int `json:"payload"`
		}
		if err := e.Decode(&req); err != nil || e.ID != id || req.Action != "test" || req.Payload["n"] != 1 {
			t.Fatalf("ack = %+v (%v); want id %s, action test, payload n=1", req, err, id)
		}
	}

	// WaitFor consumed the noise before each ack: nothing is left to read.
	short, cancelShort := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelShort()
	if e, err := c.Next(short); err == nil {
		t.Fatalf("unexpected unconsumed event %+v", e)
	}
	if n := len(c.Events()); n != 10 { // status + 3 × (noise, noise, ack)
		t.Fatalf("Events() holds %d events; want 10", n)
	}
}
