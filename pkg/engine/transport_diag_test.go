//go:build diag

package engine

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag/faultconn"
)

// A sender restarted on the same port with a different file: the live receiver worker must
// abort instead of writing the new file's bytes into the old one. The transfer is throttled
// so the first file can never finish before the switch (issue #23).
func TestLiveWorkerRejectsDifferentFileOnSenderRestart(t *testing.T) {
	const size, chunk = 4 << 20, 512 * 1024 // 4 MiB at 1 MiB/s: about 4 s, cut after the first chunk
	lim := faultconn.NewLimiter(1 << 20)
	defer SetConnWrapper(func(_ ConnRole, c net.Conn) net.Conn {
		return faultconn.Wrap(c, faultconn.Plan{Limiter: lim})
	})()

	srcDir, dstDir := t.TempDir(), t.TempDir()
	file1Path := filepath.Join(srcDir, "file1.dat")
	_ = os.WriteFile(file1Path, bytes.Repeat([]byte{0x11}, size), 0644)
	file1ID := GenerateFileID(file1Path)
	file2Path := filepath.Join(srcDir, "file2.dat")
	_ = os.WriteFile(file2Path, bytes.Repeat([]byte{0x22}, size), 0644)

	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	bindAddr := fmt.Sprintf("127.0.0.1:%d", l.Addr().(*net.TCPAddr).Port)
	l.Close()

	// 1. Sender 1 serves file 1.
	senderCtx1, senderCancel1 := context.WithCancel(context.Background())
	defer senderCancel1()
	ready := make(chan error, 1)
	sender1Done := make(chan struct{})
	go func() {
		defer close(sender1Done)
		_ = NewSender(1, chunk).ServeAndSendWithRelPathReady(senderCtx1, bindAddr, file1Path, "file1.dat", nil, 0, ready)
	}()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}

	// 2. The receiver pulls file 1 with one worker, committing every chunk.
	receiver := NewReceiver(dstDir, 1)
	receiver.SetCommitPolicy(chunk, 2*time.Second)
	recvCtx, recvCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer recvCancel()
	recvErr := make(chan error, 1)
	go func() { recvErr <- receiver.Pull(recvCtx, bindAddr, nil, file1ID) }()

	// 3. Once a chunk is durable, sender 1 stops and sender 2 takes the port with file 2.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if peek, _ := PeekResumeOffset(dstDir, "file1.dat", file1ID, size, chunk); peek >= chunk {
			break
		}
		select {
		case err := <-recvErr:
			t.Fatalf("receiver ended before the sender switch: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("no durable progress on file 1")
		}
		time.Sleep(5 * time.Millisecond)
	}
	senderCancel1()
	<-sender1Done

	senderCtx2, senderCancel2 := context.WithTimeout(context.Background(), 20*time.Second)
	defer senderCancel2()
	go func() { _ = NewSender(1, chunk).ServeAndSend(senderCtx2, bindAddr, file2Path, nil, 0) }()

	// 4. The receiver must fail rather than mix file 2 into file 1.
	select {
	case err := <-recvErr:
		if err == nil {
			t.Fatal("receiver completed although the sender switched to a different file")
		}
		t.Logf("receiver aborted on the file switch: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("receiver did not abort after the file switch")
	}
	if data, err := os.ReadFile(filepath.Join(dstDir, "file1.dat")); err == nil && bytes.IndexByte(data, 0x22) >= 0 {
		t.Fatal("corruption: file 1 on the receiver contains bytes of file 2")
	}
}
