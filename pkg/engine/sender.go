package engine

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Medboy224/medXfer/pkg/protocol"
)

// AuthorizerFunc is invoked when a receiver connects to authorize or decline the transfer
type AuthorizerFunc func(remoteAddr net.Addr, fileName string, fileSize int64) bool

type Sender struct {
	workers    int
	chunkSize  uint32
	authorizer AuthorizerFunc
}

func NewSender(workers int, chunkSize uint32) *Sender {
	if workers <= 0 {
		workers = 4
	}
	if chunkSize == 0 {
		chunkSize = 4 * 1024 * 1024
	}
	// Chunk buffers hold 8 MiB of data: a larger size would panic when serving a chunk,
	// and the receiver rejects sizes outside [minChunkSize, maxChunkSize].
	if chunkSize < minChunkSize || chunkSize > maxChunkSize {
		log.Printf("[engine] chunk size %d outside [%d, %d], using 4 MiB", chunkSize, minChunkSize, maxChunkSize)
		chunkSize = 4 * 1024 * 1024
	}
	return &Sender{
		workers:   workers,
		chunkSize: chunkSize,
	}
}

// SetAuthorizer attaches a callback that is invoked when a receiver attempts to connect
func (s *Sender) SetAuthorizer(fn AuthorizerFunc) {
	s.authorizer = fn
}

// ServeAndSend accepts resumeOffset so the progress bar jumps accurately
func (s *Sender) ServeAndSend(ctx context.Context, bindAddr, filePath string, listener TransferListener, resumeOffset int64) error {
	return s.ServeAndSendWithRelPath(ctx, bindAddr, filePath, "", listener, resumeOffset)
}

// ServeAndSendWithRelPath allows specifying a relative path for batch/folder transfers
func (s *Sender) ServeAndSendWithRelPath(ctx context.Context, bindAddr, filePath, relPath string, listener TransferListener, resumeOffset int64) error {
	return s.ServeAndSendWithRelPathReady(ctx, bindAddr, filePath, relPath, listener, resumeOffset, nil)
}

// ServeAndSendWithRelPathReady accepts a ready channel that is notified with nil immediately after
// the TCP listener is bound and ready to accept client connections, or notified with an error if setup fails.
func (s *Sender) ServeAndSendWithRelPathReady(ctx context.Context, bindAddr, filePath, relPath string, listener TransferListener, resumeOffset int64, ready chan<- error) error {
	dm, err := OpenForReading(filePath)
	if err != nil {
		if listener != nil {
			listener.OnError(err)
		}
		if ready != nil {
			select {
			case ready <- fmt.Errorf("failed to open file: %w", err):
			default:
			}
		}
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer dm.Close()

	fileSize := dm.Size()
	fileName := filepath.Base(dm.finalPath)
	if relPath != "" {
		fileName = filepath.ToSlash(relPath)
	}
	fileID := GenerateFileID(filePath)

	var lc net.ListenConfig
	listenerTCP, err := lc.Listen(ctx, "tcp4", bindAddr)
	if err != nil {
		if listener != nil {
			listener.OnError(err)
		}
		if ready != nil {
			select {
			case ready <- fmt.Errorf("failed to bind on %s: %w", bindAddr, err):
			default:
			}
		}
		return fmt.Errorf("failed to bind on %s: %w", bindAddr, err)
	}
	defer listenerTCP.Close()

	// Signal caller that the listener is bound and ready for incoming peer connections
	if ready != nil {
		select {
		case ready <- nil:
		default:
		}
	}

	totalChunks := uint32((fileSize + int64(s.chunkSize) - 1) / int64(s.chunkSize))
	if listener != nil {
		listener.OnStart(fileName, fileSize, totalChunks)
	}

	// Initialize the progress counter with the bytes already present on the receiver
	var transferredBytes int64 = resumeOffset
	var activeStreams int32
	var wg sync.WaitGroup

	startTime := time.Now()
	lastSpeedTime := startTime
	var lastSpeedBytes int64 = transferredBytes
	var currentSpeed float64

	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if listener != nil {
					current := atomic.LoadInt64(&transferredBytes)
					streams := atomic.LoadInt32(&activeStreams)

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
					} else if lastSpeedBytes == resumeOffset && current > resumeOffset {
						currentSpeed = (float64(current-resumeOffset) / 1048576.0) / now.Sub(startTime).Seconds()
					}

					if current > 0 || streams > 0 {
						percent := 0.0
						if fileSize > 0 {
							percent = (float64(current) / float64(fileSize)) * 100.0
						}
						listener.OnProgress(TransferStats{
							BytesTransferred: current,
							TotalBytes:       fileSize,
							SpeedMBps:        currentSpeed,
							ActiveStreams:    int(streams),
							ProgressPercent:  percent,
						})
					}
				}
			}
		}
	}()

	var connsMu sync.Mutex
	activeConns := make(map[net.Conn]struct{})

	go func() {
		<-ctx.Done()
		_ = listenerTCP.Close()
		connsMu.Lock()
		for c := range activeConns {
			_ = c.Close()
		}
		connsMu.Unlock()
	}()

	var authOnce sync.Once
	var authAllowed bool = true

	for {
		conn, err := listenerTCP.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				wg.Wait()
				return nil
			default:
				if listener != nil {
					listener.OnError(err)
				}
				return err
			}
		}

		if s.authorizer != nil {
			authOnce.Do(func() {
				authAllowed = s.authorizer(conn.RemoteAddr(), fileName, fileSize)
			})
			if !authAllowed {
				_ = conn.Close()
				errDeclined := fmt.Errorf("transfer declined by sender")
				if listener != nil {
					listener.OnError(errDeclined)
				}
				return errDeclined
			}
		}

		TuneConn(conn)
		wg.Add(1)
		atomic.AddInt32(&activeStreams, 1)

		connsMu.Lock()
		activeConns[conn] = struct{}{}
		connsMu.Unlock()

		go func(c net.Conn) {
			defer wg.Done()
			defer atomic.AddInt32(&activeStreams, -1)
			defer func() {
				connsMu.Lock()
				delete(activeConns, c)
				connsMu.Unlock()
				_ = c.Close()
			}()

			bufPtr := getChunkBuffer()
			defer putChunkBuffer(bufPtr)
			buf := (*bufPtr)[:s.chunkSize]

			framePtr := getChunkBuffer()
			defer putChunkBuffer(framePtr)
			frameBuf := *framePtr

			for {
				header, err := protocol.ReadHeader(c)
				if err != nil {
					return
				}

				switch header.Type {
				case protocol.TypeHandshake:
					meta := protocol.FileMetadata{
						FileName:  fileName,
						FileSize:  fileSize,
						ChunkSize: s.chunkSize,
						FileID:    fileID,
					}
					_ = protocol.WriteHandshake(c, meta)

				case protocol.TypeResume:
					resumeBuf := make([]byte, header.PayloadLen)
					if _, err := io.ReadFull(c, resumeBuf); err != nil {
						return
					}
					var offset int64
					if len(resumeBuf) >= 40 {
						reqFileID := strings.TrimSpace(string(resumeBuf[:32]))
						if fileID != "" && reqFileID != "" && reqFileID != strings.TrimSpace(fileID) {
							// Worker is requesting a DIFFERENT file! Drop connection immediately.
							return
						}
						offset = int64(binary.BigEndian.Uint64(resumeBuf[32:40]))
					} else if len(resumeBuf) >= 8 {
						offset = int64(binary.BigEndian.Uint64(resumeBuf[:8]))
					}

					for {
						current := atomic.LoadInt64(&transferredBytes)
						if current >= offset {
							break
						}
						if atomic.CompareAndSwapInt64(&transferredBytes, current, offset) {
							break
						}
					}

				case protocol.TypeRequest:
					var reqIndex uint32
					reqBytes := make([]byte, 4)
					if _, err := io.ReadFull(c, reqBytes); err != nil {
						return
					}
					reqIndex = binary.BigEndian.Uint32(reqBytes)

					offset := int64(reqIndex) * int64(s.chunkSize)
					toRead := int64(s.chunkSize)
					if offset+toRead > fileSize {
						toRead = fileSize - offset
					}

					if toRead > 0 {
						n, err := dm.ReadChunkAt(buf[:toRead], offset)
						// Allow io.EOF so the final file chunk completes successfully
						if n > 0 && (err == nil || err == io.EOF) {
							if err := writeContiguousChunk(c, frameBuf, reqIndex, uint64(offset), buf[:n]); err == nil {
								atomic.AddInt64(&transferredBytes, int64(n))
							}
						}
					}
				}
			}
		}(conn)
	}
}

func writeContiguousChunk(w io.Writer, outBuf []byte, index uint32, offset uint64, data []byte) error {
	totalLen := protocol.FrameHeaderSize + protocol.ChunkHeaderSize + len(data)
	if len(outBuf) < totalLen {
		outBuf = make([]byte, totalLen)
	}

	binary.BigEndian.PutUint16(outBuf[0:2], protocol.MagicBytes)
	outBuf[2] = protocol.Version1
	outBuf[3] = protocol.TypeChunk
	binary.BigEndian.PutUint32(outBuf[4:8], uint32(protocol.ChunkHeaderSize+len(data)))

	binary.BigEndian.PutUint32(outBuf[8:12], index)
	binary.BigEndian.PutUint64(outBuf[12:20], offset)
	binary.BigEndian.PutUint32(outBuf[20:24], uint32(len(data)))
	binary.BigEndian.PutUint32(outBuf[24:28], protocol.CalculateChecksum(data))

	copy(outBuf[28:], data)

	_, err := w.Write(outBuf[:totalLen])
	return err
}
