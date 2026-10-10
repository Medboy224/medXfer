package engine

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"
)

// Group commit thresholds (STO-04). Parameters, not protocol constants (rule R4).
const (
	defaultCommitBytes    = 64 << 20
	defaultCommitInterval = 2 * time.Second
)

// fileHandle is the subset of *os.File used by DiskManager; tests substitute a fake to simulate power loss.
type fileHandle interface {
	io.ReaderAt
	io.WriterAt
	Sync() error
	Close() error
}

type DiskManager struct {
	file      fileHandle
	stateFile fileHandle
	finalPath string
	statePath string
	fileSize  int64
	mu        sync.RWMutex
	completed []bool // written to the data file (in memory, used by the receiver)
	durable   []bool // written AND synced: the only chunks the state file may declare (invariant J)
	downBytes int64

	pending        []uint32 // completed but not yet durable
	pendingBytes   int64
	lastCommit     time.Time
	commitBytes    int64
	commitInterval time.Duration
	now            func() time.Time

	commitMu   sync.Mutex     // serializes flushes: state writes stay in order
	committing bool           // a background commit is running (guarded by mu)
	closing    bool           // Close/Finalize started: no new background commit (guarded by mu)
	commitErr  error          // first background commit failure (guarded by mu)
	commitWG   sync.WaitGroup // background commits in flight
}

func newDiskManager(file, stateFile fileHandle, finalPath, statePath string, fileSize int64, completed []bool, downBytes int64) *DiskManager {
	durable := make([]bool, len(completed))
	copy(durable, completed) // chunks loaded from the state file were committed by a previous run
	return &DiskManager{
		file: file, stateFile: stateFile, finalPath: finalPath,
		statePath: statePath, fileSize: fileSize,
		completed: completed, durable: durable, downBytes: downBytes,
		lastCommit: time.Now(), commitBytes: defaultCommitBytes,
		commitInterval: defaultCommitInterval, now: time.Now,
	}
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

	safeName, err := SafeRelPath(fileName)
	if err != nil {
		return 0, nil // unsafe peer path: treat as absent, never read outside outputDir
	}
	statePath := filepath.Join(outputDir, filepath.FromSlash(safeName)+".medxfer")

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

func CreateAndPreallocate(outputDir, fileName string, fileSize int64, chunkSize uint32, fileID string) (*DiskManager, error) {
	safeName, err := SafeRelPath(fileName)
	if err != nil {
		return nil, err
	}

	finalPath := filepath.Join(outputDir, filepath.FromSlash(safeName))
	if err := ensureSubdirs(outputDir, path.Dir(safeName)); err != nil {
		return nil, fmt.Errorf("failed to create directory for '%s': %w", safeName, err)
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
						// Safe to delete (STO-09): the name was chosen by ResolveCollision, which only
						// returns a name with a mismatched .medxfer under the explicit Overwrite policy.
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

	return newDiskManager(wrapStorage(file, finalPath), wrapStorage(stateFile, statePath),
		finalPath, statePath, fileSize, completed, downBytes), nil
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
	if err != nil {
		return n, err
	}
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if dm.commitErr != nil {
		return n, fmt.Errorf("commit progress: %w", dm.commitErr)
	}
	if int(chunkIndex) < len(dm.completed) && !dm.completed[chunkIndex] {
		dm.completed[chunkIndex] = true
		dm.pending = append(dm.pending, chunkIndex)
		dm.pendingBytes += int64(n)
	}
	// Group commit (STO-04): one fsync per 64 MiB or 2 s, run in the background so the
	// writer keeps writing while the disk flushes. At most one commit runs at a time.
	if len(dm.pending) > 0 && !dm.committing && !dm.closing &&
		(dm.pendingBytes >= dm.commitBytes || dm.now().Sub(dm.lastCommit) >= dm.commitInterval) {
		batch := dm.pending
		dm.pending, dm.pendingBytes, dm.lastCommit = nil, 0, dm.now()
		dm.committing = true
		dm.commitWG.Add(1)
		go func() {
			defer dm.commitWG.Done()
			err := dm.flush(batch)
			dm.mu.Lock()
			dm.committing = false
			if err != nil && dm.commitErr == nil {
				dm.commitErr = err
			}
			dm.mu.Unlock()
		}()
	}
	return n, nil
}

// flush enforces invariant J (STO-03) for one batch: a chunk appears in the state file
// ONLY if its bytes are durable. Order: (1) sync data, (2) write state, (3) sync state.
// Every chunk of the batch had its WriteAt return before it was queued, so the data sync
// covers it; chunks written during the sync are not in the batch and wait for the next one.
func (dm *DiskManager) flush(batch []uint32) error {
	if len(batch) == 0 {
		return nil
	}
	dm.commitMu.Lock()
	defer dm.commitMu.Unlock()
	if err := dm.file.Sync(); err != nil {
		return err
	}
	dm.mu.Lock()
	for _, idx := range batch {
		dm.durable[idx] = true
	}
	buf := make([]byte, len(dm.durable))
	for i, d := range dm.durable {
		if d {
			buf[i] = 1
		}
	}
	dm.mu.Unlock()
	if _, err := dm.stateFile.WriteAt(buf, 32); err != nil {
		return err
	}
	return dm.stateFile.Sync()
}

// drain stops background commits and returns the progress not yet committed.
func (dm *DiskManager) drain() ([]uint32, error) {
	dm.mu.Lock()
	dm.closing = true
	dm.mu.Unlock()
	dm.commitWG.Wait()
	dm.mu.Lock()
	defer dm.mu.Unlock()
	batch := dm.pending
	dm.pending, dm.pendingBytes = nil, 0
	return batch, dm.commitErr
}

// Close commits pending progress (pause, cancel, error) then closes both files.
func (dm *DiskManager) Close() error {
	batch, err := dm.drain()
	dm.mu.Lock()
	open := dm.file != nil && dm.stateFile != nil
	dm.mu.Unlock()
	if open && err == nil {
		err = dm.flush(batch)
	}
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
	return err
}

// Finalize makes the data durable BEFORE removing the state file. If the sync fails,
// the state is kept so the transfer stays resumable, and the error is returned.
func (dm *DiskManager) Finalize() error {
	batch, _ := dm.drain()
	dm.mu.Lock()
	f := dm.file
	if f != nil {
		if err := f.Sync(); err != nil {
			dm.pending = append(batch, dm.pending...) // let Close retry the commit
			dm.mu.Unlock()
			return fmt.Errorf("finalize %s: %w", filepath.Base(dm.finalPath), err)
		}
	}
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

	// Close synchronously: the data is already synced, so Close is cheap, and the file must be
	// released before the transfer is reported complete. On Windows an open file cannot be
	// moved or deleted (#32). Redundant Truncate is intentionally avoided to prevent Windows
	// NTFS zeroing and lock contention.
	if f != nil {
		_ = f.Close()
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
