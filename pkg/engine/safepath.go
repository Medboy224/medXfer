package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	// ErrBadPath is returned for a peer-supplied relative path that is not safe to use on disk.
	ErrBadPath = errors.New("unsafe path")
	// ErrPathConflict is returned when a path component already exists with the wrong type
	// (a file where a folder is needed) or escapes the destination folder. Nothing is deleted.
	ErrPathConflict = errors.New("path conflict")
)

const (
	maxPathLen      = 1024 // LIM-06
	maxComponentLen = 255  // LIM-06
)

// SafeRelPath validates a relative path received from a peer and returns it in
// "/"-separated form. It rejects per COMPONENT, never per substring: "a..b.txt"
// and "mon fichier.pdf" are valid.
// Universal rules apply everywhere. Windows rules (reserved characters and names,
// trailing dot or space) apply only when THIS receiver runs on Windows (decision D0-1),
// so "rapport: final.txt" stays valid between Linux and Android devices.
func SafeRelPath(p string) (string, error) {
	return safeRelPath(p, runtime.GOOS == "windows")
}

func safeRelPath(p string, windowsRules bool) (string, error) {
	if p == "" || len(p) > maxPathLen || strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: %q", ErrBadPath, p)
	}
	p = strings.ReplaceAll(p, `\`, "/")
	if strings.HasPrefix(p, "/") || hasVolumePrefix(p) {
		return "", fmt.Errorf("%w: absolute path %q", ErrBadPath, p)
	}
	parts := strings.Split(p, "/")
	for _, c := range parts {
		switch {
		case c == "", c == ".", c == "..":
			return "", fmt.Errorf("%w: %q", ErrBadPath, p)
		case len(c) > maxComponentLen:
			return "", fmt.Errorf("%w: component too long in %q", ErrBadPath, p)
		case hasControlChar(c):
			return "", fmt.Errorf("%w: control character in %q", ErrBadPath, p)
		case windowsRules && strings.ContainsAny(c, `:*?"<>|`): // ':' also blocks NTFS alternate streams
			return "", fmt.Errorf("%w: reserved character in %q", ErrBadPath, p)
		case windowsRules && (strings.HasSuffix(c, ".") || strings.HasSuffix(c, " ")):
			return "", fmt.Errorf("%w: trailing dot or space in %q", ErrBadPath, p)
		case windowsRules && isWindowsReserved(c):
			return "", fmt.Errorf("%w: reserved name in %q", ErrBadPath, p)
		}
	}
	return strings.Join(parts, "/"), nil
}

// hasVolumePrefix reports a Windows drive prefix ("C:", "C:x"). It is rejected on every
// platform: such a path comes from a Windows absolute path, never from a relative one.
func hasVolumePrefix(p string) bool {
	return len(p) >= 2 && p[1] == ':' &&
		(p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z')
}

func hasControlChar(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 {
			return true
		}
	}
	return false
}

// isWindowsReserved reports device names (CON, PRN, AUX, NUL, COM1-9, LPT1-9),
// with or without extension: "con.txt" and "NUL.tar.gz" are reserved too.
func isWindowsReserved(c string) bool {
	base := c
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}

// ensureSubdirs creates relDir (a SafeRelPath result, "/"-separated) under root,
// walking ONLY the components below root. It never deletes anything (STO-09, STO-11):
// a component that exists with the wrong type yields ErrPathConflict. An existing
// component that is a link is accepted only if it resolves to a folder inside root.
// The check-then-create window (TOCTOU) is closed in phase 3 by os.Root (STO-13).
func ensureSubdirs(root, relDir string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if relDir == "" || relDir == "." {
		return nil
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	cur := root
	for _, part := range strings.Split(filepath.ToSlash(relDir), "/") {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		switch {
		case os.IsNotExist(err):
			if e := os.Mkdir(cur, 0o755); e != nil && !os.IsExist(e) {
				return e
			}
		case err != nil:
			return err
		case fi.Mode().IsRegular():
			return fmt.Errorf("%w: %q is a file", ErrPathConflict, part) // no deletion
		default:
			// Folder, symlink or other reparse point (Windows junction): resolve and confine.
			target, e := filepath.EvalSymlinks(cur)
			if e != nil || !within(realRoot, target) {
				return fmt.Errorf("%w: %q leads outside the destination", ErrPathConflict, part)
			}
			if ti, e := os.Stat(target); e != nil || !ti.IsDir() {
				return fmt.Errorf("%w: %q is not a folder", ErrPathConflict, part)
			}
		}
	}
	return nil
}

// within reports whether p is root itself or below it. Both must be resolved paths.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
