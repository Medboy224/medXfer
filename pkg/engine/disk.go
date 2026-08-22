package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type DiskManager struct {
	file      *os.File
	finalPath string
	tempPath  string
	size      int64
	mu        sync.Mutex
	isClosed  bool
}

func OpenForReading(path string) (*DiskManager, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat failed: %w", err)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	return &DiskManager{
		file:      f,
		finalPath: path,
		size:      info.Size(),
	}, nil
}

func CreateAndPreallocate(destinationDir, filename string, size int64) (*DiskManager, error) {
	if err := os.MkdirAll(destinationDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create destination directory: %w", err)
	}

	finalPath := filepath.Join(destinationDir, filename)
	tempPath := filepath.Join(destinationDir, fmt.Sprintf(".%s.xferpart", filename))

	f, err := os.OpenFile(tempPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	if size > 0 {
		if err := f.Truncate(size); err != nil {
			f.Close()
			_ = os.Remove(tempPath)
			return nil, fmt.Errorf("failed to pre-allocate disk space: %w", err)
		}
	}

	return &DiskManager{
		file:      f,
		finalPath: finalPath,
		tempPath:  tempPath,
		size:      size,
	}, nil
}

func (dm *DiskManager) ReadChunkAt(buf []byte, offset int64) (int, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if dm.isClosed {
		return 0, os.ErrClosed
	}
	n, err := dm.file.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return n, err
	}
	return n, nil
}

func (dm *DiskManager) WriteChunkAt(data []byte, offset int64) (int, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if dm.isClosed {
		return 0, os.ErrClosed
	}
	return dm.file.WriteAt(data, offset)
}

func (dm *DiskManager) Size() int64 {
	return dm.size
}

func (dm *DiskManager) Finalize() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if dm.isClosed {
		return nil
	}
	dm.isClosed = true

	_ = dm.file.Sync()
	if err := dm.file.Close(); err != nil {
		return err
	}

	if dm.tempPath != "" {
		if err := os.Rename(dm.tempPath, dm.finalPath); err != nil {
			_ = os.Remove(dm.tempPath)
			return fmt.Errorf("failed to promote temp file: %w", err)
		}
	}
	return nil
}

func (dm *DiskManager) Cleanup() {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if !dm.isClosed {
		dm.isClosed = true
		_ = dm.file.Close()
	}

	if dm.tempPath != "" {
		_ = os.Remove(dm.tempPath)
	}
}

func (dm *DiskManager) Close() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if dm.isClosed {
		return nil
	}
	dm.isClosed = true
	return dm.file.Close()
}
