package engine

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Medboy224/medXfer/pkg/protocol"
)

type Receiver struct {
	outputDir       string
	workers         int
	collisionPolicy CollisionPolicy
}

func NewReceiver(outputDir string, workers int) *Receiver {
	if workers <= 0 {
		workers = 4
	}
	return &Receiver{
		outputDir:       outputDir,
		workers:         workers,
		collisionPolicy: PolicyAutoRename,
	}
}

func (r *Receiver) SetCollisionPolicy(p CollisionPolicy) {
	r.collisionPolicy = p
}

type chunkTask struct {
	index  uint32
	offset uint64
	length uint32
}

func (r *Receiver) Pull(ctx context.Context, senderAddr string, listener TransferListener, fileID string) error {
	var handshakeConn net.Conn
	for attempt := 1; attempt <= 30; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var dialer net.Dialer
		c, err := dialer.DialContext(ctx, "tcp4", senderAddr)
		if err == nil {
			handshakeConn = c
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if handshakeConn == nil {
		err := fmt.Errorf("failed to connect to sender on %s", senderAddr)
		if listener != nil {
			listener.OnError(err)
		}
		return err
	}
	TuneConn(handshakeConn)

	var hsReq [8]byte
	binary.BigEndian.PutUint16(hsReq[0:2], protocol.MagicBytes)
	hsReq[2] = protocol.Version1
	hsReq[3] = protocol.TypeHandshake
	binary.BigEndian.PutUint32(hsReq[4:8], 0)

	_ = handshakeConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := handshakeConn.Write(hsReq[:]); err != nil {
		handshakeConn.Close()
		if listener != nil {
			listener.OnError(err)
		}
		return fmt.Errorf("failed to send handshake request: %w", err)
	}

	_ = handshakeConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	header, err := protocol.ReadHeader(handshakeConn)
	if err != nil {
		handshakeConn.Close()
		if listener != nil {
			listener.OnError(err)
		}
		return fmt.Errorf("failed to read protocol header: %w", err)
	}

	if header.Type != protocol.TypeHandshake {
		handshakeConn.Close()
		err := fmt.Errorf("unexpected frame type: expected handshake, got %d", header.Type)
		if listener != nil {
			listener.OnError(err)
		}
		return err
	}

	meta, err := protocol.ReadHandshakePayload(handshakeConn, header.PayloadLen)
	handshakeConn.Close()
	if err != nil {
		if listener != nil {
			listener.OnError(err)
		}
		return fmt.Errorf("failed to parse metadata: %w", err)
	}

	// CRITICAL FILE INTEGRITY CHECK:
	// If the receiver expects a specific fileID, verify that the sender is serving the exact same file!
	if fileID != "" && meta.FileID != "" && fileID != meta.FileID {
		err := fmt.Errorf("integrity error: sender is serving a different file (expected %s, got %s)", fileID, meta.FileID)
		if listener != nil {
			listener.OnError(err)
		}
		return err
	}
	if fileID == "" && meta.FileID != "" {
		fileID = meta.FileID
	}

	// Normalize path across OS boundaries (Windows <-> Android/Linux)
	normPath := strings.ReplaceAll(meta.FileName, "\\", "/")
	normPath = path.Clean("/" + normPath)
	normPath = strings.TrimPrefix(normPath, "/")
	if normPath == "." || normPath == "" || strings.HasPrefix(normPath, "..") {
		normPath = filepath.Base(meta.FileName)
	}
	safeFileName := normPath

	// Resolve collision: Smart-Skip, Resume, Auto-Rename, or Overwrite
	res, err := ResolveCollision(r.outputDir, safeFileName, fileID, meta.FileSize, meta.ChunkSize, r.collisionPolicy)
	if err != nil {
		if listener != nil {
			listener.OnError(err)
		}
		return fmt.Errorf("collision resolution failed: %w", err)
	}

	// 1. SMART-SKIP: If identical file is already 100% complete on disk, skip network streaming!
	if res.IsDuplicate {
		totalChunks := uint32((meta.FileSize + int64(meta.ChunkSize) - 1) / int64(meta.ChunkSize))
		if totalChunks == 0 {
			totalChunks = 1
		}
		if listener != nil {
			listener.OnStart(res.ResolvedName, meta.FileSize, totalChunks)
			listener.OnProgress(TransferStats{
				BytesTransferred: meta.FileSize,
				TotalBytes:       meta.FileSize,
				SpeedMBps:        0,
				ActiveStreams:    0,
				ProgressPercent:  100.0,
			})
			listener.OnComplete(filepath.Join(r.outputDir, res.ResolvedName), 0)
		}
		return nil
	}

	// 2. Preallocate with resolved name (e.g. "video (1).mp4" if auto-renamed)
	dm, err := CreateAndPreallocate(r.outputDir, res.ResolvedName, meta.FileSize, meta.ChunkSize, fileID)
	if err != nil {
		if listener != nil {
			listener.OnError(err)
		}
		return fmt.Errorf("preallocation failed: %w", err)
	}
	defer dm.Cleanup()

	totalChunks := uint32((meta.FileSize + int64(meta.ChunkSize) - 1) / int64(meta.ChunkSize))
	if listener != nil {
		listener.OnStart(res.ResolvedName, meta.FileSize, totalChunks)
	}

	if meta.FileSize == 0 {
		if listener != nil {
			listener.OnComplete(dm.finalPath, 0)
		}
		return dm.Finalize()
	}

	var completedChunks uint32
	transferredBytes := dm.GetDownloadedBytes()

	taskQueue := make(chan chunkTask, totalChunks)
	for i := uint32(0); i < totalChunks; i++ {
		// Resume Logic: Skip chunks that are already perfectly written to disk
		if dm.IsChunkCompleted(i) {
			completedChunks++
			continue
		}

		offset := uint64(i) * uint64(meta.ChunkSize)
		length := meta.ChunkSize
		if offset+uint64(length) > uint64(meta.FileSize) {
			length = uint32(uint64(meta.FileSize) - offset)
		}
		taskQueue <- chunkTask{index: i, offset: offset, length: length}
	}
	close(taskQueue)

	// If fully resumed from disk, complete instantly
	if completedChunks == totalChunks {
		if listener != nil {
			listener.OnProgress(TransferStats{
				BytesTransferred: meta.FileSize,
				TotalBytes:       meta.FileSize,
				SpeedMBps:        0,
				ActiveStreams:    0,
				ProgressPercent:  100.0,
			})
			listener.OnComplete(dm.finalPath, 0)
		}
		return dm.Finalize()
	}

	var activeStreams int32
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	var connsMu sync.Mutex
	activeConns := make(map[net.Conn]struct{})

	go func() {
		<-workerCtx.Done()
		connsMu.Lock()
		for c := range activeConns {
			_ = c.Close()
		}
		connsMu.Unlock()
	}()

	var wg sync.WaitGroup
	errChan := make(chan error, r.workers)

	startTime := time.Now()
	lastSpeedTime := startTime
	var lastSpeedBytes int64 = transferredBytes
	var currentSpeed float64

	for w := 0; w < r.workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			var conn net.Conn

			disconnect := func() {
				if conn != nil {
					connsMu.Lock()
					delete(activeConns, conn)
					connsMu.Unlock()
					_ = conn.Close()
					conn = nil
				}
			}

			connect := func() bool {
				disconnect()
				for attempt := 1; attempt <= 5; attempt++ {
					select {
					case <-workerCtx.Done():
						return false
					default:
					}
					var dialer net.Dialer
					c, err := dialer.DialContext(workerCtx, "tcp4", senderAddr)
					if err == nil {
						conn = c
						TuneConn(conn)
						connsMu.Lock()
						activeConns[conn] = struct{}{}
						connsMu.Unlock()

						// Send TypeResume containing FileID (32B) + DownloadedBytes (8B) to authenticate the stream
						currentBytes := atomic.LoadInt64(&transferredBytes)
						var resumeFrame [48]byte
						binary.BigEndian.PutUint16(resumeFrame[0:2], protocol.MagicBytes)
						resumeFrame[2] = protocol.Version1
						resumeFrame[3] = protocol.TypeResume
						binary.BigEndian.PutUint32(resumeFrame[4:8], 40) // 32 bytes FileID + 8 bytes offset

						padFileID := fileID
						if len(padFileID) != 32 {
							padFileID = fmt.Sprintf("%-32s", padFileID)
						}
						copy(resumeFrame[8:40], []byte(padFileID))
						binary.BigEndian.PutUint64(resumeFrame[40:48], uint64(currentBytes))

						_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
						if _, err := conn.Write(resumeFrame[:]); err != nil {
							disconnect()
							continue
						}
						return true
					}
					time.Sleep(time.Duration(attempt*100) * time.Millisecond)
				}
				return false
			}

			if !connect() {
				errChan <- fmt.Errorf("worker %d failed to connect", workerID)
				return
			}
			defer disconnect()

			atomic.AddInt32(&activeStreams, 1)
			defer atomic.AddInt32(&activeStreams, -1)

			buf := make([]byte, meta.ChunkSize+4096)

			for {
				select {
				case <-workerCtx.Done():
					return
				case task, ok := <-taskQueue:
					if !ok {
						return
					}

					success := false
					for retry := 0; retry < 4; retry++ {
						if conn == nil {
							if !connect() {
								break
							}
						}

						err := r.fetchChunk(conn, task, buf, dm)
						if err == nil {
							success = true
							atomic.AddInt64(&transferredBytes, int64(task.length))
							atomic.AddUint32(&completedChunks, 1)
							break
						}

						if listener != nil {
							listener.OnChunkFailed(task.index, retry+1, err)
						}

						_ = conn.Close()
						conn = nil
						time.Sleep(150 * time.Millisecond)
					}

					if !success {
						errChan <- fmt.Errorf("chunk %d transfer failed after max retries", task.index)
						cancelWorkers()
						return
					}
				}
			}
		}(w)
	}

	doneChan := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneChan)
	}()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-doneChan:
			if atomic.LoadUint32(&completedChunks) == totalChunks {
				duration := time.Since(startTime)
				if listener != nil {
					listener.OnProgress(TransferStats{
						BytesTransferred: meta.FileSize,
						TotalBytes:       meta.FileSize,
						SpeedMBps:        currentSpeed,
						ActiveStreams:    0,
						ProgressPercent:  100.0,
					})
					listener.OnComplete(dm.finalPath, duration)
				}
				return dm.Finalize()
			}
			return fmt.Errorf("transfer terminated prematurely")

		case err := <-errChan:
			cancelWorkers()
			if listener != nil {
				listener.OnError(err)
			}
			return err

		case <-ctx.Done():
			cancelWorkers()
			return ctx.Err()

		case <-ticker.C:
			if listener != nil {
				current := atomic.LoadInt64(&transferredBytes)
				now := time.Now()
				elapsedSpeed := now.Sub(lastSpeedTime).Seconds()

				if elapsedSpeed >= 1.0 {
					delta := current - lastSpeedBytes
					currentSpeed = (float64(delta) / 1048576.0) / elapsedSpeed
					lastSpeedTime = now
					lastSpeedBytes = current
				} else if lastSpeedBytes == dm.GetDownloadedBytes() && current > dm.GetDownloadedBytes() {
					currentSpeed = (float64(current-dm.GetDownloadedBytes()) / 1048576.0) / now.Sub(startTime).Seconds()
				}

				percent := 0.0
				if meta.FileSize > 0 {
					percent = (float64(current) / float64(meta.FileSize)) * 100.0
				}
				listener.OnProgress(TransferStats{
					BytesTransferred: current,
					TotalBytes:       meta.FileSize,
					SpeedMBps:        currentSpeed,
					ActiveStreams:    int(atomic.LoadInt32(&activeStreams)),
					ProgressPercent:  percent,
				})
			}
		}
	}
}

func (r *Receiver) fetchChunk(conn net.Conn, task chunkTask, buf []byte, dm *DiskManager) error {
	var reqBuf [12]byte
	binary.BigEndian.PutUint16(reqBuf[0:2], protocol.MagicBytes)
	reqBuf[2] = protocol.Version1
	reqBuf[3] = protocol.TypeRequest
	binary.BigEndian.PutUint32(reqBuf[4:8], 4)
	binary.BigEndian.PutUint32(reqBuf[8:12], task.index)

	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(reqBuf[:]); err != nil {
		return err
	}

	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	header, err := protocol.ReadHeader(conn)
	if err != nil {
		return err
	}

	if header.Type != protocol.TypeChunk {
		return fmt.Errorf("unexpected frame type: %d", header.Type)
	}

	payloadLen := int(header.PayloadLen)
	if payloadLen > len(buf) {
		return fmt.Errorf("payload exceeds buffer size")
	}

	if _, err := io.ReadFull(conn, buf[:payloadLen]); err != nil {
		return err
	}

	chunkMeta, data, err := protocol.ParseChunkPayload(buf[:payloadLen])
	if err != nil {
		return err
	}

	if chunkMeta.Index != task.index {
		return fmt.Errorf("chunk index mismatch")
	}

	// Update the `.medxfer` state map safely
	_, err = dm.WriteChunkAt(data, int64(chunkMeta.Offset), task.index)
	return err
}
