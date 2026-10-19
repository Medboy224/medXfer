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
	}
	pair.WaitTransferDone(30 * time.Second)
}
