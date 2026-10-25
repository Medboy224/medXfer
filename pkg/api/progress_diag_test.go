//go:build diag

package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/testkit"
)

// WP-B.0 (#156, reported by the user): while a folder goes through, the file's progress is
// the file's, not the batch's: both bars no longer show the same percentage. One data
// connection and the throttle keep the first file in progress for more than a second, on
// both sides.
func TestProgressDescribesTheFileNotTheBatch(t *testing.T) {
	testkit.Throttle(t, 2<<20)
	const size = 3 << 20
	dir := filepath.Join(t.TempDir(), "folder")
	_ = os.MkdirAll(dir, 0o755)
	for i := range 2 {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.bin", i)), make([]byte, size), 0o644)
	}
	sendServer := NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Sender")
	sendServer.config.ChunkSizeMB = 1
	recvServer := NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Receiver")
	recvServer.config.Workers = 1
	pair := testkit.StartPair(t, sendServer, recvServer)
	pair.Send([]string{dir}, testkit.SendOptions{})
	pair.AcceptOffer(0)

	for _, side := range []struct {
		name string
		node *testkit.Node
	}{{"receiver", pair.Receiver}, {"sender", pair.Sender}} {
		e := side.node.WaitFor("a progress event in the middle of the first file", func(e testkit.Event) bool {
			if e.Event != "transfer_progress" {
				return false
			}
			var d TransferProgressData
			return json.Unmarshal(e.Data, &d) == nil && d.BatchBytes > 0 && d.BatchBytes < size
		}, 20*time.Second)
		var d TransferProgressData
		_ = json.Unmarshal(e.Data, &d)
		if d.FileTotalBytes != size || d.FilePercent <= d.BatchPercent {
			t.Errorf("%s: file %d bytes at %.1f %%, batch %d bytes at %.1f %%: want the file's own size and share",
				side.name, d.FileTotalBytes, d.FilePercent, d.BatchTotalBytes, d.BatchPercent)
		}
		// WP-B.5: the stages measured so far, for the developer panel's live view.
		side.node.WaitFor("a progress event with the stages and the limiting one", func(e testkit.Event) bool {
			var d TransferProgressData
			return e.Event == "transfer_progress" && json.Unmarshal(e.Data, &d) == nil &&
				d.StageMs[d.Bottleneck] > 0 && d.BottleneckConfidence != ""
		}, 20*time.Second)
	}
	pair.WaitTransferDone(30 * time.Second)
}

// WP-B.5: a stopped transfer closes its measures, with a report: the next transfer does not
// start from them (the live stages and the report would mix two transfers).
func TestStoppedTransferClosesItsMeasures(t *testing.T) {
	testkit.Throttle(t, 2<<20)
	src := filepath.Join(t.TempDir(), "big.bin")
	_ = os.WriteFile(src, make([]byte, 8<<20), 0o644)
	sendServer := NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Sender")
	recvServer := NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Receiver")
	pair := testkit.StartPair(t, sendServer, recvServer)
	pair.Send([]string{src}, testkit.SendOptions{})
	pair.AcceptOffer(0)
	pair.Receiver.WaitFor("measured stages", func(e testkit.Event) bool {
		var d TransferProgressData
		return e.Event == "transfer_progress" && json.Unmarshal(e.Data, &d) == nil && len(d.StageMs) > 0
	}, 20*time.Second)
	pair.Receiver.Request("cancel", nil)

	for _, side := range []struct {
		name string
		s    *DaemonServer
	}{{"receiver", recvServer}, {"sender", sendServer}} {
		s := side.s
		deadline := time.Now().Add(10 * time.Second)
		for {
			s.trackerMu.Lock()
			open, last := s.activeTracker != nil, s.lastSummaryReport
			s.trackerMu.Unlock()
			if !open && last != nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: tracker still open %v, report %v after the stop", side.name, open, last != nil)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// Reported by the user: paused, the other device's bar went on, and the two devices never
// showed the same percentage. A pause now drops the chunks in flight (asked again on resume),
// and the sender shows the receiver's progress: both bars stop together, on the same figure,
// and the file arrives whole once resumed.
func TestPauseStopsBothBarsOnTheSameFigure(t *testing.T) {
	testkit.Throttle(t, 2<<20)
	dir := filepath.Join(t.TempDir(), "in")
	_ = os.MkdirAll(dir, 0o755)
	data := make([]byte, 12<<20)
	for i := range data {
		data[i] = byte(i * 7)
	}
	_ = os.WriteFile(filepath.Join(dir, "big.bin"), data, 0o644)
	out := t.TempDir()
	sendServer := NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Sender")
	sendServer.config.ChunkSizeMB = 1
	recvServer := NewDaemonServerAt(t.TempDir(), 0, out, "Receiver")
	pair := testkit.StartPair(t, sendServer, recvServer)
	pair.Send([]string{dir}, testkit.SendOptions{})
	pair.AcceptOffer(0)

	bytesOf := func(e testkit.Event) int64 {
		var d TransferProgressData
		_ = json.Unmarshal(e.Data, &d)
		return d.BatchBytes
	}
	progress := func(e testkit.Event) bool { return e.Event == "transfer_progress" }
	pair.Receiver.WaitFor("a few megabytes received", func(e testkit.Event) bool {
		return progress(e) && bytesOf(e) >= 3<<20
	}, 20*time.Second)
	pair.Receiver.Request("pause", nil)

	// Every progress event of a side within d: the last figure it showed.
	last := func(n *testkit.Node, d time.Duration) int64 {
		var b int64 = -1
		end := time.Now().Add(d)
		for {
			e, ok := n.TryWaitFor(progress, time.Until(end))
			if !ok {
				return b
			}
			b = bytesOf(e)
		}
	}
	time.Sleep(700 * time.Millisecond) // the chunks being written, and the last report, go through
	recvAt, sendAt := last(pair.Receiver, 300*time.Millisecond), last(pair.Sender, 300*time.Millisecond)
	time.Sleep(1500 * time.Millisecond) // at 2 MB/s, 3 MB would have gone by
	recvLater, sendLater := last(pair.Receiver, 300*time.Millisecond), last(pair.Sender, 300*time.Millisecond)
	if recvLater > 0 && recvAt > 0 && recvLater != recvAt {
		t.Errorf("paused, the receiver's bar went on: %d then %d bytes", recvAt, recvLater)
	}
	if sendLater > 0 && sendAt > 0 && sendLater != sendAt {
		t.Errorf("paused, the sender's bar went on: %d then %d bytes", sendAt, sendLater)
	}
	if sendLater > 0 && recvLater > 0 && sendLater != recvLater {
		t.Errorf("paused, the two bars differ: sender %d, receiver %d bytes", sendLater, recvLater)
	}

	pair.Receiver.Request("resume", nil)
	pair.WaitTransferDone(30 * time.Second)
	testkit.AssertTreesEqual(t, dir, filepath.Join(out, "in"))
}
