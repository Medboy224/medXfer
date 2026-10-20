// Package testkit starts daemons for tests and drives them like the user interface does
// (DEV-15). Since J6a (WP-6.3) it calls the daemon in the same process, without the control
// WebSocket: the same requests and events, the same vocabulary (StartPair, Send, AcceptOffer,
// WaitEvent). Only the tests of the dashboard's transport open a WebSocket.
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
	StartNode() error            // identity, single port, discovery; Serve calls it too
	Serve(ln net.Listener) error // the HTTP server: Web Share portal
	Stop()
	NodeListenAddr() string // "" until Serve has opened the peer listener
	Fingerprint() string    // this daemon's key, once Serve has loaded it
	TrustDevice(fingerprint, alias string) error
	// The in-process control transport (WP-6.3): requests and events in the JSON the
	// WebSocket would carry.
	HandleJSON(request []byte, reply func(event []byte))
	SubscribeJSON(fn func(event []byte)) (cancel func())
}

// Event is an event of the control vocabulary.
type Event = wsclient.Event

// Node is a running daemon with a control client in the same process.
type Node struct {
	t        testing.TB
	Server   Server
	HTTPAddr string // 127.0.0.1:port of the HTTP server
	Client   *wsclient.Client
}

// StartNode serves srv on a random port (its HTTP server), connects an in-process control
// client, and stops both when the test ends. As a WebSocket client would, the client first
// gets the daemon's "status" event, left in the buffer.
func StartNode(t testing.TB, srv Server) *Node {
	t.Helper()
	ln, err := srv.Listen(0)
	if err != nil {
		t.Fatalf("testkit: listen: %v", err)
	}
	t.Cleanup(srv.Stop)
	// The node is up before the first request, as it was once a WebSocket could connect.
	if err := srv.StartNode(); err != nil {
		t.Fatalf("testkit: start: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()

	addr := "127.0.0.1:" + portOf(ln.Addr().String())
	c := wsclient.Local(srv.HandleJSON, srv.SubscribeJSON)
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.Send("", "get_status", nil); err != nil { // the status a WebSocket gets first
		t.Fatalf("testkit: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	if _, err := c.WaitFor(ctx, func(e Event) bool { return e.Event == "status" }); err != nil {
		t.Fatalf("testkit: no first status: %v", err)
	}
	c.Rewind() // left in the buffer, as before
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

// StartPair starts both daemons with StartNode and makes each one trust the other, as a
// pairing by QR code would: their control connections are then admitted at once.
func StartPair(t testing.TB, sender, receiver Server) *Pair {
	t.Helper()
	p := &Pair{Sender: StartNode(t, sender), Receiver: StartNode(t, receiver)}
	for _, n := range []struct {
		self, other Server
		alias       string
	}{{sender, receiver, "receiver"}, {receiver, sender, "sender"}} {
		if err := n.self.TrustDevice(n.other.Fingerprint(), n.alias); err != nil {
			t.Fatalf("testkit: trust: %v", err)
		}
	}
	return p
}

// SendOptions are the optional fields of a "send" request (none since StreamTar is gone).
type SendOptions struct{}

// Send asks the sender to send paths to the receiver, which it trusts, and returns the
// request id.
func (p *Pair) Send(paths []string, opts SendOptions) string {
	p.Sender.t.Helper()
	payload := map[string]any{
		"paths":     paths,
		"target_ip": p.Receiver.PeerAddr(),
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

// WaitTransferDone waits for the receiver's transfer_complete and returns it. The daemon
// writes nothing in the receive folder besides the transfer's files: it has no disk probe
// any more (D10, #36).
func (p *Pair) WaitTransferDone(timeout time.Duration) Event {
	p.Receiver.t.Helper()
	return p.Receiver.WaitEvent("transfer_complete", timeout)
}

// AssertTreesEqual fails the test unless directories want and got hold the same files with
// the same contents. Empty directories and the files of a transfer in progress (*.part,
// *.part.state, the root's .medxfer-transfer marker) are ignored.
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
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".part") || strings.HasSuffix(path, ".part.state") ||
			filepath.Base(path) == ".medxfer-transfer" {
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
