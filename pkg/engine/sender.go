package engine

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
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

type ProgressCallback func(bytesProcessed, totalBytes int64, speedMBps float64)

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

func (s *Sender) ServeAndSend(bindAddr string, filePath string, progressCb ProgressCallback) error {
	disk, err := OpenForReading(filePath)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer disk.Close()

	fileSize := disk.Size()
	fileName := filepath.Base(filePath)

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

	listener, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return fmt.Errorf("failed to bind sender listener: %w", err)
	}
	defer listener.Close()

	ctrlConn, err := listener.Accept()
	if err != nil {
		return fmt.Errorf("failed to accept receiver control connection: %w", err)
	}
	defer ctrlConn.Close()
	_ = TuneConn(ctrlConn, s.SocketBuf)

	if err := protocol.SendMeta(ctrlConn, meta); err != nil {
		return fmt.Errorf("failed to send metadata: %w", err)
	}

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
		joined      = 0
	)

	for joined < s.WorkerCount {
		wConn, aErr := listener.Accept()
		if aErr != nil {
			return fmt.Errorf("failed to accept worker connection: %w", aErr)
		}
		_ = TuneConn(wConn, s.SocketBuf)

		fType, fLen, hErr := protocol.ReadFrameHeader(wConn)
		if hErr != nil || fType != protocol.TypeHandshake {
			wConn.Close()
			continue
		}

		sessionPayload := make([]byte, fLen)
		if _, rErr := io.ReadFull(wConn, sessionPayload); rErr != nil || string(sessionPayload) != sessionID {
			wConn.Close()
			continue
		}

		joined++
		wg.Add(1)

		go func(workerID int, conn net.Conn) {
			defer wg.Done()
			defer conn.Close()

			readBuf := make([]byte, s.ChunkSize)

			for job := range jobs {
				sliceBuf := readBuf[:job.Length]
				if _, rErr := disk.ReadChunkAt(sliceBuf, int64(job.Offset)); rErr != nil {
					errOnce.Do(func() { transferErr = fmt.Errorf("disk read error: %w", rErr) })
					return
				}

				if wErr := protocol.WriteChunk(conn, job.Index, job.Offset, sliceBuf); wErr != nil {
					errOnce.Do(func() { transferErr = fmt.Errorf("socket write failed: %w", wErr) })
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
		}(joined, wConn)
	}

	wg.Wait()
	if transferErr != nil {
		return transferErr
	}

	frameType, _, aErr := protocol.ReadFrameHeader(ctrlConn)
	if aErr != nil || frameType != protocol.TypeAck {
		return fmt.Errorf("final confirmation ACK failed: %w", aErr)
	}

	return nil
}
