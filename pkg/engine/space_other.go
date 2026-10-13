//go:build !(linux || darwin || freebsd || windows)

package engine

import "errors"

func freeSpace(string) (int64, error) { return 0, errors.ErrUnsupported }

func isDiskFullOS(error) bool { return false }
