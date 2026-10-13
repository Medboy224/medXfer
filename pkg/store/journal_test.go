package store

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fixedContent is size bytes that do not change between runs, and the SHA-256 of each chunk:
// a fuzzing corpus built on them stays valid.
func fixedContent(size int) ([]byte, [][32]byte) {
	data := make([]byte, size)
	x := uint32(2463534242)
	for i := range data {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		data[i] = byte(x)
	}
	var sums [][32]byte
	for off := 0; off < size; off += testChunk {
		sums = append(sums, sha256.Sum256(data[off:min(off+testChunk, size)]))
	}
	return data, sums
}

// journalOf returns the journal of a file of data with the chunks in have written, closed
// half way.
func journalOf(t testing.TB, data []byte, sums [][32]byte, p Params, have ...int) []byte {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	f, err := Open(root, "f.bin", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range have {
		if err := f.WriteChunk(uint32(i), chunkOf(data, i), sums[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	j, err := os.ReadFile(filepath.Join(dir, "f.bin"+JournalSuffix))
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// stopped lays out a partial file of data and the journal j, as a stop leaves them.
func stopped(t testing.TB, dir string, data, j []byte) {
	if err := os.WriteFile(filepath.Join(dir, "f.bin"+PartSuffix), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.bin"+JournalSuffix), j, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Bible §14.2: the journal cut at every offset, the resume succeeds. The chunks of the whole
// records are kept, the others asked again, and the file ends whole. Outside the transfer's
// own folder, a header cut short reads as another transfer's: never touched (STO-09).
func TestJournalCutAtEveryOffset(t *testing.T) {
	data, sums := fixedContent(4*testChunk + 100)
	full := journalOf(t, data, sums, owned(len(data)), 0, 1, 3)
	for cut := 0; cut <= len(full); cut++ {
		root, dir := openRoot(t)
		stopped(t, dir, data, full[:cut])
		if cut < headerLen {
			if _, err := Open(root, "f.bin", params(len(data))); !errors.Is(err, ErrForeign) {
				t.Fatalf("cut at %d, not owned: %v; want ErrForeign", cut, err)
			}
		}
		f, err := Open(root, "f.bin", owned(len(data)))
		if err != nil {
			t.Fatalf("cut at %d: %v", cut, err)
		}
		records := 0
		if cut >= headerLen {
			records = (cut - headerLen) / recordLen
		}
		for k, i := range []uint32{0, 1, 3} {
			if f.Has(i) != (k < records) {
				t.Fatalf("cut at %d (%d whole records): chunk %d kept = %v", cut, records, i, f.Has(i))
			}
		}
		for i := 0; i < len(sums); i++ {
			if !f.Has(uint32(i)) {
				if err := f.WriteChunk(uint32(i), chunkOf(data, i), sums[i]); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := f.Finalize(); err != nil {
			t.Fatalf("cut at %d: %v", cut, err)
		}
		if got, _ := os.ReadFile(filepath.Join(dir, "f.bin")); !bytes.Equal(got, data) {
			t.Fatalf("cut at %d: the file differs", cut)
		}
	}
}

// Native fuzzing of the journal reader (bible §14.2): whatever the journal holds, Open never
// panics, never trusts a chunk whose data does not match (VerifyFull), and keeps within the
// file. The files are in memory (memFS): no disk work per run, which on Windows (an antivirus
// looking at each file) made the fuzzing workers stall.
func FuzzJournal(f *testing.F) {
	data, sums := fixedContent(4*testChunk + 100)
	p := params(len(data))
	full := journalOf(f, data, sums, p, 0, 1, 3)
	f.Add(full)
	f.Add(full[:headerLen+recordLen+7])
	f.Add(full[:headerLen-1])
	f.Add(append(append([]byte(nil), full...), full[headerLen:headerLen+recordLen]...)) // a record twice
	f.Add([]byte("MXST"))
	f.Add([]byte{})
	root, err := os.OpenRoot(f.TempDir()) // unused by memFS: Open needs one
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { root.Close() })
	f.Fuzz(func(t *testing.T, j []byte) {
		useMemFS(t, crashed(map[string][]byte{"f.bin" + PartSuffix: data, "f.bin" + JournalSuffix: j}))
		p := p
		p.VerifyFull = true
		g, err := Open(root, "f.bin", p)
		if err != nil {
			return
		}
		defer g.Close()
		if g.Chunks() != uint32(len(sums)) || g.Resumed() < 0 || g.Resumed() > int64(len(data)) {
			t.Fatalf("%d chunks, %d bytes resumed", g.Chunks(), g.Resumed())
		}
		for i := uint32(0); i < g.Chunks(); i++ {
			if g.Has(i) && g.hashes[i] != sums[i] {
				t.Fatalf("chunk %d kept with a hash that is not its data's", i)
			}
		}
	})
}
