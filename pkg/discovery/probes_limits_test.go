package discovery

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// endless gives bytes for ever, counting those read.
type endless struct{ read int }

func (e *endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	e.read += len(p)
	return len(p), nil
}

// #307: a peer's answer is read up to a bound, not to its end: an endless answer stops there.
func TestProbeAnswerIsBounded(t *testing.T) {
	src := &endless{}
	done := make(chan struct{})
	var err error
	go func() {
		_, err = readProbe(src)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("still reading after %d bytes", src.read)
	}
	if !errors.Is(err, errProbeTooLarge) {
		t.Fatalf("err %v", err)
	}
	if src.read > 64<<10 {
		t.Fatalf("read %d bytes for an answer of at most %d", src.read, maxProbeBytes)
	}
}

// #307: the responder reads a probe's query up to a bound: an endless query stops there.
func TestProbeQueryIsBounded(t *testing.T) {
	src := &endless{}
	done := make(chan struct{})
	go func() {
		readQuery(io.MultiReader(strings.NewReader(`{"pairing_code":"`), src))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("still reading after %d bytes", src.read)
	}
	if src.read > 64<<10 {
		t.Fatalf("read %d bytes for a query of at most %d", src.read, maxProbeBytes)
	}
}

// #307: probes answered at once are bounded: past the bound, a connection is closed at once
// instead of holding one more goroutine.
func TestProbeConnectionsAreCapped(t *testing.T) {
	addr := freeAddr(t)
	s := NewDiscoveryServer("node", 18887, &TransferOffer{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startProbes(ctx, addr)
	time.Sleep(50 * time.Millisecond)

	// Connections that send nothing: each holds the responder until its read deadline.
	for i := 0; i < maxProbeConns; i++ {
		c, err := net.Dial("tcp4", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
	}
	time.Sleep(50 * time.Millisecond)
	extra, err := net.Dial("tcp4", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	_ = extra.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := extra.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("one past the bound: %v, want closed at once", err)
	}
}
