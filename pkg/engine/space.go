package engine

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// FreeSpace is the number of bytes this user may still write on the file system that holds
// dir (STO-07, bible §9.7). A folder not made yet is looked up through its nearest parent. In
// a diag build, a test may give another answer (SetFreeSpace).
func FreeSpace(dir string) (int64, error) {
	for {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	free, err := freeSpace(dir)
	return wrapFreeSpace(dir, free, err)
}

// IsDiskFull reports whether err says the disk is full: ENOSPC (or a quota reached), or
// ERROR_DISK_FULL and ERROR_HANDLE_DISK_FULL on Windows.
func IsDiskFull(err error) bool {
	return err != nil && (errors.Is(err, syscall.ENOSPC) || isDiskFullOS(err))
}
