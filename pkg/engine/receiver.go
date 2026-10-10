package engine

import (
	"container/heap"
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
	outputDir       string
	workers         int
	collisionPolicy CollisionPolicy
	commitBytes     int64         // 0: defaultCommitBytes
	commitInterval  time.Duration // 0: defaultCommitInterval
	pauseMu         sync.Mutex
	isPaused        bool
	pauseCond       *sync.Cond
}

func NewReceiver(outputDir string, workers int) *Receiver {
	if workers <= 0 {
		workers = 4
	}
	r := &Receiver{
		outputDir:       outputDir,
		workers:         workers,
		collisionPolicy: PolicyAutoRename,
	}
	r.pauseCond = sync.NewCond(&r.pauseMu)
	return r
}

func (r *Receiver) SetCollisionPolicy(p CollisionPolicy) {
	r.collisionPolicy = p
}

// SetCommitPolicy overrides the group-commit thresholds (STO-04): progress is made durable
// every `bytes` written or every `interval`. Smaller values lose less work on a crash but fsync more.
func (r *Receiver) SetCommitPolicy(bytes int64, interval time.Duration) {
	r.commitBytes = bytes
	r.commitInterval = interval
}

func (r *Receiver) Pause() {
	r.pauseMu.Lock()
	r.isPaused = true
	r.pauseMu.Unlock()
}

func (r *Receiver) Resume() {
	r.pauseMu.Lock()
	r.isPaused = false
	r.pauseCond.Broadcast()
	r.pauseMu.Unlock()
}

func (r *Receiver) IsPaused() bool {
	r.pauseMu.Lock()
	defer r.pauseMu.Unlock()
	return r.isPaused
}

type chunkTask struct {
	index  uint32
	offset uint64
	length uint32
}

// taskHeap is a min-heap of chunk tasks ordered by index.
type taskHeap []chunkTask

func (h taskHeap) Len() int           { return len(h) }
func (h taskHeap) Less(i, j int) bool { return h[i].index < h[j].index }
func (h taskHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *taskHeap) Push(x any)        { *h = append(*h, x.(chunkTask)) }
func (h *taskHeap) Pop() any {
	old := *h
	t := old[len(old)-1]
	*h = old[:len(old)-1]
	return t
}

// taskDispatcher hands out the LOWEST missing index first. A heap (not a FIFO with
// push-front) matters for the bounded window: after two retries the queue could read
// [7, 0, ...]; checking only the head (7, outside the window) would deadlock on chunk 0.
type taskDispatcher struct {
	mu    sync.Mutex
	tasks taskHeap
}

func newTaskDispatcher(totalChunks uint32, meta protocol.FileMetadata, dm *DiskManager) (*taskDispatcher, uint32) {
	var completed uint32
	tasks := make([]chunkTask, 0, totalChunks)
	for i := uint32(0); i < totalChunks; i++ {
		if dm.IsChunkCompleted(i) {
			completed++
			continue
		}
		offset := uint64(i) * uint64(meta.ChunkSize)
		length := meta.ChunkSize
		if offset+uint64(length) > uint64(meta.FileSize) {
			length = uint32(uint64(meta.FileSize) - offset)
		}
		tasks = append(tasks, chunkTask{index: i, offset: offset, length: length})
	}
	h := taskHeap(tasks) // built in increasing index order: already a valid heap
	return &taskDispatcher{tasks: h}, completed
}

// PopWithin returns the task with the smallest index if it is below limit.
// blocked is true when tasks remain but all are outside the window.
func (td *taskDispatcher) PopWithin(limit uint64) (task chunkTask, ok bool, blocked bool) {
	td.mu.Lock()
	defer td.mu.Unlock()
	if len(td.tasks) == 0 {
		return chunkTask{}, false, false
	}
	if uint64(td.tasks[0].index) >= limit {
		return chunkTask{}, false, true
	}
	return heap.Pop(&td.tasks).(chunkTask), true, false
}

func (td *taskDispatcher) Push(tasks ...chunkTask) {
	td.mu.Lock()
	defer td.mu.Unlock()
	for _, t := range tasks {
		heap.Push(&td.tasks, t)
	}
}

// Metadata bounds (D4): a peer must not make the receiver allocate millions of tasks.
const (
	minChunkSize   = 64 << 10
	maxChunkSize   = 8 << 20 // chunk buffers hold 8 MiB of data
	maxTotalChunks = 1 << 21 // 128 GiB with 64 KiB chunks
)

func validateMeta(meta protocol.FileMetadata) error {
	if meta.FileSize < 0 {
		return fmt.Errorf("invalid metadata: negative file size %d", meta.FileSize)
	}
	if meta.ChunkSize < minChunkSize || meta.ChunkSize > maxChunkSize {
		return fmt.Errorf("invalid metadata: chunk size %d outside [%d, %d]", meta.ChunkSize, minChunkSize, maxChunkSize)
	}
	if n := (meta.FileSize + int64(meta.ChunkSize) - 1) / int64(meta.ChunkSize); n > maxTotalChunks {
		return fmt.Errorf("invalid metadata: %d chunks exceeds limit %d", n, maxTotalChunks)
	}
	return nil
}

func sendChunkRequest(conn net.Conn, task chunkTask) error {
	var reqBuf [12]byte
	binary.BigEndian.PutUint16(reqBuf[0:2], protocol.MagicBytes)
	reqBuf[2] = protocol.Version1
	reqBuf[3] = protocol.TypeRequest
	binary.BigEndian.PutUint32(reqBuf[4:8], 4)
	binary.BigEndian.PutUint32(reqBuf[8:12], task.index)

	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := conn.Write(reqBuf[:])
	return err
}

type sequencedChunk struct {
	index       uint32
	offset      int64
	data        []byte
	bufPtr      *[]byte
	netDuration time.Duration
}

func readChunkPayload(conn net.Conn, task chunkTask, buf []byte) (protocol.ChunkMeta, []byte, time.Duration, error) {
	_ = conn.SetReadDeadline(time.Now().Add(6 * time.Second))
	header, err := protocol.ReadHeader(conn)
	if err != nil {
		return protocol.ChunkMeta{}, nil, 0, err
	}

	if header.Type != protocol.TypeChunk {
		return protocol.ChunkMeta{}, nil, 0, fmt.Errorf("unexpected frame type: %d", header.Type)
	}

	payloadLen := int(header.PayloadLen)
	if payloadLen > len(buf) {
		return protocol.ChunkMeta{}, nil, 0, fmt.Errorf("payload exceeds buffer size")
	}

	netStart := time.Now()
	if _, err := io.ReadFull(conn, buf[:payloadLen]); err != nil {
		return protocol.ChunkMeta{}, nil, 0, err
	}
	netDuration := time.Since(netStart)

	chunkMeta, data, err := protocol.ParseChunkPayload(buf[:payloadLen])
	if err != nil {
		return protocol.ChunkMeta{}, nil, 0, err
	}

	if chunkMeta.Index != task.index {
		return protocol.ChunkMeta{}, nil, 0, fmt.Errorf("chunk index mismatch: expected %d, got %d", task.index, chunkMeta.Index)
	}

	return chunkMeta, data, netDuration, nil
}

func readAndWriteChunk(conn net.Conn, task chunkTask, buf []byte, dm *DiskManager) error {
	chunkMeta, data, _, err := readChunkPayload(conn, task, buf)
	if err != nil {
		return err
	}
	_, err = dm.WriteChunkAt(data, int64(chunkMeta.Offset), task.index)
	return err
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
			handshakeConn = wrapConn(ConnDialed, c)
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

	// Allow sufficient time (60s) for interactive sender authorization prompts (-i)
	_ = handshakeConn.SetReadDeadline(time.Now().Add(60 * time.Second))
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

	return r.PullWithMetadata(ctx, senderAddr, listener, meta, fileID)
}

// PullWithMetadata connects workers directly to stream chunks when metadata is already known (e.g. in batches)
func (r *Receiver) PullWithMetadata(ctx context.Context, senderAddr string, listener TransferListener, meta protocol.FileMetadata, fileID string) error {
	if meta.ChunkSize == 0 {
		meta.ChunkSize = 4 * 1024 * 1024
	}
	if err := validateMeta(meta); err != nil {
		if listener != nil {
			listener.OnError(err)
		}
		return err
	}

	// CRITICAL FILE INTEGRITY CHECK:
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

	// Validate the peer-supplied path (STO-12): a crafted offer fails instead of being silently rewritten
	safeFileName, err := SafeRelPath(meta.FileName)
	if err != nil {
		if listener != nil {
			listener.OnError(err)
		}
		return err
	}

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
	if r.commitBytes > 0 {
		dm.commitBytes = r.commitBytes
	}
	if r.commitInterval > 0 {
		dm.commitInterval = r.commitInterval
	}
	defer dm.Cleanup()

	totalChunks := uint32((meta.FileSize + int64(meta.ChunkSize) - 1) / int64(meta.ChunkSize))
	if listener != nil {
		listener.OnStart(res.ResolvedName, meta.FileSize, totalChunks)
	}

	if meta.FileSize == 0 {
		return r.finalize(dm, listener, 0)
	}

	dispatcher, initialCompleted := newTaskDispatcher(totalChunks, meta, dm)
	var completedChunks uint32 = initialCompleted
	transferredBytes := dm.GetDownloadedBytes()

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
		}
		return r.finalize(dm, listener, 0)
	}

	var activeStreams int32
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	var connsMu sync.Mutex
	activeConns := make(map[net.Conn]struct{})

	go func() {
		<-workerCtx.Done()
		r.pauseCond.Broadcast()
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

	// Bounded receive window (D3, STO-08): every chunk held in memory (in flight, queued or
	// pending) has an index < windowBase + window, so at most `window` buffers are borrowed.
	// windowBase is the lowest chunk not yet written; the writer advances it.
	window := uint64(r.workers * 3)
	if window < 8 {
		window = 8
	}
	var windowBase uint32
	for windowBase < totalChunks && dm.IsChunkCompleted(windowBase) {
		windowBase++
	}
	windowLimit := func() uint64 { return uint64(atomic.LoadUint32(&windowBase)) + window }

	writerQueue := make(chan sequencedChunk, 16)
	writerDone := make(chan struct{})

	var rollingNetMs float64
	var rollingDiskMs float64
	var rollingMu sync.RWMutex

	go func() {
		defer close(writerDone)
		nextExpected := atomic.LoadUint32(&windowBase)
		advance := func() {
			nextExpected++
			for nextExpected < totalChunks && dm.IsChunkCompleted(nextExpected) {
				nextExpected++
			}
			atomic.StoreUint32(&windowBase, nextExpected)
		}
		pending := make(map[uint32]sequencedChunk)

		writeChunk := func(ch sequencedChunk) error {
			diskStart := time.Now()
			_, err := dm.WriteChunkAt(ch.data, ch.offset, ch.index)
			diskDuration := time.Since(diskStart)
			putChunkBuffer(ch.bufPtr)

			if err != nil {
				return err
			}

			netMs := float64(ch.netDuration.Milliseconds())
			diskMs := float64(diskDuration.Milliseconds())
			rollingMu.Lock()
			if rollingNetMs == 0 {
				rollingNetMs = netMs
				rollingDiskMs = diskMs
			} else {
				rollingNetMs = 0.85*rollingNetMs + 0.15*netMs
				rollingDiskMs = 0.85*rollingDiskMs + 0.15*diskMs
			}
			rollingMu.Unlock()

			atomic.AddInt64(&transferredBytes, int64(len(ch.data)))
			atomic.AddUint32(&completedChunks, 1)
			return nil
		}

		for ch := range writerQueue {
			if workerCtx.Err() != nil {
				putChunkBuffer(ch.bufPtr)
				continue
			}

			if ch.index == nextExpected {
				if err := writeChunk(ch); err != nil {
					select {
					case errChan <- fmt.Errorf("disk write error on chunk %d: %w", ch.index, err):
					default:
					}
					cancelWorkers()
					break
				}
				advance()

				// Flush any consecutive chunks that were waiting in pending map
				for {
					nextCh, ok := pending[nextExpected]
					if !ok {
						break
					}
					delete(pending, nextExpected)
					if err := writeChunk(nextCh); err != nil {
						select {
						case errChan <- fmt.Errorf("disk write error on chunk %d: %w", nextCh.index, err):
						default:
						}
						cancelWorkers()
						break
					}
					advance()
				}
			} else {
				// Out-of-order chunk: buffer until nextExpected arrives
				pending[ch.index] = ch
			}
		}

		// Clean up any remaining pending buffers on exit/cancel
		for _, ch := range pending {
			putChunkBuffer(ch.bufPtr)
		}
	}()

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
				delays := []time.Duration{
					50 * time.Millisecond,
					100 * time.Millisecond,
					150 * time.Millisecond,
					250 * time.Millisecond,
					400 * time.Millisecond,
					500 * time.Millisecond,
					750 * time.Millisecond,
					1000 * time.Millisecond,
				}
				for attempt := 0; attempt < len(delays); attempt++ {
					select {
					case <-workerCtx.Done():
						return false
					default:
					}
					var dialer net.Dialer
					c, err := dialer.DialContext(workerCtx, "tcp4", senderAddr)
					if err == nil {
						TuneConn(c)
						conn = wrapConn(ConnDialed, c)
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
					time.Sleep(delays[attempt])
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

			var prefetched *chunkTask
			taskRetry := make(map[uint32]int)

			// popTask waits while the next tasks are outside the window; false when none remain.
			popTask := func() (chunkTask, bool) {
				for {
					task, ok, blocked := dispatcher.PopWithin(windowLimit())
					if ok || !blocked {
						return task, ok
					}
					select {
					case <-workerCtx.Done():
						return chunkTask{}, false
					case <-time.After(2 * time.Millisecond):
					}
				}
			}

			for {
				select {
				case <-workerCtx.Done():
					return
				default:
				}

				// Check pause state
				r.pauseMu.Lock()
				for r.isPaused && workerCtx.Err() == nil {
					r.pauseCond.Wait()
				}
				r.pauseMu.Unlock()
				if workerCtx.Err() != nil {
					return
				}

				if conn == nil {
					if !connect() {
						if prefetched != nil {
							dispatcher.Push(*prefetched)
							prefetched = nil
						}
						errChan <- fmt.Errorf("worker %d failed to connect", workerID)
						cancelWorkers()
						return
					}
					if prefetched != nil {
						dispatcher.Push(*prefetched)
						prefetched = nil
					}
				}

				var task chunkTask
				if prefetched != nil {
					task = *prefetched
					prefetched = nil
				} else {
					var ok bool
					task, ok = popTask()
					if !ok {
						return
					}
					if err := sendChunkRequest(conn, task); err != nil {
						taskRetry[task.index]++
						if taskRetry[task.index] >= 4 {
							errChan <- fmt.Errorf("chunk %d transfer failed after max retries", task.index)
							cancelWorkers()
							return
						}
						dispatcher.Push(task)
						disconnect()
						time.Sleep(100 * time.Millisecond)
						continue
					}
				}

				// PIPELINE: Prefetch the next chunk request so the wire stays 100% saturated!
				if nextTask, ok, _ := dispatcher.PopWithin(windowLimit()); ok {
					if err := sendChunkRequest(conn, nextTask); err == nil {
						prefetched = &nextTask
					} else {
						dispatcher.Push(nextTask)
					}
				}

				bufPtr := getChunkBuffer()
				chunkMeta, data, netDuration, err := readChunkPayload(conn, task, *bufPtr)
				if err != nil {
					putChunkBuffer(bufPtr)
					taskRetry[task.index]++
					if taskRetry[task.index] >= 4 {
						errChan <- fmt.Errorf("chunk %d transfer failed after max retries", task.index)
						cancelWorkers()
						return
					}
					if prefetched != nil {
						dispatcher.Push(*prefetched)
						prefetched = nil
					}
					dispatcher.Push(task)
					if listener != nil {
						listener.OnChunkFailed(task.index, taskRetry[task.index], err)
					}
					disconnect()
					time.Sleep(100 * time.Millisecond)
					continue
				}

				select {
				case <-workerCtx.Done():
					putChunkBuffer(bufPtr)
					return
				case writerQueue <- sequencedChunk{
					index:       chunkMeta.Index,
					offset:      int64(chunkMeta.Offset),
					data:        data,
					bufPtr:      bufPtr,
					netDuration: netDuration,
				}:
				}
			}
		}(w)
	}

	doneChan := make(chan struct{})
	go func() {
		wg.Wait()
		close(writerQueue)
		<-writerDone
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
					rollingMu.RLock()
					curNetMs := rollingNetMs
					curDiskMs := rollingDiskMs
					rollingMu.RUnlock()

					bottleneck := "BALANCED"
					reason := "Balanced network link and storage write throughput"
					if curDiskMs > 1.8*curNetMs && curDiskMs > 15 {
						bottleneck = "STORAGE"
						reason = fmt.Sprintf("Storage write latency is elevated (%.0f ms/chunk). Target drive speed is the bottleneck.", curDiskMs)
					} else if curNetMs > 1.8*curDiskMs && curNetMs > 25 {
						bottleneck = "NETWORK"
						reason = fmt.Sprintf("Network read latency is elevated (%.0f ms/chunk). Link throughput is the bottleneck.", curNetMs)
					}

					listener.OnProgress(TransferStats{
						BytesTransferred:   meta.FileSize,
						TotalBytes:         meta.FileSize,
						SpeedMBps:          currentSpeed,
						ActiveStreams:      0,
						ProgressPercent:    100.0,
						DiskWriteLatencyMs: curDiskMs,
						NetReadLatencyMs:   curNetMs,
						Bottleneck:         bottleneck,
						BottleneckReason:   reason,
					})
				}
				return r.finalize(dm, listener, duration)
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

				if elapsedSpeed >= 0.5 {
					delta := current - lastSpeedBytes
					instantSpeed := (float64(delta) / 1048576.0) / elapsedSpeed
					if currentSpeed == 0 {
						currentSpeed = instantSpeed
					} else {
						currentSpeed = 0.7*currentSpeed + 0.3*instantSpeed
					}
					lastSpeedTime = now
					lastSpeedBytes = current
				} else if lastSpeedBytes == dm.GetDownloadedBytes() && current > dm.GetDownloadedBytes() {
					currentSpeed = (float64(current-dm.GetDownloadedBytes()) / 1048576.0) / now.Sub(startTime).Seconds()
				}

				percent := 0.0
				if meta.FileSize > 0 {
					percent = (float64(current) / float64(meta.FileSize)) * 100.0
				}

				rollingMu.RLock()
				curNetMs := rollingNetMs
				curDiskMs := rollingDiskMs
				rollingMu.RUnlock()

				bottleneck := "BALANCED"
				reason := "Balanced network link and storage write throughput"
				if curDiskMs > 1.8*curNetMs && curDiskMs > 15 {
					bottleneck = "STORAGE"
					reason = fmt.Sprintf("Storage write latency is elevated (%.0f ms/chunk). Target drive speed is the bottleneck.", curDiskMs)
				} else if curNetMs > 1.8*curDiskMs && curNetMs > 25 {
					bottleneck = "NETWORK"
					reason = fmt.Sprintf("Network read latency is elevated (%.0f ms/chunk). Link throughput is the bottleneck.", curNetMs)
				}

				listener.OnProgress(TransferStats{
					BytesTransferred:   current,
					TotalBytes:         meta.FileSize,
					SpeedMBps:          currentSpeed,
					ActiveStreams:      int(atomic.LoadInt32(&activeStreams)),
					ProgressPercent:    percent,
					DiskWriteLatencyMs: curDiskMs,
					NetReadLatencyMs:   curNetMs,
					Bottleneck:         bottleneck,
					BottleneckReason:   reason,
				})
			}
		}
	}
}

// finalize makes the file durable BEFORE telling the user it is complete (D2).
func (r *Receiver) finalize(dm *DiskManager, listener TransferListener, duration time.Duration) error {
	if err := dm.Finalize(); err != nil {
		if listener != nil {
			listener.OnError(err)
		}
		return err
	}
	if listener != nil {
		listener.OnComplete(dm.finalPath, duration)
	}
	return nil
}

func (r *Receiver) fetchChunk(conn net.Conn, task chunkTask, buf []byte, dm *DiskManager) error {
	if err := sendChunkRequest(conn, task); err != nil {
		return err
	}
	return readAndWriteChunk(conn, task, buf, dm)
}
