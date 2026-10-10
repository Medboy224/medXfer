//go:build diag

package engine

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/protocol"
)

// STO-08: with chunk 0 stuck, the other workers must not buffer the whole file.
func TestWindowBoundsMemory(t *testing.T) {
	const chunk, total, workers = 64 * 1024, 64, 4
	window := int64(workers * 3)
	content := func(i int) []byte { return chunkContent(i, chunk) }
	gate := make(chan struct{})
	addr := gatedSender(t, chunk, content, gate)

	dst := t.TempDir()
	r := NewReceiver(dst, workers)
	baseline := buffersInUse.Load()
	buffersPeak.Store(baseline)

	done := make(chan error, 1)
	go func() {
		done <- r.PullWithMetadata(context.Background(), addr, nil,
			protocol.FileMetadata{FileName: "big.bin", FileSize: total * chunk, ChunkSize: chunk}, "id")
	}()
	time.Sleep(300 * time.Millisecond) // let the other workers fill the window
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("transfer failed: %v", err)
	}
	if peak := buffersPeak.Load() - baseline; peak > window {
		t.Fatalf("peak buffers held = %d; want <= window %d", peak, window)
	}
	got, err := os.ReadFile(filepath.Join(dst, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < total; i++ {
		if !bytes.Equal(got[i*chunk:(i+1)*chunk], content(i)) {
			t.Fatalf("chunk %d corrupted", i)
		}
	}
}
