package engine

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTuneConn(t *testing.T) {
	// Create a local TCP listener to generate a net.Conn
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer l.Close()

	go func() {
		conn, _ := l.Accept()
		if conn != nil {
			_ = conn.Close()
		}
	}()

	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()

	// Ensure TuneConn applies settings without panicking
	TuneConn(conn)
}

// mockListener satisfies the TransferListener interface for tests
type mockListener struct {
	started bool
}

func (m *mockListener) OnStart(fileName string, fileSize int64, chunkCount uint32) { m.started = true }
func (m *mockListener) OnProgress(stats TransferStats)                             {}
func (m *mockListener) OnChunkFailed(chunkIndex uint32, retryCount int, err error) {}
func (m *mockListener) OnComplete(savePath string, duration time.Duration)         {}
func (m *mockListener) OnError(err error)                                          {}

type customListener struct {
	onProgress func(stats TransferStats)
}

func (c *customListener) OnStart(fileName string, fileSize int64, chunkCount uint32) {}
func (c *customListener) OnProgress(stats TransferStats) {
	if c.onProgress != nil {
		c.onProgress(stats)
	}
}
func (c *customListener) OnChunkFailed(chunkIndex uint32, retryCount int, err error) {}
func (c *customListener) OnComplete(savePath string, duration time.Duration)         {}
func (c *customListener) OnError(err error)                                          {}

func TestEngineInitialization(t *testing.T) {
	sender := NewSender(4, 2*1024*1024)
	if sender == nil {
		t.Fatal("Expected Sender to initialize")
	}

	receiver := NewReceiver(".", 8)
	if receiver == nil {
		t.Fatal("Expected Receiver to initialize")
	}

	_ = &mockListener{} // Verify interface implementation
}

func TestDiskManagerResumeAndCancellation(t *testing.T) {
	tempDir := t.TempDir()
	fileName := "test_resume_file.bin"
	chunkSize := uint32(1024)
	fileSize := int64(3072) // 3 chunks of 1KB each
	fileID := "1234567890abcdef1234567890abcdef"

	// 1. Initial creation and write 1st chunk
	dm, err := CreateAndPreallocate(tempDir, fileName, fileSize, chunkSize, fileID)
	if err != nil {
		t.Fatalf("CreateAndPreallocate failed: %v", err)
	}

	chunk0 := make([]byte, 1024)
	for i := range chunk0 {
		chunk0[i] = 'A'
	}
	if _, err := dm.WriteChunkAt(chunk0, 0, 0); err != nil {
		t.Fatalf("WriteChunkAt chunk 0 failed: %v", err)
	}

	// 2. Simulate Cancellation / early exit (Cleanup without Finalize)
	dm.Cleanup()

	// 3. Verify PeekResumeOffset returns 1024 bytes
	peekBytes, err := PeekResumeOffset(tempDir, fileName, fileID, fileSize, chunkSize)
	if err != nil {
		t.Fatalf("PeekResumeOffset returned error: %v", err)
	}
	if peekBytes != 1024 {
		t.Fatalf("Expected peekBytes = 1024, got %d", peekBytes)
	}

	// 4. Reopen (Resuming the download)
	dm2, err := CreateAndPreallocate(tempDir, fileName, fileSize, chunkSize, fileID)
	if err != nil {
		t.Fatalf("Reopening with CreateAndPreallocate failed: %v", err)
	}
	if !dm2.IsChunkCompleted(0) {
		t.Fatal("Expected chunk 0 to be completed on resume")
	}
	if dm2.IsChunkCompleted(1) {
		t.Fatal("Expected chunk 1 to be uncompleted on resume")
	}
	if dm2.GetDownloadedBytes() != 1024 {
		t.Fatalf("Expected GetDownloadedBytes() = 1024, got %d", dm2.GetDownloadedBytes())
	}

	// Write chunk 1 and chunk 2
	chunk1 := make([]byte, 1024)
	chunk2 := make([]byte, 1024)
	if _, err := dm2.WriteChunkAt(chunk1, 1024, 1); err != nil {
		t.Fatalf("WriteChunkAt chunk 1 failed: %v", err)
	}
	if _, err := dm2.WriteChunkAt(chunk2, 2048, 2); err != nil {
		t.Fatalf("WriteChunkAt chunk 2 failed: %v", err)
	}

	// 5. Finalize upon completion
	if err := dm2.Finalize(); err != nil {
		t.Fatalf("Finalize failed: %v", err)
	}

	// Verify state file was removed
	statePath := tempDir + "/" + fileName + ".medxfer"
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("Expected .medxfer file to be removed after Finalize")
	}
}

type recordingListener struct {
	mu           sync.Mutex
	started      bool
	progresses   []TransferStats
	completed    bool
	completePath string
}

func (r *recordingListener) OnStart(fileName string, fileSize int64, chunkCount uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = true
}

func (r *recordingListener) OnProgress(stats TransferStats) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.progresses = append(r.progresses, stats)
}

// progressCount reads the number of progress events under the lock: the receiver
// appends to progresses from its own goroutine (a bare len() is a data race).
func (r *recordingListener) progressCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.progresses)
}

func (r *recordingListener) OnChunkFailed(chunkIndex uint32, retryCount int, err error) {}

func (r *recordingListener) OnComplete(savePath string, duration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completed = true
	r.completePath = savePath
}

func (r *recordingListener) OnError(err error) {}

func TestEndToEndTransferResume(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	fileName := "large_test.dat"
	srcPath := filepath.Join(srcDir, fileName)
	fileSize := int64(64 * 1024 * 1024) // 64MB
	chunkSize := uint32(512 * 1024)     // 512KB (128 chunks)

	// Create test file
	data := make([]byte, fileSize)
	for i := range data {
		data[i] = byte(i % 251)
	}
	if err := os.WriteFile(srcPath, data, 0644); err != nil {
		t.Fatalf("Failed to create src file: %v", err)
	}
	fileID := GenerateFileID(srcPath)

	// 1. First transfer - cancel after 2 chunks
	sender1 := NewSender(1, chunkSize)
	receiver1 := NewReceiver(dstDir, 1)
	// Commit every chunk so partial progress reaches the state file before the
	// 64 MiB file completes (default group commit is 64 MiB / 2 s).
	receiver1.SetCommitPolicy(int64(chunkSize), 2*time.Second)

	ctx1, cancel1 := context.WithCancel(context.Background())
	senderListener1 := &recordingListener{}
	recvListener1 := &recordingListener{}

	l1, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port1 := l1.Addr().(*net.TCPAddr).Port
	l1.Close()

	bindAddr1 := fmt.Sprintf("127.0.0.1:%d", port1)
	// Every goroutine is awaited before the test ends: on Windows a file still open by a
	// stopping sender or receiver makes the TempDir cleanup fail (issue #41).
	send1Done, recv1Done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(send1Done)
		_ = sender1.ServeAndSend(ctx1, bindAddr1, srcPath, senderListener1, 0)
	}()

	time.Sleep(50 * time.Millisecond)

	go func() {
		defer close(recv1Done)
		_ = receiver1.Pull(ctx1, bindAddr1, recvListener1, fileID)
	}()

	// Cancel as soon as some progress is durable: with group commit, waiting for a
	// larger amount races with the end of the transfer on a slow-fsync disk (CI)
	for i := 0; i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		peek, err := PeekResumeOffset(dstDir, fileName, fileID, fileSize, chunkSize)
		t.Logf("poll %d: peek=%d err=%v recvProgress=%d", i, peek, err, recvListener1.progressCount())
		if peek > 0 {
			break
		}
	}
	cancel1()
	stopStart := time.Now()
	for name, done := range map[string]chan struct{}{"receiver 1": recv1Done, "sender 1": send1Done} {
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatalf("%s still running 15 s after its context was cancelled", name)
		}
	}
	t.Logf("first transfer stopped %v after cancel", time.Since(stopStart))

	statePathDebug := filepath.Join(dstDir, fileName+".medxfer")
	rawB, readErr := os.ReadFile(statePathDebug)
	t.Logf("statePath=%s readErr=%v rawBLen=%d", statePathDebug, readErr, len(rawB))
	if len(rawB) >= 32 {
		t.Logf("rawB[:32]=%q fileID=%q match=%v", string(rawB[:32]), fileID, string(rawB[:32]) == fileID)
		t.Logf("rawB[32:]=%v", rawB[32:])
	}

	// Check peeked resume offset on receiver
	peekOffset, err := PeekResumeOffset(dstDir, fileName, fileID, fileSize, chunkSize)
	if err != nil {
		t.Fatalf("PeekResumeOffset failed: %v", err)
	}
	t.Logf("Peeked resume offset after cancel: %d bytes (out of %d)", peekOffset, fileSize)
	if peekOffset == 0 {
		t.Fatalf("Expected peekOffset > 0, got 0")
	}

	// 2. Second transfer - resume
	l2, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port2 := l2.Addr().(*net.TCPAddr).Port
	l2.Close()

	bindAddr2 := fmt.Sprintf("127.0.0.1:%d", port2)
	sender2 := NewSender(2, chunkSize)
	receiver2 := NewReceiver(dstDir, 2)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()

	senderListener2 := &recordingListener{}
	recvListener2 := &recordingListener{}

	// Notice: pass peekOffset as in node mode!
	send2Done := make(chan struct{})
	go func() {
		defer close(send2Done)
		_ = sender2.ServeAndSend(ctx2, bindAddr2, srcPath, senderListener2, peekOffset)
	}()
	defer func() { // runs before TempDir cleanup: the sender must have released srcPath
		cancel2()
		<-send2Done
	}()

	time.Sleep(50 * time.Millisecond)

	err = receiver2.Pull(ctx2, bindAddr2, recvListener2, fileID)
	if err != nil {
		t.Fatalf("Second transfer failed: %v", err)
	}

	// Verify sender's recorded progress starts at or above peekOffset
	senderListener2.mu.Lock()
	if len(senderListener2.progresses) > 0 {
		firstSenderProgress := senderListener2.progresses[0]
		t.Logf("First sender progress: %d bytes (%.1f%%)", firstSenderProgress.BytesTransferred, firstSenderProgress.ProgressPercent)
		if firstSenderProgress.BytesTransferred < peekOffset {
			t.Errorf("Expected first sender progress >= %d, got %d", peekOffset, firstSenderProgress.BytesTransferred)
		}
	}
	senderListener2.mu.Unlock()

	// Verify file content matches
	dstPath := filepath.Join(dstDir, fileName)
	dstData, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("Failed to read dst file: %v", err)
	}
	if !bytes.Equal(data, dstData) {
		t.Fatal("Data content mismatch between source and destination")
	}
}

func TestReceiverReconnectsToRestartedSender(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	fileName := "reconnect_test.dat"
	srcPath := filepath.Join(srcDir, fileName)
	fileSize := int64(32 * 1024 * 1024) // 32MB
	chunkSize := uint32(512 * 1024)     // 512KB (64 chunks)

	data := make([]byte, fileSize)
	for i := range data {
		data[i] = byte(i % 251)
	}
	if err := os.WriteFile(srcPath, data, 0644); err != nil {
		t.Fatalf("Failed to create src file: %v", err)
	}
	fileID := GenerateFileID(srcPath)

	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	bindAddr := fmt.Sprintf("127.0.0.1:%d", port)

	// Receiver stays alive across sender restart
	receiver := NewReceiver(dstDir, 2)
	// Commit every chunk so partial progress is visible before the sender is stopped.
	receiver.SetCommitPolicy(int64(chunkSize), 2*time.Second)
	recvListener := &recordingListener{}
	recvCtx, recvCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer recvCancel()

	// 1. Sender 1 starts
	sender1 := NewSender(2, chunkSize)
	senderListener1 := &recordingListener{}
	ctx1, cancel1 := context.WithCancel(context.Background())

	go func() {
		_ = sender1.ServeAndSend(ctx1, bindAddr, srcPath, senderListener1, 0)
	}()

	time.Sleep(50 * time.Millisecond)

	// Receiver starts in background
	recvErrChan := make(chan error, 1)
	go func() {
		recvErrChan <- receiver.Pull(recvCtx, bindAddr, recvListener, fileID)
	}()

	// Wait for some durable progress, then STOP SENDER ONLY
	for i := 0; i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		peek, _ := PeekResumeOffset(dstDir, fileName, fileID, fileSize, chunkSize)
		if peek > 0 {
			break
		}
	}
	// Stop sender 1 (simulating sender cancellation)
	cancel1()
	time.Sleep(100 * time.Millisecond)

	peekOffset, _ := PeekResumeOffset(dstDir, fileName, fileID, fileSize, chunkSize)
	t.Logf("Receiver is still alive at %d bytes while sender was stopped", peekOffset)

	// 2. Sender 2 starts on the same port with initial offset 0 (as a fresh server)
	sender2 := NewSender(2, chunkSize)
	senderListener2 := &recordingListener{}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()

	go func() {
		// Even if sender2 starts with 0, receiver's reconnect will sync TypeResume!
		_ = sender2.ServeAndSend(ctx2, bindAddr, srcPath, senderListener2, 0)
	}()

	// Wait for receiver to finish pulling
	select {
	case err := <-recvErrChan:
		if err != nil {
			t.Fatalf("Receiver pull failed on reconnect: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Receiver timed out waiting to complete")
	}

	// Verify file content matches 100%
	dstPath := filepath.Join(dstDir, fileName)
	dstData, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("Failed to read dst file: %v", err)
	}
	if !bytes.Equal(data, dstData) {
		t.Fatal("Data content mismatch between source and destination")
	}
}

func TestFileSwitchIntegrity(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create file 1 (64MB)
	file1Name := "file1.bin"
	file1Path := filepath.Join(srcDir, file1Name)
	file1Data := bytes.Repeat([]byte{0xAA}, 16*1024*1024)
	_ = os.WriteFile(file1Path, file1Data, 0644)
	file1ID := GenerateFileID(file1Path)

	// Create file 2 (32MB of completely different bytes)
	file2Name := "file2.bin"
	file2Path := filepath.Join(srcDir, file2Name)
	file2Data := bytes.Repeat([]byte{0xBB}, 8*1024*1024)
	_ = os.WriteFile(file2Path, file2Data, 0644)
	file2ID := GenerateFileID(file2Path)

	l, _ := net.Listen("tcp4", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	bindAddr := fmt.Sprintf("127.0.0.1:%d", port)

	// 1. Start Sender on file 2
	sender := NewSender(2, 512*1024)
	senderListener := &recordingListener{}
	senderCtx, senderCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer senderCancel()

	go func() {
		_ = sender.ServeAndSend(senderCtx, bindAddr, file2Path, senderListener, 0)
	}()
	time.Sleep(50 * time.Millisecond)

	// 2. A stale receiver expecting file 1 attempts to pull from sender serving file 2
	receiver := NewReceiver(dstDir, 2)
	recvListener := &recordingListener{}
	recvCtx, recvCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer recvCancel()

	// Must fail with integrity error!
	err := receiver.Pull(recvCtx, bindAddr, recvListener, file1ID)
	if err == nil {
		t.Fatal("Expected receiver.Pull to fail with fileID mismatch, but succeeded")
	}
	t.Logf("Correctly rejected mismatched file with error: %v", err)

	// 3. Now receiver pulls with correct file 2 ID
	recvListener2 := &recordingListener{}
	recvCtx2, recvCancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer recvCancel2()

	err = receiver.Pull(recvCtx2, bindAddr, recvListener2, file2ID)
	if err != nil {
		t.Fatalf("Pulling file 2 failed: %v", err)
	}

	// Verify file 2 on disk matches source file 2
	dstPath := filepath.Join(dstDir, file2Name)
	dstData, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded file2: %v", err)
	}
	if !bytes.Equal(file2Data, dstData) {
		t.Fatal("Downloaded file 2 does not match source content")
	}
}

func TestSameNameDifferentContentIntegrity(t *testing.T) {
	dstDir := t.TempDir()
	fileName := "duplicate_name.mp4"
	chunkSize := uint32(512 * 1024)
	fileSize1 := int64(4 * 1024 * 1024)
	file1ID := "11111111111111111111111111111111"

	// 1. Simulate partial download of file 1
	dm1, err := CreateAndPreallocate(dstDir, fileName, fileSize1, chunkSize, file1ID)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = dm1.WriteChunkAt(bytes.Repeat([]byte{0x11}, int(chunkSize)), 0, 0)
	dm1.Cleanup()

	// 2. A new file with the same name is offered, but different fileID
	file2ID := "22222222222222222222222222222222"
	fileSize2 := int64(2 * 1024 * 1024)

	// PeekResumeOffset must return 0 bytes (no resume)
	peek, err := PeekResumeOffset(dstDir, fileName, file2ID, fileSize2, chunkSize)
	if err != nil {
		t.Fatalf("PeekResumeOffset returned error: %v", err)
	}
	if peek != 0 {
		t.Fatalf("Expected peek = 0 for mismatched fileID, got %d", peek)
	}

	// CreateAndPreallocate must safely clean up the old partial file and create fresh for file 2
	dm2, err := CreateAndPreallocate(dstDir, fileName, fileSize2, chunkSize, file2ID)
	if err != nil {
		t.Fatalf("CreateAndPreallocate failed to replace outdated partial state: %v", err)
	}
	if dm2.IsChunkCompleted(0) {
		t.Fatal("Expected chunk 0 to NOT be completed for new file")
	}
	if dm2.GetDownloadedBytes() != 0 {
		t.Fatalf("Expected downloaded bytes = 0, got %d", dm2.GetDownloadedBytes())
	}
	dm2.Cleanup()
}

func TestFolderBatchTransfer(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create test folder tree:
	// my_project/
	//   main.go
	//   assets/
	//     logo.png
	//   data/
	//     records.csv
	projectDir := filepath.Join(srcDir, "my_project")
	_ = os.MkdirAll(filepath.Join(projectDir, "assets"), 0755)
	_ = os.MkdirAll(filepath.Join(projectDir, "data"), 0755)

	files := map[string][]byte{
		"my_project/main.go":          []byte("package main\nfunc main() {}\n"),
		"my_project/assets/logo.png":  bytes.Repeat([]byte{0x89, 0x50, 0x4E, 0x47}, 1024), // 4KB
		"my_project/data/records.csv": []byte("id,name\n1,alice\n2,bob\n"),
	}

	for rel, content := range files {
		full := filepath.Join(srcDir, filepath.FromSlash(rel))
		if err := os.WriteFile(full, content, 0644); err != nil {
			t.Fatalf("Failed creating test file: %v", err)
		}
	}

	// Sequential transfer loop as orchestrated by batch session
	for relPath, expectedContent := range files {
		srcFull := filepath.Join(srcDir, filepath.FromSlash(relPath))
		fileID := GenerateFileID(srcFull)

		l, _ := net.Listen("tcp4", "127.0.0.1:0")
		port := l.Addr().(*net.TCPAddr).Port
		l.Close()
		bindAddr := fmt.Sprintf("127.0.0.1:%d", port)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		sender := NewSender(2, 64*1024)
		go func(sFull, rPath string) {
			_ = sender.ServeAndSendWithRelPath(ctx, bindAddr, sFull, rPath, nil, 0)
		}(srcFull, relPath)

		time.Sleep(30 * time.Millisecond)

		receiver := NewReceiver(dstDir, 2)
		err := receiver.Pull(ctx, bindAddr, nil, fileID)
		cancel()

		if err != nil {
			t.Fatalf("Batch file '%s' transfer failed: %v", relPath, err)
		}

		// Verify file landed in correct subdirectory
		dstFull := filepath.Join(dstDir, filepath.FromSlash(relPath))
		dstData, err := os.ReadFile(dstFull)
		if err != nil {
			t.Fatalf("Failed reading transferred file '%s': %v", dstFull, err)
		}
		if !bytes.Equal(expectedContent, dstData) {
			t.Fatalf("Content mismatch in '%s'", relPath)
		}
	}
}

func TestEndToEndSmartSkip(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	fileName := "presentation.pdf"
	content := []byte("Complete Presentation Content 2026")

	// 1. Create file in srcDir
	srcPath := filepath.Join(srcDir, fileName)
	_ = os.WriteFile(srcPath, content, 0644)
	fileID := GenerateFileID(srcPath)

	// 2. Pre-create IDENTICAL file in dstDir
	dstPath := filepath.Join(dstDir, fileName)
	_ = os.WriteFile(dstPath, content, 0644)

	l, _ := net.Listen("tcp4", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	bindAddr := fmt.Sprintf("127.0.0.1:%d", port)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sender := NewSender(2, 64*1024)
	go func() {
		_ = sender.ServeAndSend(ctx, bindAddr, srcPath, nil, 0)
	}()
	time.Sleep(30 * time.Millisecond)

	// Receiver pulls file: should instantly Smart-Skip with 0 errors
	receiver := NewReceiver(dstDir, 2)
	receiver.SetCollisionPolicy(PolicySkip)
	err := receiver.Pull(ctx, bindAddr, nil, fileID)
	if err != nil {
		t.Fatalf("Smart-skip Pull failed: %v", err)
	}

	// Verify original file on destination was untouched and not corrupted
	dstData, err := os.ReadFile(dstPath)
	if err != nil || !bytes.Equal(dstData, content) {
		t.Fatalf("Destination file was corrupted: %v", err)
	}
}

func TestEndToEndCollisionAutoRename(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	fileName := "photo.jpg"
	existingContent := []byte("Existing Photo on Receiver")
	incomingContent := []byte("New Incoming Photo With Different Content")

	// 1. Pre-create photo.jpg in dstDir with old content
	dstOriginalPath := filepath.Join(dstDir, fileName)
	_ = os.WriteFile(dstOriginalPath, existingContent, 0644)

	// 2. Create photo.jpg in srcDir with new content
	srcPath := filepath.Join(srcDir, fileName)
	_ = os.WriteFile(srcPath, incomingContent, 0644)
	fileID := GenerateFileID(srcPath)

	l, _ := net.Listen("tcp4", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	bindAddr := fmt.Sprintf("127.0.0.1:%d", port)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sender := NewSender(2, 64*1024)
	go func() {
		_ = sender.ServeAndSend(ctx, bindAddr, srcPath, nil, 0)
	}()
	time.Sleep(30 * time.Millisecond)

	receiver := NewReceiver(dstDir, 2)
	err := receiver.Pull(ctx, bindAddr, nil, fileID)
	if err != nil {
		t.Fatalf("Auto-rename Pull failed: %v", err)
	}

	// 3. Verify photo.jpg still has the old content
	oldData, _ := os.ReadFile(dstOriginalPath)
	if !bytes.Equal(oldData, existingContent) {
		t.Fatalf("Original photo.jpg was overwritten! Got %q, want %q", oldData, existingContent)
	}

	// 4. Verify photo (1).jpg exists and has the new content
	renamedPath := filepath.Join(dstDir, "photo (1).jpg")
	newData, err := os.ReadFile(renamedPath)
	if err != nil {
		t.Fatalf("Expected 'photo (1).jpg' to exist: %v", err)
	}
	if !bytes.Equal(newData, incomingContent) {
		t.Fatalf("photo (1).jpg content mismatch. Got %q, want %q", newData, incomingContent)
	}
}
