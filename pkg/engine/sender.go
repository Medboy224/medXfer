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

type Sender struct {
	workers   int
	chunkSize uint32
}

func NewSender(workers int, chunkSize uint32) *Sender {
	if workers <= 0 {
		workers = 4
	}
	if chunkSize == 0 {
		chunkSize = 2 * 1024 * 1024
	}
	return &Sender{
		workers:   workers,
		chunkSize: chunkSize,
	}
}

func (s *Sender) ServeAndSend(ctx context.Context, bindAddr, filePath string, listener TransferListener) error {
	dm, err := OpenForReading(filePath)
	if err != nil {
		if listener != nil {
			listener.OnError(err)
		}
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer dm.Close()

	fileSize := dm.Size()
	fileName := filepath.Base(dm.finalPath)

	var lc net.ListenConfig
	listenerTCP, err := lc.Listen(ctx, "tcp4", bindAddr)
	if err != nil {
		if listener != nil {
			listener.OnError(err)
		}
		return fmt.Errorf("failed to bind on %s: %w", bindAddr, err)
	}
	defer listenerTCP.Close()

	totalChunks := uint32((fileSize + int64(s.chunkSize) - 1) / int64(s.chunkSize))
	if listener != nil {
		listener.OnStart(fileName, fileSize, totalChunks)
	}

	var transferredBytes int64
	var activeStreams int32
	var wg sync.WaitGroup

	startTime := time.Now()
	lastSpeedTime := startTime
	var lastSpeedBytes int64
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

					// Smooth speed over a 1-second window
					if elapsedSpeed >= 1.0 {
						delta := current - lastSpeedBytes
						currentSpeed = (float64(delta) / 1048576.0) / elapsedSpeed
						lastSpeedTime = now
						lastSpeedBytes = current
					} else if lastSpeedBytes == 0 && current > 0 {
						currentSpeed = (float64(current) / 1048576.0) / now.Sub(startTime).Seconds()
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

	go func() {
		<-ctx.Done()
		_ = listenerTCP.Close()
	}()

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

		TuneConn(conn)
		wg.Add(1)
		atomic.AddInt32(&activeStreams, 1)

		go func(c net.Conn) {
			defer wg.Done()
			defer atomic.AddInt32(&activeStreams, -1)
			defer c.Close()

			buf := make([]byte, s.chunkSize)
			frameBuf := make([]byte, protocol.FrameHeaderSize+protocol.ChunkHeaderSize+int(s.chunkSize))

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
					}
					_ = protocol.WriteHandshake(c, meta)

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
						// VITAL FIX: Allow io.EOF so the final file chunk is successfully sent!
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
