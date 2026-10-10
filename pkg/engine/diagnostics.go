package engine

import (
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

// DiskBenchResult contains detailed metrics for a storage benchmark
type DiskBenchResult struct {
	TargetDir       string  `json:"target_dir"`
	SizeBytes       int64   `json:"size_bytes"`
	WriteSpeedMBps  float64 `json:"write_speed_mbps"`
	ReadSpeedMBps   float64 `json:"read_speed_mbps"`
	WriteDurationMs int64   `json:"write_duration_ms"`
	ReadDurationMs  int64   `json:"read_duration_ms"`
	Rating          string  `json:"rating"`
	Warning         string  `json:"warning,omitempty"`
}

// NetBenchResult contains metrics for a peer network benchmark
type NetBenchResult struct {
	RemoteIP       string  `json:"remote_ip"`
	MinRTTMs       float64 `json:"min_rtt_ms"`
	AvgRTTMs       float64 `json:"avg_rtt_ms"`
	MaxRTTMs       float64 `json:"max_rtt_ms"`
	ThroughputMBps float64 `json:"throughput_mbps"`
	DurationMs     int64   `json:"duration_ms"`
	LinkType       string  `json:"link_type"`
	Rating         string  `json:"rating"`
}

// BenchmarkDisk executes a synchronous write and read benchmark on the specified directory.
// It writes sizeBytes using buffered blocks, forces an OS sync (fsync) to measure raw hardware
// capabilities (bypassing volatile RAM cache), reads the data back, and removes the temp file.
func BenchmarkDisk(dir string, sizeBytes int64) (*DiskBenchResult, error) {
	if sizeBytes <= 0 {
		sizeBytes = 32 * 1024 * 1024 // Default 32 MB
	}
	if sizeBytes < 4*1024*1024 {
		sizeBytes = 4 * 1024 * 1024
	}
	if sizeBytes > 512*1024*1024 {
		sizeBytes = 512 * 1024 * 1024
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to access target directory: %w", err)
	}

	testFileName := fmt.Sprintf(".medxfer_bench_%d.tmp", time.Now().UnixNano())
	testFilePath := filepath.Join(dir, testFileName)

	defer func() {
		_ = os.Remove(testFilePath)
	}()

	// 1 MiB of random data (DEV-09): zeros or a short repeated pattern would let a compressing
	// or deduplicating file system skip most of the writes.
	blockSize := 1024 * 1024
	pattern := make([]byte, blockSize)
	_, _ = rand.Read(pattern)

	// 1. Write Benchmark
	f, err := os.OpenFile(testFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create benchmark file: %w", err)
	}

	var written int64
	writeStart := time.Now()

	for written < sizeBytes {
		toWrite := int64(blockSize)
		if sizeBytes-written < toWrite {
			toWrite = sizeBytes - written
		}
		n, writeErr := f.Write(pattern[:toWrite])
		if writeErr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("write error during disk benchmark: %w", writeErr)
		}
		written += int64(n)
	}

	// Sync to hardware storage to include raw drive commit time
	if syncErr := f.Sync(); syncErr != nil {
		_ = f.Close()
		return nil, fmt.Errorf("sync error during disk benchmark: %w", syncErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		return nil, fmt.Errorf("close error during disk benchmark: %w", closeErr)
	}

	writeDuration := time.Since(writeStart)
	// Floor at 1 ms: on Windows the clock advances in ~15.6 ms ticks, a cached 4 MiB
	// write can measure 0, and the resulting +Inf speed cannot be encoded as JSON.
	if writeDuration < time.Millisecond {
		writeDuration = time.Millisecond
	}
	writeDurationMs := writeDuration.Milliseconds()

	writeSpeedMBps := (float64(sizeBytes) / (1024 * 1024)) / writeDuration.Seconds()

	// 2. Read Benchmark
	rf, err := os.Open(testFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to re-open file for read benchmark: %w", err)
	}

	readBuffer := make([]byte, blockSize)
	var bytesRead int64
	readStart := time.Now()

	for {
		n, rErr := rf.Read(readBuffer)
		if n > 0 {
			bytesRead += int64(n)
		}
		if rErr == io.EOF {
			break
		}
		if rErr != nil {
			_ = rf.Close()
			return nil, fmt.Errorf("read error during disk benchmark: %w", rErr)
		}
	}
	_ = rf.Close()

	readDuration := time.Since(readStart)
	// Floor at 1 ms: on Windows the clock advances in ~15.6 ms ticks, a cached 4 MiB
	// read can measure 0, and the resulting +Inf speed cannot be encoded as JSON.
	if readDuration < time.Millisecond {
		readDuration = time.Millisecond
	}
	readDurationMs := readDuration.Milliseconds()

	readSpeedMBps := (float64(bytesRead) / (1024 * 1024)) / readDuration.Seconds()

	// Categorize storage capability and potential bottlenecks
	rating := "NVMe SSD (Ultra-Fast)"
	warning := ""

	switch {
	case writeSpeedMBps >= 180:
		rating = "NVMe / PCIe SSD (Ultra-Fast)"
	case writeSpeedMBps >= 60:
		rating = "SATA SSD / Fast Internal HDD (Good)"
	case writeSpeedMBps >= 25:
		rating = "Mechanical HDD / USB 3.0 (Moderate)"
		warning = "Moderate storage speed. High-speed transfers (>30 MB/s) may occasionally wait on disk flush."
	default:
		rating = "Slow Storage / USB 2.0 / SD Card (Bottleneck)"
		warning = fmt.Sprintf("Physical write speed is only %.1f MB/s. Transfers will be capped by storage.", writeSpeedMBps)
	}

	return &DiskBenchResult{
		TargetDir:       dir,
		SizeBytes:       sizeBytes,
		WriteSpeedMBps:  writeSpeedMBps,
		ReadSpeedMBps:   readSpeedMBps,
		WriteDurationMs: writeDurationMs,
		ReadDurationMs:  readDurationMs,
		Rating:          rating,
		Warning:         warning,
	}, nil
}

// QuickDiskProbe conducts an ultra-fast (~50ms) write check to detect slow storage before transfer start
func QuickDiskProbe(dir string) (writeSpeedMBps float64, isSlow bool, err error) {
	bench, err := BenchmarkDisk(dir, 8*1024*1024) // 8 MB burst
	if err != nil {
		return 0, false, err
	}
	return bench.WriteSpeedMBps, bench.WriteSpeedMBps < 25.0, nil
}

// RunNetworkBurstServer starts an in-memory TCP drain listener on the specified port (or 0 for dynamic port).
// It accepts one connection, reads sizeBytes (or until EOF/timeout), closes the connection, and reports speed.
func RunNetworkBurstServer(port int, sizeBytes int64, timeout time.Duration) (int, func() (float64, error), error) {
	listener, err := net.Listen("tcp4", fmt.Sprintf(":%d", port))
	if err != nil {
		return 0, nil, fmt.Errorf("failed to listen for net bench: %w", err)
	}

	actualPort := listener.Addr().(*net.TCPAddr).Port

	waitFn := func() (float64, error) {
		defer listener.Close()

		if tcpL, ok := listener.(*net.TCPListener); ok && timeout > 0 {
			_ = tcpL.SetDeadline(time.Now().Add(timeout))
		}

		conn, err := listener.Accept()
		if err != nil {
			return 0, fmt.Errorf("failed to accept net bench conn: %w", err)
		}
		defer conn.Close()

		if tcpC, ok := conn.(*net.TCPConn); ok {
			_ = tcpC.SetNoDelay(true)
			_ = tcpC.SetReadBuffer(4 * 1024 * 1024)
		}

		buf := make([]byte, 128*1024)
		var totalRead int64
		start := time.Now()

		for totalRead < sizeBytes {
			if timeout > 0 {
				_ = conn.SetReadDeadline(time.Now().Add(timeout))
			}
			n, rErr := conn.Read(buf)
			if n > 0 {
				totalRead += int64(n)
			}
			if rErr != nil {
				if rErr == io.EOF {
					break
				}
				return 0, fmt.Errorf("net bench read error: %w", rErr)
			}
		}

		dur := time.Since(start)
		if dur.Milliseconds() < 1 {
			dur = time.Millisecond
		}
		speedMBps := (float64(totalRead) / (1024 * 1024)) / dur.Seconds()
		return speedMBps, nil
	}

	return actualPort, waitFn, nil
}

// RunNetworkBurstClient connects to targetAddr and pushes sizeBytes of memory buffer at maximum speed.
func RunNetworkBurstClient(targetAddr string, sizeBytes int64, timeout time.Duration) (float64, error) {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.Dial("tcp4", targetAddr)
	if err != nil {
		return 0, fmt.Errorf("failed to dial net bench target: %w", err)
	}
	defer conn.Close()

	if tcpC, ok := conn.(*net.TCPConn); ok {
		_ = tcpC.SetNoDelay(true)
		_ = tcpC.SetWriteBuffer(4 * 1024 * 1024)
	}

	buf := make([]byte, 128*1024)
	var written int64
	start := time.Now()

	for written < sizeBytes {
		toWrite := int64(len(buf))
		if sizeBytes-written < toWrite {
			toWrite = sizeBytes - written
		}
		if timeout > 0 {
			_ = conn.SetWriteDeadline(time.Now().Add(timeout))
		}
		n, wErr := conn.Write(buf[:toWrite])
		if wErr != nil {
			return 0, fmt.Errorf("net bench write error: %w", wErr)
		}
		written += int64(n)
	}

	dur := time.Since(start)
	if dur.Milliseconds() < 1 {
		dur = time.Millisecond
	}
	speedMBps := (float64(written) / (1024 * 1024)) / dur.Seconds()
	return speedMBps, nil
}

// ClassifyNetworkLink analyzes latency and throughput to identify the connection medium
func ClassifyNetworkLink(avgRTTMs float64, speedMBps float64) (linkType string, rating string) {
	switch {
	case avgRTTMs <= 1.5 && speedMBps >= 30.0:
		return "USB Tethering / Direct Ethernet", "Excellent (Wire-Speed)"
	case avgRTTMs <= 5.0 && speedMBps >= 20.0:
		return "Wi-Fi 5 GHz (High Quality)", "Very Good (Fast Wi-Fi)"
	case avgRTTMs <= 15.0:
		return "Wi-Fi 2.4 GHz / Standard", "Good"
	default:
		return "High-Latency / Congested Wi-Fi", "Moderate (Jitter / Latency Detected)"
	}
}
