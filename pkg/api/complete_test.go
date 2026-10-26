package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/testkit"
)

// WP-B.8: the sender learns the end from the receiver's DONE or from its "complete",
// whichever comes first: once, and its report says completed.
func TestSentTransferCompletesOnce(t *testing.T) {
	for range 5 {
		sendDir := t.TempDir()
		file := filepath.Join(sendDir, "a.bin")
		if err := os.WriteFile(file, make([]byte, 6<<20), 0o644); err != nil {
			t.Fatal(err)
		}
		sender := NewDaemonServer(0, sendDir, "SenderNode")
		pair := testkit.StartPair(t, sender, NewDaemonServer(0, t.TempDir(), "ReceiverNode"))
		pair.Send([]string{file}, testkit.SendOptions{})
		pair.AcceptOffer(0)
		pair.WaitTransferDone(0)
		pair.Sender.WaitEvent("transfer_complete", 10*time.Second)
		pair.Sender.ExpectNoEvent("transfer_complete", time.Second)
		if rep := sender.GetLastSummaryReport(); rep == nil || rep.Outcome != diag.OutcomeCompleted {
			t.Fatalf("sender report: %+v, want completed", rep)
		}
	}
}
