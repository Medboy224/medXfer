package engine

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Medboy224/medXfer/pkg/protocol"
)

type ChunkJob struct {
	Index  uint32
	Offset uint64
	Length uint32
}

type ProgressCallback func(bytesSent, totalBytes int64, speedMBps float64)

type Sender struct {
	WorkerCount int
	ChunkSize   uint32
	SocketBuf   int
}

func NewSender(workers int, chunkSize uint32) *Sender {
	if workers <= 0 {
		workers = 4
	}
	if chunkSize == 0 {
		chunkSize = protocol.DefaultChunkSize
	}
	return &Sender{
		WorkerCount: workers,
		ChunkSize:   chunkSize,
		SocketBuf:   DefaultSocketBuffer,
	}
}

func (s *Sender) Transfer(filePath string, targetAddr string, progressCb ProgressCallback) error {
	disk, err := OpenForReading(filePath)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer disk.Close()

	fileSize := disk.Size()
	fileName := filepath.Base(filePath)

	// Calculate chunk counts
	totalChunks := uint32((fileSize + int64(s.ChunkSize) - 1) / int64(s.ChunkSize))
	if totalChunks == 0 {
		totalChunks = 1
	}

	sessionBytes := make([]byte, 8)
	_, _ = rand.Read(sessionBytes)
	sessionID := hex.EncodeToString(sessionBytes)

	meta := &protocol.Metadata{
		SessionID:   sessionID,
		FileName:    fileName,
		FileSize:    fileSize,
		ChunkSize:   s.ChunkSize,
		TotalChunks: totalChunks,
	}

	// 1. Establish Control Connection
	ctrlConn, err := net.Dial("tcp", targetAddr)
	if err != nil {
		return fmt.Errorf("failed to connect to receiver control port: %w", err)
	}
	defer ctrlConn.Close()
	_ = TuneConn(ctrlConn, s.SocketBuf)

	if err := protocol.SendMeta(ctrlConn, meta); err != nil {
		return fmt.Errorf("failed to send metadata: %w", err)
	}

	// 2. Spawn Parallel Worker Sockets
	jobs := make(chan ChunkJob, totalChunks)
	for i := uint32(0); i < totalChunks; i++ {
		offset := uint64(i) * uint64(s.ChunkSize)
		length := s.ChunkSize
		if offset+uint64(length) > uint64(fileSize) {
			length = uint32(uint64(fileSize) - offset)
		}
		jobs <- ChunkJob{Index: i, Offset: offset, Length: length}
	}
	close(jobs)

	var (
		wg          sync.WaitGroup
		bytesSent   int64
		errOnce     sync.Once
		transferErr error
		startTime   = time.Now()
	)

	// 3. Launch Sender Worker Pool
	for w := 0; w < s.WorkerCount; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			wConn, cErr := net.Dial("tcp", targetAddr)
			if cErr != nil {
				errOnce.Do(func() { transferErr = fmt.Errorf("worker %d dial failed: %w", workerID, cErr) })
				return
			}
			defer wConn.Close()
			_ = TuneConn(wConn, s.SocketBuf)

			// Handshake worker with SessionID
			if hErr := protocol.WriteRawFrame(wConn, protocol.TypeHandshake, []byte(sessionID)); hErr != nil {
				errOnce.Do(func() { transferErr = fmt.Errorf("worker %d handshake failed: %w", workerID, hErr) })
				return
			}

			readBuf := make([]byte, s.ChunkSize)

			for job := range jobs {
				sliceBuf := readBuf[:job.Length]
				if _, rErr := disk.ReadChunkAt(sliceBuf, int64(job.Offset)); rErr != nil {
					errOnce.Do(func() { transferErr = fmt.Errorf("disk read failed at chunk %d: %w", job.Index, rErr) })
					return
				}

				if wErr := protocol.WriteChunk(wConn, job.Index, job.Offset, sliceBuf); wErr != nil {
					errOnce.Do(func() { transferErr = fmt.Errorf("socket write failed at chunk %d: %w", job.Index, wErr) })
					return
				}

				curSent := atomic.AddInt64(&bytesSent, int64(job.Length))
				if progressCb != nil {
					elapsed := time.Since(startTime).Seconds()
					speedMBps := 0.0
					if elapsed > 0 {
						speedMBps = (float64(curSent) / (1024 * 1024)) / elapsed
					}
					progressCb(curSent, fileSize, speedMBps)
				}
			}
		}(w)
	}

	wg.Wait()
	if transferErr != nil {
		return transferErr
	}

	// 4. Wait for receiver ACK confirmation
	frameType, _, aErr := protocol.ReadFrameHeader(ctrlConn)
	if aErr != nil || frameType != protocol.TypeAck {
		return fmt.Errorf("final transfer ACK failed: %w", aErr)
	}

	return nil
}
