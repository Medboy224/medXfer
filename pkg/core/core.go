// Package core is medXfer running inside the caller's process, with no control socket (bible
// §10.2, API-01): the interface starts it, calls it with requests, reads its events, and stops
// it. These four functions are what the C library exposes (cmd/libmedxfer, §10.4), and what the
// command line and the tests use directly.
//
// The vocabulary is the daemon's (API-02): RequestMessage {id, action, payload} in, events
// {event, data} out. Only the transport changes.
package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Medboy224/medXfer/pkg/api"
)

// SchemaVersion is the version of the messages' schema, frozen by the golden files of
// testdata (API-06). A change that breaks a caller bumps it. 2: the trust model (lot « Modèle
// de confiance »): no more trust_request, trust_respond, trust_expired; trust_offer,
// trust_answer, pin_needed, pin_requested, trust_offered, trust_established, trust_declined,
// trust_incomplete; "once" becomes "trust" in the pairing actions. 3: the core no longer
// creates a hotspot, the platform does (lot « Audit », A1): no more hotspot_start,
// hotspot_status, hotspot_stop, open_hotspot_settings, hotspot_started, hotspot_stopped. 4: what
// the application needs (J6b, WP-B.0): trust_list, trust_forget, trust_rename, export_bundle,
// trusted_devices, bundle_exported; send takes descriptors (items); transfer_progress lists the
// files in progress (items). Added since, without a break: transfer_progress's stages
// (stage_ms, bottleneck_confidence, WP-B.5); pin_refuse and pin_refused (the refusal of a PIN
// asked for).
const SchemaVersion = 4

// DefaultQueueSize bounds the event queue (B11): past it, an event is dropped and counted, and
// the engine never waits for the interface.
const DefaultQueueSize = 65536

// progressGap is the least time between two progress events: 10 per second at most (B7).
const progressGap = 100 * time.Millisecond

// ErrNotStarted: Call before Start, or after Stop.
var ErrNotStarted = errors.New("core: not started")

// Config is what the caller gives: paths come from it, never from the core's guesses (B6).
type Config struct {
	DataDir     string `json:"data_dir"`     // configuration, identity, trusted devices, reports
	DownloadDir string `json:"download_dir"` // where received files go by default
	DeviceName  string `json:"device_name"`  // shown to a device once paired, never announced
	QueueSize   int    `json:"queue_size,omitempty"`
}

// Response is the synchronous answer to a Call (API-02): the replies meant for the caller
// alone. Events that come later, or for everyone, are read with NextEvent.
type Response struct {
	ID     string             `json:"id,omitempty"`
	OK     bool               `json:"ok"`
	Error  string             `json:"error,omitempty"`
	Events []api.EventMessage `json:"events,omitempty"`
}

// Stats counts what the event queue holds and what it dropped (B11).
type Stats struct {
	Queued int   `json:"queued"`
	Drops  int64 `json:"drops"`
}

// Core is one medXfer device. Several may run in one process (D2 of the J6a plan): the tests
// pair two of them; the C library holds one.
type Core struct {
	cfg      Config
	srv      *api.DaemonServer
	events   chan api.EventMessage
	drops    atomic.Int64
	stopCh   chan struct{}
	dispatch func(api.Responder, api.RequestMessage) // tests replace it

	mu           sync.Mutex
	started      bool
	stopped      bool
	unsubscribe  func()
	lastProgress time.Time
}

// New prepares a core; nothing listens until Start.
func New(cfg Config) (*Core, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("core: no data_dir (B6: paths come from the caller)")
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = DefaultQueueSize
	}
	dir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	cfg.DataDir = dir
	c := &Core{
		cfg:    cfg,
		srv:    api.NewDaemonServerAt(cfg.DataDir, 0, cfg.DownloadDir, cfg.DeviceName),
		events: make(chan api.EventMessage, cfg.QueueSize),
		stopCh: make(chan struct{}),
	}
	c.dispatch = c.srv.Dispatch
	c.unsubscribe = c.srv.Subscribe(c.push)
	return c, nil
}

// Start starts the device: identity, single port, discovery. A second call does nothing (B4).
func (c *Core) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return ErrNotStarted
	}
	if c.started {
		return nil
	}
	if err := c.srv.StartNode(); err != nil {
		return err
	}
	c.started = true
	return nil
}

// Call runs one request and returns its replies. A panic in a handler becomes an error, never
// a crash of the caller (B5).
func (c *Core) Call(req api.RequestMessage) (resp Response) {
	resp.ID = req.ID
	c.mu.Lock()
	running := c.started && !c.stopped
	c.mu.Unlock()
	if !running {
		resp.Error = ErrNotStarted.Error()
		return resp
	}
	rec := &callResponder{c: c}
	defer func() {
		if r := recover(); r != nil {
			rec.close()
			resp.OK, resp.Error, resp.Events = false, fmt.Sprintf("core: panic in %q: %v", req.Action, r), nil
		}
	}()
	c.dispatch(rec, req)
	resp.Events = rec.close()
	resp.OK = true
	for _, e := range resp.Events {
		if e.Event == "action_error" {
			resp.OK, resp.Error = false, errorText(e.Data)
			break
		}
	}
	return resp
}

// CallJSON is Call for a JSON request, with a JSON answer (B3): the C library's medxfer_call.
func (c *Core) CallJSON(request []byte) []byte {
	var req api.RequestMessage
	var resp Response
	if err := json.Unmarshal(request, &req); err != nil {
		resp.Error = "core: invalid request: " + err.Error()
	} else {
		resp = c.Call(req)
	}
	out, err := json.Marshal(resp)
	if err != nil { // an event whose data cannot be encoded: say so, keep the id
		out, _ = json.Marshal(Response{ID: resp.ID, Error: "core: unencodable reply: " + err.Error()})
	}
	return out
}

// NextEvent returns the next event, or false after timeout (0: do not wait; negative: no
// limit) or once Stop has woken every reader (B9).
func (c *Core) NextEvent(timeout time.Duration) (api.EventMessage, bool) {
	select {
	case e := <-c.events:
		return e, true
	default:
	}
	if timeout == 0 {
		return api.EventMessage{}, false
	}
	var expired <-chan time.Time // nil with a negative timeout: no limit (§3.3, #156)
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		expired = t.C
	}
	select {
	case e := <-c.events:
		return e, true
	case <-expired:
	case <-c.stopCh:
	}
	return api.EventMessage{}, false
}

// Stop stops the device and wakes every NextEvent waiting. A second call does nothing. The
// readers are woken first: a server slow to stop does not keep them waiting (B9), else the
// app's reader, and with it the process, stayed alive after its window closed (WP-B.8).
func (c *Core) Stop() {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	c.stopped = true
	c.mu.Unlock()
	close(c.stopCh)
	c.unsubscribe()
	serverStopping()
	c.srv.Stop()
}

// serverStopping runs just before the server stops; tests hold it.
var serverStopping = func() {}

// Stopped reports whether Stop has been called.
func (c *Core) Stopped() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped
}

// Stats reports the event queue.
func (c *Core) Stats() Stats { return Stats{Queued: len(c.events), Drops: c.drops.Load()} }

// NodeAddr is the address of the device's single port, once started ("" before).
func (c *Core) NodeAddr() string { return c.srv.NodeListenAddr() }

// Fingerprint is this device's key, once started.
func (c *Core) Fingerprint() string { return c.srv.Fingerprint() }

// JournalDir is where the diagnostic bundle reads the journal (DEV-11), in data_dir: the
// program that holds the core opens it there (internal/cabi).
func (c *Core) JournalDir() string { return c.srv.JournalDir() }

// push queues an event without ever blocking the engine: a full queue drops and counts (B11);
// progress events closer than progressGap are dropped too, the next one carries the news (B7).
// The last one, at 100 %, always passes: no other comes after it (audit §3.7, #156).
func (c *Core) push(e api.EventMessage) {
	if e.Event == "transfer_progress" {
		c.mu.Lock()
		now := time.Now()
		if now.Sub(c.lastProgress) < progressGap && !finalProgress(e) {
			c.mu.Unlock()
			return
		}
		c.lastProgress = now
		c.mu.Unlock()
	}
	select {
	case c.events <- e:
	default:
		c.drops.Add(1)
	}
}

// finalProgress reports whether e is the progress event of a finished transfer.
func finalProgress(e api.EventMessage) bool {
	d, ok := e.Data.(api.TransferProgressData)
	return ok && (d.BatchPercent >= 100 || (d.BatchTotalBytes > 0 && d.BatchBytes >= d.BatchTotalBytes))
}

// callResponder gathers the replies a handler gives while Call runs. A reply that comes later
// (a handler that answers from a goroutine) is queued for NextEvent, with the request's id: it
// is still meant for this caller alone, and never broadcast nor journaled.
type callResponder struct {
	c       *Core
	mu      sync.Mutex
	closed  bool
	replies []api.EventMessage
}

func (r *callResponder) Reply(e api.EventMessage) {
	r.mu.Lock()
	if !r.closed {
		r.replies = append(r.replies, e)
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	r.c.push(e)
}

func (r *callResponder) close() []api.EventMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return r.replies
}

// errorText is the message of an action_error event.
func errorText(data any) string {
	switch d := data.(type) {
	case map[string]string:
		return d["error"]
	case map[string]any:
		if s, ok := d["error"].(string); ok {
			return s
		}
	}
	return "action failed"
}
