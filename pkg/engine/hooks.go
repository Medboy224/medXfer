package engine

// Fault-injection points (DEV-10). In a production build they are identity functions
// (hooks_prod.go); a build with the "diag" tag lets tests install wrappers from
// pkg/diag/faultconn and pkg/diag/faultfs (hooks_diag.go). The engine never imports diag.

// ConnRole tells a connection wrapper which side opened the connection.
type ConnRole int

const (
	ConnDialed   ConnRole = iota // opened by a Receiver (handshake and data workers)
	ConnAccepted                 // accepted by a Sender
)

// StorageFile is the subset of *os.File that DiskManager uses for the data and state files.
type StorageFile = fileHandle
