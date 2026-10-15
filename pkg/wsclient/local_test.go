package wsclient

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// localDaemon answers each request with an event named after its action, and can broadcast.
type localDaemon struct {
	mu      sync.Mutex
	subs    []func([]byte)
	release chan struct{} // the "slow" action waits for it
}

func (d *localDaemon) handle(raw []byte, reply func([]byte)) {
	var req request
	_ = json.Unmarshal(raw, &req)
	if req.Action == "slow" {
		<-d.release
	}
	out, _ := json.Marshal(Event{ID: req.ID, Event: req.Action + "_done"})
	reply(out)
}

func (d *localDaemon) subscribe(fn func([]byte)) func() {
	d.mu.Lock()
	d.subs = append(d.subs, fn)
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		d.subs = nil
		d.mu.Unlock()
	}
}

func (d *localDaemon) broadcast(name string) {
	out, _ := json.Marshal(Event{Event: name})
	d.mu.Lock()
	subs := append([]func([]byte){}, d.subs...)
	d.mu.Unlock()
	for _, fn := range subs {
		fn(out)
	}
}

// WP-6.3: as over the WebSocket, Send returns at once, requests run one at a time and in
// order, replies and broadcasts all reach the client, and Close ends the waits.
func TestLocalClient(t *testing.T) {
	d := &localDaemon{release: make(chan struct{})}
	c := Local(d.handle, d.subscribe)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_, _ = c.Send("1", "slow", nil) // the handler waits: Send must not
		_, _ = c.Send("2", "fast", nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Send blocked on a waiting handler")
	}
	d.broadcast("news")
	if e, err := c.WaitFor(ctx, func(e Event) bool { return e.Event == "news" }); err != nil || e.Event != "news" {
		t.Fatalf("broadcast: %v", err)
	}
	close(d.release)
	first, err := c.WaitFor(ctx, func(e Event) bool { return e.ID != "" })
	if err != nil || first.ID != "1" {
		t.Fatalf("first reply %+v, %v; want request 1's", first, err)
	}
	if second, err := c.WaitFor(ctx, func(e Event) bool { return e.ID != "" }); err != nil || second.ID != "2" {
		t.Fatalf("second reply %+v, %v; want request 2's", second, err)
	}

	waiting := make(chan error, 1)
	go func() {
		_, err := c.WaitFor(ctx, func(e Event) bool { return e.Event == "never" })
		waiting <- err
	}()
	time.Sleep(20 * time.Millisecond)
	_ = c.Close()
	if err := <-waiting; !errors.Is(err, ErrClosed) {
		t.Fatalf("wait after Close: %v, want ErrClosed", err)
	}
	if _, err := c.Send("3", "fast", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Send after Close: %v", err)
	}
	d.broadcast("late") // no subscriber any more: nothing happens
}
