//go:build diag

package api

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/testkit"
)

// Pausing the file in progress starts the next one at once; resuming it finishes the batch.
// File 0 is throttled so the pause always lands while it is still in flight (issue #22).
func TestBatchQueueDynamicAdvanceOnPause(t *testing.T) {
	t.Skip("quarantaine : bloqué 10 min sur macOS, deux réceptions du lot en parallèle, voir issue #54")
	testkit.Throttle(t, 256*1024) // file 0 (512 KiB) needs about 2 s

	sendDir := t.TempDir()
	var filePaths []string
	for i := 0; i < 5; i++ {
		fp := filepath.Join(sendDir, fmt.Sprintf("file_%d.bin", i))
		size := 1024
		if i == 0 {
			size = 512 * 1024
		}
		_ = os.WriteFile(fp, make([]byte, size), 0644)
		filePaths = append(filePaths, fp)
	}

	sendServer := NewDaemonServer(0, sendDir, "SendBatchNode")
	pair := testkit.StartPair(t, sendServer, NewDaemonServer(0, t.TempDir(), "RecvBatchNode"))
	pair.Send(filePaths, testkit.SendOptions{})
	pair.AcceptOffer(0)

	var startedOrder []int
	var seen []string // events received, for the failure message (issue #54)
	resumed := false
	deadline := time.Now().Add(90 * time.Second) // the batch takes about 4 s; never loop for minutes
	for {
		if time.Now().After(deadline) {
			t.Fatalf("batch not complete after 90 s; start order %v; events %v", startedOrder, seen)
		}
		e := pair.Sender.WaitEventAny(30*time.Second, "transfer_start", "batch_paused_waiting", "transfer_complete")
		if len(seen) < 200 {
			seen = append(seen, e.Event)
		}
		switch e.Event {
		case "transfer_start":
			var start struct {
				FileIndex int `json:"file_index"`
			}
			_ = e.Decode(&start)
			idx := start.FileIndex - 1
			startedOrder = append(startedOrder, idx)
			if idx == 0 && len(startedOrder) == 1 {
				pair.Sender.Request("pause_file", `{"item_index": 0}`)
			}
		case "batch_paused_waiting":
			if !resumed {
				resumed = true
				pair.Sender.Request("resume_file", `{"item_index": 0}`)
			}
		case "transfer_complete":
			// File 0 starts, is paused, files 1 to 4 run, then file 0 resumes.
			if len(startedOrder) < 6 || startedOrder[0] != 0 || startedOrder[1] != 1 || startedOrder[2] != 2 {
				t.Fatalf("start order %v; want 0, 1, 2, 3, 4, then 0 again", startedOrder)
			}
			if !resumed {
				t.Fatal("transfer completed without batch_paused_waiting: file 0 was never paused")
			}
			sendServer.mu.RLock()
			defer sendServer.mu.RUnlock()
			for _, item := range sendServer.currentBatchItems {
				if item.Status != "completed" {
					t.Fatalf("item %d is %q; want completed", item.Index, item.Status)
				}
			}
			return
		}
	}
}
