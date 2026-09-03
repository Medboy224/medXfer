package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSplitNameAndExt(t *testing.T) {
	cases := []struct {
		input   string
		wantDir string
		wantBas string
		wantExt string
	}{
		{"video.mp4", "", "video", ".mp4"},
		{"archive.tar.gz", "", "archive", ".tar.gz"},
		{"folder/sub/data.tar.bz2", "folder/sub", "data", ".tar.bz2"},
		{"folder\\sub\\doc.pdf", "folder/sub", "doc", ".pdf"},
		{"Dockerfile", "", "Dockerfile", ""},
		{".gitignore", "", "", ".gitignore"},
		{"folder/notes.txt", "folder", "notes", ".txt"},
	}

	for _, c := range cases {
		d, b, e := SplitNameAndExt(c.input)
		if d != c.wantDir || b != c.wantBas || e != c.wantExt {
			t.Errorf("SplitNameAndExt(%q) = (%q, %q, %q); want (%q, %q, %q)",
				c.input, d, b, e, c.wantDir, c.wantBas, c.wantExt)
		}
	}
}

func TestCollisionSmartSkip(t *testing.T) {
	tmpDir := t.TempDir()
	fileName := "report.pdf"
	filePath := filepath.Join(tmpDir, fileName)

	content := []byte("Original PDF Document Content 12345")
	_ = os.WriteFile(filePath, content, 0644)
	fileID := GenerateFileID(filePath)

	res, err := ResolveCollision(tmpDir, fileName, fileID, int64(len(content)), 64*1024, PolicyAutoRename)
	if err != nil {
		t.Fatalf("ResolveCollision failed: %v", err)
	}

	if !res.IsDuplicate {
		t.Fatalf("Expected IsDuplicate = true for identical file, got false")
	}
	if res.ResolvedName != fileName {
		t.Fatalf("Expected ResolvedName = %q, got %q", fileName, res.ResolvedName)
	}
	if res.ResumeBytes != int64(len(content)) {
		t.Fatalf("Expected ResumeBytes = %d, got %d", len(content), res.ResumeBytes)
	}
}

func TestCollisionAutoRename(t *testing.T) {
	tmpDir := t.TempDir()
	fileName := "video.mp4"

	// 1. Create an existing video.mp4 with DIFFERENT content
	existingPath := filepath.Join(tmpDir, fileName)
	_ = os.WriteFile(existingPath, []byte("Existing Video"), 0644)

	// 2. Incoming transfer has different content & FileID
	incomingFile := filepath.Join(tmpDir, "incoming.mp4")
	incomingContent := []byte("Incoming New Video With Different Data 98765")
	_ = os.WriteFile(incomingFile, incomingContent, 0644)
	incomingID := GenerateFileID(incomingFile)

	res, err := ResolveCollision(tmpDir, fileName, incomingID, int64(len(incomingContent)), 64*1024, PolicyAutoRename)
	if err != nil {
		t.Fatalf("ResolveCollision failed: %v", err)
	}

	if res.IsDuplicate {
		t.Fatalf("Expected IsDuplicate = false for different file")
	}
	if res.ResolvedName != "video (1).mp4" {
		t.Fatalf("Expected ResolvedName = 'video (1).mp4', got %q", res.ResolvedName)
	}

	// 3. Now also create video (1).mp4 on disk with different content
	_ = os.WriteFile(filepath.Join(tmpDir, "video (1).mp4"), []byte("Second Video"), 0644)

	res2, err := ResolveCollision(tmpDir, fileName, incomingID, int64(len(incomingContent)), 64*1024, PolicyAutoRename)
	if err != nil {
		t.Fatalf("ResolveCollision failed: %v", err)
	}
	if res2.ResolvedName != "video (2).mp4" {
		t.Fatalf("Expected ResolvedName = 'video (2).mp4', got %q", res2.ResolvedName)
	}
}

func TestCollisionCompoundExtensionRename(t *testing.T) {
	tmpDir := t.TempDir()
	fileName := "backup.tar.gz"

	existingPath := filepath.Join(tmpDir, fileName)
	_ = os.WriteFile(existingPath, []byte("Old Backup"), 0644)

	res, err := ResolveCollision(tmpDir, fileName, "fake-file-id-12345678901234567", 100, 64*1024, PolicyAutoRename)
	if err != nil {
		t.Fatalf("ResolveCollision failed: %v", err)
	}

	if res.ResolvedName != "backup (1).tar.gz" {
		t.Fatalf("Expected ResolvedName = 'backup (1).tar.gz', got %q", res.ResolvedName)
	}
}

func TestCollisionNestedDirectoryAutoRename(t *testing.T) {
	tmpDir := t.TempDir()
	fileName := "assets/icons/logo.png"

	_ = os.MkdirAll(filepath.Join(tmpDir, "assets", "icons"), 0755)
	existingPath := filepath.Join(tmpDir, "assets", "icons", "logo.png")
	_ = os.WriteFile(existingPath, []byte("Old Logo"), 0644)

	res, err := ResolveCollision(tmpDir, fileName, "different-file-id-abcdef123456", 50, 64*1024, PolicyAutoRename)
	if err != nil {
		t.Fatalf("ResolveCollision failed: %v", err)
	}

	if res.ResolvedName != "assets/icons/logo (1).png" {
		t.Fatalf("Expected ResolvedName = 'assets/icons/logo (1).png', got %q", res.ResolvedName)
	}
}

func TestCollisionPolicyOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	fileName := "config.json"

	existingPath := filepath.Join(tmpDir, fileName)
	_ = os.WriteFile(existingPath, []byte("{\"version\": 1}"), 0644)

	res, err := ResolveCollision(tmpDir, fileName, "new-file-id-112233445566778899", 50, 64*1024, PolicyOverwrite)
	if err != nil {
		t.Fatalf("ResolveCollision failed: %v", err)
	}

	if res.ResolvedName != "config.json" {
		t.Fatalf("Expected ResolvedName = 'config.json', got %q", res.ResolvedName)
	}
	if res.IsDuplicate {
		t.Fatalf("Expected IsDuplicate = false for overwrite")
	}
}

func TestCollisionPolicySkip(t *testing.T) {
	tmpDir := t.TempDir()
	fileName := "config.json"

	existingPath := filepath.Join(tmpDir, fileName)
	_ = os.WriteFile(existingPath, []byte("{\"version\": 1}"), 0644)

	res, err := ResolveCollision(tmpDir, fileName, "different-id-998877665544332211", 50, 64*1024, PolicySkip)
	if err != nil {
		t.Fatalf("ResolveCollision failed: %v", err)
	}

	if !res.IsDuplicate {
		t.Fatalf("Expected IsDuplicate = true (skipped) for PolicySkip")
	}
}

func TestCollisionResumeMatchingState(t *testing.T) {
	tmpDir := t.TempDir()
	fileName := "partial.dat"
	filePath := filepath.Join(tmpDir, fileName)

	fileData := bytes.Repeat([]byte{0xAA}, 1024*1024)
	_ = os.WriteFile(filePath, fileData, 0644)
	fileID := GenerateFileID(filePath)

	// Create valid .medxfer state for 512KB completed
	dm, err := CreateAndPreallocate(tmpDir, fileName, 1024*1024, 64*1024, fileID)
	if err != nil {
		t.Fatalf("CreateAndPreallocate failed: %v", err)
	}
	for i := uint32(0); i < 8; i++ {
		_, _ = dm.WriteChunkAt(make([]byte, 64*1024), int64(i)*64*1024, i)
	}
	_ = dm.Close()

	res, err := ResolveCollision(tmpDir, fileName, fileID, 1024*1024, 64*1024, PolicyAutoRename)
	if err != nil {
		t.Fatalf("ResolveCollision failed: %v", err)
	}

	if !res.IsResume {
		t.Fatalf("Expected IsResume = true")
	}
	if res.ResumeBytes != 8*64*1024 {
		t.Fatalf("Expected ResumeBytes = %d, got %d", 8*64*1024, res.ResumeBytes)
	}
	if res.ResolvedName != fileName {
		t.Fatalf("Expected ResolvedName = %q, got %q", fileName, res.ResolvedName)
	}
}
