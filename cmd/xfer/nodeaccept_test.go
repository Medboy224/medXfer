package main

import (
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// #305: a connection that waits, as one whose PIN is being typed, holds no other: a device of
// mutual trust that connects meanwhile is served at once.
func TestAcceptEachServesConnectionsApart(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	release := make(chan struct{})
	defer close(release)
	var n atomic.Int32
	served := make(chan int32, 2)
	go acceptEach(ln, func(c net.Conn) {
		defer c.Close()
		i := n.Add(1)
		if i == 1 {
			<-release // the PIN typed, minutes later
		}
		served <- i
	})

	first, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	for end := time.Now().Add(2 * time.Second); n.Load() == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the first connection was not taken")
		}
	}
	second, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	select {
	case i := <-served:
		if i != 2 {
			t.Fatalf("served %d first, want the second connection", i)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the second connection waited for the first")
	}
}
