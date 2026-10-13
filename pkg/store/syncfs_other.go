//go:build !linux

package store

import "os"

// canSyncFS: no syncfs here; each file is finalized alone (Windows: the cost of a small file
// is its creation, not its fsync, bench-results/README.md).
var canSyncFS = false

// syncFS is not available: Batch.Flush syncs each file alone. Tests replace it.
var syncFS = func(*os.Root) error { return errNoSyncFS }
