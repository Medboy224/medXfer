//go:build linux || darwin || freebsd

package engine

import (
	"errors"
	"syscall"
)

func freeSpace(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

func isDiskFullOS(err error) bool { return errors.Is(err, syscall.EDQUOT) }
