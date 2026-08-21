package engine

import (
	"bytes"
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

func (r *Receiver) ListenAndReceive(bindAddr string, progressCb ProgressCallback) error {
	listener, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return fmt.Errorf("failed to bind receiver to %s: %w", bindAddr, err)
	}
	defer listener.Close()

	// 1. Accept Control Connection
	ctrlConn, err := listener.Accept()
	if err != nil {
		return fmt.Errorf("failed to accept control connection: %w", err)
	}
	defer ctrlConn.Close()
	_ = TuneConn(ctrlConn, r.SocketBuf)

	meta, err := protocol.RecvMeta(ctrlConn)
	if err != nil {
		return fmt.Errorf("failed to receive session metadata: %w", err)
	}

	// 2. Preallocate disk storage
	disk, err := CreateAndPreallocate(r.OutputDir, meta.FileName, meta.FileSize)
	if err != nil {
		return fmt.Errorf("failed to preallocate disk file: %w", err)
	}
	defer disk.Close()

	var (
		wg             sync.WaitGroup
		completedBytes int64
		chunksReceived uint32
		errOnce        sync.Once
		transferErr    error
		startTime      = time.Now()
		workersJoined  = 0
	)

	// 3. Accept Worker Connections matching SessionID
	for workersJoined < r.WorkerCount {
		conn, aErr := listener.Accept()
		if aErr != nil {
			return fmt.Errorf("failed accepting worker stream: %w", aErr)
		}
		_ = TuneConn(conn, r.SocketBuf)

		// Read handshake frame
		fType, fLen, hErr := protocol.ReadFrameHeader(conn)
		if hErr != nil || fType != protocol.TypeHandshake {
			conn.Close()
			continue
		}

		sessionPayload := make([]byte, fLen)
		if _, rErr := io.ReadFull(conn, sessionPayload); rErr != nil || !bytes.Equal(sessionPayload, []byte(meta.SessionID)) {
			conn.Close()
			continue
		}

		workersJoined++
		wg.Add(1)

		go func(workerConn net.Conn) {
			defer wg.Done()
			defer workerConn.Close()

			chunkBuf := make([]byte, meta.ChunkSize)

			for {
				if atomic.LoadUint32(&chunksReceived) >= meta.TotalChunks {
					return
				}

				header, cErr := protocol.ReadChunk(workerConn, chunkBuf)
				if cErr != nil {
					if cErr == io.EOF || atomic.LoadUint32(&chunksReceived) >= meta.TotalChunks {
						return
					}
					errOnce.Do(func() { transferErr = fmt.Errorf("chunk read failed: %w", cErr) })
					return
				}

				// Concurrent thread-safe random access write
				sliceData := chunkBuf[:header.DataLen]
				if _, wErr := disk.WriteChunkAt(sliceData, int64(header.Offset)); wErr != nil {
					errOnce.Do(func() { transferErr = fmt.Errorf("disk write failed at offset %d: %w", header.Offset, wErr) })
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
		}(conn)
	}

	wg.Wait()
	if transferErr != nil {
		return transferErr
	}

	// 4. Send Confirmation ACK to sender
	_ = disk.Sync()
	_ = protocol.WriteRawFrame(ctrlConn, protocol.TypeAck, []byte("OK"))

	return nil
}
