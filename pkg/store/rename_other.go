//go:build !linux && !darwin && !windows

package store

import "os"

// renameNoReplace: not here; rename checks, then renames.
var renameNoReplace = func(*os.Root, string, string) error { return errNoReplace }
