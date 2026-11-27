package store

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames from to to, in the same folder of root, unless a file is at to
// (ErrExists): renameat2 with RENAME_NOREPLACE, one call. errNoReplace where the file system
// does not support it.
var renameNoReplace = func(root *os.Root, from, to string) error {
	d, err := root.Open(filepath.Dir(to))
	if err != nil {
		return err
	}
	defer d.Close()
	fd := int(d.Fd())
	err = unix.Renameat2(fd, filepath.Base(from), fd, filepath.Base(to), unix.RENAME_NOREPLACE)
	switch {
	case errors.Is(err, unix.EEXIST):
		return ErrExists
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOSYS):
		return errNoReplace
	}
	return err
}
