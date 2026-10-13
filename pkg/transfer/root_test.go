package transfer

// Where the files of a transfer go (WP-4.7, STO-10, decisions D5 and D11 of the J4b plan): a
// single file goes to the destination itself; several files go to a root folder of their
// own, resolved once, never merged with what the user has.

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/store"
)

// singleFile is the offer of one file, "f.bin", with no folder.
func singleFile(t *testing.T, size int) (protocol.Offer, []string, []byte) {
	t.Helper()
	data := make([]byte, size)
	_, _ = rand.Read(data)
	src := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	offer := protocol.Offer{Root: "f.bin", ChunkSize: testChunk, TotalBytes: int64(size),
		Items: []protocol.OfferItem{{Item: 0, Path: "f.bin", Size: int64(size)}}}
	return offer, []string{src}, data
}

func writeUserFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, _ := os.ReadFile(path)
	return string(b)
}

// STO-10: a folder sent to a destination that has a folder of that name is received whole in
// "folder (1)"; the user's folder gets nothing and loses nothing.
func TestFolderGoesToAFreshRoot(t *testing.T) {
	offer, paths, contents := tree(t, []int{testChunk + 3, 10})
	out := t.TempDir()
	mine := filepath.Join(out, "folder", "d0", "f0000.bin")
	writeUserFile(t, mine, "the user's own file")
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	offer.TransferID = p.id.String()
	res := runOffer(t, p.receiver(out), p.addr, offer)
	for i, r := range res {
		want := filepath.Join(out, "folder (1)", filepath.FromSlash(offer.Items[i].Path[len("folder/"):]))
		if r.State != ItemDone || r.SavedPath != want {
			t.Fatalf("item %d: %+v; want it in %s", i, r, want)
		}
		if got, _ := os.ReadFile(r.SavedPath); !bytes.Equal(got, contents[i]) {
			t.Fatalf("item %d differs", i)
		}
	}
	if got := readFile(t, mine); got != "the user's own file" {
		t.Fatalf("the user's file was changed: %q", got)
	}
	if entries, _ := os.ReadDir(filepath.Join(out, "folder")); len(entries) != 1 {
		t.Fatalf("something was added to the user's folder: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(out, "folder (1)", store.MarkerName)); err == nil {
		t.Fatal("the marker stayed after a complete transfer")
	}
}

// D11: a transfer cut in "folder (1)" resumes in "folder (1)", not in a new "folder (2)": the
// marker of its root holds its key. The marker goes once every file is there.
func TestResumeFindsItsRootAgain(t *testing.T) {
	offer, paths, contents := tree(t, []int{8 * testChunk})
	out := t.TempDir()
	writeUserFile(t, filepath.Join(out, "folder", "x.txt"), "the user's")
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	offer.TransferID = p.id.String()
	first := p.receiver(out)
	first.Workers = 1
	v := newValve(3 * testChunk)
	first.dial = v.dial
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-v.reached; cancel() }()
	_, _ = first.Run(ctx, p.addr, offer)
	if _, err := os.Stat(filepath.Join(out, "folder (1)", store.MarkerName)); err != nil {
		t.Fatalf("no marker in the root of a cut transfer: %v", err)
	}

	q := p.again(t)
	q.serve(t, offer, paths)
	offer.TransferID = q.id.String()
	res := runOffer(t, q.receiver(out), q.addr, offer)
	if want := filepath.Join(out, "folder (1)", "d0", "f0000.bin"); res[0].State != ItemDone || res[0].SavedPath != want {
		t.Fatalf("resumed into %+v; want %s", res[0], want)
	}
	if _, err := os.Stat(filepath.Join(out, "folder (2)")); err == nil {
		t.Fatal("the resume made a new root folder")
	}
	if got, _ := os.ReadFile(res[0].SavedPath); !bytes.Equal(got, contents[0]) {
		t.Fatal("the resumed file differs")
	}
	if _, err := os.Stat(filepath.Join(out, "folder (1)", store.MarkerName)); err == nil {
		t.Fatal("the marker stayed after the transfer completed")
	}
}

// D5: "skip" with a folder already there refuses the whole offer: nothing is served, nothing
// is touched.
func TestSkipRefusesTheWholeFolder(t *testing.T) {
	offer, paths, contents := tree(t, []int{3*testChunk + 7, 10})
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	out := t.TempDir()
	offer.TransferID = p.id.String()
	runOffer(t, p.receiver(out), p.addr, offer)

	q := newOfferPair(t)
	again := q.serve(t, offer, paths)
	offer.TransferID = q.id.String()
	r := q.receiver(out)
	r.Policy = store.PolicySkip
	for _, res := range runOffer(t, r, q.addr, offer) {
		if res.State != ItemSkipped {
			t.Fatalf("second run: %+v; want skipped", res)
		}
	}
	if served := again.Progress().Bytes; served != 0 {
		t.Fatalf("the sender served %d bytes for a refused offer", served)
	}
	checkFiles(t, out, offer, contents, nil)
	if _, err := os.Stat(filepath.Join(out, "folder (1)")); err == nil {
		t.Fatal("a copy was made")
	}
}

// D5: for a folder, "overwrite" behaves as "rename": a folder of the user is never emptied.
func TestOverwriteNeverEmptiesAFolder(t *testing.T) {
	offer, paths, contents := tree(t, []int{10})
	out := t.TempDir()
	mine := filepath.Join(out, "folder", "d0", "f0000.bin")
	writeUserFile(t, mine, "the user's own file")
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	offer.TransferID = p.id.String()
	r := p.receiver(out)
	r.Policy = store.PolicyOverwrite
	res := runOffer(t, r, p.addr, offer)
	if res[0].State != ItemDone || res[0].SavedPath != filepath.Join(out, "folder (1)", "d0", "f0000.bin") {
		t.Fatalf("result %+v", res[0])
	}
	if got := readFile(t, mine); got != "the user's own file" {
		t.Fatalf("the user's file was overwritten: %q", got)
	}
	if got, _ := os.ReadFile(res[0].SavedPath); !bytes.Equal(got, contents[0]) {
		t.Fatal("received file differs")
	}
}

// A single file goes to the destination itself; a name taken by the user gives "f (1).bin".
func TestSingleFileNameTakenIsRenamed(t *testing.T) {
	offer, paths, data := singleFile(t, testChunk+3)
	out := t.TempDir()
	mine := filepath.Join(out, "f.bin")
	writeUserFile(t, mine, "the user's own file")
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	offer.TransferID = p.id.String()
	res := runOffer(t, p.receiver(out), p.addr, offer)
	if res[0].State != ItemDone || res[0].SavedPath != filepath.Join(out, "f (1).bin") {
		t.Fatalf("result %+v", res[0])
	}
	if got := readFile(t, mine); got != "the user's own file" {
		t.Fatalf("the user's file was changed: %q", got)
	}
	if got, _ := os.ReadFile(res[0].SavedPath); !bytes.Equal(got, data) {
		t.Fatal("received file differs")
	}
}

// #75: with the overwrite policy, the user's file under the offered name stays whole until a
// verified chunk replaces it; a transfer that brings nothing leaves it as it was.
func TestOverwriteKeepsTheFileUntilDataArrives(t *testing.T) {
	offer, paths, data := singleFile(t, 2*testChunk+5)
	out := t.TempDir()
	mine := filepath.Join(out, "f.bin")
	writeUserFile(t, mine, "the user's own file, to be replaced only by real data")

	silent := newOfferPair(t)
	offer.TransferID = silent.id.String()
	r := silent.receiver(out)
	r.Policy = store.PolicyOverwrite
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	_, _ = r.Run(ctx, silent.addr, offer)
	cancel()
	if got := readFile(t, mine); got != "the user's own file, to be replaced only by real data" {
		t.Fatalf("the user's file was changed by a transfer that brought nothing: %q", got)
	}

	p := newOfferPair(t)
	p.serve(t, offer, paths)
	offer.TransferID = p.id.String()
	r = p.receiver(out)
	r.Policy = store.PolicyOverwrite
	if res := runOffer(t, r, p.addr, offer); res[0].State != ItemDone || res[0].SavedPath != mine {
		t.Fatalf("run: %+v", res)
	}
	if got, _ := os.ReadFile(mine); !bytes.Equal(got, data) {
		t.Fatal("the file was not replaced")
	}
}

// D5: several paths with no folder in common go to the root the offer proposes.
func TestSeveralPathsGetARoot(t *testing.T) {
	offer, paths, contents := tree(t, []int{10, 20})
	offer.Items[0].Path, offer.Items[1].Path = "a.bin", "sub/b.bin"
	offer.Root = "medXfer 2026-10-13 10h42"
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	out := t.TempDir()
	offer.TransferID = p.id.String()
	res := runOffer(t, p.receiver(out), p.addr, offer)
	for i, want := range []string{"a.bin", filepath.Join("sub", "b.bin")} {
		full := filepath.Join(out, "medXfer 2026-10-13 10h42", want)
		if res[i].SavedPath != full {
			t.Fatalf("item %d in %s; want %s", i, res[i].SavedPath, full)
		}
		if got, _ := os.ReadFile(full); !bytes.Equal(got, contents[i]) {
			t.Fatalf("item %d differs", i)
		}
	}
}
