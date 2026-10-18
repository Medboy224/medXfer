package transfer

import (
	"io"
	"sync/atomic"
	"time"
)

// The speed shown is the network's: bytes are counted as they cross a data connection, a few
// KiB at a time, and measured over a sliding window. Counted per verified chunk (2 MiB) over
// 200 ms, a steady 6.7 MB/s link was shown as a saw-tooth between 2 and 13 MB/s.

// speedWindow is how far back the receiver's speed looks: long enough to hold many TLS
// records and to smooth Wi-Fi's bursts, short enough to follow a real change within two
// seconds.
const speedWindow = 2 * time.Second

// senderSpeedWindow is the sender's: its system takes several MB at once into its socket
// buffers and lets them go in bursts, which a 2 s window shows as 4 to 8 MB/s on a steady
// 6.6 MB/s link; over 5 s, 6.1 to 6.7 (measured phone to PC, 18/10/2026).
const senderSpeedWindow = 5 * time.Second

type speedSample struct {
	at    time.Time
	bytes int64
}

// senderWarmup is how long a sender shows no speed: its first writes fill the system's socket
// buffers at once (16 MB in 200 ms, 8 chunks in flight), which says nothing of the network.
// The receiver reads at the network's pace from the start.
const senderWarmup = 2 * time.Second

// speedMeter turns successive byte counts into a speed over the last speedWindow.
type speedMeter struct {
	samples []speedSample // oldest first; the first one is the window's start
	start   time.Time
	warmup  time.Duration // no speed before start+warmup; the window starts there
	window  time.Duration
}

func newSpeedMeter() *speedMeter {
	now := time.Now()
	return &speedMeter{samples: []speedSample{{at: now}}, start: now, window: speedWindow}
}

// newSenderSpeedMeter is a sender's meter: no speed during senderWarmup, then a longer window.
func newSenderSpeedMeter() *speedMeter {
	m := newSpeedMeter()
	m.warmup, m.window = senderWarmup, senderSpeedWindow
	return m
}

// update records bytes, counted now, and returns the speed in MB/s since the window's start.
func (m *speedMeter) update(bytes int64) float64 {
	return m.updateAt(time.Now(), bytes)
}

func (m *speedMeter) updateAt(now time.Time, bytes int64) float64 {
	if now.Sub(m.start) < m.warmup {
		m.samples = append(m.samples[:0], speedSample{now, bytes}) // the window will start here
		return 0
	}
	m.samples = append(m.samples, speedSample{now, bytes})
	// Keep one sample at or before the window's start: the speed always spans the window.
	for len(m.samples) > 2 && now.Sub(m.samples[1].at) >= m.window {
		m.samples = m.samples[1:]
	}
	first := m.samples[0]
	dt := now.Sub(first.at).Seconds()
	if dt <= 0 {
		return 0
	}
	// A count that went back (a file started over, a full disk) is no speed.
	return max(float64(bytes-first.bytes), 0) / (1 << 20) / dt
}

// countingReader counts what is read through it: each read of a TLS connection returns one
// record at most, 16 KiB.
type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c countingReader) Read(p []byte) (int, error) {
	k, err := c.r.Read(p)
	c.n.Add(int64(k))
	return k, err
}

// writeSlice is how much writeCounted hands the connection at once.
const writeSlice = 128 << 10

// writeCounted writes b to w a slice at a time, counting each slice once written: a whole
// 2 MiB chunk counted at once would make the speed jump.
func writeCounted(w io.Writer, b []byte, n *atomic.Int64) (int, error) {
	done := 0
	for done < len(b) {
		k, err := w.Write(b[done:min(done+writeSlice, len(b))])
		done += k
		n.Add(int64(k))
		if err != nil {
			return done, err
		}
	}
	return done, nil
}
