package engine

import (
	"archive/tar"
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/manifest"
)

// StreamTar packs files from the manifest on-the-fly and writes them directly into w.
// No intermediate temporary files are created on disk.
func StreamTar(ctx context.Context, w io.Writer, m *manifest.Manifest, listener TransferListener) error {
	if m == nil || len(m.Items) == 0 {
		return fmt.Errorf("empty manifest")
	}

	tw := tar.NewWriter(w)
	defer tw.Close()

	if listener != nil {
		listener.OnStart(m.RootName, m.TotalBytes, uint32(m.TotalFiles))
	}

	startTime := time.Now()
	lastSpeedTime := startTime
	var totalTransferred int64
	var lastSpeedBytes int64
	var speed float64

	bufPtr := getChunkBuffer()
	defer putChunkBuffer(bufPtr)
	buf := (*bufPtr)[:1024*1024]

	for _, item := range m.Items {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		info, err := os.Stat(item.FullPath)
		if err != nil {
			return fmt.Errorf("failed to stat '%s': %w", item.FullPath, err)
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("failed to create tar header for '%s': %w", item.RelPath, err)
		}
		header.Name = filepath.ToSlash(item.RelPath)

		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("failed to write tar header for '%s': %w", item.RelPath, err)
		}

		file, err := os.Open(item.FullPath)
		if err != nil {
			return fmt.Errorf("failed to open '%s': %w", item.FullPath, err)
		}

		var fileWritten int64
		for {
			select {
			case <-ctx.Done():
				file.Close()
				return ctx.Err()
			default:
			}

			nr, er := file.Read(buf)
			if nr > 0 {
				nw, ew := tw.Write(buf[:nr])
				if nw > 0 {
					totalTransferred += int64(nw)
					fileWritten += int64(nw)
				}
				if ew != nil {
					file.Close()
					return ew
				}
				if nr != nw {
					file.Close()
					return io.ErrShortWrite
				}

				now := time.Now()
				elapsed := now.Sub(lastSpeedTime).Seconds()
				if elapsed >= 0.5 && listener != nil {
					delta := totalTransferred - lastSpeedBytes
					instantSpeed := (float64(delta) / 1048576.0) / elapsed
					if speed == 0 {
						speed = instantSpeed
					} else {
						speed = 0.7*speed + 0.3*instantSpeed
					}
					lastSpeedBytes = totalTransferred
					lastSpeedTime = now

					percent := 0.0
					if m.TotalBytes > 0 {
						percent = (float64(totalTransferred) / float64(m.TotalBytes)) * 100.0
					}
					listener.OnProgress(TransferStats{
						BytesTransferred: totalTransferred,
						TotalBytes:       m.TotalBytes,
						SpeedMBps:        speed,
						ActiveStreams:    1,
						ProgressPercent:  percent,
					})
				}
			}
			if er != nil {
				if er != io.EOF {
					file.Close()
					return er
				}
				break
			}
		}
		file.Close()
	}

	if err := tw.Close(); err != nil {
		return err
	}

	if listener != nil {
		duration := time.Since(startTime)
		listener.OnProgress(TransferStats{
			BytesTransferred: m.TotalBytes,
			TotalBytes:       m.TotalBytes,
			SpeedMBps:        0,
			ActiveStreams:    0,
			ProgressPercent:  100.0,
		})
		listener.OnComplete(m.RootName, duration)
	}

	return nil
}

// ExtractTar reads an incoming tar stream from r and extracts files directly to destDir.
// It includes strict path traversal protections (preventing Zip Slip / Tar Slip vulnerabilities).
func ExtractTar(ctx context.Context, r io.Reader, destDir string, totalBytes int64, totalFiles int, listener TransferListener) error {
	tr := tar.NewReader(r)

	destClean := filepath.Clean(destDir)
	if err := os.MkdirAll(destClean, 0755); err != nil {
		return fmt.Errorf("failed to create destination dir '%s': %w", destClean, err)
	}

	if listener != nil {
		listener.OnStart(filepath.Base(destClean), totalBytes, uint32(totalFiles))
	}

	startTime := time.Now()
	lastSpeedTime := startTime
	var totalTransferred int64
	var lastSpeedBytes int64
	var speed float64

	bufPtr := getChunkBuffer()
	defer putChunkBuffer(bufPtr)
	buf := (*bufPtr)[:1024*1024]

	// Asynchronous file closing & metadata commit pipeline:
	// Offloads fsync/OS cache flushing and Chtimes away from the network reader goroutine,
	// preventing TCP ZeroWindow stalls and connection collapse between files!
	closeTasks := make(chan *os.File, 64)
	var closeWg sync.WaitGroup
	var closeOnce sync.Once
	closeWg.Add(1)
	go func() {
		defer closeWg.Done()
		for f := range closeTasks {
			_ = f.Close()
		}
	}()

	shutdownWorkers := func() {
		closeOnce.Do(func() {
			close(closeTasks)
			closeWg.Wait()
		})
	}
	defer shutdownWorkers()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read error: %w", err)
		}

		// Directory entries end with "/": strip it before per-component validation (STO-12)
		safeRel, err := SafeRelPath(strings.TrimSuffix(header.Name, "/"))
		if err != nil {
			return fmt.Errorf("security error: rejected tar path: %w", err)
		}

		targetPath := filepath.Join(destClean, filepath.FromSlash(safeRel))
		if !strings.HasPrefix(targetPath, destClean+string(filepath.Separator)) && targetPath != destClean {
			return fmt.Errorf("security error: file path escapes target directory: %s", targetPath)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := ensureSubdirs(destClean, safeRel); err != nil {
				return fmt.Errorf("failed to create directory '%s': %w", targetPath, err)
			}

		case tar.TypeReg, tar.TypeRegA:
			parentDir := filepath.Dir(targetPath)
			if err := ensureSubdirs(destClean, path.Dir(safeRel)); err != nil {
				return fmt.Errorf("failed to create parent dir '%s': %w", parentDir, err)
			}

			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, header.FileInfo().Mode().Perm())
			if err != nil {
				return fmt.Errorf("failed to create file '%s': %w", targetPath, err)
			}

			// Buffer writes in 2MB chunks to align with disk sectors and prevent filesystem fragmentation
			bw := bufio.NewWriterSize(outFile, 2*1024*1024)

			var readErr error
			for {
				select {
				case <-ctx.Done():
					_ = outFile.Close()
					return ctx.Err()
				default:
				}

				nr, er := tr.Read(buf)
				if nr > 0 {
					nw, ew := bw.Write(buf[:nr])
					if nw > 0 {
						totalTransferred += int64(nw)
					}
					if ew != nil {
						_ = outFile.Close()
						return ew
					}
					if nr != nw {
						_ = outFile.Close()
						return io.ErrShortWrite
					}

					now := time.Now()
					elapsed := now.Sub(lastSpeedTime).Seconds()
					if elapsed >= 0.5 && listener != nil {
						delta := totalTransferred - lastSpeedBytes
						instantSpeed := (float64(delta) / 1048576.0) / elapsed
						if speed == 0 {
							speed = instantSpeed
						} else {
							speed = 0.7*speed + 0.3*instantSpeed
						}
						lastSpeedBytes = totalTransferred
						lastSpeedTime = now

						percent := 0.0
						if totalBytes > 0 {
							percent = (float64(totalTransferred) / float64(totalBytes)) * 100.0
						}
						listener.OnProgress(TransferStats{
							BytesTransferred: totalTransferred,
							TotalBytes:       totalBytes,
							SpeedMBps:        speed,
							ActiveStreams:    1,
							ProgressPercent:  percent,
						})
					}
				}
				if er != nil {
					if er != io.EOF {
						readErr = er
					}
					break
				}
			}

			if readErr != nil {
				_ = outFile.Close()
				return readErr
			}

			// Flush memory buffer into OS cache (instantaneous)
			if err := bw.Flush(); err != nil {
				_ = outFile.Close()
				return fmt.Errorf("failed to flush file '%s': %w", targetPath, err)
			}

			// Dispatch file close asynchronously to prevent network stalling
			select {
			case closeTasks <- outFile:
			case <-ctx.Done():
				_ = outFile.Close()
				return ctx.Err()
			}
		}
	}

	// Ensure all background file closes & mtimes have completed before signalling completion
	shutdownWorkers()

	if listener != nil {
		duration := time.Since(startTime)
		listener.OnProgress(TransferStats{
			BytesTransferred: totalTransferred,
			TotalBytes:       totalBytes,
			SpeedMBps:        0,
			ActiveStreams:    0,
			ProgressPercent:  100.0,
		})
		listener.OnComplete(destClean, duration)
	}

	return nil
}
