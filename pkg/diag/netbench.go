package diag

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	mrand "math/rand/v2"
	"net"
	"sort"
	"time"
)

// Network benchmark (DEV-07, base version): latency by ping-pong, then throughput in both
// directions, each measured by the side that receives the data. It runs over raw TCP: the
// encrypted measurement (step R2) comes with the secure channel of J3.
//
// Protocol: the client sends magic, code (8 bytes) and size (uint64); the server answers one
// status byte. Then RTT pings of 8 bytes echoed back, the upload (client to server) followed by
// the server's measured duration (int64 ns), and the download (server to client).

const (
	netBenchMagic = "MXNB1"
	netBenchPings = 20
	netBenchIO    = 30 * time.Second // deadline for each step

	// NetBenchMinSize and NetBenchMaxSize bound the bytes sent in each direction.
	NetBenchMinSize = 1 << 20
	NetBenchMaxSize = 1 << 30
)

var errBadCode = errors.New("netbench: wrong code")

// NetBenchResult is the outcome of a network benchmark, as printed by `xfer bench net --json`.
type NetBenchResult struct {
	SchemaVersion int     `json:"schema_version"`
	SizeBytes     int64   `json:"size_bytes"`
	RTTMinMs      float64 `json:"rtt_min_ms"`
	RTTAvgMs      float64 `json:"rtt_avg_ms"`
	RTTP95Ms      float64 `json:"rtt_p95_ms"`
	UploadMBps    float64 `json:"upload_mbps"`   // client to server, measured by the server
	DownloadMBps  float64 `json:"download_mbps"` // server to client, measured by the client
	LinkType      string  `json:"link_type"`
	Rating        string  `json:"rating"`
	Encrypted     bool    `json:"encrypted"`
}

// NewNetBenchCode returns a random single-session code, shown by the listening side.
func NewNetBenchCode() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ServeNetBench answers benchmark sessions on ln, one at a time, until ctx ends or, with
// once, after the first session. Each session must present code. Errors of a session are
// passed to onError (if not nil) and do not stop the server.
func ServeNetBench(ctx context.Context, ln net.Listener, code string, once bool, onError func(error)) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		err = serveNetBenchSession(conn, code)
		_ = conn.Close()
		if err != nil && onError != nil {
			onError(err)
		}
		if once && !errors.Is(err, errBadCode) {
			return err
		}
	}
}

func serveNetBenchSession(conn net.Conn, code string) error {
	_ = conn.SetDeadline(time.Now().Add(netBenchIO))
	hdr := make([]byte, len(netBenchMagic)+8+8)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return err
	}
	gotCode := hdr[len(netBenchMagic) : len(netBenchMagic)+8]
	size := int64(binary.BigEndian.Uint64(hdr[len(netBenchMagic)+8:]))
	if string(hdr[:len(netBenchMagic)]) != netBenchMagic || subtle.ConstantTimeCompare(gotCode, []byte(padCode(code))) != 1 {
		_, _ = conn.Write([]byte{1})
		return errBadCode
	}
	if size < NetBenchMinSize || size > NetBenchMaxSize {
		_, _ = conn.Write([]byte{2})
		return fmt.Errorf("netbench: size %d out of range", size)
	}
	if _, err := conn.Write([]byte{0}); err != nil {
		return err
	}

	// RTT: echo the pings.
	ping := make([]byte, 8)
	for i := 0; i < netBenchPings; i++ {
		if _, err := io.ReadFull(conn, ping); err != nil {
			return err
		}
		if _, err := conn.Write(ping); err != nil {
			return err
		}
	}

	// Upload: receive and time it, then report the duration.
	_ = conn.SetDeadline(time.Now().Add(netBenchIO))
	d, err := receiveTimed(conn, size)
	if err != nil {
		return err
	}
	if err := binary.Write(conn, binary.BigEndian, int64(d)); err != nil {
		return err
	}

	// Download: send.
	_ = conn.SetDeadline(time.Now().Add(netBenchIO))
	return sendRandom(conn, size)
}

// RunNetBench measures the link to a server started by ServeNetBench.
func RunNetBench(ctx context.Context, addr, code string, size int64) (*NetBenchResult, error) {
	if size < NetBenchMinSize || size > NetBenchMaxSize {
		return nil, fmt.Errorf("size must be between %d and %d bytes", NetBenchMinSize, NetBenchMaxSize)
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(netBenchIO))
	}

	hdr := append([]byte(netBenchMagic), padCode(code)...)
	hdr = binary.BigEndian.AppendUint64(hdr, uint64(size))
	if _, err := conn.Write(hdr); err != nil {
		return nil, err
	}
	status := make([]byte, 1)
	if _, err := io.ReadFull(conn, status); err != nil {
		return nil, fmt.Errorf("no answer from the benchmark server: %w", err)
	}
	switch status[0] {
	case 0:
	case 1:
		return nil, errBadCode
	default:
		return nil, fmt.Errorf("benchmark server refused the session (status %d)", status[0])
	}

	// RTT.
	rtts := make([]float64, 0, netBenchPings)
	ping, pong := make([]byte, 8), make([]byte, 8)
	for i := 0; i < netBenchPings; i++ {
		binary.BigEndian.PutUint64(ping, uint64(i))
		start := time.Now()
		if _, err := conn.Write(ping); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(conn, pong); err != nil {
			return nil, err
		}
		if !bytes.Equal(ping, pong) {
			return nil, errors.New("netbench: corrupted ping")
		}
		rtts = append(rtts, float64(time.Since(start).Nanoseconds())/1e6)
	}

	// Upload, timed by the server.
	if err := sendRandom(conn, size); err != nil {
		return nil, err
	}
	var upNs int64
	if err := binary.Read(conn, binary.BigEndian, &upNs); err != nil {
		return nil, err
	}

	// Download, timed here.
	down, err := receiveTimed(conn, size)
	if err != nil {
		return nil, err
	}

	sort.Float64s(rtts)
	var sum float64
	for _, r := range rtts {
		sum += r
	}
	r := &NetBenchResult{
		SchemaVersion: SchemaVersion,
		SizeBytes:     size,
		RTTMinMs:      round3(rtts[0]),
		RTTAvgMs:      round3(sum / float64(len(rtts))),
		RTTP95Ms:      round3(rtts[(len(rtts)*95+99)/100-1]),
		UploadMBps:    round2(mbps(size, time.Duration(upNs))),
		DownloadMBps:  round2(mbps(size, down)),
	}
	r.LinkType, r.Rating = classifyLink(r.RTTAvgMs, math.Min(r.UploadMBps, r.DownloadMBps))
	return r, nil
}

// classifyLink mirrors engine.ClassifyNetworkLink (diag must not be needed by the engine,
// and the engine copy stays for the dashboard).
func classifyLink(avgRTTMs, speedMBps float64) (string, string) {
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

// sendRandom writes size bytes of random data (DEV-09: no zeros a link could compress).
func sendRandom(w io.Writer, size int64) error {
	buf := randomBlock()
	for size > 0 {
		n := int64(len(buf))
		if size < n {
			n = size
		}
		if _, err := w.Write(buf[:n]); err != nil {
			return err
		}
		size -= n
	}
	return nil
}

// receiveTimed reads exactly size bytes and returns the time taken, from the first byte.
func receiveTimed(r io.Reader, size int64) (time.Duration, error) {
	buf := make([]byte, 256*1024)
	first := make([]byte, 1)
	if _, err := io.ReadFull(r, first); err != nil {
		return 0, err
	}
	start := time.Now()
	if _, err := io.CopyBuffer(io.Discard, io.LimitReader(r, size-1), buf); err != nil {
		return 0, err
	}
	d := time.Since(start)
	if d < time.Millisecond {
		d = time.Millisecond // Windows clock granularity: never divide by zero (see #15)
	}
	return d, nil
}

// randomBlock returns 1 MiB from a fast in-memory generator: crypto/rand would make the
// source, not the link, the bottleneck on slow devices.
func randomBlock() []byte {
	var seed [32]byte
	_, _ = rand.Read(seed[:])
	gen := mrand.NewChaCha8(seed)
	buf := make([]byte, 1<<20)
	_, _ = gen.Read(buf)
	return buf
}

func padCode(code string) []byte {
	b := make([]byte, 8)
	copy(b, code)
	return b
}

func mbps(size int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(size) / (1 << 20) / d.Seconds()
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
