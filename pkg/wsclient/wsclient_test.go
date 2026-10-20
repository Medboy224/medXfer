package wsclient

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fakeDaemon answers each request with two unrelated events, then an "ack" carrying the
// request; a "status" event comes first, as a daemon sends on connection.
func fakeDaemon(t *testing.T) *Client {
	t.Helper()
	var subs []func([]byte)
	c := Local(func(raw []byte, reply func([]byte)) {
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		noise, _ := json.Marshal(map[string]any{"event": "noise"})
		ack, _ := json.Marshal(map[string]any{"event": "ack", "id": req["id"], "data": req})
		reply(noise)
		reply(noise)
		reply(ack)
	}, func(fn func([]byte)) func() {
		subs = append(subs, fn)
		return func() {}
	})
	t.Cleanup(func() { _ = c.Close() })
	status, _ := json.Marshal(map[string]any{"event": "status"})
	for _, fn := range subs {
		fn(status)
	}
	return c
}

func TestClient(t *testing.T) {
	c := fakeDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

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

// Watch replays the events already received, then delivers new ones, in order, once each.
func TestWatch(t *testing.T) {
	c := fakeDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Next(ctx); err != nil { // "status" is now in the past
		t.Fatal(err)
	}

	seen := make(chan string, 16)
	c.Watch(func(e Event) { seen <- e.Event })
	if _, err := c.Send("", "test", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitFor(ctx, func(e Event) bool { return e.Event == "ack" }); err != nil {
		t.Fatal(err)
	}
	var got []string
	for len(got) < 4 {
		select {
		case name := <-seen:
			got = append(got, name)
		case <-ctx.Done():
			t.Fatalf("Watch delivered only %v", got)
		}
	}
	if strings.Join(got, ",") != "status,noise,noise,ack" {
		t.Fatalf("Watch delivered %v; want status, noise, noise, ack", got)
	}
}
