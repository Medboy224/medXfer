package engine

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type DiskManager struct {
	file      *os.File
	stateFile *os.File
	finalPath string
	statePath string
	fileSize  int64
	mu        sync.Mutex
	completed []bool
	downBytes int64
}

// GenerateFileID creates an unforgeable hash using Name, Size, and the first 4KB of data
func GenerateFileID(filePath string) string {
	info, err := os.Stat(filePath)
	if err != nil {
		return ""
	}
	f, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer f.Close()

	buf := make([]byte, 4096)
	n, _ := f.Read(buf)

	h := md5.New()
	_, _ = fmt.Fprintf(h, "%s-%d-", info.Name(), info.Size())
	h.Write(buf[:n])
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
		if fileSize > 0 {
			file.Truncate(fileSize)
		}

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
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if int(index) < len(dm.completed) {
		return dm.completed[index]
	}
	return false
}

func (dm *DiskManager) GetDownloadedBytes() int64 { return dm.downBytes }

func (dm *DiskManager) WriteChunkAt(data []byte, offset int64, chunkIndex uint32) (int, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	n, err := dm.file.WriteAt(data, offset)
	if err == nil {
		dm.completed[chunkIndex] = true
		dm.stateFile.WriteAt([]byte{1}, int64(32+chunkIndex))
	}
	return n, err
}

func (dm *DiskManager) Close() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()
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
	defer dm.mu.Unlock()
	if dm.file != nil {
		_ = dm.file.Close()
		dm.file = nil
	}
	if dm.stateFile != nil {
		_ = dm.stateFile.Close()
		dm.stateFile = nil
		_ = os.Remove(dm.statePath)
	}
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
