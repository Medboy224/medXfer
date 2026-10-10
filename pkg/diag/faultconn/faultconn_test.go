//go:build diag

package faultconn

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
)

// tcpPair returns the two ends of a loopback TCP connection.
func tcpPair(t *testing.T) (client, server net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		accepted <- c
	}()
	client, err = net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server = <-accepted
	t.Cleanup(func() { client.Close(); server.Close() })
	return client, server
}

func TestLimiterCapsThroughput(t *testing.T) {
	client, server := tcpPair(t)
	const size, rate = 200 * 1024, 1 << 20 // 200 KiB at 1 MiB/s: about 0.2 s
	w := Wrap(client, Plan{Limiter: NewLimiter(rate)})
	go func() { _, _ = w.Write(make([]byte, size)); w.Close() }()

	start := time.Now()
	n, err := io.Copy(io.Discard, server)
	elapsed := time.Since(start)
	if err != nil || n != size {
		t.Fatalf("received %d, %v; want %d", n, err, size)
	}
	if elapsed < 150*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("200 KiB at 1 MiB/s took %v; want about 200ms", elapsed)
	}
}

func TestCutAfterBytes(t *testing.T) {
	for _, reset := range []bool{false, true} {
		client, server := tcpPair(t)
		w := Wrap(client, Plan{CutAfter: 4096, Reset: reset})
		n, err := w.Write(make([]byte, 10000))
		if n != 4096 || !errors.Is(err, ErrCut) {
			t.Fatalf("reset=%v: Write = %d, %v; want 4096, ErrCut", reset, n, err)
		}
		if _, err := w.Write([]byte{1}); !errors.Is(err, ErrCut) {
			t.Fatalf("reset=%v: write after the cut = %v; want ErrCut", reset, err)
		}
		got, err := io.ReadAll(server)
		if reset {
			// An RST may discard data still in flight: only the error matters.
			if err == nil {
				t.Fatalf("reset: peer saw a clean close after %d bytes; want a reset error", len(got))
			}
		} else if err != nil || len(got) != 4096 {
			t.Fatalf("close: peer read %d, %v; want 4096 bytes then EOF", len(got), err)
		}
	}
}

func TestJitterIsDeterministic(t *testing.T) {
	seq := func(seed int64) []time.Duration {
		c := Wrap(nil, Plan{Seed: seed, Latency: time.Millisecond, Jitter: time.Second}).(*conn)
		var out []time.Duration
		for i := 0; i < 5; i++ {
			out = append(out, c.delay())
		}
		return out
	}
	a, b, other := seq(42), seq(42), seq(7)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("same seed, different delays: %v vs %v", a, b)
		}
		if a[i] < time.Millisecond || a[i] >= time.Millisecond+time.Second {
			t.Fatalf("delay %v outside [latency, latency+jitter)", a[i])
		}
	}
	same := true
	for i := range a {
		same = same && a[i] == other[i]
	}
	if same {
		t.Fatalf("seeds 42 and 7 gave the same delays %v", a)
	}
}

type nopListener struct{}

func (nopListener) OnStart(string, int64, uint32)    {}
func (nopListener) OnProgress(engine.TransferStats)  {}
func (nopListener) OnChunkFailed(uint32, int, error) {}
func (nopListener) OnComplete(string, time.Duration) {}
func (nopListener) OnError(error)                    {}

// transfer sends size bytes through the engine on loopback and returns the elapsed time.
func transfer(t *testing.T, size int, chunk uint32) time.Duration {
	t.Helper()
	src := filepath.Join(t.TempDir(), "f.bin")
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i * 7 % 251)
	}
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ready := make(chan error, 1)
	go func() {
		_ = engine.NewSender(2, chunk).ServeAndSendWithRelPathReady(ctx, addr, src, "f.bin", nopListener{}, 0, ready)
	}()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	start := time.Now()
	if err := engine.NewReceiver(dst, 2).Pull(ctx, addr, nopListener{}, engine.GenerateFileID(src)); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	elapsed := time.Since(start)
	got, err := os.ReadFile(filepath.Join(dst, "f.bin"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("received file differs from the source (err %v, %d of %d bytes)", err, len(got), len(data))
	}
	return elapsed
}

// The engine's connections go through the installed wrapper: a shared limiter slows the
// whole transfer down, and the file still arrives intact.
func TestEngineTransferThrottled(t *testing.T) {
	lim := NewLimiter(2 << 20) // 2 MiB/s for all connections
	restore := engine.SetConnWrapper(func(_ engine.ConnRole, c net.Conn) net.Conn {
		return Wrap(c, Plan{Limiter: lim})
	})
	defer restore()

	if elapsed := transfer(t, 1<<20, 64*1024); elapsed < 300*time.Millisecond {
		t.Fatalf("1 MiB at 2 MiB/s took %v; the limiter was not applied", elapsed)
	}
}

// A data connection reset mid-chunk: the receiver reconnects, retries, and the file is intact.
func TestEngineTransferSurvivesCut(t *testing.T) {
	var dialed atomic.Int32
	restore := engine.SetConnWrapper(func(role engine.ConnRole, c net.Conn) net.Conn {
		if role == engine.ConnDialed && dialed.Add(1) == 2 { // 1 = handshake, 2 = first data worker
			return Wrap(c, Plan{CutAfter: 200 * 1024, Reset: true})
		}
		return c
	})
	defer restore()

	transfer(t, 1<<20, 64*1024)
	if dialed.Load() < 3 {
		t.Fatalf("only %d connections dialed; the receiver did not reconnect after the cut", dialed.Load())
	}
}
