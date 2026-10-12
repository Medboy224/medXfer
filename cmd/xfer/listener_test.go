package main

import (
	"context"
	"testing"

	"github.com/Medboy224/medXfer/pkg/transfer"
)

// #44, #33: the sender stops when the receiver says it is done (DONE), not before: every
// chunk served is not enough (the receiver may still write, or come back after a lost
// connection), and after a resume the sender serves less than the total and must stop anyway.
func TestSenderWaitsForReceiverBeforeCompleting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := newCLIEvents(true, cancel)
	e.bar = nil // no progress bar in test output
	const size = 64 << 20

	e.OnProgress(transfer.Progress{Bytes: size, Total: size, Streams: 4})
	e.OnProgress(transfer.Progress{Bytes: size, Total: size, Streams: 0})
	if ctx.Err() != nil {
		t.Fatal("sender stopped before the receiver said it was done")
	}
	e.OnProgress(transfer.Progress{Bytes: size / 3, Total: size, Finished: true}) // a resume
	if ctx.Err() == nil {
		t.Fatal("sender did not stop on the receiver's DONE")
	}
}
