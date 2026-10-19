//go:build windows

package api

import (
	"os"
	"syscall"
	"testing"
)

// openFD opens path and returns a handle of its own, as an Android picker hands a descriptor
// over.
func openFD(t *testing.T, path string) int64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, _ := syscall.GetCurrentProcess()
	var h syscall.Handle
	if err := syscall.DuplicateHandle(p, syscall.Handle(f.Fd()), p, &h, 0, false, syscall.DUPLICATE_SAME_ACCESS); err != nil {
		t.Fatal(err)
	}
	return int64(h)
}
