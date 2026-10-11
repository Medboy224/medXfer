// Command core is the G2a prototype library: the medXfer Go core loaded by a Flutter app
// through a C boundary (bible §10, plan docs/medxfer-plan-prototype-g2a.md §4).
// Built with -buildmode=c-shared. Throwaway code: it lives on branch spike/g2a only.
//
// Contract (plan §4.1):
//
//	B1 every char* returned by the library is freed with medxfer_free, never by the caller's
//	   allocator; in the callback model, the event string handed to the callback belongs to
//	   the receiver, which frees it with medxfer_free after reading it.
//	B2 no Go pointer crosses the boundary after a call returns (C.CString copies).
//	B3 messages are UTF-8 JSON.
//	B4 medxfer_start is idempotent.
//	B5 every goroutine of the core recovers panics into a "core_error" event; a panic in
//	   medxfer_call becomes a JSON error.
//	B6 paths come from the caller (config.data_dir, config.download_dir).
//	B7 progress events are rate-limited (10 per second per transfer).
package main

/*
#include <stdlib.h>
typedef void (*medxfer_event_cb)(const char*);
static void medxfer_call_cb(medxfer_event_cb cb, const char* s) { cb(s); }
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
	"unsafe"
)

func main() {} // required by -buildmode=c-shared

// ---- C boundary -------------------------------------------------------------------------

//export medxfer_start
func medxfer_start(config *C.char) (out *C.char) {
	defer recoverToJSON(&out)
	return C.CString(core.start(C.GoString(config)))
}

//export medxfer_call
func medxfer_call(request *C.char) (out *C.char) {
	defer recoverToJSON(&out)
	return C.CString(core.call(C.GoString(request)))
}

//export medxfer_set_event_callback
func medxfer_set_event_callback(cb C.medxfer_event_cb) {
	core.setCallback(cb)
}

//export medxfer_next_event
func medxfer_next_event(timeoutMS C.int) *C.char {
	s, ok := core.nextEvent(time.Duration(timeoutMS) * time.Millisecond)
	if !ok {
		return nil
	}
	return C.CString(s)
}

//export medxfer_stop
func medxfer_stop() { core.stop() }

//export medxfer_free
func medxfer_free(p *C.char) { C.free(unsafe.Pointer(p)) }

func recoverToJSON(out **C.char) {
	if r := recover(); r != nil {
		*out = C.CString(jsonString(map[string]any{"ok": false, "error": fmt.Sprintf("panic: %v", r)}))
	}
}

// ---- core state ---------------------------------------------------------------------------

type config struct {
	DataDir     string `json:"data_dir"`
	DownloadDir string `json:"download_dir"`
}

type coreState struct {
	mu      sync.Mutex
	started bool
	cfg     config
	stopCh  chan struct{}
	startAt time.Time
	calls   int // medxfer_start calls since the library was loaded (hot restart: G2A-05)

	cbMu sync.RWMutex
	cb   C.medxfer_event_cb

	queue chan string // poll model; bounded, oldest kept, drops counted
	drops int64
}

var core = &coreState{queue: make(chan string, 65536)}

func (c *coreState) start(cfgJSON string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var cfg config
	if cfgJSON != "" {
		if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
			return jsonString(map[string]any{"ok": false, "error": "invalid config: " + err.Error()})
		}
	}
	c.calls++
	if c.started { // B4: Flutter hot restart re-runs Dart, not the process
		return jsonString(map[string]any{"ok": true, "already_started": true, "start_calls": c.calls})
	}
	c.started, c.cfg, c.stopCh, c.startAt = true, cfg, make(chan struct{}), time.Now()
	return jsonString(map[string]any{"ok": true, "already_started": false, "start_calls": c.calls})
}

func (c *coreState) stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return
	}
	close(c.stopCh)
	c.started = false
	for { // drop pending poll events
		select {
		case <-c.queue:
		default:
			return
		}
	}
}

func (c *coreState) running() (chan struct{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopCh, c.started
}

func (c *coreState) setCallback(cb C.medxfer_event_cb) {
	c.cbMu.Lock()
	c.cb = cb
	c.cbMu.Unlock()
}

// emit delivers an event: to the callback when one is set (callback model), else to the
// queue read by medxfer_next_event (poll model).
func (c *coreState) emit(event string, data map[string]any) {
	msg := map[string]any{"event": event, "ts_us": time.Now().UnixMicro()}
	if data != nil {
		msg["data"] = data
	}
	s := jsonString(msg)
	c.cbMu.RLock()
	cb := c.cb
	c.cbMu.RUnlock()
	if cb != nil {
		C.medxfer_call_cb(cb, C.CString(s)) // the receiver frees it (B1)
		return
	}
	select {
	case c.queue <- s:
	default:
		c.mu.Lock()
		c.drops++
		c.mu.Unlock()
	}
}

func (c *coreState) nextEvent(timeout time.Duration) (string, bool) {
	if timeout <= 0 {
		select {
		case s := <-c.queue:
			return s, true
		default:
			return "", false
		}
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case s := <-c.queue:
		return s, true
	case <-t.C:
		return "", false
	}
}

// goSafe runs f in a goroutine whose panic becomes a core_error event (B5).
func (c *coreState) goSafe(name string, f func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.emit("core_error", map[string]any{
					"where": name, "error": fmt.Sprint(r), "stack": string(debug.Stack()),
				})
			}
		}()
		f()
	}()
}

// ---- dispatch -----------------------------------------------------------------------------

type request struct {
	ID      string          `json:"id"`
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload"`
}

func (c *coreState) call(reqJSON string) string {
	var req request
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		return jsonString(map[string]any{"ok": false, "error": "invalid request: " + err.Error()})
	}
	res, err := c.dispatch(req)
	if err != nil {
		return jsonString(map[string]any{"id": req.ID, "ok": false, "error": err.Error()})
	}
	if res == nil {
		res = map[string]any{}
	}
	res["id"], res["ok"] = req.ID, true
	return jsonString(res)
}

func (c *coreState) dispatch(req request) (map[string]any, error) {
	if h, ok := handlers[req.Action]; ok {
		return h(c, req.Payload)
	}
	return nil, fmt.Errorf("unknown action %q", req.Action)
}

type handler func(c *coreState, payload json.RawMessage) (map[string]any, error)

var handlers = map[string]handler{}

func init() {
	handlers["ping"] = func(c *coreState, _ json.RawMessage) (map[string]any, error) {
		_, started := c.running()
		return map[string]any{"pong": true, "started": started}, nil
	}
	handlers["stats"] = func(c *coreState, _ json.RawMessage) (map[string]any, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		return map[string]any{"queued": len(c.queue), "drops": c.drops, "started": c.started}, nil
	}
	// emit_events sends count numbered "tick" events at per_sec events per second (G2A-03).
	handlers["emit_events"] = func(c *coreState, p json.RawMessage) (map[string]any, error) {
		var args struct {
			Count  int `json:"count"`
			PerSec int `json:"per_sec"`
		}
		if err := json.Unmarshal(p, &args); err != nil || args.Count <= 0 || args.PerSec <= 0 {
			return nil, fmt.Errorf("payload must be {count>0, per_sec>0}")
		}
		stopCh, started := c.running()
		if !started {
			return nil, fmt.Errorf("core not started")
		}
		c.goSafe("emit_events", func() {
			start := time.Now()
			interval := time.Second / time.Duration(args.PerSec)
			for i := 1; i <= args.Count; i++ {
				if d := time.Until(start.Add(time.Duration(i-1) * interval)); d > 0 {
					select {
					case <-stopCh:
						return
					case <-time.After(d):
					}
				}
				c.emit("tick", map[string]any{"seq": i, "of": args.Count})
			}
			c.emit("ticks_done", map[string]any{"count": args.Count, "elapsed_ms": time.Since(start).Milliseconds()})
		})
		return map[string]any{"scheduled": args.Count}, nil
	}
	// poll_close wakes the poll-model reader with a sentinel so it can leave its loop: killing
	// an isolate does not interrupt a native call in progress (seen in G2A-03).
	handlers["poll_close"] = func(c *coreState, _ json.RawMessage) (map[string]any, error) {
		select {
		case c.queue <- `{"event":"__poll_close__"}`:
		default:
		}
		return nil, nil
	}
	// memstats reports the Go runtime's own memory, to split the process RSS between Go and Dart.
	handlers["memstats"] = func(*coreState, json.RawMessage) (map[string]any, error) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return map[string]any{
			"sys_mb": float64(m.Sys) / 1048576, "heap_inuse_mb": float64(m.HeapInuse) / 1048576,
			"stack_inuse_mb": float64(m.StackInuse) / 1048576, "num_gc": m.NumGC,
			"goroutines": runtime.NumGoroutine(), "gomaxprocs": runtime.GOMAXPROCS(0), "num_cpu": runtime.NumCPU(),
		}, nil
	}
	handlers["panic_call"] = func(*coreState, json.RawMessage) (map[string]any, error) {
		panic("test panic in medxfer_call (G2A-04)")
	}
	handlers["panic_goroutine"] = func(c *coreState, _ json.RawMessage) (map[string]any, error) {
		c.goSafe("panic_goroutine", func() { panic("test panic in a core goroutine (G2A-04)") })
		return map[string]any{"launched": true}, nil
	}
}

func jsonString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"ok":false,"error":"json encoding failed"}`
	}
	return string(b)
}
