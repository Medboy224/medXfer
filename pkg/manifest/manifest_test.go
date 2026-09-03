package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildManifest(t *testing.T) {
	tempDir := t.TempDir()

	// Create test folder structure
	// tempDir/
	//   folderA/
	//     file1.txt
	//     subfolder/
	//       file2.bin
	folderA := filepath.Join(tempDir, "folderA")
	subfolder := filepath.Join(folderA, "subfolder")
	_ = os.MkdirAll(subfolder, 0755)

	file1Path := filepath.Join(folderA, "file1.txt")
	_ = os.WriteFile(file1Path, []byte("hello world"), 0644)

	file2Path := filepath.Join(subfolder, "file2.bin")
	_ = os.WriteFile(file2Path, []byte("binary data 12345"), 0644)

	m, err := Build([]string{folderA})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if m.TotalFiles != 2 {
		t.Errorf("Expected 2 files, got %d", m.TotalFiles)
	}

	expectedSize := int64(len("hello world") + len("binary data 12345"))
	if m.TotalBytes != expectedSize {
		t.Errorf("Expected %d bytes, got %d", expectedSize, m.TotalBytes)
	}

	// Verify relative paths use forward slashes
	foundFile1 := false
	foundFile2 := false
	for _, item := range m.Items {
		if item.RelPath == "folderA/file1.txt" {
			foundFile1 = true
		}
		if item.RelPath == "folderA/subfolder/file2.bin" {
			foundFile2 = true
		}
		if item.FileID == "" {
			t.Errorf("Item %s missing FileID", item.RelPath)
		}
	}

	if !foundFile1 || !foundFile2 {
		t.Errorf("Expected relative paths folderA/file1.txt and folderA/subfolder/file2.bin, items: %+v", m.Items)
	}
}
