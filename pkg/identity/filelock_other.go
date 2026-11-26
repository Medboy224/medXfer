//go:build !(linux || darwin || android || freebsd || netbsd || openbsd || dragonfly || windows)

package identity

import "os"

// lockFile and unlockFile do nothing here: one process at a time changes trusted.json.
func lockFile(*os.File) error { return nil }

func unlockFile(*os.File) error { return nil }
