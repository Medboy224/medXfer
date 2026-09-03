package engine

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// CollisionPolicy defines how the receiver handles existing files with the same name
type CollisionPolicy int

const (
	// PolicyAutoRename automatically creates "file (1).ext", "file (2).ext" if a different file exists
	PolicyAutoRename CollisionPolicy = iota
	// PolicyOverwrite replaces and overwrites the existing file
	PolicyOverwrite
	// PolicySkip skips the incoming transfer if any file with that name exists
	PolicySkip
)

// CollisionResult contains the decision made by the collision resolver
type CollisionResult struct {
	ResolvedName string // The final relative file path to use on disk
	IsDuplicate  bool   // True if the file is 100% identical and complete (Smart Skip)
	IsResume     bool   // True if a valid matching partial .medxfer file exists
	ResumeBytes  int64  // Byte offset to resume from (or full size if duplicate)
}

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

// formatCandidateName constructs a path with an incremented counter
func formatCandidateName(dir, base string, count int, ext string) string {
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

// ResolveCollision inspects the receiver disk and determines whether to Smart-Skip,
// Resume, Auto-Rename, or Overwrite.
func ResolveCollision(outputDir, fileName, fileID string, fileSize int64, chunkSize uint32, policy CollisionPolicy) (CollisionResult, error) {
	if chunkSize == 0 {
		chunkSize = 2 * 1024 * 1024
	}

	dir, base, ext := SplitNameAndExt(fileName)

	// Check if the original name or any incremented candidate matches
	maxAttempts := 1000
	if policy != PolicyAutoRename {
		maxAttempts = 1
	}

	for count := 0; count < maxAttempts; count++ {
		candidateRel := formatCandidateName(dir, base, count, ext)

		normCandidate := strings.ReplaceAll(candidateRel, "/", string(filepath.Separator))
		normCandidate = strings.ReplaceAll(normCandidate, "\\", string(filepath.Separator))

		targetPath := filepath.Join(outputDir, normCandidate)
		statePath := targetPath + ".medxfer"

		stateInfo, stateErr := os.Stat(statePath)
		targetInfo, targetErr := os.Stat(targetPath)

		// 1. Check for in-progress partial transfer (.medxfer exists)
		if stateErr == nil && !stateInfo.IsDir() {
			resumeBytes, _ := PeekResumeOffset(outputDir, candidateRel, fileID, fileSize, chunkSize)
			if resumeBytes > 0 {
				if fileSize > 0 && resumeBytes >= fileSize {
					// State file indicates 100% completed
					return CollisionResult{
						ResolvedName: candidateRel,
						IsDuplicate:  true,
						IsResume:     false,
						ResumeBytes:  fileSize,
					}, nil
				}
				// Valid matching partial file -> Resume
				return CollisionResult{
					ResolvedName: candidateRel,
					IsDuplicate:  false,
					IsResume:     true,
					ResumeBytes:  resumeBytes,
				}, nil
			}

			// Stale/mismatched .medxfer
			if policy == PolicyOverwrite {
				_ = os.Remove(statePath)
				_ = os.Remove(targetPath)
				return CollisionResult{
					ResolvedName: candidateRel,
					IsDuplicate:  false,
					IsResume:     false,
					ResumeBytes:  0,
				}, nil
			} else if policy == PolicySkip {
				return CollisionResult{
					ResolvedName: candidateRel,
					IsDuplicate:  true,
					ResumeBytes:  0,
				}, nil
			}
			// For PolicyAutoRename with mismatched partial, check next candidate
			continue
		}

		// 2. Check for completed file on disk (target exists, no .medxfer)
		if targetErr == nil && !targetInfo.IsDir() {
			// Check if file on disk is an EXACT cryptographic match (Smart Skip)
			if targetInfo.Size() == fileSize {
				localFileID := GenerateFileID(targetPath)
				if localFileID != "" && fileID != "" && strings.TrimSpace(localFileID) == strings.TrimSpace(fileID) {
					// 100% Identical File Already Exists -> Zero-Byte Smart Skip
					return CollisionResult{
						ResolvedName: candidateRel,
						IsDuplicate:  true,
						IsResume:     false,
						ResumeBytes:  fileSize,
					}, nil
				}
			}

			// File exists but has DIFFERENT content
			if policy == PolicyOverwrite {
				return CollisionResult{
					ResolvedName: candidateRel,
					IsDuplicate:  false,
					IsResume:     false,
					ResumeBytes:  0,
				}, nil
			} else if policy == PolicySkip {
				return CollisionResult{
					ResolvedName: candidateRel,
					IsDuplicate:  true,
					ResumeBytes:  0,
				}, nil
			}

			// For PolicyAutoRename, continue loop to find next available "file (N).ext"
			continue
		}

		// 3. Neither target nor state file exists -> Safe to use this candidate name
		return CollisionResult{
			ResolvedName: candidateRel,
			IsDuplicate:  false,
			IsResume:     false,
			ResumeBytes:  0,
		}, nil
	}

	// Fallback if max attempts exceeded
	fallback := formatCandidateName(dir, base, 1, ext)
	return CollisionResult{ResolvedName: fallback}, nil
}
