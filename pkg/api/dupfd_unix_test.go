//go:build !windows

package api

import (
	"os"
	"syscall"
	"testing"
)

// openFD opens path and returns a descriptor of its own, as an Android picker hands one over.
func openFD(t *testing.T, path string) int64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	return int64(fd)
}
