//go:build diag

package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/testkit"
)

// tempDir is t.TempDir, emptied with retries first: on Windows, a transfer that has just
// ended may hold its files open for a moment more.
func tempDir(t *testing.T) string {
	d := t.TempDir()
	t.Cleanup(func() {
		for i := 0; i < 50 && os.RemoveAll(d) != nil; i++ {
			time.Sleep(100 * time.Millisecond)
		}
	})
	return d
}

// failedPair starts a throttled transfer of one file and waits until it is under way.
func failedPair(t *testing.T) (*testkit.Pair, *DaemonServer, *DaemonServer) {
	t.Helper()
	testkit.Throttle(t, 2<<20)
	file := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(file, make([]byte, 16<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	sendServer := NewDaemonServerAt(tempDir(t), 0, tempDir(t), "Sender")
	sendServer.config.ChunkSizeMB = 1
	recvServer := NewDaemonServerAt(tempDir(t), 0, tempDir(t), "Receiver")
	pair := testkit.StartPair(t, sendServer, recvServer)
	pair.Send([]string{file}, testkit.SendOptions{})
	pair.AcceptOffer(0)
	pair.Receiver.WaitFor("chunks received", func(e testkit.Event) bool {
		var d TransferProgressData
		return e.Event == "transfer_progress" && json.Unmarshal(e.Data, &d) == nil && d.BatchBytes > 0
	}, 20*time.Second)
	return pair, sendServer, recvServer
}

func failure(t *testing.T, e testkit.Event) map[string]string {
	t.Helper()
	var d map[string]string
	if err := json.Unmarshal(e.Data, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

// WP-B.8, found on the real devices: the sender's data side dies while the session lives on
// (its Wi-Fi gone for more than a minute). The receiver gives up: its app is told the transfer
// failed, not just one file, and the report says so with what was received.
func TestReceiverFailureIsTold(t *testing.T) {
	pair, sendServer, recvServer := failedPair(t)
	sendServer.mu.Lock()
	stopServing := sendServer.transferCancel
	sendServer.transferCancel = nil
	sendServer.mu.Unlock()
	stopServing() // the data connections close; the session stays

	d := failure(t, pair.Receiver.WaitEvent("transfer_failed", 30*time.Second))
	if d["by"] != "self" || d["error"] == "" {
		t.Fatalf("receiver: transfer_failed %v, want by self with the reason", d)
	}
	rep := recvServer.GetLastSummaryReport()
	if rep == nil || rep.Outcome != diag.OutcomeFailed || rep.DoneBytes <= 0 || rep.DoneBytes >= rep.TotalBytes {
		t.Fatalf("receiver report: %+v, want failed with the bytes received", rep)
	}
}

// WP-B.8: the side where a transfer fails tells the other one, which ends its side too
// instead of showing it "in progress" for ever.
func TestPeerFailureEndsThisSide(t *testing.T) {
	pair, sendServer, recvServer := failedPair(t)
	recvServer.mu.RLock()
	sess, id := recvServer.activeSession, recvServer.activeTransferID
	recvServer.mu.RUnlock()
	if err := sess.Send(session.Message{Type: session.TypeFailed, TransferID: id, Error: "disk gone"}); err != nil {
		t.Fatal(err)
	}

	d := failure(t, pair.Sender.WaitEvent("transfer_failed", 10*time.Second))
	if d["by"] != "peer" || d["error"] != "disk gone" || d["transfer_id"] != id {
		t.Fatalf("sender: transfer_failed %v, want by peer with its reason", d)
	}
	sendServer.mu.RLock()
	active := sendServer.activeTransferID
	sendServer.mu.RUnlock()
	if active != "" {
		t.Fatalf("the sender still has transfer %s active", active)
	}
	if rep := sendServer.GetLastSummaryReport(); rep == nil || rep.Outcome != diag.OutcomeFailed {
		t.Fatalf("sender report: %+v, want failed", rep)
	}
	// Another transfer's failure, or one that came late, changes nothing.
	_ = sess.Send(session.Message{Type: session.TypeFailed, TransferID: "late", Error: "x"})
	pair.Sender.ExpectNoEvent("transfer_failed", 500*time.Millisecond)
	// The receiver, whose failure was only simulated, ends its side as it would have.
	pair.Receiver.Request("cancel", nil)
	pair.Receiver.WaitEvent("transfer_canceled", 10*time.Second)
}
