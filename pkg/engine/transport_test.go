package engine

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestEndToEndMultiStreamTransfer(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	testFileName := "large_test_payload.bin"
	testFileSize := int64(20 * 1024 * 1024) // 20 MB test payload
	srcFilePath := filepath.Join(srcDir, testFileName)

	// Step 1: Generate random source file
	data := make([]byte, 1024*1024) // 1 MB buffer
	srcFile, err := os.Create(srcFilePath)
	if err != nil {
		t.Fatal(err)
	}
	for written := int64(0); written < testFileSize; written += int64(len(data)) {
		_, _ = rand.Read(data)
		_, _ = srcFile.Write(data)
	}
	_ = srcFile.Close()

	// Step 2: Initialize Sender on loopback (Server Role)
	listenAddr := "127.0.0.1:18899"
	workers := 4
	chunkSize := uint32(2 * 1024 * 1024) // 2 MB chunks (10 chunks total)

	sender := NewSender(workers, chunkSize)
	receiver := NewReceiver(dstDir, workers)

	var wg sync.WaitGroup
	wg.Add(1)

	// Start Sender in background
	go func() {
		defer wg.Done()
		if sErr := sender.ServeAndSend(listenAddr, srcFilePath, nil); sErr != nil {
			t.Errorf("Sender failed: %v", sErr)
		}
	}()

	// Give sender time to bind socket
	time.Sleep(100 * time.Millisecond)

	// Step 3: Execute Transfer via Receiver (Client Pull Role)
	err = receiver.Pull(listenAddr, nil)
	if err != nil {
		t.Fatalf("Receiver pull failed: %v", err)
	}

	wg.Wait()

	// Step 4: Verify file integrity bit-by-bit
	dstFilePath := filepath.Join(dstDir, testFileName)
	srcContent, err := os.ReadFile(srcFilePath)
	if err != nil {
		t.Fatal(err)
	}
	dstContent, err := os.ReadFile(dstFilePath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(srcContent, dstContent) {
		t.Fatal("Transferred file content does not match source file!")
	}
}
