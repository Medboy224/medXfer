//go:build linux || darwin || android || freebsd || netbsd || openbsd || dragonfly

package identity

import (
	"os"
	"syscall"
)

// lockFile takes f's exclusive lock, waiting for another process to release it. The end of the
// process releases it too.
func lockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }

func unlockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
