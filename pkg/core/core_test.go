package core

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/api"
)

func newCore(t *testing.T, name string) *Core {
	t.Helper()
	c, err := New(Config{DataDir: t.TempDir(), DownloadDir: t.TempDir(), DeviceName: name})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Stop)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	return c
}

// waitEvent reads events until one named name, failing after timeout.
func waitEvent(t *testing.T, c *Core, name string, timeout time.Duration) api.EventMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			t.Fatalf("no %s event within %v", name, timeout)
		}
		if e, ok := c.NextEvent(left); ok && e.Event == name {
			return e
		}
	}
}

// API-01, B4: Start and Stop may be called twice; Call needs a started core.
func TestStartStopAndCallOrder(t *testing.T) {
	c, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if r := c.Call(api.RequestMessage{ID: "1", Action: "get_status"}); r.OK || r.Error != ErrNotStarted.Error() || r.ID != "1" {
		t.Fatalf("Call before Start: %+v", r)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if c.NodeAddr() == "" || c.Fingerprint() == "" {
		t.Fatal("a started core has no single port or no key")
	}
	c.Stop()
	c.Stop()
	if r := c.Call(api.RequestMessage{Action: "get_status"}); r.OK {
		t.Fatal("Call after Stop succeeded")
	}
	if _, err := New(Config{}); err == nil {
		t.Fatal("a core without data_dir (B6)")
	}
}

// API-02: a request gets its replies in the Response, with its id; a refused one says why.
func TestCallReplies(t *testing.T) {
	c := newCore(t, "Solo")
	r := c.Call(api.RequestMessage{ID: "s", Action: "get_status"})
	if !r.OK || r.ID != "s" || len(r.Events) != 1 || r.Events[0].Event != "status" {
		t.Fatalf("get_status: %+v", r)
	}
	r = c.Call(api.RequestMessage{ID: "u", Action: "no_such_action"})
	if r.OK || !strings.Contains(r.Error, "unknown action") {
		t.Fatalf("unknown action: %+v", r)
	}
	out := c.CallJSON([]byte(`{"id":"j","action":"get_status"}`))
	var jr Response
	if err := json.Unmarshal(out, &jr); err != nil || !jr.OK || jr.ID != "j" {
		t.Fatalf("CallJSON: %s (%v)", out, err)
	}
	if out := c.CallJSON([]byte(`not json`)); !strings.Contains(string(out), "invalid request") {
		t.Fatalf("CallJSON with bad JSON: %s", out)
	}
}

// The exit criterion of J6a in small: two devices in one process, no control socket, pair by
// PIN. The PIN comes back in the Response of pair_pin_show (to this caller only); "paired"
// reaches both through NextEvent.
func TestTwoCoresPairByPIN(t *testing.T) {
	a, b := newCore(t, "Shower"), newCore(t, "Typist")
	r := a.Call(api.RequestMessage{ID: "show", Action: "pair_pin_show"})
	if !r.OK || len(r.Events) != 1 || r.Events[0].Event != "pair_pin_shown" {
		t.Fatalf("pair_pin_show: %+v", r)
	}
	pin, _ := r.Events[0].Data.(map[string]any)["pin"].(string)
	if len(strings.ReplaceAll(pin, " ", "")) != 6 {
		t.Fatalf("PIN %q", pin)
	}
	_, port, err := net.SplitHostPort(a.NodeAddr())
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"target": net.JoinHostPort("127.0.0.1", port), "pin": pin})
	if r := b.Call(api.RequestMessage{ID: "pin", Action: "pair_pin", Payload: payload}); !r.OK {
		t.Fatalf("pair_pin: %+v", r)
	}
	waitEvent(t, b, "paired", 10*time.Second)
	waitEvent(t, a, "paired", 10*time.Second)
}

// B11: a full queue drops and counts, it never blocks the engine. B7: progress events closer
// than 100 ms are dropped.
func TestQueueBoundAndProgressRate(t *testing.T) {
	c, err := New(Config{DataDir: t.TempDir(), QueueSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	for i := 0; i < 10; i++ {
		c.push(api.EventMessage{Event: "log"})
	}
	if s := c.Stats(); s.Queued != 4 || s.Drops != 6 {
		t.Fatalf("stats %+v, want 4 queued and 6 drops", s)
	}
	for c.Stats().Queued > 0 {
		c.NextEvent(0)
	}
	for i := 0; i < 5; i++ {
		c.push(api.EventMessage{Event: "transfer_progress"})
	}
	if s := c.Stats(); s.Queued != 1 {
		t.Fatalf("%d progress events queued in a burst, want 1", s.Queued)
	}
	time.Sleep(progressGap + 20*time.Millisecond)
	c.push(api.EventMessage{Event: "transfer_progress"})
	if s := c.Stats(); s.Queued != 2 {
		t.Fatalf("a progress event after the gap was dropped (%d queued)", s.Queued)
	}
}

// B5: a panic in a handler is an error for that call; the core keeps running.
func TestPanicBecomesAnError(t *testing.T) {
	c := newCore(t, "Solo")
	c.dispatch = func(api.Responder, api.RequestMessage) { panic("boom") }
	if r := c.Call(api.RequestMessage{ID: "p", Action: "get_status"}); r.OK || !strings.Contains(r.Error, "boom") || r.ID != "p" {
		t.Fatalf("panic: %+v", r)
	}
	c.dispatch = c.srv.Dispatch
	if r := c.Call(api.RequestMessage{Action: "get_status"}); !r.OK {
		t.Fatalf("after a panic: %+v", r)
	}
}

// B9: Stop wakes a reader waiting in NextEvent at once.
func TestStopWakesTheReader(t *testing.T) {
	c := newCore(t, "Solo")
	for { // empty what the start queued
		if _, ok := c.NextEvent(0); !ok {
			break
		}
	}
	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		c.NextEvent(time.Minute)
		done <- time.Since(start)
	}()
	time.Sleep(50 * time.Millisecond)
	c.Stop()
	select {
	case d := <-done:
		if d > 5*time.Second {
			t.Fatalf("the reader woke after %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not wake the reader")
	}
}

// WP-B.8: a server slow to stop does not keep the readers waiting. They are woken before it
// stops, else the app's reader kept the process alive after its window closed.
func TestStopWakesTheReaderBeforeTheServerStops(t *testing.T) {
	c := newCore(t, "Solo")
	for {
		if _, ok := c.NextEvent(0); !ok {
			break
		}
	}
	release := make(chan struct{})
	serverStopping = func() { <-release }
	t.Cleanup(func() { serverStopping = func() {} })
	woken := make(chan struct{})
	go func() {
		c.NextEvent(-1)
		close(woken)
	}()
	time.Sleep(50 * time.Millisecond)
	stopped := make(chan struct{})
	go func() {
		c.Stop()
		close(stopped)
	}()
	select {
	case <-woken:
	case <-time.After(5 * time.Second):
		t.Fatal("the reader waited for the server to stop")
	}
	if !c.Stopped() {
		t.Fatal("Stopped is false once Stop has begun")
	}
	close(release)
	<-stopped // before the cleanup puts serverStopping back
}

// Two cores keep their data apart (D2): each has its own key.
func TestCoresKeepTheirDataApart(t *testing.T) {
	a, b := newCore(t, "A"), newCore(t, "B")
	if a.Fingerprint() == b.Fingerprint() {
		t.Fatal("two cores share a key")
	}
}

// Audit §3.7 (#156): the limiter of B7 never drops the last progress event, at 100 %: the
// interface would stay at 99 %.
func TestFinalProgressPassesTheLimiter(t *testing.T) {
	c, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	progress := func(done, total int64) api.EventMessage {
		return api.NewEvent("transfer_progress", api.TransferProgressData{BatchBytes: done, BatchTotalBytes: total,
			BatchPercent: float64(done) * 100 / float64(total)})
	}
	c.push(progress(50, 100))
	c.push(progress(100, 100)) // within progressGap of the previous one
	if s := c.Stats(); s.Queued != 2 {
		t.Fatalf("%d progress events queued, want 2: the final one was dropped", s.Queued)
	}
}
