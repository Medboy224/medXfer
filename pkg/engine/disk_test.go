package engine

import (
	"bytes"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentChunkReadWrite(t *testing.T) {
	tempDir := t.TempDir()
	fileName := "concurrent_test.bin"
	chunkSize := 1024 * 1024 // 1 MB
	totalChunks := 8
	fileSize := int64(chunkSize * totalChunks)

	// Step 1: Preallocate file
	dm, err := CreateAndPreallocate(tempDir, fileName, fileSize)
	if err != nil {
		t.Fatalf("CreateAndPreallocate failed: %v", err)
	}

	// Step 2: Concurrently write chunks in random order
	var wg sync.WaitGroup
	chunkDataList := make([][]byte, totalChunks)

	for i := 0; i < totalChunks; i++ {
		chunkDataList[i] = bytes.Repeat([]byte{byte(i + 1)}, chunkSize)
	}

	for i := 0; i < totalChunks; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			offset := int64(idx * chunkSize)
			_, wErr := dm.WriteChunkAt(chunkDataList[idx], offset)
			if wErr != nil {
				t.Errorf("WriteChunkAt failed for chunk %d: %v", idx, wErr)
			}
		}(i)
	}
	wg.Wait()
	_ = dm.Close()

	// Step 3: Verify contents by reading back
	readDm, err := OpenForReading(filepath.Join(tempDir, fileName))
	if err != nil {
		t.Fatalf("OpenForReading failed: %v", err)
	}
	defer readDm.Close()

	for i := 0; i < totalChunks; i++ {
		readBuf := make([]byte, chunkSize)
		offset := int64(i * chunkSize)
		_, rErr := readDm.ReadChunkAt(readBuf, offset)
		if rErr != nil {
			t.Fatalf("ReadChunkAt failed at index %d: %v", i, rErr)
		}
		if !bytes.Equal(readBuf, chunkDataList[i]) {
			t.Fatalf("Data corruption detected at chunk %d", i)
		}
	}
}
