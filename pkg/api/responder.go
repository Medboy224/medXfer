package api

import (
	"encoding/json"

	"github.com/gorilla/websocket"
)

// Responder receives the replies to one request (D1 of the J6a plan): what only the client
// that asked should see, as opposed to Broadcast. A handler never knows the transport behind
// it: the dashboard's WebSocket today, the in-process caller of pkg/core next.
type Responder interface {
	Reply(evt EventMessage)
}

// wsResponder answers a request that came over the dashboard's WebSocket.
type wsResponder struct {
	s    *DaemonServer
	conn *websocket.Conn
}

func (r wsResponder) Reply(evt EventMessage) { r.s.sendTo(r.conn, evt) }

// jsonResponder answers with the event encoded in JSON, as the WebSocket would carry it.
type jsonResponder func(event []byte)

func (r jsonResponder) Reply(evt EventMessage) {
	if b, err := json.Marshal(evt); err == nil {
		r(b)
	}
}

// HandleJSON runs one request given in JSON; each reply meant for the caller alone reaches
// reply, in JSON. It is the in-process transport of testkit and xfer ctl (WP-6.3): the same
// bytes as the WebSocket, without a socket.
func (s *DaemonServer) HandleJSON(request []byte, reply func(event []byte)) {
	var req RequestMessage
	if err := json.Unmarshal(request, &req); err != nil {
		jsonResponder(reply).Reply(NewEvent("action_error", map[string]string{"error": "invalid request: " + err.Error()}))
		return
	}
	s.dispatch(jsonResponder(reply), req)
}

// SubscribeJSON is Subscribe with every broadcast event encoded in JSON.
func (s *DaemonServer) SubscribeJSON(fn func(event []byte)) (cancel func()) {
	return s.Subscribe(func(evt EventMessage) {
		if b, err := json.Marshal(evt); err == nil {
			fn(b)
		}
	})
}
