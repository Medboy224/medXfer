package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// renameNoReplace renames from to to, in root, unless a file is at to (ErrExists): MoveFileEx
// without MOVEFILE_REPLACE_EXISTING, one call. It takes paths: the root's own, then the names
// of the transfer, already checked (SafeRelPath).
var renameNoReplace = func(root *os.Root, from, to string) error {
	src, err := windows.UTF16PtrFromString(longPath(filepath.Join(root.Name(), from)))
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(longPath(filepath.Join(root.Name(), to)))
	if err != nil {
		return err
	}
	err = windows.MoveFileEx(src, dst, 0)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) {
		return ErrExists
	}
	return err
}

// longPath lets MoveFileEx take an absolute path longer than MAX_PATH, as os does.
func longPath(p string) string {
	if len(p) < 248 || !filepath.IsAbs(p) || strings.HasPrefix(p, `\\`) {
		return p
	}
	return `\\?\` + p
}
