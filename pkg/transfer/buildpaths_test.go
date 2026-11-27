package transfer

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/protocol"
)

func writeTree(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, n := range names {
		p := filepath.Join(root, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// xfer send ., ./ and .. name the folder itself: its files keep their place under its
// name (#300). They gave "./b.txt" and ".b.txt".
func TestBuildOfferFromDotPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "top")
	writeTree(t, root, "a/b.txt", "ab.txt")
	whole := []string{"top/a/b.txt", "top/ab.txt"}
	for _, c := range []struct {
		name, dir, arg string
		root           string
		want           []string
	}{
		{"dot", root, ".", "top", whole},
		{"dot slash", root, "./", "top", whole},
		{"parent", filepath.Join(root, "a"), "..", "top", whole},
		{"relative", filepath.Join(root, "a"), "../a", "a", []string{"a/b.txt"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Chdir(c.dir)
			offer, files, err := BuildOffer([]string{c.arg}, protocol.DefaultChunkSize)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, it := range offer.Items {
				got = append(got, it.Path)
			}
			slices.Sort(got)
			if !slices.Equal(got, c.want) || offer.Root != c.root {
				t.Fatalf("xfer send %s: root %q, paths %q; want %q, %q", c.arg, offer.Root, got, c.root, c.want)
			}
			for i, f := range files {
				if b, err := os.ReadFile(f); err != nil || filepath.Base(string(b)) != filepath.Base(offer.Items[i].Path) {
					t.Fatalf("item %q is read from %q (%v)", offer.Items[i].Path, f, err)
				}
			}
		})
	}
}

// The sender checks the paths of its offer as the receiver will: a path the receiver would
// refuse fails at once, named, rather than file by file over there (#300).
func TestBuildOfferRefusesAPathTheReceiverRefuses(t *testing.T) {
	root := filepath.Join(t.TempDir(), "top")
	writeTree(t, root, "photo‮gpj.exe") // shown as "photoexe.jpg" (#313)
	_, _, err := BuildOffer([]string{root}, protocol.DefaultChunkSize)
	if !errors.Is(err, engine.ErrBadPath) {
		t.Fatalf("BuildOffer = %v; want %v", err, engine.ErrBadPath)
	}
}
