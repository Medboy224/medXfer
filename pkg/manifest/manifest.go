package manifest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Medboy224/medXfer/pkg/engine"
)

// Item represents a single file entry in the batch manifest
type Item struct {
	RelPath  string `json:"rel_path"` // Normalized relative path using forward slashes (e.g., "docs/notes.txt")
	Size     int64  `json:"size"`     // Size in bytes
	FileID   string `json:"file_id"`  // Unique content hash
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

	rootName := ""
	if len(paths) == 1 {
		clean := filepath.Clean(paths[0])
		rootName = filepath.Base(clean)
	} else {
		rootName = fmt.Sprintf("batch_%d_files", len(paths))
	}

	for _, p := range paths {
		cleanPath := filepath.Clean(p)
		info, err := os.Stat(cleanPath)
		if err != nil {
			return nil, fmt.Errorf("failed to access '%s': %w", p, err)
		}

		if info.IsDir() {
			baseDir := filepath.Dir(cleanPath)
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

				relPath, err := filepath.Rel(baseDir, path)
				if err != nil {
					return err
				}

				normRelPath := filepath.ToSlash(relPath)
				fileID := engine.GenerateFileID(path)

				items = append(items, Item{
					RelPath:  normRelPath,
					Size:     fileInfo.Size(),
					FileID:   fileID,
					FullPath: path,
				})
				totalBytes += fileInfo.Size()
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("failed scanning directory '%s': %w", p, err)
			}
		} else if info.Mode().IsRegular() {
			normRelPath := filepath.ToSlash(filepath.Base(cleanPath))
			fileID := engine.GenerateFileID(cleanPath)

			items = append(items, Item{
				RelPath:  normRelPath,
				Size:     info.Size(),
				FileID:   fileID,
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
