package engine

import (
	"io"
	"net"
)

// Fault-injection points (DEV-10). In a production build they are identity functions
// (hooks_prod.go); a build with the "diag" tag lets tests install wrappers from
// pkg/diag/faultconn and pkg/diag/faultfs (hooks_diag.go). The engine never imports diag.

// ConnRole tells a connection wrapper which side opened the connection.
type ConnRole int

const (
	ConnDialed   ConnRole = iota // opened by a Receiver (handshake and data workers)
	ConnAccepted                 // accepted by a Sender
)

// StorageFile is what pkg/store needs of a data or journal file; *os.File has it.
type StorageFile interface {
	io.ReaderAt
	io.WriterAt
	Sync() error
	Truncate(int64) error
	Close() error
}

// WrapStorage prepares a file opened by pkg/store: in a diag build, the fault wrapper a test
// installed (pkg/diag/faultfs); otherwise f itself.
func WrapStorage(f StorageFile, path string) StorageFile { return wrapStorage(f, path) }

// WrapAccepted prepares a connection accepted outside the engine (pkg/transfer.Hub) the way
// the engine prepares its own: socket tuning on the raw TCP connection, then the fault
// wrapper of diag builds, both before the secure channel, so that faults hit network bytes.
func WrapAccepted(c net.Conn) net.Conn {
	TuneConn(c)
	return wrapConn(ConnAccepted, c)
}

// WrapDialed prepares a connection opened outside the engine (pkg/transfer receivers) the way
// the engine prepares its own: socket tuning, then the fault wrapper of diag builds.
func WrapDialed(c net.Conn) net.Conn {
	TuneConn(c)
	return wrapConn(ConnDialed, c)
}
