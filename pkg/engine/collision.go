package engine

import (
	"fmt"
	"path"
	"strings"
)

// SplitNameAndExt splits a relative path into (directory, baseName, extension),
// correctly handling multi-part extensions like ".tar.gz", ".tar.bz2", ".tar.xz".
func SplitNameAndExt(relPath string) (dir, base, ext string) {
	norm := strings.ReplaceAll(relPath, "\\", "/")
	dir = path.Dir(norm)
	if dir == "." {
		dir = ""
	}
	fileName := path.Base(norm)

	lower := strings.ToLower(fileName)
	multiExts := []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst"}
	for _, m := range multiExts {
		if strings.HasSuffix(lower, m) {
			base = fileName[:len(fileName)-len(m)]
			ext = fileName[len(fileName)-len(m):]
			return dir, base, ext
		}
	}

	ext = path.Ext(fileName)
	base = strings.TrimSuffix(fileName, ext)
	return dir, base, ext
}

// FormatCandidateName is the name of the count-th candidate: "name.ext", then
// "name (1).ext", "name (2).ext"...
func FormatCandidateName(dir, base string, count int, ext string) string {
	var name string
	if count == 0 {
		name = base + ext
	} else {
		name = fmt.Sprintf("%s (%d)%s", base, count, ext)
	}

	if dir != "" && dir != "." {
		return path.Join(dir, name)
	}
	return name
}
