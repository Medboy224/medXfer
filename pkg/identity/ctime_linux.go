//go:build linux || android

package identity

import (
	"os"
	"syscall"
	"time"
)

// changeTime is when fi's file last changed state (ctime): set by the kernel on a creation, a
// rename or a chtimes, never put back. An inode number may be reused at once: it may not.
func changeTime(fi os.FileInfo) time.Time {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec))
	}
	return time.Time{}
}
