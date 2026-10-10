// Package testkit starts daemons for tests and drives them like the user interface does,
// through the control WebSocket (DEV-15, version 0). The same vocabulary (StartPair, Send,
// AcceptOffer, WaitEvent) will be re-implemented with direct calls into the core in J6a,
// without changing the tests.
//
// testkit does not import pkg/api: tests pass the daemon in, as anything that satisfies
// Server, so the api package's own tests can use it.
package testkit

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/wsclient"
)

// DefaultTimeout bounds every wait that does not give its own.
const DefaultTimeout = 10 * time.Second

// Server is what testkit needs from a daemon; *api.DaemonServer implements it.
type Server interface {
	Listen(port int) (net.Listener, error)
	Serve(ln net.Listener) error
	Stop()
	ControlToken() string
	NodeListenAddr() string // "" until Serve has opened the peer listener
	PairingCode() string
}

// Event is an event of the control WebSocket.
type Event = wsclient.Event

// Node is a running daemon with a connected control client.
type Node struct {
	t        testing.TB
	Server   Server
	HTTPAddr string // 127.0.0.1:port of the HTTP server
	Client   *wsclient.Client
}

// StartNode serves srv on a random port, connects a control client, and stops both when the
// test ends. The daemon's first "status" event is left in the buffer.
func StartNode(t testing.TB, srv Server) *Node {
	t.Helper()
	ln, err := srv.Listen(0)
	if err != nil {
		t.Fatalf("testkit: listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)

	addr := "127.0.0.1:" + portOf(ln.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	c, err := wsclient.Dial(ctx, addr, srv.ControlToken())
	if err != nil {
		t.Fatalf("testkit: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return &Node{t: t, Server: srv, HTTPAddr: addr, Client: c}
}

// Port returns the port of the node's HTTP server.
func (n *Node) Port() int {
	_, port, _ := net.SplitHostPort(n.HTTPAddr)
	p, _ := strconv.Atoi(port)
	return p
}

// URL returns the address of path on the node's HTTP server.
func (n *Node) URL(path string) string { return "http://" + n.HTTPAddr + path }

// ControlRequest builds an HTTP request carrying the node's control token.
func (n *Node) ControlRequest(method, path string) *http.Request {
	n.t.Helper()
	req, err := http.NewRequest(method, n.URL(path), nil)
	if err != nil {
		n.t.Fatalf("testkit: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+n.Server.ControlToken())
	return req
}

// PeerAddr returns the loopback address of the node's peer (pairing) listener, waiting for
// Serve to open it.
func (n *Node) PeerAddr() string {
	n.t.Helper()
	deadline := time.Now().Add(DefaultTimeout)
	for {
		if a := n.Server.NodeListenAddr(); a != "" {
			return "127.0.0.1:" + portOf(a)
		}
		if time.Now().After(deadline) {
			n.t.Fatal("testkit: peer listener not started")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Request sends an action on the control WebSocket and returns its request id. payload may
// be nil, a JSON string, a json.RawMessage, or a value to encode.
func (n *Node) Request(action string, payload any) string {
	n.t.Helper()
	return n.RequestID("", action, payload)
}

// RequestID is Request with a chosen request id.
func (n *Node) RequestID(id, action string, payload any) string {
	n.t.Helper()
	id, err := n.Client.Send(id, action, payload)
	if err != nil {
		n.t.Fatalf("testkit: send %s: %v", action, err)
	}
	return id
}

// WaitFor returns the first new event that matches, skipping the others. It fails the test
// after timeout (DefaultTimeout when 0).
func (n *Node) WaitFor(what string, match func(Event) bool, timeout time.Duration) Event {
	n.t.Helper()
	e, ok := n.TryWaitFor(match, timeout)
	if !ok {
		n.t.Fatalf("testkit: no %s within %v; events received: %s", what, orDefault(timeout), n.eventNames())
	}
	return e
}

// TryWaitFor is WaitFor without failing: ok is false at the timeout or if the connection closed.
func (n *Node) TryWaitFor(match func(Event) bool, timeout time.Duration) (Event, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), orDefault(timeout))
	defer cancel()
	e, err := n.Client.WaitFor(ctx, match)
	return e, err == nil
}

// WaitEvent returns the next event named name, skipping the others.
func (n *Node) WaitEvent(name string, timeout time.Duration) Event {
	n.t.Helper()
	return n.WaitFor("event "+name, func(e Event) bool { return e.Event == name }, timeout)
}

// WaitEventAny returns the next event whose name is one of names.
func (n *Node) WaitEventAny(timeout time.Duration, names ...string) Event {
	n.t.Helper()
	return n.WaitFor("event in "+strings.Join(names, ", "), func(e Event) bool {
		for _, name := range names {
			if e.Event == name {
				return true
			}
		}
		return false
	}, timeout)
}

// WaitReply returns the next event answering request id.
func (n *Node) WaitReply(id string, timeout time.Duration) Event {
	n.t.Helper()
	return n.WaitFor("reply to "+id, func(e Event) bool { return e.ID == id }, timeout)
}

// ExpectNoEvent fails the test if an event named name arrives within d.
func (n *Node) ExpectNoEvent(name string, d time.Duration) {
	n.t.Helper()
	if e, ok := n.TryWaitFor(func(e Event) bool { return e.Event == name }, d); ok {
		n.t.Fatalf("testkit: unexpected event %s: %s", name, e.Data)
	}
}

// NextEvent returns the next event, whatever it is.
func (n *Node) NextEvent(timeout time.Duration) Event {
	n.t.Helper()
	return n.WaitFor("event", nil, timeout)
}

func (n *Node) eventNames() string {
	var names []string
	for _, e := range n.Client.Events() {
		names = append(names, e.Event)
	}
	return "[" + strings.Join(names, " ") + "]"
}

// Pair is a sender and a receiver daemon.
type Pair struct {
	Sender, Receiver *Node
}

// StartPair starts both daemons with StartNode.
func StartPair(t testing.TB, sender, receiver Server) *Pair {
	t.Helper()
	return &Pair{Sender: StartNode(t, sender), Receiver: StartNode(t, receiver)}
}

// SendOptions are the optional fields of a "send" request.
type SendOptions struct {
	UseTarStream bool
}

// Send asks the sender to send paths to the receiver, pairing with the receiver's code, and
// returns the request id.
func (p *Pair) Send(paths []string, opts SendOptions) string {
	p.Sender.t.Helper()
	payload := map[string]any{
		"paths":     paths,
		"target_ip": p.Receiver.PeerAddr(),
		"code":      p.Receiver.Server.PairingCode(),
	}
	if opts.UseTarStream {
		payload["use_tar_stream"] = true
	}
	return p.Sender.Request("send", payload)
}

// AcceptOffer waits for the receiver's incoming offer, accepts it and returns the offer event.
func (p *Pair) AcceptOffer(timeout time.Duration) Event {
	p.Receiver.t.Helper()
	offer := p.Receiver.WaitEvent("incoming_offer", timeout)
	p.Receiver.Request("respond_offer", map[string]any{"accept": true})
	return offer
}

// WaitTransferDone waits for the receiver's transfer_complete and for the end of the
// pre-flight disk probe (transfer_preflight), in either order, and returns the completion
// event. The daemon runs that probe in the receive folder when an offer is accepted; until it
// ends, its temporary file is in that folder (issue #36).
func (p *Pair) WaitTransferDone(timeout time.Duration) Event {
	p.Receiver.t.Helper()
	var complete Event
	var done, probed bool
	for !done || !probed {
		e := p.Receiver.WaitEventAny(timeout, "transfer_complete", "transfer_preflight")
		if e.Event == "transfer_complete" {
			complete, done = e, true
		} else {
			probed = true
		}
	}
	return complete
}

// AssertTreesEqual fails the test unless directories want and got hold the same files with
// the same contents. Empty directories and *.medxfer state files are ignored.
func AssertTreesEqual(t testing.TB, want, got string) {
	t.Helper()
	problems, err := treeDiff(want, got)
	if err != nil {
		t.Fatalf("testkit: %v", err)
	}
	if len(problems) > 0 {
		t.Fatalf("trees differ (%s vs %s):\n  %s", want, got, strings.Join(problems, "\n  "))
	}
}

// treeDiff lists the differences between two directory trees, sorted.
func treeDiff(want, got string) ([]string, error) {
	w, err := readTree(want)
	if err != nil {
		return nil, err
	}
	g, err := readTree(got)
	if err != nil {
		return nil, err
	}
	var problems []string
	for rel, data := range w {
		gd, ok := g[rel]
		switch {
		case !ok:
			problems = append(problems, "missing "+rel)
		case !bytes.Equal(gd, data):
			problems = append(problems, "different content: "+rel)
		}
	}
	for rel := range g {
		if _, ok := w[rel]; !ok {
			problems = append(problems, "unexpected "+rel)
		}
	}
	sort.Strings(problems)
	return problems, nil
}

func readTree(root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".medxfer") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read tree %s: %w", root, err)
	}
	return files, nil
}

func portOf(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return port
}

func orDefault(d time.Duration) time.Duration {
	if d <= 0 {
		return DefaultTimeout
	}
	return d
}
