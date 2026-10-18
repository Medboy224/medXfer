package transfer

import (
	"bytes"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

// A steady link shows a steady speed: 6.7 MB/s counted every 200 ms for 10 s reads within 3 %
// once the window is full, where the old meter swung between 2 and 13 MB/s.
func TestSpeedIsSteadyOnASteadyLink(t *testing.T) {
	const rate = 6.7 * (1 << 20) // bytes per second
	m := newSpeedMeter()
	t0 := m.samples[0].at
	for i := 1; i <= 50; i++ {
		at := t0.Add(time.Duration(i) * 200 * time.Millisecond)
		s := m.updateAt(at, int64(rate*float64(i)*0.2))
		if at.Sub(t0) >= speedWindow && (s < 6.5 || s > 6.9) {
			t.Fatalf("at %v: %.2f MB/s, want 6.7 ± 3 %%", at.Sub(t0), s)
		}
	}
	if n := len(m.samples); n > 12 {
		t.Fatalf("%d samples kept for a 2 s window at 200 ms", n)
	}
	// The link stops: the speed falls to 0 within the window.
	last := m.samples[len(m.samples)-1]
	if s := m.updateAt(last.at.Add(speedWindow+200*time.Millisecond), last.bytes); s != 0 {
		t.Fatalf("stalled link shows %.2f MB/s", s)
	}
}

// The counters see the bytes as they cross the connection, a slice at a time.
func TestCountingReaderAndWriter(t *testing.T) {
	var n atomic.Int64
	var steps []int64
	w := writerFunc(func(p []byte) (int, error) {
		if len(p) > writeSlice {
			t.Fatalf("write of %d bytes", len(p))
		}
		steps = append(steps, n.Load())
		return len(p), nil
	})
	chunk := make([]byte, 2<<20+13)
	if k, err := writeCounted(w, chunk, &n); err != nil || k != len(chunk) || n.Load() != int64(len(chunk)) {
		t.Fatalf("wrote %d (%v), counted %d", k, err, n.Load())
	}
	if len(steps) != 17 {
		t.Fatalf("%d writes for 2 MiB + 13 bytes, want 17", len(steps))
	}
	var r atomic.Int64
	got, _ := io.ReadAll(countingReader{bytes.NewReader(chunk), &r})
	if len(got) != len(chunk) || r.Load() != int64(len(chunk)) {
		t.Fatalf("read %d, counted %d", len(got), r.Load())
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// A sender fills its socket buffers at once: its first 2 s show no speed, and the window starts
// after them, so the 16 MB written in the first 200 ms never show as 80 MB/s.
func TestSenderSpeedSkipsTheBufferFill(t *testing.T) {
	m := newSenderSpeedMeter()
	t0 := m.start
	const rate = 6.5 * (1 << 20)
	burst := int64(16 << 20)
	for i := 1; i <= 50; i++ {
		el := time.Duration(i) * 200 * time.Millisecond
		sent := burst + int64(rate*max(el.Seconds()-1.6, 0)) // the buffers drain from 1.6 s on
		s := m.updateAt(t0.Add(el), sent)
		if el < senderWarmup && s != 0 {
			t.Fatalf("at %v: %.1f MB/s during the warm-up", el, s)
		}
		if el >= senderWarmup+senderSpeedWindow && (s < 6.3 || s > 6.7) {
			t.Fatalf("at %v: %.2f MB/s, want 6.5", el, s)
		}
		if s > 10 {
			t.Fatalf("at %v: %.1f MB/s: the buffer fill shows", el, s)
		}
	}
}
