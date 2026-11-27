package wsclient

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// pushClient is a client whose test delivers the daemon's events itself.
func pushClient(t *testing.T) (*Client, func(event string)) {
	t.Helper()
	var deliver func([]byte)
	c := Local(func([]byte, func([]byte)) {}, func(fn func([]byte)) func() {
		deliver = fn
		return func() {}
	})
	return c, func(event string) { deliver([]byte(fmt.Sprintf(`{"event":%q}`, event))) }
}

func names(events []Event) string {
	var s []string
	for _, e := range events {
		s = append(s, e.Event)
	}
	return strings.Join(s, ",")
}

// fn may call the client: Watch calls it without holding the client's lock (#314). With the
// lock held, a fn that read Events blocked for good.
func TestWatchMayCallTheClient(t *testing.T) {
	c, push := pushClient(t)
	push("status")
	seen := make(chan string, 2)
	go c.Watch(func(e Event) { seen <- e.Event + ":" + names(c.Events()) })
	select {
	case got := <-seen:
		if got != "status:status" {
			t.Fatalf("fn saw %q; want status:status", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fn blocked on Events: Watch held the client's lock") // c stays open: Close would block too
	}
	push("next")
	if got := <-seen; got != "next:status,next" {
		t.Fatalf("fn saw %q; want next:status,next", got)
	}
	_ = c.Close()
}

// A client keeps the latest events only, so that a long xfer ctl does not grow without end
// (#314): the oldest go first, consumed or not, for Next and Watch alike.
func TestClientKeepsTheLatestEvents(t *testing.T) {
	c, push := pushClient(t)
	t.Cleanup(func() { _ = c.Close() })
	c.Keep(3)
	for i := range 5 {
		push(fmt.Sprintf("e%d", i))
	}
	if got := names(c.Events()); got != "e2,e3,e4" {
		t.Fatalf("Events() = %s; want e2,e3,e4", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e, err := c.Next(ctx); err != nil || e.Event != "e2" {
		t.Fatalf("Next = %s, %v; want e2", e.Event, err)
	}
	push("e5")
	if got := names(c.Events()); got != "e3,e4,e5" {
		t.Fatalf("Events() = %s; want e3,e4,e5", got)
	}
	if e, err := c.Next(ctx); err != nil || e.Event != "e3" {
		t.Fatalf("Next = %s, %v; want e3", e.Event, err)
	}

	var watched []string
	c.Watch(func(e Event) { watched = append(watched, e.Event) })
	if got := strings.Join(watched, ","); got != "e3,e4,e5" {
		t.Fatalf("Watch replayed %s; want e3,e4,e5", got)
	}
}

// Without a word from its user, a client keeps DefaultKeep events.
func TestClientIsBoundedByDefault(t *testing.T) {
	c, push := pushClient(t)
	t.Cleanup(func() { _ = c.Close() })
	for range DefaultKeep + 10 {
		push("e")
	}
	if n := len(c.Events()); n != DefaultKeep {
		t.Fatalf("%d events kept; want %d", n, DefaultKeep)
	}
}
