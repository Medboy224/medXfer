package engine

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"testing"
)

func TestBenchmarkDisk(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "medxfer_bench_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Run standard 4MB benchmark
	result, err := BenchmarkDisk(tempDir, 4*1024*1024)
	if err != nil {
		t.Fatalf("BenchmarkDisk failed: %v", err)
	}

	if result.WriteSpeedMBps <= 0 {
		t.Errorf("Expected positive write speed, got %f", result.WriteSpeedMBps)
	}
	if result.ReadSpeedMBps <= 0 {
		t.Errorf("Expected positive read speed, got %f", result.ReadSpeedMBps)
	}
	if result.TargetDir != tempDir {
		t.Errorf("Expected TargetDir %s, got %s", tempDir, result.TargetDir)
	}
	if result.Rating == "" {
		t.Errorf("Expected non-empty rating")
	}

	t.Logf("BenchmarkDisk Result: Write=%.2f MB/s, Read=%.2f MB/s, Rating=%s",
		result.WriteSpeedMBps, result.ReadSpeedMBps, result.Rating)
}

func TestQuickDiskProbe(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "medxfer_probe_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	speed, _, err := QuickDiskProbe(tempDir)
	if err != nil {
		t.Fatalf("QuickDiskProbe failed: %v", err)
	}

	if speed <= 0 {
		t.Errorf("Expected positive speed, got %f", speed)
	}
	t.Logf("QuickDiskProbe Speed: %.2f MB/s", speed)
}

func TestNetworkBurst(t *testing.T) {
	testSize := int64(8 * 1024 * 1024) // 8 MB

	port, waitFn, err := RunNetworkBurstServer(0, testSize, 2*1000000000)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	serverDone := make(chan struct {
		speed float64
		err   error
	}, 1)

	go func() {
		speed, sErr := waitFn()
		serverDone <- struct {
			speed float64
			err   error
		}{speed, sErr}
	}()

	clientSpeed, cErr := RunNetworkBurstClient(fmt.Sprintf("127.0.0.1:%d", port), testSize, 2*1000000000)
	if cErr != nil {
		t.Fatalf("client burst failed: %v", cErr)
	}

	res := <-serverDone
	if res.err != nil {
		t.Fatalf("server burst failed: %v", res.err)
	}

	t.Logf("Network Burst Loopback: Client=%.2f MB/s, Server=%.2f MB/s", clientSpeed, res.speed)
	if res.speed <= 0 || clientSpeed <= 0 {
		t.Errorf("Expected positive speeds")
	}
}

// On Windows the clock advances in ~15.6 ms ticks: a cached 4 MiB read can measure 0 s.
// The speed must stay finite, otherwise the result cannot be sent as JSON (the daemon then
// sent an empty WebSocket message and TestDaemonDiskBenchmarkAction failed on Windows).
func TestBenchmarkDiskResultIsJSONEncodable(t *testing.T) {
	for i := 0; i < 5; i++ {
		res, err := BenchmarkDisk(t.TempDir(), 4*1024*1024)
		if err != nil {
			t.Fatal(err)
		}
		for name, v := range map[string]float64{"write": res.WriteSpeedMBps, "read": res.ReadSpeedMBps} {
			if math.IsInf(v, 0) || math.IsNaN(v) || v <= 0 {
				t.Fatalf("%s speed = %v; want a finite positive number", name, v)
			}
		}
		if _, err := json.Marshal(res); err != nil {
			t.Fatalf("result not encodable as JSON: %v", err)
		}
	}
}
