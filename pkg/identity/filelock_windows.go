package identity

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes f's exclusive lock, waiting for another process to release it. The end of the
// process releases it too, but closing the file may release it late: unlockFile does at once.
func lockFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, new(windows.Overlapped))
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
