package api

import (
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/session"
)

// #313: a transfer's report is written to disk outside the server's lock: ended by the peer's
// "complete", by its failure or by a cancel, a slow disk does not hold every other action.
func TestReportWrittenOutsideTheLock(t *testing.T) {
	for name, end := range map[string]func(s *DaemonServer){
		"complete": func(s *DaemonServer) { s.completeSent("t") },
		"failed":   func(s *DaemonServer) { s.peerFailed(session.Message{TransferID: "t"}) },
		"cancel":   func(s *DaemonServer) { s.cancelTransfer(false, "") },
	} {
		t.Run(name, func(t *testing.T) {
			s := NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Solo")
			writing, release := make(chan struct{}), make(chan struct{})
			old := saveReport
			saveReport = func(dir string, r *diag.Report, keep int) (string, error) {
				close(writing)
				<-release
				return old(dir, r, keep)
			}
			t.Cleanup(func() { saveReport = old })

			s.trackerFor("t", "f.bin", 1, 1) // the transfer's measures
			s.mu.Lock()
			s.activeTransferID = "t"
			s.transferCancel = func() {}
			s.mu.Unlock()
			ended := make(chan struct{})
			go func() { end(s); close(ended) }()
			<-writing
			locked := make(chan struct{})
			go func() { s.mu.Lock(); s.mu.Unlock(); close(locked) }()
			select {
			case <-locked:
			case <-time.After(2 * time.Second):
				close(release)
				<-ended
				t.Fatal("the server's lock was held while the report was written")
			}
			close(release)
			<-ended
			if s.GetLastSummaryReport() == nil {
				t.Fatal("no report")
			}
		})
	}
}
