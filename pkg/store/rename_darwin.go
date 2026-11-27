package store

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames from to to, in the same folder of root, unless a file is at to
// (ErrExists): renameatx_np with RENAME_EXCL, one call. errNoReplace where the file system
// does not support it.
var renameNoReplace = func(root *os.Root, from, to string) error {
	d, err := root.Open(filepath.Dir(to))
	if err != nil {
		return err
	}
	defer d.Close()
	fd := int(d.Fd())
	err = unix.RenameatxNp(fd, filepath.Base(from), fd, filepath.Base(to), unix.RENAME_EXCL)
	switch {
	case errors.Is(err, unix.EEXIST):
		return ErrExists
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOTSUP):
		return errNoReplace
	}
	return err
}
