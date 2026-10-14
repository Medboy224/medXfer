package wsclient

import (
	"encoding/json"
	"errors"
	"sync"
)

// ErrClosed ends the waits of a Local client once it is closed.
var ErrClosed = errors.New("wsclient: closed")

// Local connects to a daemon in the same process, without a socket (WP-6.3 of the J6a plan):
// handle runs one request and gives its replies to reply; subscribe delivers every broadcast
// event. Both carry the JSON the WebSocket would. *api.DaemonServer provides them as
// HandleJSON and SubscribeJSON.
//
// As over the WebSocket, Send returns at once and the daemon runs the requests one at a time,
// in order: a handler that waits does not block the caller.
func Local(handle func(request []byte, reply func(event []byte)), subscribe func(fn func(event []byte)) (cancel func())) *Client {
	c := &Client{changed: make(chan struct{})}
	t := &localTransport{c: c, handle: handle, requests: make(chan []byte, 256), done: make(chan struct{})}
	c.tr = t
	t.unsubscribe = subscribe(c.deliverJSON)
	go t.run()
	return c
}

// deliverJSON records an event given in JSON.
func (c *Client) deliverJSON(data []byte) {
	var e Event
	if err := json.Unmarshal(data, &e); err == nil {
		c.deliver(e, nil)
	}
}

type localTransport struct {
	c           *Client
	handle      func(request []byte, reply func(event []byte))
	unsubscribe func()
	requests    chan []byte
	done        chan struct{}
	once        sync.Once
}

func (t *localTransport) send(request []byte) error {
	select { // closed first: select picks at random between ready cases
	case <-t.done:
		return ErrClosed
	default:
	}
	select {
	case t.requests <- request:
		return nil
	case <-t.done:
		return ErrClosed
	}
}

// run hands the requests to the daemon one at a time, in order, like the WebSocket's reader.
func (t *localTransport) run() {
	for {
		select {
		case <-t.done:
			return
		case req := <-t.requests:
			select {
			case <-t.done: // closed meanwhile: the request is dropped, as on a closed socket
				return
			default:
			}
			t.handle(req, t.c.deliverJSON)
		}
	}
}

// close stops the requests and the events, and ends the waits with ErrClosed.
func (t *localTransport) close() error {
	t.once.Do(func() {
		t.unsubscribe()
		close(t.done)
		t.c.deliver(Event{}, ErrClosed)
	})
	return nil
}
