package diag

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"
)

func startNetBench(t *testing.T, code string) (addr string, done chan error) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() { done <- ServeNetBench(ctx, ln, code, true, nil) }()
	t.Cleanup(cancel)
	return ln.Addr().String(), done
}

func TestNetBench(t *testing.T) {
	code := NewNetBenchCode()
	if len(code) != 8 || code == NewNetBenchCode() {
		t.Fatalf("codes must be 8 random hex characters, got %q", code)
	}
	addr, done := startNetBench(t, code)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A wrong code is refused and does not end a --once server.
	if _, err := RunNetBench(ctx, addr, "deadbeef", NetBenchMinSize); !errors.Is(err, errBadCode) {
		t.Fatalf("wrong code: %v; want errBadCode", err)
	}

	res, err := RunNetBench(ctx, addr, code, 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("server session: %v", err)
	}
	if res.UploadMBps <= 0 || res.DownloadMBps <= 0 || res.RTTAvgMs < res.RTTMinMs || res.RTTP95Ms < res.RTTMinMs {
		t.Fatalf("implausible result: %+v", res)
	}
	if res.Encrypted || res.SchemaVersion != SchemaVersion || res.LinkType == "" {
		t.Fatalf("result header: %+v", res)
	}
	if _, err := json.Marshal(res); err != nil { // no +Inf or NaN (see #15)
		t.Fatalf("result not JSON-encodable: %v", err)
	}
	t.Logf("loopback: %+v", res)
}

func TestNetBenchRejectsSizeOutOfRange(t *testing.T) {
	for _, size := range []int64{0, NetBenchMinSize - 1, NetBenchMaxSize + 1} {
		if _, err := RunNetBench(context.Background(), "127.0.0.1:1", "x", size); err == nil {
			t.Errorf("size %d accepted", size)
		}
	}
}
