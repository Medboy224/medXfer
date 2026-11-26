package main

import (
	"net"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/session"
)

// #306: xfer node ignores the peer's progress and pings, sent a few times a second, instead of
// answering each with unsupported; another unknown message still gets its answer.
func TestNodeIgnoresProgressAndPings(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	dialed := make(chan net.Conn, 1)
	go func() {
		c, _ := net.Dial("tcp4", ln.Addr().String())
		dialed <- c
	}()
	a, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	b := <-dialed
	if b == nil {
		t.Fatal("no connection")
	}
	node, peer := session.NewChannel(a), session.NewChannel(b)
	defer node.Close()
	defer peer.Close()

	for i := 0; i < 5; i++ {
		answerUnknown(node, session.Message{Type: session.TypeProgress})
		answerUnknown(node, session.Message{Type: session.TypePing})
	}
	answerUnknown(node, session.Message{Type: "not_a_type"})
	_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
	m, err := peer.Read()
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != session.TypeError || m.Error != session.Unsupported("not_a_type").Error {
		t.Fatalf("first answer %q (%s): progress and pings were answered", m.Error, m.Type)
	}
}
