//go:build !(linux || android || darwin || freebsd || netbsd)

package identity

import (
	"os"
	"time"
)

// changeTime is zero here: on Windows the file's identity (os.SameFile) tells a new file, as
// NTFS does not reuse a file id at once.
func changeTime(os.FileInfo) time.Time { return time.Time{} }
