package engine

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type DiskManager struct {
	file      *os.File
	stateFile *os.File
	finalPath string
	statePath string
	fileSize  int64
	mu        sync.RWMutex
	completed []bool
	downBytes int64
	dirty     bool
	lastSync  time.Time
}

// GenerateFileID creates a fast unique hash from FileInfo metadata (Name, Size, ModTime)
// without performing blocking disk reads or network seeks.
func GenerateFileID(filePath string) string {
	info, err := os.Stat(filePath)
	if err != nil {
		return ""
	}
	return GenerateFileIDFromInfo(info)
}

// GenerateFileIDFromInfo generates a unique hash from already available os.FileInfo metadata
func GenerateFileIDFromInfo(info os.FileInfo) string {
	if info == nil {
		return ""
	}
	h := md5.New()
	_, _ = fmt.Fprintf(h, "%s-%d-%d", info.Name(), info.Size(), info.ModTime().UnixNano())
	return fmt.Sprintf("%x", h.Sum(nil))
}

// PeekResumeOffset returns how many valid bytes already exist on disk if fileID matches
func PeekResumeOffset(outputDir, fileName, fileID string, fileSize int64, chunkSize uint32) (int64, error) {
	if chunkSize == 0 {
		chunkSize = 2 * 1024 * 1024
	}
	if fileSize <= 0 {
		return 0, nil
	}
	if len(fileID) != 32 {
		fileID = fmt.Sprintf("%-32s", fileID)
	}

	normPath := strings.ReplaceAll(fileName, "/", string(filepath.Separator))
	normPath = strings.ReplaceAll(normPath, "\\", string(filepath.Separator))
	statePath := filepath.Join(outputDir, normPath+".medxfer")

	b, err := os.ReadFile(statePath)
	if err != nil || len(b) < 32 {
		return 0, nil
	} // No existing file, normal start

	// INTEGRITY CHECK: If it's a different file with the same name, start fresh from 0
	if string(b[:32]) != fileID {
		return 0, nil
	}

	totalChunks := uint32((fileSize + int64(chunkSize) - 1) / int64(chunkSize))
	if len(b) < int(32+totalChunks) {
		return 0, nil
	}

	var downBytes int64
	for i := uint32(0); i < totalChunks; i++ {
		if b[32+i] == 1 {
			length := int64(chunkSize)
			if i == totalChunks-1 {
				length = fileSize - (int64(i) * int64(chunkSize))
			}
			downBytes += length
		}
	}
	return downBytes, nil
}

func ensureDirectory(targetDir string) error {
	if targetDir == "" || targetDir == "." {
		return nil
	}

	cleanDir := filepath.Clean(targetDir)
	parts := strings.Split(cleanDir, string(filepath.Separator))

	current := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if current == "" {
			current = part
		} else {
			current = filepath.Join(current, part)
		}

		if info, err := os.Stat(current); err == nil {
			if !info.IsDir() {
				// A regular file already exists where a folder needs to be created. Clean it up!
				_ = os.Remove(current)
				_ = os.Remove(current + ".medxfer")
			}
		}
	}

	return os.MkdirAll(targetDir, 0755)
}

func CreateAndPreallocate(outputDir, fileName string, fileSize int64, chunkSize uint32, fileID string) (*DiskManager, error) {
	normPath := strings.ReplaceAll(fileName, "/", string(filepath.Separator))
	normPath = strings.ReplaceAll(normPath, "\\", string(filepath.Separator))

	finalPath := filepath.Join(outputDir, normPath)
	targetDir := filepath.Dir(finalPath)
	if err := ensureDirectory(targetDir); err != nil {
		return nil, fmt.Errorf("failed to create directory '%s': %w", targetDir, err)
	}
	statePath := finalPath + ".medxfer"

	if len(fileID) != 32 {
		fileID = fmt.Sprintf("%-32s", fileID)
	}

	totalChunks := uint32(0)
	if chunkSize > 0 {
		totalChunks = uint32((fileSize + int64(chunkSize) - 1) / int64(chunkSize))
	}

	completed := make([]bool, totalChunks)
	var downBytes int64
	var file, stateFile *os.File
	var err error

	if _, err = os.Stat(statePath); err == nil {
		file, err = os.OpenFile(finalPath, os.O_RDWR, 0644)
		if err == nil {
			stateFile, err = os.OpenFile(statePath, os.O_RDWR, 0644)
			if err == nil {
				stateBytes := make([]byte, 32+totalChunks)
				n, err := stateFile.ReadAt(stateBytes, 0)

				if (err == nil || err.Error() == "EOF") && n >= 32 {
					if string(stateBytes[:32]) == fileID && n == int(32+totalChunks) {
						for i := uint32(0); i < totalChunks; i++ {
							if stateBytes[32+i] == 1 {
								completed[i] = true
								length := int64(chunkSize)
								if i == totalChunks-1 {
									length = fileSize - (int64(i) * int64(chunkSize))
								}
								downBytes += length
							}
						}
					} else {
						// The partial file on disk belongs to a DIFFERENT file with the same name.
						// Safely clean up the old mismatched state and start fresh for the new file!
						file.Close()
						stateFile.Close()
						file = nil
						stateFile = nil
						_ = os.Remove(finalPath)
						_ = os.Remove(statePath)
					}
				}
			}
		}
	}

	if file == nil || stateFile == nil {
		file, err = os.Create(finalPath)
		if err != nil {
			return nil, err
		}
		// NOTE: Avoid file.Truncate(fileSize) here. On Windows NTFS (especially USB external HDDs),
		// Truncate forces the OS to synchronously write zeroes across the entire file range before any chunk write,
		// doubling disk write volume and stalling the transfer. File grows dynamically as chunks are written.

		stateFile, err = os.Create(statePath)
		if err != nil {
			file.Close()
			return nil, err
		}

		initBytes := make([]byte, 32+totalChunks)
		copy(initBytes[:32], []byte(fileID))
		stateFile.Write(initBytes)

		downBytes = 0
		for i := range completed {
			completed[i] = false
		}
	}

	return &DiskManager{
		file: file, stateFile: stateFile, finalPath: finalPath,
		statePath: statePath, fileSize: fileSize,
		completed: completed, downBytes: downBytes,
	}, nil
}

func (dm *DiskManager) IsChunkCompleted(index uint32) bool {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	if int(index) < len(dm.completed) {
		return dm.completed[index]
	}
	return false
}

func (dm *DiskManager) GetDownloadedBytes() int64 { return dm.downBytes }

func (dm *DiskManager) WriteChunkAt(data []byte, offset int64, chunkIndex uint32) (int, error) {
	// 1. Lockless parallel write of data payload directly to disk!
	n, err := dm.file.WriteAt(data, offset)
	if err == nil {
		dm.mu.Lock()
		if int(chunkIndex) < len(dm.completed) {
			dm.completed[chunkIndex] = true
			dm.dirty = true
		}
		// Periodically flush metadata to disk (every 32 chunks or 2 seconds).
		// Minimizes mechanical drive head seek thrashing between payload file and .medxfer!
		if dm.dirty && (chunkIndex%32 == 0 || time.Since(dm.lastSync) >= 2*time.Second) {
			dm.syncStateLocked()
		}
		dm.mu.Unlock()
	}
	return n, err
}

func (dm *DiskManager) syncStateLocked() {
	if !dm.dirty || dm.stateFile == nil {
		return
	}
	buf := make([]byte, len(dm.completed))
	for i, c := range dm.completed {
		if c {
			buf[i] = 1
		}
	}
	_, _ = dm.stateFile.WriteAt(buf, 32)
	dm.dirty = false
	dm.lastSync = time.Now()
}

func (dm *DiskManager) Close() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if dm.dirty && dm.stateFile != nil {
		dm.syncStateLocked()
	}
	if dm.file != nil {
		_ = dm.file.Close()
		dm.file = nil
	}
	if dm.stateFile != nil {
		_ = dm.stateFile.Close()
		dm.stateFile = nil
	}
	return nil
}

func (dm *DiskManager) Finalize() error {
	dm.mu.Lock()
	f := dm.file
	sf := dm.stateFile
	sp := dm.statePath
	dm.file = nil
	dm.stateFile = nil
	dm.mu.Unlock()

	// Clean up metadata immediately (fast 32-byte unlink)
	if sf != nil {
		_ = sf.Close()
	}
	if sp != "" {
		_ = os.Remove(sp)
	}

	// Close payload file asynchronously so network batch pipeline doesn't block on OS buffer flush.
	// Redundant Truncate is intentionally avoided to prevent Windows NTFS zeroing and lock contention.
	go func(fileToClose *os.File) {
		if fileToClose != nil {
			_ = fileToClose.Close()
		}
	}(f)

	return nil
}

func (dm *DiskManager) Cleanup() {
	_ = dm.Close()
}

func OpenForReading(filePath string) (*DiskManager, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	return &DiskManager{file: file, finalPath: filePath, fileSize: info.Size()}, nil
}
func (dm *DiskManager) ReadChunkAt(b []byte, off int64) (n int, err error) {
	return dm.file.ReadAt(b, off)
}
func (dm *DiskManager) Size() int64 { return dm.fileSize }
