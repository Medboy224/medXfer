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
	}

	if !foundFile1 || !foundFile2 {
		t.Errorf("Expected relative paths folderA/file1.txt and folderA/subfolder/file2.bin, items: %+v", m.Items)
	}
}

// Audit §3.6 (#156): two paths of the same name (letter case aside) do not make the offer
// fail: the second one is named as the receiver names a collision, "name (1).ext".
func TestSameNamesAreNumbered(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string) string {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(rel), 0o644)
		return p
	}
	a, b, c := write("a/doc.pdf"), write("b/doc.pdf"), write("c/DOC.pdf")
	write("x/photos/1.jpg")
	write("y/photos/2.jpg")
	write("z/arch.tar.gz")
	write("w/arch.tar.gz")
	m, err := Build([]string{a, b, c, filepath.Join(dir, "x", "photos"), filepath.Join(dir, "y", "photos"),
		filepath.Join(dir, "z", "arch.tar.gz"), filepath.Join(dir, "w", "arch.tar.gz")})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range m.Items {
		got = append(got, it.RelPath)
	}
	want := []string{"doc.pdf", "doc (1).pdf", "DOC (2).pdf", "photos/1.jpg", "photos (1)/2.jpg", "arch.tar.gz", "arch (1).tar.gz"}
	if len(got) != len(want) {
		t.Fatalf("paths %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("paths %q, want %q", got, want)
		}
	}
}
