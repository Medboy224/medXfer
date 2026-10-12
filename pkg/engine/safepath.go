package engine

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
)

var (
	// ErrBadPath is returned for a peer-supplied relative path that is not safe to use on disk.
	ErrBadPath = errors.New("unsafe path")
)

const (
	maxPathLen      = 1024 // LIM-06
	maxComponentLen = 255  // LIM-06
)

// SafeRelPath validates a relative path received from a peer and returns it in
// "/"-separated form. It rejects per COMPONENT, never per substring: "a..b.txt"
// and "mon fichier.pdf" are valid.
// Universal rules apply everywhere. Windows rules (reserved characters and names, trailing
// dot or space) apply only when THIS receiver runs on Windows (decision D0-1), so
// "rapport: final.txt" and "c:notes.txt" stay valid between Linux and Android devices. On
// Windows, ':' is rejected in every component, which also blocks drive prefixes ("C:x").
func SafeRelPath(p string) (string, error) {
	return safeRelPath(p, runtime.GOOS == "windows")
}

func safeRelPath(p string, windowsRules bool) (string, error) {
	if p == "" || len(p) > maxPathLen || strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: %q", ErrBadPath, p)
	}
	p = strings.ReplaceAll(p, `\`, "/")
	if strings.HasPrefix(p, "/") {
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
