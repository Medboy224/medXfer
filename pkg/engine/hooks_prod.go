//go:build !diag

package engine

import "net"

func wrapConn(_ ConnRole, c net.Conn) net.Conn { return c }

func wrapStorage(f StorageFile, _ string) StorageFile { return f }

func wrapFreeSpace(_ string, free int64, err error) (int64, error) { return free, err }
