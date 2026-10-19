package securechan

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

// shortUnauth shortens the life of an Unauth connection for one test.
func shortUnauth(t *testing.T, d time.Duration) {
	old := unauthTimeout
	unauthTimeout = d
	t.Cleanup(func() { unauthTimeout = old })
}

func unauthPair(t *testing.T) (client, server *Conn) {
	a, b := newDevice(t), newDevice(t)
	return connect(t, Config{Identity: a.id, AllowUnknown: true}, Config{Identity: b.id, AllowUnknown: true})
}

// readTimesOut reads c until an error and returns how long it took; it fails the test if the
// read is not ended by the deadline before limit.
func readTimesOut(t *testing.T, c *Conn, limit time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	done := make(chan error, 1)
	go func() { _, err := c.Read(make([]byte, 1)); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("read ended with %v, want a deadline", err)
		}
		return time.Since(start)
	case <-time.After(limit):
		c.Close()
		t.Fatalf("an Unauth connection still reads after %v", limit)
		return 0
	}
}

// Audit §3.5 (#156): a deadline set by a caller, or cleared, does not lift the Unauth bound
// (CHAN-04): only Authenticate does.
func TestUnauthDeadlineIsABound(t *testing.T) {
	shortUnauth(t, 300*time.Millisecond)
	for name, set := range map[string]func(c *Conn){
		"cleared":      func(c *Conn) { _ = c.SetDeadline(time.Time{}) },
		"read cleared": func(c *Conn) { _ = c.SetReadDeadline(time.Time{}) },
		"later":        func(c *Conn) { _ = c.SetReadDeadline(time.Now().Add(time.Hour)) },
	} {
		t.Run(name, func(t *testing.T) {
			_, s := unauthPair(t)
			set(s)
			readTimesOut(t, s, 3*time.Second)
		})
	}
}

// A deadline earlier than the Unauth bound still applies.
func TestUnauthKeepsEarlierDeadline(t *testing.T) {
	shortUnauth(t, 5*time.Second)
	_, s := unauthPair(t)
	_ = s.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if d := readTimesOut(t, s, 3*time.Second); d > 2*time.Second {
		t.Fatalf("the earlier deadline was not kept: read ended after %v", d)
	}
}

// AwaitPerson lets an Unauth connection wait for a person (a PIN typed), then the normal
// bound again: never without end.

// Authenticate lifts the bound: a cleared deadline is then no deadline.
func TestAuthenticateLiftsUnauthDeadline(t *testing.T) {
	shortUnauth(t, 200*time.Millisecond)
	c, s := unauthPair(t)
	c.Authenticate()
	s.Authenticate()
	_ = s.SetDeadline(time.Time{})
	go func() { time.Sleep(600 * time.Millisecond); _, _ = c.Write([]byte{1}) }()
	done := make(chan error, 1)
	go func() { _, err := s.Read(make([]byte, 1)); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("an authenticated connection was cut: %v", err)
		}
	case <-time.After(5 * time.Second):
		s.Close()
		t.Fatal("nothing read in 5 s")
	}
}

// WriteDeadline gives back the write deadline a caller asked for (session.Channel restores it
// after its own, audit 7).

var _ net.Conn = (*Conn)(nil)
