// Package wsclient speaks the control vocabulary (requests in, events out) to a daemon in the
// same process (Local): it sends requests and buffers every event in order, so a slow reader
// never blocks the daemon. pkg/testkit and `xfer ctl` are built on it. Its WebSocket transport
// went with the web dashboard, at gate G7 (DEV-19-R).
package wsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

// Event is one event sent by the daemon. Data is kept raw: Decode it into the expected type.
type Event struct {
	ID        string          `json:"id,omitempty"`
	Event     string          `json:"event"`
	Data      json.RawMessage `json:"data,omitempty"`
	Timestamp int64           `json:"timestamp"`
}

// Decode unmarshals the event data into v.
func (e Event) Decode(v any) error {
	if len(e.Data) == 0 {
		return fmt.Errorf("event %q has no data", e.Event)
	}
	return json.Unmarshal(e.Data, v)
}

// Fields returns the event data as a map, or nil if it is not a JSON object.
func (e Event) Fields() map[string]any {
	var m map[string]any
	_ = json.Unmarshal(e.Data, &m)
	return m
}

type request struct {
	ID      string          `json:"id,omitempty"`
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// transport carries requests to the daemon, in the same process (Local, WP-6.3 of the J6a
// plan).
type transport interface {
	send(request []byte) error
	close() error
}

// Client speaks the control vocabulary (requests in, events out) to a daemon, whatever the
// transport: it keeps every event received, for WaitFor, Watch and Events.
type Client struct {
	tr      transport
	writeMu sync.Mutex
	nextID  atomic.Int64

	mu      sync.Mutex
	events  []Event
	cursor  int           // events before cursor were consumed by Next / WaitFor
	changed chan struct{} // closed and replaced whenever events or err change
	err     error         // read error that ended the connection
	watch   func(Event)   // called for every event, in order, from the reading goroutine
}

// deliver records an event, or the error that ended the transport, and wakes the waiters.
func (c *Client) deliver(e Event, err error) {
	c.mu.Lock()
	if c.err != nil { // already ended: nothing more is kept
		c.mu.Unlock()
		return
	}
	if err != nil {
		c.err = err
	} else {
		c.events = append(c.events, e)
	}
	close(c.changed)
	c.changed = make(chan struct{})
	watch := c.watch
	c.mu.Unlock()
	if err == nil && watch != nil {
		watch(e)
	}
}

// Send sends a request. payload may be nil, a json.RawMessage, a string holding JSON, or any
// value that encodes to JSON. An empty id is replaced by a generated one, which is returned.
func (c *Client) Send(id, action string, payload any) (string, error) {
	if id == "" {
		id = fmt.Sprintf("req-%d", c.nextID.Add(1))
	}
	req := request{ID: id, Action: action}
	switch p := payload.(type) {
	case nil:
	case json.RawMessage:
		req.Payload = p
	case string:
		req.Payload = json.RawMessage(p)
	default:
		b, err := json.Marshal(p)
		if err != nil {
			return id, err
		}
		req.Payload = b
	}
	data, err := json.Marshal(req)
	if err != nil {
		return id, err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return id, c.tr.send(data)
}

// WaitFor returns the first unconsumed event that matches, consuming it and every event
// before it. It fails when ctx ends or the connection closes first.
func (c *Client) WaitFor(ctx context.Context, match func(Event) bool) (Event, error) {
	for {
		c.mu.Lock()
		for c.cursor < len(c.events) {
			e := c.events[c.cursor]
			c.cursor++
			if match == nil || match(e) {
				c.mu.Unlock()
				return e, nil
			}
		}
		err, changed := c.err, c.changed
		c.mu.Unlock()
		if err != nil {
			return Event{}, fmt.Errorf("connection closed: %w", err)
		}
		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		case <-changed:
		}
	}
}

// Next returns the next unconsumed event, whatever it is.
func (c *Client) Next(ctx context.Context) (Event, error) { return c.WaitFor(ctx, nil) }

// Watch calls fn for every event, in order: first for the events already received, then for
// each new one, from the goroutine that reads the connection. fn must not block for long.
// Watch does not consume events: WaitFor and Next still see them.
func (c *Client) Watch(fn func(Event)) {
	c.mu.Lock()
	past := append([]Event(nil), c.events...)
	c.watch = fn
	// Hold the lock while replaying so a new event cannot be delivered before the past ones.
	for _, e := range past {
		fn(e)
	}
	c.mu.Unlock()
}

// Rewind makes every event received so far unconsumed again: WaitFor and Next see them anew.
func (c *Client) Rewind() {
	c.mu.Lock()
	c.cursor = 0
	c.mu.Unlock()
}

// Events returns a copy of every event received so far, consumed or not.
func (c *Client) Events() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Event(nil), c.events...)
}

// Close closes the connection.
func (c *Client) Close() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.tr.close()
}
