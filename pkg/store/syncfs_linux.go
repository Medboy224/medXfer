package store

import (
	"os"
	"runtime"
	"syscall"
)

// sysSyncfs is the number of syncfs(2) on this architecture; Go's syscall package does not
// define it everywhere. 0: unknown here, each file is finalized alone.
var sysSyncfs = map[string]uintptr{
	"amd64": 306, "arm64": 267, "386": 344, "arm": 373, "riscv64": 267, "loong64": 267,
}[runtime.GOARCH]

// canSyncFS: Linux and Android make a whole file system durable in one call.
var canSyncFS = sysSyncfs != 0

// fuseMagic is FUSE_SUPER_MAGIC, the type statfs gives a FUSE file system: Android's shared
// storage (/storage/emulated) is one.
const fuseMagic = 0x65735546

// onFUSE reports whether root is on FUSE, where syncfs makes nothing durable: it fails
// (ENOENT, Android 16) or succeeds without writing anything (Android 12). Tests replace it.
var onFUSE = func(root *os.Root) bool {
	d, err := root.Open(".")
	if err != nil {
		return false
	}
	defer d.Close()
	var st syscall.Statfs_t
	if err := syscall.Fstatfs(int(d.Fd()), &st); err != nil {
		return false
	}
	return int64(st.Type) == fuseMagic
}

// syncFS makes everything written to the file system of root durable: syncfs(2). Tests
// replace it.
var syncFS = func(root *os.Root) error {
	d, err := root.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	if _, _, e := syscall.Syscall(sysSyncfs, d.Fd(), 0, 0); e != 0 {
		return e
	}
	return nil
}
