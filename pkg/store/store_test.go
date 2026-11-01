package store

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const testChunk = 1024

func openRoot(t *testing.T) (*os.Root, string) {
	t.Helper()
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, dir
}

func content(t *testing.T, size int) ([]byte, [][32]byte) {
	t.Helper()
	data := make([]byte, size)
	_, _ = rand.Read(data)
	var sums [][32]byte
	for off := 0; off < size; off += testChunk {
		sums = append(sums, sha256.Sum256(data[off:min(off+testChunk, size)]))
	}
	return data, sums
}

func chunkOf(data []byte, i int) []byte {
	return data[i*testChunk : min((i+1)*testChunk, len(data))]
}

// writeChunk writes a chunk and lets the group commit run, as the receiver does.
func writeChunk(f *File, i uint32, data []byte, sum [32]byte) error {
	if err := f.WriteChunk(i, data, sum); err != nil {
		return err
	}
	return f.CommitIfDue()
}

func params(size int) Params {
	return Params{Key: Key{1}, Size: int64(size), ChunkSize: testChunk}
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
	return err == nil
}

// STO-01, STO-02, bible §9.6: the bytes go to .part, in any order; the file gets its name only
// when Finalize renames it, and the journal goes.
func TestWriteAndFinalize(t *testing.T) {
	root, dir := openRoot(t)
	data, sums := content(t, 3*testChunk+100)
	f, err := Open(root, "a/b/file.bin", params(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{2, 0, 3, 1} {
		if err := writeChunk(f, uint32(i), chunkOf(data, i), sums[i]); err != nil {
			t.Fatal(err)
		}
	}
	if exists(dir, "a/b/file.bin") || !exists(dir, "a/b/file.bin"+PartSuffix) {
		t.Fatal("the final name exists before Finalize, or the .part does not")
	}
	if got, err := f.Hashes(); err != nil || len(got) != 4 || got[3] != sums[3] {
		t.Fatalf("hashes %v, %v", got, err)
	}
	if err := f.Finalize(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "a", "b", "file.bin"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("final file differs (%v)", err)
	}
	if exists(dir, "a/b/file.bin"+PartSuffix) || exists(dir, "a/b/file.bin"+JournalSuffix) {
		t.Fatal(".part or journal left after Finalize")
	}
}

func TestEmptyFile(t *testing.T) {
	root, dir := openRoot(t)
	f, err := Open(root, "empty.txt", params(0))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Finalize(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "empty.txt")); err != nil || fi.Size() != 0 {
		t.Fatalf("empty file: %v, %v", fi, err)
	}
}

// Bible §9.5: closed half way, the file reopens with its durable chunks, and only the others
// are written.
func TestResume(t *testing.T) {
	root, dir := openRoot(t)
	data, sums := content(t, 4*testChunk)
	f, err := Open(root, "f.bin", params(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 2} {
		_ = writeChunk(f, uint32(i), chunkOf(data, i), sums[i])
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	g, err := Open(root, "f.bin", params(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !g.Has(0) || g.Has(1) || !g.Has(2) || g.Has(3) || g.Resumed() != 2*testChunk {
		t.Fatalf("resumed %d bytes, chunks %v %v %v %v", g.Resumed(), g.Has(0), g.Has(1), g.Has(2), g.Has(3))
	}
	if _, err := g.Hashes(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Hashes of an incomplete file: %v", err)
	}
	for _, i := range []int{1, 3} {
		_ = writeChunk(g, uint32(i), chunkOf(data, i), sums[i])
	}
	if err := g.Finalize(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "f.bin")); !bytes.Equal(got, data) {
		t.Fatal("resumed file differs")
	}
}

// STO-05: a torn tail (a record cut by a crash) is ignored and cut; the chunks before it stay.
func TestTornTailIsCut(t *testing.T) {
	root, dir := openRoot(t)
	data, sums := content(t, 3*testChunk)
	f, _ := Open(root, "f.bin", params(len(data)))
	for i := 0; i < 2; i++ {
		_ = writeChunk(f, uint32(i), chunkOf(data, i), sums[i])
	}
	_ = f.Close()
	journal := filepath.Join(dir, "f.bin"+JournalSuffix)
	j, _ := os.OpenFile(journal, os.O_WRONLY|os.O_APPEND, 0)
	_, _ = j.Write([]byte("half a record...."))
	j.Close()

	g, err := Open(root, "f.bin", params(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if !g.Has(0) || !g.Has(1) || g.Has(2) {
		t.Fatal("the chunks before the torn tail were lost")
	}
	if fi, _ := os.Stat(journal); fi.Size() != headerLen+2*recordLen {
		t.Fatalf("journal of %d bytes; the torn tail was not cut", fi.Size())
	}
}

// STO-06: on resume the last 8 chunks of the journal are checked against the data; with
// VerifyFull, all of them.
func TestLastChunksAreVerified(t *testing.T) {
	for _, full := range []bool{false, true} {
		root, dir := openRoot(t)
		data, sums := content(t, 12*testChunk)
		f, _ := Open(root, "f.bin", params(len(data)))
		for i := 0; i < 12; i++ {
			_ = writeChunk(f, uint32(i), chunkOf(data, i), sums[i])
		}
		_ = f.Close()
		part, _ := os.OpenFile(filepath.Join(dir, "f.bin"+PartSuffix), os.O_WRONLY, 0)
		// Each byte is flipped: a fixed byte would change nothing when the random data
		// already holds it, once in 256 (#91).
		_, _ = part.WriteAt([]byte{data[1*testChunk+5] ^ 0xFF}, 1*testChunk+5)   // chunk 1: not among the last 8
		_, _ = part.WriteAt([]byte{data[10*testChunk+5] ^ 0xFF}, 10*testChunk+5) // chunk 10: among the last 8
		part.Close()

		p := params(len(data))
		p.VerifyFull = full
		g, err := Open(root, "f.bin", p)
		if err != nil {
			t.Fatal(err)
		}
		if g.Has(10) {
			t.Errorf("full=%v: altered chunk 10 kept", full)
		}
		if g.Has(1) == full {
			t.Errorf("full=%v: chunk 1 kept = %v", full, g.Has(1))
		}
		if !g.Has(0) || !g.Has(11) {
			t.Errorf("full=%v: good chunks dropped", full)
		}
		g.Close()
	}
}

// STO-09: a partial file of another transfer, or a .part without journal, is never touched.
func TestForeignPartialIsLeftAlone(t *testing.T) {
	root, dir := openRoot(t)
	data, sums := content(t, 2*testChunk)
	f, _ := Open(root, "f.bin", params(len(data)))
	_ = writeChunk(f, 0, chunkOf(data, 0), sums[0])
	_ = f.Close()
	before, _ := os.ReadFile(filepath.Join(dir, "f.bin"+JournalSuffix))

	for name, p := range map[string]Params{
		"other key":   {Key: Key{2}, Size: int64(len(data)), ChunkSize: testChunk},
		"other size":  {Key: Key{1}, Size: int64(len(data)) + 1, ChunkSize: testChunk},
		"other chunk": {Key: Key{1}, Size: int64(len(data)), ChunkSize: 2 * testChunk},
	} {
		if _, err := Open(root, "f.bin", p); !errors.Is(err, ErrForeign) {
			t.Errorf("%s: Open = %v, want ErrForeign", name, err)
		}
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "f.bin"+JournalSuffix)); !bytes.Equal(before, after) {
		t.Fatal("the journal of another transfer was changed")
	}

	_ = os.WriteFile(filepath.Join(dir, "g.bin"+PartSuffix), []byte("someone's data"), 0o644)
	if _, err := Open(root, "g.bin", params(10)); !errors.Is(err, ErrForeign) {
		t.Fatalf("a .part without journal: Open = %v, want ErrForeign", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "g.bin"+PartSuffix)); string(got) != "someone's data" {
		t.Fatal("a .part without journal was changed")
	}
}

// This transfer's journal without its .part (deleted by hand): the file starts over.
func TestJournalWithoutDataStartsOver(t *testing.T) {
	root, dir := openRoot(t)
	data, sums := content(t, 2*testChunk)
	f, _ := Open(root, "f.bin", params(len(data)))
	_ = writeChunk(f, 0, chunkOf(data, 0), sums[0])
	_ = f.Close()
	_ = os.Remove(filepath.Join(dir, "f.bin"+PartSuffix))
	g, err := Open(root, "f.bin", params(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if g.Has(0) || g.Resumed() != 0 {
		t.Fatal("chunks kept without their data")
	}
}

func TestFinalizeRules(t *testing.T) {
	root, dir := openRoot(t)
	data, sums := content(t, 2*testChunk)
	f, _ := Open(root, "f.bin", params(len(data)))
	_ = writeChunk(f, 0, chunkOf(data, 0), sums[0])
	if err := f.Finalize(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Finalize of an incomplete file: %v", err)
	}
	_ = writeChunk(f, 1, chunkOf(data, 1), sums[1])
	_ = os.WriteFile(filepath.Join(dir, "f.bin"), []byte("appeared meanwhile"), 0o644)
	if err := f.Finalize(); !errors.Is(err, ErrExists) {
		t.Fatalf("Finalize over a file that appeared: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "f.bin")); string(got) != "appeared meanwhile" {
		t.Fatal("the file that appeared was replaced")
	}

	p := params(len(data))
	p.Overwrite = true
	g, err := Open(root, "f.bin", p)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Finalize(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "f.bin")); !bytes.Equal(got, data) {
		t.Fatal("Overwrite did not replace the file")
	}
}

// Bible §9.6: on Windows an antivirus may hold the new file; the rename is tried again.
func TestRenameIsRetried(t *testing.T) {
	root, dir := openRoot(t)
	saved, savedTries := rename, renameTries
	t.Cleanup(func() { rename, renameTries = saved, savedTries })
	renameTries = []time.Duration{0, time.Millisecond, time.Millisecond, time.Millisecond}
	fails := 0
	rename = func(r *os.Root, from, to string) error {
		if fails < 2 {
			fails++
			return errors.New("held by an antivirus")
		}
		return saved(r, from, to)
	}
	data, sums := content(t, 10)
	f, _ := Open(root, "f.bin", params(len(data)))
	_ = writeChunk(f, 0, data, sums[0])
	if err := f.Finalize(); err != nil || fails != 2 || !exists(dir, "f.bin") {
		t.Fatalf("Finalize = %v after %d failures", err, fails)
	}
}

func TestDiscardRemovesOnlyItsFiles(t *testing.T) {
	root, dir := openRoot(t)
	_ = os.WriteFile(filepath.Join(dir, "f.bin"), []byte("user's file"), 0o644)
	data, sums := content(t, 10)
	f, _ := Open(root, "f.bin", params(len(data)))
	_ = writeChunk(f, 0, data, sums[0])
	if err := f.Discard(); err != nil {
		t.Fatal(err)
	}
	if exists(dir, "f.bin"+PartSuffix) || exists(dir, "f.bin"+JournalSuffix) || !exists(dir, "f.bin") {
		t.Fatal("Discard left its files or removed the user's")
	}
}

func TestResumeKey(t *testing.T) {
	base := ResumeKey([32]byte{1}, "a/b.txt", 10, 20)
	if base != ResumeKey([32]byte{1}, "a/b.txt", 10, 20) {
		t.Fatal("the key is not deterministic")
	}
	for name, k := range map[string]Key{
		"sender": ResumeKey([32]byte{2}, "a/b.txt", 10, 20),
		"path":   ResumeKey([32]byte{1}, "a/c.txt", 10, 20),
		"size":   ResumeKey([32]byte{1}, "a/b.txt", 11, 20),
		"mtime":  ResumeKey([32]byte{1}, "a/b.txt", 10, 21),
	} {
		if k == base {
			t.Errorf("another %s gives the same key", name)
		}
	}
}

// memFS keeps files in memory, with what is written (vol) apart from what is durable (dur):
// a crash keeps dur only. snap records the durable state after every operation.
type memFS struct {
	mu    sync.Mutex
	files map[string]*memFile
	snaps []map[string][]byte
	syncs int
}

type memFile struct {
	fs       *memFS
	vol, dur []byte
	failSync bool          // Sync fails, as on a disk unplugged
	gate     chan struct{} // Sync waits for it, as a slow disk
}

func (m *memFS) open(_ *os.Root, name string, flag int) (handle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[name]
	switch {
	case ok && flag&os.O_EXCL != 0:
		return nil, os.ErrExist
	case !ok && flag&os.O_CREATE == 0:
		return nil, os.ErrNotExist
	case !ok:
		f = &memFile{fs: m}
		m.files[name] = f
	}
	return f, nil
}

func (m *memFS) snapshot() {
	s := map[string][]byte{}
	for n, f := range m.files {
		s[n] = append([]byte(nil), f.dur...)
	}
	m.snaps = append(m.snaps, s)
}

// crashed is a memFS holding the durable state of snapshot s.
func crashed(s map[string][]byte) *memFS {
	m := &memFS{files: map[string]*memFile{}}
	for n, d := range s {
		m.files[n] = &memFile{fs: m, vol: append([]byte(nil), d...), dur: append([]byte(nil), d...)}
	}
	return m
}

func (f *memFile) WriteAt(p []byte, off int64) (int, error) {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	if need := int(off) + len(p); need > len(f.vol) {
		f.vol = append(f.vol, make([]byte, need-len(f.vol))...)
	}
	copy(f.vol[off:], p)
	f.fs.snapshot()
	return len(p), nil
}

func (f *memFile) ReadAt(p []byte, off int64) (int, error) {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	if off >= int64(len(f.vol)) {
		return 0, fmt.Errorf("EOF")
	}
	n := copy(p, f.vol[off:])
	if n < len(p) {
		return n, fmt.Errorf("EOF")
	}
	return n, nil
}

func (f *memFile) Sync() error {
	f.fs.mu.Lock()
	gate := f.gate
	f.fs.mu.Unlock()
	if gate != nil {
		<-gate
	}
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	if f.failSync {
		return errors.New("disk unplugged")
	}
	f.dur = append(f.dur[:0], f.vol...)
	f.fs.syncs++
	f.fs.snapshot()
	return nil
}

func (f *memFile) Truncate(n int64) error {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	if int(n) < len(f.vol) {
		f.vol = f.vol[:n]
	}
	return nil
}

func (f *memFile) Close() error { return nil }

func useMemFS(t *testing.T, m *memFS) {
	t.Helper()
	saved := openFile
	openFile = m.open
	t.Cleanup(func() { openFile = saved })
}

// journalChunks decodes the records of a journal as the store writes them, without any check
// of the data: what the journal declares.
func journalChunks(j []byte) []uint32 {
	var out []uint32
	for off := headerLen; off+recordLen <= len(j); off += recordLen {
		rec := j[off : off+recordLen]
		if binary.BigEndian.Uint32(rec[36:40]) != crc32.ChecksumIEEE(rec[:36]) {
			break
		}
		out = append(out, binary.BigEndian.Uint32(rec[0:4]))
	}
	return out
}

// Invariant J (STO-03): whatever the moment of a power loss, every chunk the durable journal
// declares has its data durable. A power loss is simulated after each file operation, for
// chunks written in a random order with small commits. The durable journal is read as it is,
// before the check of the last chunks on resume, which would hide a broken order.
func TestPowerLossNeverLies(t *testing.T) {
	root, _ := openRoot(t)
	data, sums := content(t, 10*testChunk+300)
	for seed := uint64(1); seed <= 20; seed++ {
		m := &memFS{files: map[string]*memFile{}}
		useMemFS(t, m)
		p := params(len(data))
		p.CommitBytes = 2 * testChunk
		f, err := Open(root, "f.bin", p)
		if err != nil {
			t.Fatal(err)
		}
		order := mrand.New(mrand.NewPCG(seed, 7)).Perm(11)
		for _, i := range order {
			if err := writeChunk(f, uint32(i), chunkOf(data, i), sums[i]); err != nil {
				t.Fatal(err)
			}
		}
		_ = f.Close()

		for k, snap := range m.snaps {
			part := snap["f.bin"+PartSuffix]
			for _, i := range journalChunks(snap["f.bin"+JournalSuffix]) {
				off, want := int(i)*testChunk, chunkOf(data, int(i))
				if len(part) < off+len(want) || !bytes.Equal(part[off:off+len(want)], want) {
					t.Fatalf("seed %d, power loss %d: the journal declares chunk %d, its data is not durable", seed, k, i)
				}
			}
			// And a resume from that state only claims chunks whose data is there.
			useMemFS(t, crashed(snap))
			if g, err := Open(root, "f.bin", p); err == nil {
				for i := 0; i < 11; i++ {
					off, want := i*testChunk, chunkOf(data, i)
					if g.Has(uint32(i)) && (len(part) < off+len(want) || !bytes.Equal(part[off:off+len(want)], want)) {
						t.Fatalf("seed %d, power loss %d: resume claims chunk %d without its data", seed, k, i)
					}
				}
				g.Close()
			}
		}
	}
}

// STO-04: commits happen every CommitBytes, or after CommitInterval.
func TestGroupCommit(t *testing.T) {
	root, _ := openRoot(t)
	m := &memFS{files: map[string]*memFile{}}
	useMemFS(t, m)
	data, sums := content(t, 6*testChunk)
	p := params(len(data))
	p.CommitBytes, p.CommitInterval = 3*testChunk, time.Hour
	f, _ := Open(root, "f.bin", p)
	journalLen := func() int { // made by the first commit
		if j := m.files["f.bin"+JournalSuffix]; j != nil {
			return len(j.dur)
		}
		return 0
	}
	for i := 0; i < 2; i++ {
		_ = writeChunk(f, uint32(i), chunkOf(data, i), sums[i])
	}
	if journalLen() != 0 { // the journal is made by the first commit
		t.Fatal("committed before CommitBytes")
	}
	_ = writeChunk(f, 2, chunkOf(data, 2), sums[2])
	_ = f.Settle() // the commit runs in the background (WP-E.5)
	if journalLen() != headerLen+3*recordLen {
		t.Fatalf("not committed at CommitBytes: journal of %d bytes", journalLen())
	}
	f.p.CommitInterval = time.Millisecond
	time.Sleep(5 * time.Millisecond) // the clock may not move between two close calls (Windows)
	_ = writeChunk(f, 3, chunkOf(data, 3), sums[3])
	_ = f.Settle()
	if journalLen() != headerLen+4*recordLen {
		t.Fatal("not committed after CommitInterval")
	}
}

// WP-E.5 of the cable plan: the commit runs in the background, so the writers go on while the
// disk syncs (in the writer, receiving and syncing took turns: 40 MB/s on a disk that writes
// 64). When the next commit is due and the first still syncs, the writer waits for the disk.
func TestCommitDoesNotHoldTheWriter(t *testing.T) {
	root, _ := openRoot(t)
	m := &memFS{files: map[string]*memFile{}}
	useMemFS(t, m)
	data, sums := content(t, 8*testChunk)
	p := params(len(data))
	p.CommitBytes, p.CommitInterval = 2*testChunk, time.Hour
	f, err := Open(root, "f.bin", p)
	if err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	m.mu.Lock()
	m.files["f.bin"+PartSuffix].gate = gate // a slow disk: Sync waits
	m.mu.Unlock()
	for i := range 2 { // the commit of chunks 0 and 1 starts, and waits for the disk
		if err := writeChunk(f, uint32(i), chunkOf(data, i), sums[i]); err != nil {
			t.Fatal(err)
		}
	}
	for taken := false; !taken; { // until the commit has taken its chunks
		f.mu.Lock()
		taken = f.pendingBytes == 0
		f.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	if err := writeChunk(f, 2, chunkOf(data, 2), sums[2]); err != nil { // the writer goes on
		t.Fatal(err)
	}
	behind := make(chan error, 1)
	go func() { behind <- writeChunk(f, 3, chunkOf(data, 3), sums[3]) }() // the next commit due, the first still syncing
	select {
	case <-behind:
		t.Fatal("the writer did not wait for a disk a commit behind")
	case <-time.After(50 * time.Millisecond):
	}
	close(gate)
	if err := <-behind; err != nil {
		t.Fatal(err)
	}
	for i := 4; i < 8; i++ {
		if err := writeChunk(f, uint32(i), chunkOf(data, i), sums[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Settle(); err != nil {
		t.Fatal(err)
	}
	j := m.files["f.bin"+JournalSuffix]
	if got := journalChunks(j.dur); len(got) < 4 {
		t.Fatalf("journal %v after the commits", got)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// The error of a commit run in the background is not lost: the next call returns it, and the
// writer stops there (a disk unplugged, a disk full: STO-07).
func TestBackgroundCommitErrorIsReported(t *testing.T) {
	root, _ := openRoot(t)
	m := &memFS{files: map[string]*memFile{}}
	useMemFS(t, m)
	data, sums := content(t, 4*testChunk)
	p := params(len(data))
	p.CommitBytes, p.CommitInterval = testChunk, time.Hour
	f, err := Open(root, "f.bin", p)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.files["f.bin"+PartSuffix].failSync = true
	m.mu.Unlock()
	if err := writeChunk(f, 0, chunkOf(data, 0), sums[0]); err != nil {
		t.Fatalf("the first commit runs in the background: %v", err)
	}
	if err := f.Settle(); err == nil {
		t.Fatal("the background commit's error is lost")
	}
	// Still failing: the next calls report it again, by the write or by Settle.
	err = errors.Join(writeChunk(f, 1, chunkOf(data, 1), sums[1]), writeChunk(f, 2, chunkOf(data, 2), sums[2]), f.Settle())
	if err == nil {
		t.Fatal("a failing disk is never reported")
	}
}

// Bench ladder R4: NoSync never syncs.
func TestNoSync(t *testing.T) {
	root, _ := openRoot(t)
	m := &memFS{files: map[string]*memFile{}}
	useMemFS(t, m)
	data, sums := content(t, 3*testChunk)
	p := params(len(data))
	p.NoSync, p.CommitBytes = true, testChunk
	f, _ := Open(root, "f.bin", p)
	for i := 0; i < 3; i++ {
		_ = writeChunk(f, uint32(i), chunkOf(data, i), sums[i])
	}
	_ = f.Close()
	if m.syncs != 0 {
		t.Fatalf("%d syncs with NoSync", m.syncs)
	}
}

// owned are the parameters of a file in its transfer's own folder.
func owned(size int) Params {
	p := params(size)
	p.Owned = true
	return p
}

// In its transfer's own folder, a file received in one go never has a journal (one less file
// made and removed per small file); a file paused before its end has one, and is resumed.
func TestJournalOnlyWhenNeeded(t *testing.T) {
	root, dir := openRoot(t)
	data, sums := content(t, 10)
	f, _ := Open(root, "small.bin", owned(len(data)))
	_ = writeChunk(f, 0, data, sums[0])
	if exists(dir, "small.bin"+JournalSuffix) {
		t.Fatal("a journal before any commit")
	}
	if err := f.Finalize(); err != nil || !exists(dir, "small.bin") || exists(dir, "small.bin"+JournalSuffix) {
		t.Fatalf("Finalize = %v; the small file must end without journal", err)
	}

	big, bsums := content(t, 3*testChunk)
	g, _ := Open(root, "big.bin", owned(len(big)))
	_ = writeChunk(g, 0, chunkOf(big, 0), bsums[0])
	_ = g.Close()
	if !exists(dir, "big.bin"+JournalSuffix) {
		t.Fatal("closed half way without a journal: not resumable")
	}
	h, err := Open(root, "big.bin", owned(len(big)))
	if err != nil || !h.Has(0) {
		t.Fatalf("resume after a pause: %v", err)
	}
	h.Close()

	e, _ := Open(root, "empty.bin", owned(0))
	_ = e.Close()
	if r, err := Resolve(root, "empty.bin", owned(0), PolicyAutoRename); err != nil || !r.Resume {
		t.Fatalf("an empty file closed before its end is not resumable: %+v, %v", r, err)
	}
}

// A stop (a kill) before the first commit leaves a .part without journal (chaos, WP-4.10). In
// its transfer's own folder it is this transfer's: it starts over under its name, the stale
// files gone; nothing named "f (1)" appears.
func TestStopBeforeFirstCommitInOwnFolder(t *testing.T) {
	root, dir := openRoot(t)
	data, sums := content(t, 2*testChunk)
	f, _ := Open(root, "f.bin", owned(len(data)))
	_ = writeChunk(f, 0, chunkOf(data, 0), sums[0])
	f.part.Close() // the process stops: nothing committed, nothing closed properly
	if !exists(dir, "f.bin"+PartSuffix) || exists(dir, "f.bin"+JournalSuffix) {
		t.Fatal("setup: want a .part without journal")
	}
	pl, err := Resolve(root, "f.bin", owned(len(data)), PolicySkip)
	if err != nil || pl.Name != "f.bin" || !pl.Resume {
		t.Fatalf("Resolve = %+v, %v; want f.bin, resumed", pl, err)
	}
	g, err := Open(root, "f.bin", owned(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if g.Has(0) || g.Resumed() != 0 {
		t.Fatal("a chunk of a stale .part was trusted")
	}
	for i := 0; i < 2; i++ {
		_ = writeChunk(g, uint32(i), chunkOf(data, i), sums[i])
	}
	if err := g.Finalize(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "f.bin")); !bytes.Equal(got, data) {
		t.Fatal("the file started over differs")
	}

	// A journal torn before its header was whole: the same.
	writeRaw(t, dir, "g.bin"+PartSuffix, "stale data")
	writeRaw(t, dir, "g.bin"+JournalSuffix, "MXS")
	if g, err := Open(root, "g.bin", owned(len(data))); err != nil || g.Has(0) {
		t.Fatalf("a torn journal in the own folder: %v", err)
	} else {
		g.Close()
	}
}

// Outside a transfer's own folder, the journal's header is written with the file: a stop
// before the first commit leaves a partial file that is resumed under its name.
func TestStopBeforeFirstCommitSingleFile(t *testing.T) {
	root, dir := openRoot(t)
	data, sums := content(t, 2*testChunk)
	f, _ := Open(root, "f.bin", params(len(data)))
	_ = writeChunk(f, 0, chunkOf(data, 0), sums[0])
	f.part.Close() // stopped dead
	f.journal.Close()
	if !exists(dir, "f.bin"+JournalSuffix) {
		t.Fatal("no journal header with the file")
	}
	pl, err := Resolve(root, "f.bin", params(len(data)), PolicyAutoRename)
	if err != nil || pl.Name != "f.bin" || !pl.Resume {
		t.Fatalf("Resolve = %+v, %v; want f.bin, resumed", pl, err)
	}
	// A .part without journal is still another transfer's there, never touched (STO-09).
	writeRaw(t, dir, "h.bin"+PartSuffix, "someone else's")
	if _, err := Open(root, "h.bin", params(len(data))); !errors.Is(err, ErrForeign) {
		t.Fatalf("Open over a foreign .part = %v; want ErrForeign", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "h.bin"+PartSuffix)); string(got) != "someone else's" || exists(dir, "h.bin"+JournalSuffix) {
		t.Fatal("a foreign .part was touched")
	}
}

func writeRaw(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
