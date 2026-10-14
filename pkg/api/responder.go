package api

import "github.com/gorilla/websocket"

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
