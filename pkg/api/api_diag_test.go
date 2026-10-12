//go:build diag

package api

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/testkit"
)

// Pausing the file in progress lets the receiver's scheduler fetch the others at once;
// resuming it ends the transfer (issues #22, #54). The pause is asked by the sender and
// applied by the receiver (D6). One data connection and 1 MiB chunks keep at most 2 MiB of
// file 0 in flight, and the throttle makes file 0 last about 3 s, so the pause always lands
// while file 0 still has chunks to request.
func TestBatchQueueDynamicAdvanceOnPause(t *testing.T) {
	testkit.Throttle(t, 2<<20)

	sendDir := t.TempDir()
	var filePaths []string
	for i := 0; i < 5; i++ {
		fp := filepath.Join(sendDir, fmt.Sprintf("file_%d.bin", i))
		size := 1024
		if i == 0 {
			size = 6 << 20
		}
		_ = os.WriteFile(fp, make([]byte, size), 0644)
		filePaths = append(filePaths, fp)
	}

	sendServer := NewDaemonServer(0, sendDir, "SendBatchNode")
	sendServer.config.ChunkSizeMB = 1
	recvDir := t.TempDir()
	recvServer := NewDaemonServer(0, recvDir, "RecvBatchNode")
	recvServer.config.Workers = 1
	pair := testkit.StartPair(t, sendServer, recvServer)
	pair.Send(filePaths, testkit.SendOptions{})
	pair.AcceptOffer(0)

	var done []int
	var seen []string // events received, for the failure message
	paused, resumed := false, false
	deadline := time.Now().Add(90 * time.Second) // the transfer takes about 4 s
	for {
		if time.Now().After(deadline) {
			t.Fatalf("transfer not complete after 90 s; items done %v; events %v", done, seen)
		}
		e := pair.Receiver.WaitEventAny(30*time.Second, "transfer_start", "item_completed", "batch_paused_waiting", "transfer_complete")
		if len(seen) < 200 {
			seen = append(seen, e.Event)
		}
		var ev struct {
			FileIndex int `json:"file_index"`
			ItemIndex int `json:"item_index"`
		}
		_ = e.Decode(&ev)
		switch e.Event {
		case "transfer_start":
			if ev.FileIndex == 1 && !paused {
				paused = true
				pair.Sender.Request("pause_file", `{"item_index": 0}`)
			}
		case "item_completed":
			done = append(done, ev.ItemIndex)
		case "batch_paused_waiting":
			if !resumed {
				resumed = true
				pair.Sender.Request("resume_file", `{"item_index": 0}`)
			}
		case "transfer_complete":
			if !resumed {
				t.Fatalf("transfer completed without batch_paused_waiting: file 0 was never paused (done %v)", done)
			}
			if !slices.Equal(done, []int{1, 2, 3, 4, 0}) {
				t.Fatalf("items done in order %v; want 1, 2, 3, 4, then 0", done)
			}
			for _, fp := range filePaths {
				want, _ := os.Stat(fp)
				got, err := os.Stat(filepath.Join(recvDir, filepath.Base(fp)))
				if err != nil || got.Size() != want.Size() {
					t.Fatalf("%s not received whole (%v)", filepath.Base(fp), err)
				}
			}
			return
		}
	}
}
