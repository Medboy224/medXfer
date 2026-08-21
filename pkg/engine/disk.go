package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// DiskManager coordinates thread-safe, random-access disk I/O
type DiskManager struct {
	file     *os.File
	path     string
	size     int64
	mu       sync.Mutex
	isClosed bool
}

// OpenForReading prepares a source file for parallel chunk reads
func OpenForReading(path string) (*DiskManager, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat failed: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("directories are not yet supported directly: %s", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	return &DiskManager{
		file: f,
		path: path,
		size: info.Size(),
	}, nil
}

// CreateAndPreallocate initializes destination file and reserves disk space
func CreateAndPreallocate(destinationDir, filename string, size int64) (*DiskManager, error) {
	if err := os.MkdirAll(destinationDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create destination directory: %w", err)
	}

	fullPath := filepath.Join(destinationDir, filename)
	f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create destination file: %w", err)
	}

	// Portable pre-allocation across Android, Windows, and Linux
	if size > 0 {
		if err := f.Truncate(size); err != nil {
			f.Close()
			return nil, fmt.Errorf("failed to pre-allocate %d bytes on disk: %w", size, err)
		}
	}

	return &DiskManager{
		file: f,
		path: fullPath,
		size: size,
	}, nil
}

// ReadChunkAt reads a specific slice of the file without modifying file cursor
func (dm *DiskManager) ReadChunkAt(buf []byte, offset int64) (int, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if dm.isClosed {
		return 0, os.ErrClosed
	}

	n, err := dm.file.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return n, fmt.Errorf("failed to read at offset %d: %w", offset, err)
	}
	return n, nil
}

// WriteChunkAt writes a chunk directly to its target byte offset concurrently
func (dm *DiskManager) WriteChunkAt(data []byte, offset int64) (int, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if dm.isClosed {
		return 0, os.ErrClosed
	}

	n, err := dm.file.WriteAt(data, offset)
	if err != nil {
		return n, fmt.Errorf("failed to write at offset %d: %w", offset, err)
	}
	return n, nil
}

// Size returns the total file size in bytes
func (dm *DiskManager) Size() int64 {
	return dm.size
}

// Path returns the absolute or relative file path
func (dm *DiskManager) Path() string {
	return dm.path
}

// Sync flushes kernel buffers to physical storage
func (dm *DiskManager) Sync() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if dm.isClosed {
		return os.ErrClosed
	}
	return dm.file.Sync()
}

// Close flushes and closes the file descriptor
func (dm *DiskManager) Close() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if dm.isClosed {
		return nil
	}
	dm.isClosed = true
	_ = dm.file.Sync()
	return dm.file.Close()
}
