package api

import (
	"sync"
	"testing"
)

// recorder is a Responder in memory: the replies a request got, without any transport.
type recorder struct {
	mu      sync.Mutex
	replies []EventMessage
}

func (r *recorder) Reply(evt EventMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replies = append(r.replies, evt)
}

// WP-6.1 (D1 of the J6a plan): the handlers answer whoever asked through Responder, with no
// WebSocket behind it. The reply carries the request's id.
func TestHandlersAnswerWithoutWebSocket(t *testing.T) {
	s, _ := startOwnIdentity(t, "InProcess")
	for _, c := range []struct{ action, want string }{
		{"get_status", "status"},
		{"no_such_action", "action_error"},
		{"pair_pin", "action_error"}, // no payload: refused before anything is sent
	} {
		rec := &recorder{}
		s.dispatch(rec, RequestMessage{ID: "req-" + c.action, Action: c.action})
		if len(rec.replies) != 1 || rec.replies[0].Event != c.want || rec.replies[0].ID != "req-"+c.action {
			t.Fatalf("%s: replies %+v, want one %s", c.action, rec.replies, c.want)
		}
	}
}
