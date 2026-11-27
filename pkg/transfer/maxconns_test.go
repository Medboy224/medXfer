package transfer

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

// #313, LIM-07: a transfer has MaxWorkers data connections at most. Each holds about 6 MiB of
// buffers on the sender: one more is closed at once.
func TestSenderRefusesConnectionsOverMaxWorkers(t *testing.T) {
	offer, paths, _ := tree(t, []int{4 * testChunk})
	s, err := NewSender(offer, paths)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Serve(ctx, ln) }()

	for i := 0; i < MaxWorkers; i++ {
		c, err := net.Dial("tcp4", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
	}
	for end := time.Now().Add(5 * time.Second); s.Progress().Streams < MaxWorkers; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("%d connections served, want %d", s.Progress().Streams, MaxWorkers)
		}
	}
	extra, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	_ = extra.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = extra.Read(make([]byte, 1))
	if !errors.Is(err, io.EOF) && !isReset(err) {
		t.Fatalf("connection %d: read %v; want it closed by the sender", MaxWorkers+1, err)
	}
	if n := s.Progress().Streams; n != MaxWorkers {
		t.Fatalf("%d connections served, want %d", n, MaxWorkers)
	}
}

// isReset reports a connection closed by the other side with a reset rather than an EOF.
func isReset(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && !op.Timeout() && !errors.Is(err, os.ErrDeadlineExceeded)
}
