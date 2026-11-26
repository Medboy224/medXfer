package api

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/session"
)

// otherPeer is a connection that reports another remote address: the session of a peer at
// 192.0.2.1 (TEST-NET-1), so that a connection from this machine is not the peer's.
type otherPeer struct{ net.Conn }

func (otherPeer) RemoteAddr() net.Addr { return &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 40000} }

// benchSession is a session over the loopback: the side that answers the benchmark, its remote
// address reported by wrap, and the side that asks for it.
func benchSession(t *testing.T, wrap func(net.Conn) net.Conn) (answers, asks *session.Channel) {
	t.Helper()
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
	answers, asks = session.NewChannel(wrap(a)), session.NewChannel(b)
	t.Cleanup(func() { answers.Close(); asks.Close() })
	return answers, asks
}

// readBench is the next message of the benchmark that asks sees.
func readBench(t *testing.T, asks *session.Channel, want string) session.Message {
	t.Helper()
	_ = asks.SetReadDeadline(time.Now().Add(10 * time.Second))
	m, err := asks.Read()
	if err != nil || m.Type != want {
		t.Fatalf("got %+v (%v), want %s", m, err, want)
	}
	return m
}

// #313: the burst port takes the connection of the session's peer only. Another address that
// reaches it first is closed and measures nothing.
func TestAnswerBenchOnlyThePeer(t *testing.T) {
	answers, asks := benchSession(t, func(c net.Conn) net.Conn { return otherPeer{c} })
	const size = 1 << 20
	AnswerBench(answers, session.Message{Type: "bench_burst_req", BenchSize: size})
	ready := readBench(t, asks, "bench_burst_ready")
	c, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(ready.DataPort))), 2*time.Second)
	if err == nil {
		_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, _ = c.Write(make([]byte, size))
		c.Close()
	}
	if ack := readBench(t, asks, "bench_burst_ack"); ack.FileSize != 0 {
		t.Fatalf("a connection from another address than the peer's was measured: %d", ack.FileSize)
	}
}

// #313: the size the peer asks for is bounded.
func TestAnswerBenchBoundsTheSize(t *testing.T) {
	answers, asks := benchSession(t, func(c net.Conn) net.Conn { return c })
	AnswerBench(answers, session.Message{Type: "bench_burst_req", BenchSize: 1 << 40})
	if ready := readBench(t, asks, "bench_burst_ready"); ready.BenchSize > maxBenchBurst {
		t.Fatalf("granted %d bytes, want at most %d", ready.BenchSize, maxBenchBurst)
	}
}
