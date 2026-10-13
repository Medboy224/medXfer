package engine

import (
	"fmt"
	"path/filepath"
	"syscall"
	"testing"
)

func TestFreeSpace(t *testing.T) {
	dir := t.TempDir()
	free, err := FreeSpace(dir)
	if err != nil || free <= 0 {
		t.Fatalf("FreeSpace(%s) = %d, %v", dir, free, err)
	}
	// A folder not made yet: the space of the disk it would be on.
	if later, err := FreeSpace(filepath.Join(dir, "not", "yet")); err != nil || later <= 0 {
		t.Fatalf("FreeSpace of a folder to make = %d, %v", later, err)
	}
}

func TestIsDiskFull(t *testing.T) {
	if !IsDiskFull(fmt.Errorf("write f.part: %w", syscall.ENOSPC)) {
		t.Fatal("ENOSPC is a full disk")
	}
	if IsDiskFull(nil) || IsDiskFull(fmt.Errorf("write f.part: %w", syscall.EACCES)) {
		t.Fatal("not a full disk")
	}
}
