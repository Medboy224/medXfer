package testkit

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// AssertTreesEqual must notice every kind of difference, and nothing else.
func TestTreeDiff(t *testing.T) {
	want, got := t.TempDir(), t.TempDir()
	writeFiles(t, want, map[string]string{"a.txt": "a", "dir/b.txt": "b", "dir/c.txt": "c"})
	writeFiles(t, got, map[string]string{"a.txt": "a", "dir/b.txt": "B", "extra.txt": "x", "a.txt.medxfer": "state"})
	_ = os.Mkdir(filepath.Join(got, "empty"), 0o755)

	problems, err := treeDiff(want, got)
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"different content: dir/b.txt", "missing dir/c.txt", "unexpected extra.txt"}
	if !reflect.DeepEqual(problems, expected) {
		t.Fatalf("treeDiff = %q; want %q", problems, expected)
	}

	same := t.TempDir()
	writeFiles(t, same, map[string]string{"a.txt": "a", "dir/b.txt": "b", "dir/c.txt": "c"})
	AssertTreesEqual(t, want, same)
}
