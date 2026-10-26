package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/testkit"
)

// stopsWithin fails the test when srv.Stop takes longer than d: the app waits for it before
// its window closes, and a stop that hangs keeps the process alive without a window.
func stopsWithin(t *testing.T, name string, srv *DaemonServer, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		srv.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s: Stop still running after %v", name, d)
	}
}

// After a transfer, the session stays open between the two devices: each one still stops at
// once, the side that sent as the side that received.
func TestStopWithAnOpenSession(t *testing.T) {
	for _, side := range []string{"sender", "receiver"} {
		t.Run(side, func(t *testing.T) {
			sendDir := t.TempDir()
			file := filepath.Join(sendDir, "a.bin")
			if err := os.WriteFile(file, make([]byte, 3<<20), 0o644); err != nil {
				t.Fatal(err)
			}
			sender, receiver := NewDaemonServer(0, sendDir, "SenderNode"), NewDaemonServer(0, t.TempDir(), "ReceiverNode")
			pair := testkit.StartPair(t, sender, receiver)
			pair.Send([]string{file}, testkit.SendOptions{})
			pair.AcceptOffer(0)
			pair.WaitTransferDone(0)
			if side == "sender" {
				stopsWithin(t, side, sender, 3*time.Second)
			} else {
				stopsWithin(t, side, receiver, 3*time.Second)
			}
		})
	}
}
