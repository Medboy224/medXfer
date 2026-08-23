package engine

import (
	"bytes"
	"crypto/rand"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentChunkReadWrite(t *testing.T) {
	tempDir := t.TempDir()
	fileName := "concurrent_test.bin"
	chunkCount := 8
	chunkSize := 1024 * 1024 // 1 MB per chunk
	totalSize := int64(chunkCount * chunkSize)

	// Step 1: Preallocate target file
	dm, err := CreateAndPreallocate(tempDir, fileName, totalSize)
	if err != nil {
		t.Fatalf("CreateAndPreallocate failed: %v", err)
	}

	// Prepare mock chunk data
	expectedChunks := make([][]byte, chunkCount)
	for i := 0; i < chunkCount; i++ {
		expectedChunks[i] = make([]byte, chunkSize)
		_, _ = rand.Read(expectedChunks[i])
	}

	// Step 2: Concurrently write chunks into the preallocated file
	var wg sync.WaitGroup
	for i := 0; i < chunkCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			offset := int64(index * chunkSize)
			n, wErr := dm.WriteChunkAt(expectedChunks[index], offset)
			if wErr != nil {
				t.Errorf("WriteChunkAt error at chunk %d: %v", index, wErr)
			}
			if n != chunkSize {
				t.Errorf("WriteChunkAt short write at chunk %d: got %d, want %d", index, n, chunkSize)
			}
		}(i)
	}
	wg.Wait()

	// Step 3: Finalize (flushes buffers, closes handle, renames .xferpart -> final file)
	if fErr := dm.Finalize(); fErr != nil {
		t.Fatalf("dm.Finalize failed: %v", fErr)
	}

	// Step 4: Open file for concurrent reading
	finalPath := filepath.Join(tempDir, fileName)
	readDM, err := OpenForReading(finalPath)
	if err != nil {
		t.Fatalf("OpenForReading failed: %v", err)
	}
	defer readDM.Close()

	if readDM.Size() != totalSize {
		t.Fatalf("Unexpected file size: got %d, want %d", readDM.Size(), totalSize)
	}

	// Step 5: Concurrently read and verify each chunk
	for i := 0; i < chunkCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			buf := make([]byte, chunkSize)
			offset := int64(index * chunkSize)
			n, rErr := readDM.ReadChunkAt(buf, offset)
			if rErr != nil {
				t.Errorf("ReadChunkAt error at chunk %d: %v", index, rErr)
			}
			if n != chunkSize {
				t.Errorf("ReadChunkAt short read at chunk %d: got %d, want %d", index, n, chunkSize)
			}
			if !bytes.Equal(buf, expectedChunks[index]) {
				t.Errorf("Data corruption at chunk %d", index)
			}
		}(i)
	}
	wg.Wait()
}
