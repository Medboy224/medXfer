package api

import (
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/session"
)

// maxBenchBurst bounds the burst the other device may ask for: 8 times the 8 MiB of test_network.
const maxBenchBurst = 64 << 20

// AnswerBench answers the other device's network benchmark (test_network): a ping with its
// pong; a burst request with a data port, then the speed it received at. It reports whether
// msg was one of them. The daemon and `xfer node` share it: a peer that does not answer
// leaves the benchmark without a measure (WP-B.5).
//
// The data port is in the clear: it listens on the session's address only, and measures the
// session's peer only, maxBenchBurst bytes at most (#313).
func AnswerBench(sess *session.Channel, msg session.Message) bool {
	switch msg.Type {
	case "bench_ping":
		_ = sess.Send(session.Message{Type: "bench_pong", BenchPingTS: msg.BenchPingTS})
	case "bench_burst_req":
		size := msg.BenchSize
		if size <= 0 {
			size = 8 * 1024 * 1024
		}
		size = min(size, maxBenchBurst)
		port, wait, err := engine.RunNetworkBurstServer(sess.LocalIP(), sess.RemoteIP(), size, 4*time.Second)
		if err != nil {
			return true
		}
		_ = sess.Send(session.Message{Type: "bench_burst_ready", DataPort: port, BenchSize: size})
		go func() {
			speed, _ := wait()
			_ = sess.Send(session.Message{
				Type:      "bench_burst_ack",
				FileSize:  int64(speed * 100), // the speed in hundredths of MB/s
				BenchSize: size,
			})
		}()
	default:
		return false
	}
	return true
}
