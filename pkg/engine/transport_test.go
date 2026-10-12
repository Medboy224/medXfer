package engine

import (
	"net"
	"os"
	"testing"
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
