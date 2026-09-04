package engine

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

	bufPtr := getChunkBuffer()
	defer putChunkBuffer(bufPtr)
	buf := (*bufPtr)[:256*1024]

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
				if elapsed >= 0.2 && listener != nil {
					delta := totalTransferred - lastSpeedBytes
					speed := (float64(delta) / 1048576.0) / elapsed
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

	bufPtr := getChunkBuffer()
	defer putChunkBuffer(bufPtr)
	buf := (*bufPtr)[:256*1024]

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

		cleanRel := filepath.Clean(header.Name)
		if strings.HasPrefix(cleanRel, "..") || filepath.IsAbs(cleanRel) {
			return fmt.Errorf("security error: path traversal attempt detected in tar path '%s'", header.Name)
		}

		targetPath := filepath.Join(destClean, cleanRel)
		if !strings.HasPrefix(targetPath, destClean+string(filepath.Separator)) && targetPath != destClean {
			return fmt.Errorf("security error: file path escapes target directory: %s", targetPath)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory '%s': %w", targetPath, err)
			}

		case tar.TypeReg, tar.TypeRegA:
			parentDir := filepath.Dir(targetPath)
			if err := os.MkdirAll(parentDir, 0755); err != nil {
				return fmt.Errorf("failed to create parent dir '%s': %w", parentDir, err)
			}

			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, header.FileInfo().Mode().Perm())
			if err != nil {
				return fmt.Errorf("failed to create file '%s': %w", targetPath, err)
			}

			for {
				select {
				case <-ctx.Done():
					outFile.Close()
					return ctx.Err()
				default:
				}

				nr, er := tr.Read(buf)
				if nr > 0 {
					nw, ew := outFile.Write(buf[:nr])
					if nw > 0 {
						totalTransferred += int64(nw)
					}
					if ew != nil {
						outFile.Close()
						return ew
					}
					if nr != nw {
						outFile.Close()
						return io.ErrShortWrite
					}

					now := time.Now()
					elapsed := now.Sub(lastSpeedTime).Seconds()
					if elapsed >= 0.2 && listener != nil {
						delta := totalTransferred - lastSpeedBytes
						speed := (float64(delta) / 1048576.0) / elapsed
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
						outFile.Close()
						return er
					}
					break
				}
			}
			outFile.Close()

			if !header.ModTime.IsZero() {
				_ = os.Chtimes(targetPath, header.ModTime, header.ModTime)
			}
		}
	}

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
