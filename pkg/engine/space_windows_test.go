package engine

import (
	"io/fs"
	"testing"
)

// Windows says "disk full" with its own codes, not ENOSPC.
func TestIsDiskFullWindows(t *testing.T) {
	for _, err := range []error{errorDiskFull, errorHandleDiskFull} {
		if !IsDiskFull(&fs.PathError{Op: "write", Path: "f.part", Err: err}) {
			t.Fatalf("%v is a full disk", err)
		}
	}
}
