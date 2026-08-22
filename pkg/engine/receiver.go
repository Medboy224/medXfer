package engine

import (
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Medboy224/medXfer/pkg/protocol"
)

type Receiver struct {
	OutputDir   string
	SocketBuf   int
	WorkerCount int
}

func NewReceiver(outputDir string, workers int) *Receiver {
	if workers <= 0 {
		workers = 4
	}
	return &Receiver{
		OutputDir:   outputDir,
		SocketBuf:   DefaultSocketBuffer,
		WorkerCount: workers,
	}
}

func (r *Receiver) Pull(targetAddr string, progressCb ProgressCallback) error {
	ctrlConn, err := net.Dial("tcp", targetAddr)
	if err != nil {
		return fmt.Errorf("could not connect to sender control port: %w", err)
	}
	defer ctrlConn.Close()
	_ = TuneConn(ctrlConn, r.SocketBuf)

	meta, err := protocol.RecvMeta(ctrlConn)
	if err != nil {
		return fmt.Errorf("failed to read metadata: %w", err)
	}

	disk, err := CreateAndPreallocate(r.OutputDir, meta.FileName, meta.FileSize)
	if err != nil {
		return fmt.Errorf("failed to create staging file: %w", err)
	}

	transferSuccess := false
	defer func() {
		if !transferSuccess {
			disk.Cleanup()
		}
	}()

	var (
		wg             sync.WaitGroup
		completedBytes int64
		chunksReceived uint32
		errOnce        sync.Once
		transferErr    error
		startTime      = time.Now()
	)

	for w := 0; w < r.WorkerCount; w++ {
		wConn, cErr := net.Dial("tcp", targetAddr)
		if cErr != nil {
			return fmt.Errorf("worker %d dial failed: %w", w, cErr)
		}
		_ = TuneConn(wConn, r.SocketBuf)

		if hErr := protocol.WriteRawFrame(wConn, protocol.TypeHandshake, []byte(meta.SessionID)); hErr != nil {
			wConn.Close()
			return fmt.Errorf("worker %d handshake failed: %w", w, hErr)
		}

		wg.Add(1)
		go func(workerID int, conn net.Conn) {
			defer wg.Done()
			defer conn.Close()

			chunkBuf := make([]byte, meta.ChunkSize)

			for {
				if atomic.LoadUint32(&chunksReceived) >= meta.TotalChunks {
					return
				}

				header, rErr := protocol.ReadChunk(conn, chunkBuf)
				if rErr != nil {
					if rErr == io.EOF || atomic.LoadUint32(&chunksReceived) >= meta.TotalChunks {
						return
					}
					errOnce.Do(func() { transferErr = fmt.Errorf("chunk read failed: %w", rErr) })
					return
				}

				sliceData := chunkBuf[:header.DataLen]
				if _, wErr := disk.WriteChunkAt(sliceData, int64(header.Offset)); wErr != nil {
					errOnce.Do(func() { transferErr = fmt.Errorf("disk write failed: %w", wErr) })
					return
				}

				atomic.AddUint32(&chunksReceived, 1)
				curBytes := atomic.AddInt64(&completedBytes, int64(header.DataLen))

				if progressCb != nil {
					elapsed := time.Since(startTime).Seconds()
					speedMBps := 0.0
					if elapsed > 0 {
						speedMBps = (float64(curBytes) / (1024 * 1024)) / elapsed
					}
					progressCb(curBytes, meta.FileSize, speedMBps)
				}
			}
		}(w, wConn)
	}

	wg.Wait()
	if transferErr != nil {
		return transferErr
	}

	if err := disk.Finalize(); err != nil {
		return fmt.Errorf("failed to finalize received file: %w", err)
	}
	transferSuccess = true

	_ = protocol.WriteRawFrame(ctrlConn, protocol.TypeAck, []byte("OK"))
	return nil
}
