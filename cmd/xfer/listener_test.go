package main

import (
	"context"
	"testing"

	"github.com/Medboy224/medXfer/pkg/transfer"
)

// #44: once every chunk is served, the sender must keep serving while the receiver is still
// connected, and stop once it has gone.
func TestSenderWaitsForReceiverBeforeCompleting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := newCLIEvents(true, cancel)
	e.bar = nil // no progress bar in test output
	const size = 64 << 20

	e.OnProgress(transfer.Progress{Bytes: size, Total: size, Streams: 4})
	if ctx.Err() != nil {
		t.Fatal("sender stopped with every chunk served while 4 receiver connections were still open")
	}
	e.OnProgress(transfer.Progress{Bytes: size, Total: size, Streams: 0})
	if ctx.Err() == nil {
		t.Fatal("sender did not stop after the receiver closed its connections")
	}
}
