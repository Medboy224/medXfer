//go:build !diag

package engine

import "net"

func wrapConn(_ ConnRole, c net.Conn) net.Conn { return c }

func wrapStorage(f StorageFile, _ string) StorageFile { return f }

// Chunk buffer accounting exists only in diag builds (issue #26).
func countBufferGet() {}
func countBufferPut() {}
