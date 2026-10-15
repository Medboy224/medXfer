package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/testkit"
)

// #129: the sender gets transfer_complete for every transfer, once, even when its Serve ends
// before the receiver's "complete" message arrives (which is the usual order on the loopback).
func TestSenderGetsTransferComplete(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(f, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	pair := testkit.StartPair(t, NewDaemonServer(0, dir, "S"), NewDaemonServer(0, t.TempDir(), "R"))
	const n = 10
	for i := 1; i <= n; i++ {
		pair.Send([]string{f}, testkit.SendOptions{})
		pair.AcceptOffer(0)
		pair.WaitTransferDone(0)
		pair.Sender.WaitEvent("transfer_complete", 5*time.Second)
	}
	pair.Sender.ExpectNoEvent("transfer_complete", 300*time.Millisecond) // no duplicate
	got := 0
	for _, e := range pair.Sender.Client.Events() {
		if e.Event == "transfer_complete" {
			got++
		}
	}
	if got != n {
		t.Fatalf("the sender got %d transfer_complete for %d transfers", got, n)
	}
}
