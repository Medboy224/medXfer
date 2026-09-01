package engine

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Medboy224/medXfer/pkg/protocol"
)

type Receiver struct {
	outputDir string
	workers   int
}

func NewReceiver(outputDir string, workers int) *Receiver {
	if workers <= 0 {
		workers = 4
	}
	return &Receiver{
		outputDir: outputDir,
		workers:   workers,
	}
}

type chunkTask struct {
	index  uint32
	offset uint64
	length uint32
}

func (r *Receiver) Pull(ctx context.Context, senderAddr string, listener TransferListener) error {
	var dialer net.Dialer
	handshakeConn, err := dialer.DialContext(ctx, "tcp4", senderAddr)
	if err != nil {
		if listener != nil {
			listener.OnError(err)
		}
		return fmt.Errorf("handshake connection failed: %w", err)
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

	safeFileName := filepath.Base(meta.FileName)

	dm, err := CreateAndPreallocate(r.outputDir, safeFileName, meta.FileSize)
	if err != nil {
		if listener != nil {
			listener.OnError(err)
		}
		return fmt.Errorf("preallocation failed: %w", err)
	}
	defer dm.Cleanup()

	totalChunks := uint32((meta.FileSize + int64(meta.ChunkSize) - 1) / int64(meta.ChunkSize))
	if listener != nil {
		listener.OnStart(safeFileName, meta.FileSize, totalChunks)
	}

	if meta.FileSize == 0 {
		if listener != nil {
			listener.OnComplete(dm.finalPath, 0)
		}
		return dm.Finalize()
	}

	taskQueue := make(chan chunkTask, totalChunks)
	for i := uint32(0); i < totalChunks; i++ {
		offset := uint64(i) * uint64(meta.ChunkSize)
		length := meta.ChunkSize
		if offset+uint64(length) > uint64(meta.FileSize) {
			length = uint32(uint64(meta.FileSize) - offset)
		}
		taskQueue <- chunkTask{index: i, offset: offset, length: length}
	}
	close(taskQueue)

	var transferredBytes int64
	var completedChunks uint32
	var activeStreams int32

	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	var wg sync.WaitGroup
	errChan := make(chan error, r.workers)

	startTime := time.Now()
	lastSpeedTime := startTime
	var lastSpeedBytes int64
	var currentSpeed float64

	for w := 0; w < r.workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			var conn net.Conn

			connect := func() bool {
				if conn != nil {
					_ = conn.Close()
				}
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
			defer func() {
				if conn != nil {
					_ = conn.Close()
				}
			}()

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
						select {
						case taskQueue <- task:
						default:
							errChan <- fmt.Errorf("chunk %d aborted after max retries", task.index)
							cancelWorkers()
							return
						}
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
					speed := float64(meta.FileSize) / (1048576.0 * duration.Seconds())
					listener.OnProgress(TransferStats{
						BytesTransferred: meta.FileSize,
						TotalBytes:       meta.FileSize,
						SpeedMBps:        speed,
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

				// Smooth speed over a 1-second window
				if elapsedSpeed >= 1.0 {
					delta := current - lastSpeedBytes
					currentSpeed = (float64(delta) / 1048576.0) / elapsedSpeed
					lastSpeedTime = now
					lastSpeedBytes = current
				} else if lastSpeedBytes == 0 && current > 0 {
					// Fallback for the first second
					currentSpeed = (float64(current) / 1048576.0) / now.Sub(startTime).Seconds()
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

	_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := conn.Write(reqBuf[:]); err != nil {
		return err
	}

	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
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
		return fmt.Errorf("chunk index mismatch: expected %d, got %d", task.index, chunkMeta.Index)
	}

	_, err = dm.WriteChunkAt(data, int64(chunkMeta.Offset))
	return err
}
