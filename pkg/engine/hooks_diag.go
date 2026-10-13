//go:build diag

package engine

import (
	"net"
	"sync/atomic"
)

// ConnWrapper and StorageWrapper replace a connection or a storage file with a faulty one.
type (
	ConnWrapper    func(role ConnRole, c net.Conn) net.Conn
	StorageWrapper func(f StorageFile, path string) StorageFile
	FreeSpaceFunc  func(dir string) (int64, error)
)

var (
	connWrapper    atomic.Pointer[ConnWrapper]
	storageWrapper atomic.Pointer[StorageWrapper]
	freeSpaceFunc  atomic.Pointer[FreeSpaceFunc]
)

// SetConnWrapper installs w for every connection opened from now on, process-wide, and
// returns a function that restores the previous wrapper. Tests that use it must not run
// in parallel with other transfer tests.
func SetConnWrapper(w ConnWrapper) (restore func()) {
	prev := connWrapper.Swap(&w)
	return func() { connWrapper.Store(prev) }
}

// SetStorageWrapper installs w for every data and state file opened by DiskManager from
// now on, process-wide, and returns a function that restores the previous wrapper.
func SetStorageWrapper(w StorageWrapper) (restore func()) {
	prev := storageWrapper.Swap(&w)
	return func() { storageWrapper.Store(prev) }
}

// SetFreeSpace makes FreeSpace answer with f, process-wide: a disk nearly full without
// filling one. It returns a function that restores the real answer.
func SetFreeSpace(f FreeSpaceFunc) (restore func()) {
	prev := freeSpaceFunc.Swap(&f)
	return func() { freeSpaceFunc.Store(prev) }
}

func wrapConn(role ConnRole, c net.Conn) net.Conn {
	if w := connWrapper.Load(); w != nil && *w != nil {
		return (*w)(role, c)
	}
	return c
}

func wrapStorage(f StorageFile, path string) StorageFile {
	if w := storageWrapper.Load(); w != nil && *w != nil {
		return (*w)(f, path)
	}
	return f
}

func wrapFreeSpace(dir string, free int64, err error) (int64, error) {
	if f := freeSpaceFunc.Load(); f != nil && *f != nil {
		return (*f)(dir)
	}
	return free, err
}
