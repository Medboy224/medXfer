package api

import (
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/session"
)

// #306: the peer's unsupported answers are logged once per message type and session: an older
// peer answered each progress, a few times a second, and the journal turned in minutes.
func TestUnsupportedIsLoggedOncePerType(t *testing.T) {
	s, n := startOwnIdentity(t, "Laptop")
	for i := 0; i < 5; i++ {
		s.handleSessionMessage(session.Unsupported(session.TypeProgress))
	}
	s.handleSessionMessage(session.Unsupported(session.TypeTransferCheck))
	s.handleSessionMessage(session.Unsupported(session.TypeTransferCheck))

	count := func(what string) int {
		c := 0
		for _, e := range n.Client.Events() {
			if e.Event == "log" && strings.Contains(string(e.Data), session.Unsupported(what).Error) {
				c++
			}
		}
		return c
	}
	for end := time.Now().Add(5 * time.Second); count(session.TypeTransferCheck) == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the unsupported transfer_check was not logged")
		}
	}
	time.Sleep(200 * time.Millisecond) // the other lines, if any, arrive meanwhile
	if p, c := count(session.TypeProgress), count(session.TypeTransferCheck); p != 1 || c != 1 {
		t.Fatalf("logged %d times for progress and %d for transfer_check, want once each", p, c)
	}
}
