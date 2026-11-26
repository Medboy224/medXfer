package api

import (
	"testing"

	"github.com/Medboy224/medXfer/pkg/session"
)

// #313: the peer's pause, resume, skip and cancel name their transfer. A late one, for a
// transfer that ended, leaves the next one alone; one without a name (xfer, older versions)
// still applies.
func TestPeerControlNamesItsTransfer(t *testing.T) {
	s := NewDaemonServer(0, t.TempDir(), "Laptop")
	canceled := false
	s.mu.Lock()
	s.activeTransferID = "t2"
	s.transferCancel = func() { canceled = true }
	s.currentBatchItems = []BatchFileInfo{{Index: 0, Status: "transferring"}}
	s.mu.Unlock()

	item := 0
	for _, m := range []session.Message{
		{Type: "pause", TransferID: "t1"},
		{Type: "pause", TransferID: "t1", Item: &item},
		{Type: "skip", TransferID: "t1", Item: &item},
		{Type: "cancel", TransferID: "t1"},
	} {
		s.handleSessionMessage(m)
	}
	s.mu.RLock()
	paused, id, status := s.isPaused, s.activeTransferID, s.currentBatchItems[0].Status
	s.mu.RUnlock()
	if canceled || paused || id != "t2" || status != "transferring" {
		t.Fatalf("messages for transfer t1 changed t2: canceled %v, paused %v, id %q, item %q", canceled, paused, id, status)
	}

	s.handleSessionMessage(session.Message{Type: "pause", TransferID: "t2"})
	s.mu.RLock()
	paused = s.isPaused
	s.mu.RUnlock()
	if !paused {
		t.Fatal("a pause for the transfer in progress did not apply")
	}
	s.handleSessionMessage(session.Message{Type: "cancel"}) // as xfer sends it
	if !canceled {
		t.Fatal("a cancel without a transfer id no longer applies")
	}
}
