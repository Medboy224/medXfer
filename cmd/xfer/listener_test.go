package main

import (
	"context"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
)

// #44: at 100 % written, the sender must keep serving while the receiver is still connected.
func TestSenderWaitsForReceiverBeforeCompleting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &cliListener{cancel: cancel, isSender: true}
	const size = 64 << 20

	l.OnProgress(engine.TransferStats{BytesTransferred: size, TotalBytes: size, ActiveStreams: 4})
	select {
	case <-ctx.Done():
		t.Fatal("sender stopped at 100 % written while 4 receiver connections were still open")
	case <-time.After(300 * time.Millisecond):
	}

	l.OnProgress(engine.TransferStats{BytesTransferred: size, TotalBytes: size, ActiveStreams: 0})
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("sender did not stop after the receiver closed its connections")
	}
}
