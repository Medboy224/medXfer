package core

import (
	"slices"
	"testing"

	"github.com/Medboy224/medXfer/pkg/api"
)

// #297: a full queue never drops the end of a transfer. It comes after the events queued
// before it; what comes after it is dropped until it is read, never put before it.
func TestFullQueueKeepsTransferEnds(t *testing.T) {
	c, err := New(Config{DataDir: t.TempDir(), QueueSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	for i := 0; i < 4; i++ {
		c.push(api.EventMessage{Event: "log"})
	}
	ends := []string{"transfer_complete", "transfer_failed", "transfer_canceled"}
	for _, e := range ends {
		c.push(api.EventMessage{Event: e})
		c.push(api.EventMessage{Event: "log"})
	}
	if s := c.Stats(); s.Queued != 7 || s.Drops != 3 {
		t.Fatalf("stats %+v, want 7 queued (3 ends) and 3 drops", s)
	}
	c.NextEvent(0)
	c.push(api.EventMessage{Event: "log"}) // there is room again, but not before the ends

	var got []string
	for {
		e, ok := c.NextEvent(0)
		if !ok {
			break
		}
		got = append(got, e.Event)
	}
	if want := append([]string{"log", "log", "log"}, ends...); !slices.Equal(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	c.push(api.EventMessage{Event: "log"})
	if e, ok := c.NextEvent(0); !ok || e.Event != "log" {
		t.Fatalf("after the ends: %v %v, want the queue as before", e, ok)
	}
}
