package manifest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Medboy224/medXfer/pkg/engine"
)

// Item represents a single file entry in the batch manifest
type Item struct {
	RelPath  string `json:"rel_path"` // Normalized relative path using forward slashes (e.g., "docs/notes.txt")
	Size     int64  `json:"size"`     // Size in bytes
	MTime    int64  `json:"mtime"`    // modification time, unix nanoseconds: a changed source is detected (XFER-07)
	FullPath string `json:"-"`        // Local absolute source path (never serialized to network)
}

// Manifest represents a complete directory or batch of files to transfer
type Manifest struct {
	BatchID    string `json:"batch_id"`
	RootName   string `json:"root_name"`
	TotalFiles int    `json:"total_files"`
	TotalBytes int64  `json:"total_bytes"`
	Items      []Item `json:"items"`
}

// Build creates a Manifest from one or more file or directory paths
func Build(paths []string) (*Manifest, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("no paths provided")
	}

	var items []Item
	var totalBytes int64

	rootName := fmt.Sprintf("batch_%d_files", len(paths))
	used := map[string]bool{} // top-level names already given, lower case
	for _, p := range paths {
		if p == "" { // Clean would make it ".": the whole current folder
			return nil, fmt.Errorf("an empty path designates no file")
		}
		cleanPath := filepath.Clean(p)
		info, err := os.Stat(cleanPath)
		if err != nil {
			return nil, fmt.Errorf("failed to access '%s': %w", p, err)
		}
		top, err := topName(cleanPath)
		if err != nil {
			return nil, fmt.Errorf("failed to access '%s': %w", p, err)
		}
		if len(paths) == 1 {
			rootName = top
		}
		name := uniqueTop(top, info.IsDir(), used)

		if info.IsDir() {
			err = filepath.WalkDir(cleanPath, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				// Skip special files (sockets, pipes, etc.)
				if !d.Type().IsRegular() {
					return nil
				}

				fileInfo, err := d.Info()
				if err != nil {
					return err
				}

				// Relative to the folder itself: "." and ".." are not in the path walked (#300).
				relPath, err := filepath.Rel(cleanPath, path)
				if err != nil {
					return err
				}

				normRelPath := name + "/" + filepath.ToSlash(relPath)

				items = append(items, Item{
					RelPath:  normRelPath,
					Size:     fileInfo.Size(),
					MTime:    fileInfo.ModTime().UnixNano(),
					FullPath: path,
				})
				totalBytes += fileInfo.Size()
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("failed scanning directory '%s': %w", p, err)
			}
		} else if info.Mode().IsRegular() {
			normRelPath := name

			items = append(items, Item{
				RelPath:  normRelPath,
				Size:     info.Size(),
				MTime:    info.ModTime().UnixNano(),
				FullPath: cleanPath,
			})
			totalBytes += info.Size()
		}
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no transferable files found in given paths")
	}

	batchIDBytes := make([]byte, 8)
	_, _ = rand.Read(batchIDBytes)
	batchID := hex.EncodeToString(batchIDBytes)

	return &Manifest{
		BatchID:    batchID,
		RootName:   rootName,
		TotalFiles: len(items),
		TotalBytes: totalBytes,
		Items:      items,
	}, nil
}

// topName is the name a cleaned path goes by in the offer: its last element, or, for "." and
// "..", the name of the folder they designate (#300).
func topName(cleanPath string) (string, error) {
	top := filepath.Base(cleanPath)
	if top != "." && top != ".." {
		return top, nil
	}
	abs, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", err
	}
	return filepath.Base(abs), nil
}

// uniqueTop is the name the path top gets in the offer: top itself, or, when an earlier path
// already has this name (letter case aside), "top (1)", "top (2)"... as the receiver names a
// collision (audit §3.6, #156). A file keeps its extension last: "doc (1).pdf".
func uniqueTop(top string, dir bool, used map[string]bool) string {
	base, ext := top, ""
	if !dir {
		_, base, ext = engine.SplitNameAndExt(top)
	}
	for n := 0; ; n++ {
		name := engine.FormatCandidateName("", base, n, ext)
		if key := strings.ToLower(name); !used[key] {
			used[key] = true
			return name
		}
	}
}

// SummaryString returns a readable summary of the manifest
func (m *Manifest) SummaryString() string {
	if m == nil {
		return "Empty Manifest"
	}
	sizeMB := float64(m.TotalBytes) / (1024 * 1024)
	if sizeMB < 1024 {
		return fmt.Sprintf("'%s' (%d files, %.2f MB)", m.RootName, m.TotalFiles, sizeMB)
	}
	sizeGB := sizeMB / 1024
	return fmt.Sprintf("'%s' (%d files, %.2f GB)", m.RootName, m.TotalFiles, sizeGB)
}
