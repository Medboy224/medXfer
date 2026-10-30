// Package cabi is the logic of the C library's boundary (bible §10.4, cmd/libmedxfer), in
// plain Go so that it is tested without a C compiler: cmd/libmedxfer only copies strings
// across (B1, B2). Messages are UTF-8 JSON (B3).
//
// The library holds one core per process. medxfer_start is idempotent (B4): a Flutter hot
// restart runs Dart again, not the process, and finds the core running.
package cabi

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/api"
	"github.com/Medboy224/medXfer/pkg/core"
)

// StoppedEvent is the sentinel NextEvent gives the readers once the core stops (B9): the
// reading isolate leaves its loop by itself, never killed inside a native call.
const StoppedEvent = `{"event":"core_stopped"}`

var lib struct {
	mu          sync.Mutex
	core        *core.Core
	calls       int    // medxfer_start calls since the library was loaded (hot restarts, G2A-05)
	stopJournal func() // closes the core's journal
}

// Start starts the core with config ({"data_dir", "download_dir", "device_name"}). A second
// call returns already_started without touching it (B4). The core's journal (DEV-11) is opened
// first, in data_dir, where the application's diagnostic bundle reads it: without it, the
// bundle held no journal, and a failure seen once in a real test left no trace (#169).
func Start(config string) (out string) {
	defer recoverJSON(&out)
	lib.mu.Lock()
	defer lib.mu.Unlock()
	lib.calls++
	if lib.core != nil {
		return encode(map[string]any{"ok": true, "already_started": true, "start_calls": lib.calls, "schema": core.SchemaVersion})
	}
	var cfg core.Config
	if config != "" {
		if err := json.Unmarshal([]byte(config), &cfg); err != nil {
			return encode(map[string]any{"ok": false, "error": "invalid config: " + err.Error()})
		}
	}
	c, err := core.New(cfg)
	if err != nil {
		return encode(map[string]any{"ok": false, "error": err.Error()})
	}
	stopJournal := api.StartJournal(c.JournalDir())
	if err := c.Start(); err != nil {
		c.Stop()
		stopJournal()
		return encode(map[string]any{"ok": false, "error": err.Error()})
	}
	lib.core, lib.stopJournal = c, stopJournal
	return encode(map[string]any{"ok": true, "already_started": false, "start_calls": lib.calls, "schema": core.SchemaVersion})
}

// Call runs one JSON request {id, action, payload} and returns its JSON response {id, ok, ...}.
// A panic becomes an error response (B5).
func Call(request string) (out string) {
	defer recoverJSON(&out)
	c := current()
	if c == nil {
		return encode(core.Response{Error: core.ErrNotStarted.Error()})
	}
	return string(c.CallJSON([]byte(request)))
}

// NextEvent returns the next event in JSON, or ok false after timeout (0: no wait; negative:
// no limit). Once the core is stopped it returns StoppedEvent to every reader, at once (B9).
func NextEvent(timeout time.Duration) (event string, ok bool) {
	defer func() {
		if r := recover(); r != nil { // B5
			event, ok = encode(map[string]any{"event": "core_error", "data": map[string]string{"error": fmt.Sprintf("panic: %v", r)}}), true
		}
	}()
	c := current()
	if c == nil {
		return StoppedEvent, true
	}
	e, ok := c.NextEvent(timeout)
	if !ok {
		if c.Stopped() { // woken by Stop, which may still hold the lock: not current()
			return StoppedEvent, true
		}
		return "", false
	}
	b, err := json.Marshal(e)
	if err != nil {
		return encode(map[string]any{"event": "core_error", "data": map[string]string{"error": err.Error()}}), true
	}
	return string(b), true
}

// Stop stops the core; a later Start starts a fresh one. The readers get StoppedEvent at once.
// The lock is held until the core has stopped: a Start meanwhile waits, so two cores never run
// at once (audit 12, #156). But at most stopWait: the app's window waits for this call to close,
// and a stop that never ended kept the process alive without a window (WP-B.8). A core still
// stopping then is left to end alone, or with the process.
func Stop() {
	defer func() { _ = recover() }() // B5: nothing crosses into C
	lib.mu.Lock()
	defer lib.mu.Unlock()
	c, stopJournal := lib.core, lib.stopJournal
	lib.core, lib.stopJournal = nil, nil
	if c == nil {
		return
	}
	done := make(chan struct{})
	hook := stopping // read here: a test may change it once this call has returned
	go func() {
		defer close(done)
		defer func() { _ = recover() }() // B5, and a panic here would end the process
		defer stopJournal()              // after the core: its last events are journaled too
		hook()
		c.Stop()
	}()
	select {
	case <-done:
	case <-time.After(stopWait):
	}
}

// stopping runs just before a core stops; tests slow it down.
var stopping = func() {}

// stopWait bounds Stop.
var stopWait = 2 * time.Second

// Stats is the event queue of the running core (B11), in JSON.
func Stats() (out string) {
	defer recoverJSON(&out)
	c := current()
	if c == nil {
		return encode(core.Stats{})
	}
	return encode(c.Stats())
}

func current() *core.Core {
	lib.mu.Lock()
	defer lib.mu.Unlock()
	return lib.core
}

func recoverJSON(out *string) {
	if r := recover(); r != nil {
		*out = encode(map[string]any{"ok": false, "error": fmt.Sprintf("panic: %v", r)})
	}
}

func encode(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"ok":false,"error":"unencodable"}`
	}
	return string(b)
}
