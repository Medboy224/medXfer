// Package store keeps the files a receiver writes (bible chapter 9): the bytes go to
// <name>.part, a journal <name>.part.state says exactly which chunks are durable, and the file
// gets its name only once it is complete and verified. Everything goes through an os.Root
// opened on the destination (STO-13).
package store

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
)

// Defaults of the group commit (STO-04; parameters, rule R4).
const (
	DefaultCommitBytes    = 64 << 20
	DefaultCommitInterval = 2 * time.Second
	verifyTail            = 8 // chunks checked again on resume (STO-06)
)

// Suffixes of a file being received.
const (
	PartSuffix    = ".part"
	JournalSuffix = ".part.state"
)

var (
	// ErrForeign: name already has a partial file whose journal is not this transfer's (another
	// key, size or chunk size, or a journal that cannot be read). It is never touched (STO-09):
	// the caller picks another name.
	ErrForeign = errors.New("store: a partial file of another transfer is in the way")
	// ErrExists: the final name was taken while the file was being received.
	ErrExists = errors.New("store: the final name already exists")
	// ErrIncomplete: Finalize was called before every chunk was written.
	ErrIncomplete = errors.New("store: the file is not complete")
	// ErrPathConflict: a component of the path exists with the wrong type (a file where a
	// folder is needed), or leads outside the destination. Nothing is deleted (STO-11).
	ErrPathConflict = errors.New("store: path conflict")
)

// Key identifies what a partial file is a part of (bible §8.3): resume only what has the
// same key.
type Key [32]byte

// ResumeKey is SHA-256(sender's key ‖ path ‖ size ‖ modification time) (bible §8.3, D2).
func ResumeKey(sender [32]byte, path string, size, mtime int64) Key {
	h := sha256.New()
	h.Write([]byte("medxfer-v1-resume"))
	h.Write(sender[:])
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(path)))
	h.Write(n[:])
	h.Write([]byte(path))
	binary.BigEndian.PutUint64(n[:], uint64(size))
	h.Write(n[:])
	binary.BigEndian.PutUint64(n[:], uint64(mtime))
	h.Write(n[:])
	var k Key
	copy(k[:], h.Sum(nil))
	return k
}

// Params describe the file to receive.
type Params struct {
	Key            Key
	Size           int64
	ChunkSize      uint32
	CommitBytes    int64         // 0: DefaultCommitBytes
	CommitInterval time.Duration // 0: DefaultCommitInterval
	VerifyFull     bool          // on resume, check every chunk, not only the last 8 (verify=full)
	NoSync         bool          // never fsync: not durable, for the bench ladder only
	Overwrite      bool          // Finalize may replace a file that has the final name
	// Owned: the file is in a folder of this transfer only, its root folder, found by its
	// marker (STO-10, D11). A partial file there without a readable journal of this transfer
	// is this transfer's, left by a stop before its first commit: it starts over under its
	// name. Elsewhere, the journal's header is written with the file, so that such a stop
	// leaves a partial file that reads as this transfer's.
	Owned  bool
	OnSync func(time.Duration)
}

func (p Params) chunks() uint32 {
	if p.Size <= 0 {
		return 0
	}
	return uint32((p.Size + int64(p.ChunkSize) - 1) / int64(p.ChunkSize))
}

// handle is what the store needs of a file; *os.File has it, tests replace it.
type handle = engine.StorageFile

// openFile opens name in root; tests replace it to simulate a power loss. In a diag build,
// a test may wrap the file with faults (pkg/diag/faultfs).
var openFile = func(root *os.Root, name string, flag int) (handle, error) {
	f, err := root.OpenFile(name, flag, 0o644)
	if err != nil {
		return nil, err
	}
	return engine.WrapStorage(f, name), nil
}

// rename renames in root; tests replace it to simulate a file held by an antivirus.
var rename = func(root *os.Root, from, to string) error { return root.Rename(from, to) }

// File is one file being received.
type File struct {
	root    *os.Root
	name    string // final name, slash-separated, relative to root
	p       Params
	n       uint32
	part    handle
	journal handle

	mu           sync.Mutex
	have         []bool     // chunks written (durable or not)
	hashes       [][32]byte // their SHA-256
	resumed      int64      // bytes found durable when opened
	order        []uint32   // chunks in the order of the journal, when opened
	pending      []record   // written, not yet in the journal
	pendingBytes int64
	lastCommit   time.Time
	journalEnd   int64
	closed       bool

	commitMu sync.Mutex // one commit at a time: the journal stays in order
}

type record struct {
	index uint32
	sum   [32]byte
}

// Journal layout (bible §9.3).
const (
	headerLen = 4 + 1 + 32 + 8 + 4 + 4 + 4
	recordLen = 4 + 32 + 4
	version   = 1
)

var magic = [4]byte{'M', 'X', 'S', 'T'}

// Open opens the partial file of name (relative to root, slash-separated), creating it and
// its folders if needed. If a journal of the same transfer is there, its durable chunks are
// known after the journal's torn tail is cut and the last 8 chunks (every chunk with
// VerifyFull) are checked against the data (bible §9.5). A partial file of another transfer
// gives ErrForeign and is left as it is.
func Open(root *os.Root, name string, p Params) (*File, error) {
	if p.ChunkSize == 0 {
		return nil, fmt.Errorf("store: chunk size 0")
	}
	if p.CommitBytes <= 0 {
		p.CommitBytes = DefaultCommitBytes
	}
	if p.CommitInterval <= 0 {
		p.CommitInterval = DefaultCommitInterval
	}
	f := &File{root: root, name: name, p: p, n: p.chunks(), lastCommit: time.Now()}
	f.have = make([]bool, f.n)
	f.hashes = make([][32]byte, f.n)
	partName, journalName := f.osName(PartSuffix), f.osName(JournalSuffix)
	if dir := path.Dir(name); dir != "." {
		if err := root.MkdirAll(filepath.FromSlash(dir), 0o755); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrPathConflict, dir, err)
		}
	}

	journal, err := openFile(root, journalName, os.O_RDWR)
	switch {
	case err == nil:
		if err := f.load(journal); err != nil {
			journal.Close()
			if !p.Owned || !errors.Is(err, ErrForeign) {
				return nil, err
			}
			// In this transfer's own folder: a journal it cannot read is its own, torn by a
			// stop; it goes with its data (STO-09), and the file starts over.
			if err := f.removeStale(); err != nil {
				return nil, err
			}
			f.have, f.hashes, f.order = make([]bool, f.n), make([][32]byte, f.n), nil
			break
		}
		part, err := openFile(root, partName, os.O_RDWR)
		if err == nil {
			f.part, f.journal = part, journal
			if err := f.verify(); err != nil {
				f.closeFiles()
				return nil, err
			}
			return f, nil
		}
		journal.Close()
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		// This transfer's journal without its data (the .part was deleted): it is ours, so it
		// may go (STO-09), and the file starts over.
		if err := root.Remove(journalName); err != nil {
			return nil, err
		}
		f.have = make([]bool, f.n)
		f.hashes = make([][32]byte, f.n)
		f.order = nil
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}

	// No journal: a new file. A .part without a journal is not ours, unless it is in this
	// transfer's own folder: a stop before its first commit left it there.
	if _, err := root.Lstat(partName); err == nil {
		if !p.Owned {
			return nil, fmt.Errorf("%w: %s has data but no journal", ErrForeign, name)
		}
		if err := f.removeStale(); err != nil {
			return nil, err
		}
	}
	// In its own folder, the journal of a file is made by its first commit (or Close): a
	// file finished in one go never has one (Finalize), which saves a file per small file.
	// Elsewhere, its header comes first, written but not synced: a stop (a kill) leaves it
	// with the data, and the file is resumed under its name, not "name (1)".
	if !p.Owned {
		if err := f.ensureJournal(); err != nil {
			return nil, err
		}
	}
	part, err := openFile(root, partName, os.O_RDWR|os.O_CREATE|os.O_EXCL)
	if err != nil {
		if f.journal != nil {
			f.closeJournal()
			_ = root.Remove(journalName)
		}
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: %s has data but no journal", ErrForeign, name)
		}
		return nil, err
	}
	f.part = part
	return f, nil
}

// removeStale removes the partial file of name and its journal, in this transfer's own
// folder only (Params.Owned): they are this transfer's (STO-09).
func (f *File) removeStale() error {
	for _, n := range []string{f.osName(PartSuffix), f.osName(JournalSuffix)} {
		if err := f.root.Remove(n); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// ensureJournal creates the journal with its header, if this file has none yet. Called with
// commitMu held, or once nothing else uses the file.
func (f *File) ensureJournal() error {
	if f.journal != nil {
		return nil
	}
	j, err := openFile(f.root, f.osName(JournalSuffix), os.O_RDWR|os.O_CREATE|os.O_EXCL)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w: %s", ErrForeign, f.name)
	}
	if err != nil {
		return err
	}
	if _, err := j.WriteAt(f.header(), 0); err != nil {
		// A journal without its header would not read as this transfer's: it goes, as this
		// transfer just made it (a full disk, STO-07).
		j.Close()
		_ = f.root.Remove(f.osName(JournalSuffix))
		return err
	}
	f.journal, f.journalEnd = j, headerLen
	return nil
}

func (f *File) osName(suffix string) string { return filepath.FromSlash(f.name + suffix) }

func (f *File) header() []byte {
	b := make([]byte, headerLen)
	copy(b[0:4], magic[:])
	b[4] = version
	copy(b[5:37], f.p.Key[:])
	binary.BigEndian.PutUint64(b[37:45], uint64(f.p.Size))
	binary.BigEndian.PutUint32(b[45:49], f.p.ChunkSize)
	binary.BigEndian.PutUint32(b[49:53], f.n)
	binary.BigEndian.PutUint32(b[53:57], crc32.ChecksumIEEE(b[:53]))
	return b
}

// load reads the journal of an existing partial file: its header must be this transfer's;
// its records are read up to the first invalid one, and a torn tail is cut (STO-05).
func (f *File) load(j handle) error {
	h := make([]byte, headerLen)
	if _, err := j.ReadAt(h, 0); err != nil {
		return fmt.Errorf("%w: %s: unreadable journal header", ErrForeign, f.name)
	}
	want := f.header()
	if string(h) != string(want) {
		return fmt.Errorf("%w: %s", ErrForeign, f.name)
	}
	off := int64(headerLen)
	rec := make([]byte, recordLen)
	for {
		if n, _ := j.ReadAt(rec, off); n < recordLen {
			break
		}
		if binary.BigEndian.Uint32(rec[36:40]) != crc32.ChecksumIEEE(rec[:36]) {
			break
		}
		index := binary.BigEndian.Uint32(rec[0:4])
		if index >= f.n {
			break
		}
		copy(f.hashes[index][:], rec[4:36])
		f.have[index] = true
		f.order = append(f.order, index)
		off += recordLen
	}
	if err := j.Truncate(off); err != nil { // the torn tail, if any
		return err
	}
	f.journalEnd = off
	return nil
}

// verify checks again the last 8 chunks the journal declares (all of them with VerifyFull)
// against the data: one that does not match is asked again (STO-06). Then it counts what
// was found.
func (f *File) verify() error {
	// The journal is appended to in the order chunks became durable: its last records are the
	// last chunks written before the stop, the ones a lying fsync would have lost.
	var check []uint32
	seen := map[uint32]bool{}
	for k := len(f.order) - 1; k >= 0 && (f.p.VerifyFull || len(check) < verifyTail); k-- {
		if i := f.order[k]; !seen[i] {
			seen[i] = true
			check = append(check, i)
		}
	}
	buf := make([]byte, f.p.ChunkSize)
	for _, i := range check {
		n := f.chunkLen(i)
		if _, err := f.part.ReadAt(buf[:n], int64(i)*int64(f.p.ChunkSize)); err != nil || sha256.Sum256(buf[:n]) != f.hashes[i] {
			f.have[i] = false
		}
	}
	for i := range f.have {
		if f.have[i] {
			f.resumed += int64(f.chunkLen(uint32(i)))
		}
	}
	return nil
}

func (f *File) chunkLen(i uint32) int64 {
	off := int64(i) * int64(f.p.ChunkSize)
	return min(int64(f.p.ChunkSize), f.p.Size-off)
}

// Chunks is the number of chunks of the file.
func (f *File) Chunks() uint32 { return f.n }

// Has reports whether chunk i is written: found durable when opened, or written since.
func (f *File) Has(i uint32) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return i < f.n && f.have[i]
}

// Resumed is the number of bytes found durable when the file was opened.
func (f *File) Resumed() int64 { return f.resumed }

// Hashes returns the SHA-256 of every chunk, for the digest, once the file is complete: the
// chunks of an earlier session keep the hash of their journal (no need to read them again).
func (f *File) Hashes() ([][32]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.have {
		if !h {
			return nil, ErrIncomplete
		}
	}
	return append([][32]byte(nil), f.hashes...), nil
}

// WriteChunk writes chunk i, whose SHA-256 the caller checked. Several goroutines may write
// different chunks at once. The chunk is in the journal once a commit makes it durable:
// CommitIfDue after each write keeps the group commit going.
func (f *File) WriteChunk(i uint32, data []byte, sum [32]byte) error {
	if i >= f.n || int64(len(data)) != f.chunkLen(i) {
		return fmt.Errorf("store: chunk %d of %d bytes does not fit %s", i, len(data), f.name)
	}
	if _, err := f.part.WriteAt(data, int64(i)*int64(f.p.ChunkSize)); err != nil {
		return err
	}
	f.mu.Lock()
	f.have[i], f.hashes[i] = true, sum
	f.pending = append(f.pending, record{i, sum})
	f.pendingBytes += int64(len(data))
	f.mu.Unlock()
	return nil
}

// CommitIfDue commits once 64 MiB or 2 s are pending (STO-04). It is apart from WriteChunk
// so that a caller measures the write and the fsync as two stages (DEV-03).
func (f *File) CommitIfDue() error {
	f.mu.Lock()
	due := len(f.pending) > 0 && (f.pendingBytes >= f.p.CommitBytes || time.Since(f.lastCommit) >= f.p.CommitInterval)
	f.mu.Unlock()
	if due {
		return f.Commit()
	}
	return nil
}

// Commit makes the chunks written so far durable and notes them in the journal, in that
// order (invariant J, STO-03): fsync of the data, then the records, then fsync of the journal.
// A chunk is in the journal only if its bytes are durable.
func (f *File) Commit() error {
	f.commitMu.Lock()
	defer f.commitMu.Unlock()
	f.mu.Lock()
	batch := f.pending
	f.pending, f.pendingBytes, f.lastCommit = nil, 0, time.Now()
	f.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	if f.p.OnSync != nil {
		defer func(start time.Time) { f.p.OnSync(time.Since(start)) }(time.Now())
	}
	err := f.commit(batch)
	if err != nil {
		f.mu.Lock()
		f.pending = append(batch, f.pending...) // a later commit tries again
		f.mu.Unlock()
	}
	return err
}

func (f *File) commit(batch []record) error {
	if !f.p.NoSync {
		if err := f.part.Sync(); err != nil { // 1. the data is durable
			return err
		}
	}
	buf := make([]byte, 0, len(batch)*recordLen)
	for _, r := range batch {
		var rec [recordLen]byte
		binary.BigEndian.PutUint32(rec[0:4], r.index)
		copy(rec[4:36], r.sum[:])
		binary.BigEndian.PutUint32(rec[36:40], crc32.ChecksumIEEE(rec[:36]))
		buf = append(buf, rec[:]...)
	}
	if err := f.ensureJournal(); err != nil {
		return err
	}
	if _, err := f.journal.WriteAt(buf, f.journalEnd); err != nil { // 2. it is noted
		return err
	}
	if !f.p.NoSync {
		if err := f.journal.Sync(); err != nil { // 3. the note is durable
			return err
		}
	}
	f.journalEnd += int64(len(buf))
	return nil
}

// notePending writes the pending records to the journal and syncs it, once the data is known
// durable (Finalize synced it): the file can be resumed after a failed rename.
func (f *File) notePending() error {
	f.commitMu.Lock()
	defer f.commitMu.Unlock()
	f.mu.Lock()
	batch := f.pending
	f.pending, f.pendingBytes = nil, 0
	f.mu.Unlock()
	saved := f.p.NoSync
	f.p.NoSync = true // the data sync is done: write the records, then sync the journal below
	err := f.commit(batch)
	f.p.NoSync = saved
	if err == nil && !saved {
		err = f.journal.Sync()
	}
	return err
}

// Close commits and closes the files: the partial file stays, resumable (pause, cancel, a
// lost sender).
func (f *File) Close() error {
	if f.markClosed() {
		return nil
	}
	err := f.Commit()
	if err == nil && f.journal == nil { // nothing to commit (an empty file): resumable all the same
		f.commitMu.Lock()
		err = f.ensureJournal()
		if err == nil && !f.p.NoSync {
			err = f.journal.Sync()
		}
		f.commitMu.Unlock()
	}
	if cerr := f.closeFiles(); err == nil {
		err = cerr
	}
	return err
}

// Suspend closes the file when the disk is full (STO-07): what is durable stays, to resume
// once there is room. A file that could not get its journal is removed: its .part alone would
// not read as this transfer's, and this transfer made it in this session (STO-09).
func (f *File) Suspend() error {
	err := f.Close()
	if f.journal == nil {
		if rerr := f.root.Remove(f.osName(PartSuffix)); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			err = errors.Join(err, rerr)
		}
	}
	return err
}

func (f *File) markClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	was := f.closed
	f.closed = true
	return was
}

func (f *File) closeFiles() error {
	var err error
	for _, h := range []handle{f.part, f.journal} {
		if h != nil {
			if cerr := h.Close(); err == nil {
				err = cerr
			}
		}
	}
	return err
}

// renameTries: on Windows an antivirus may hold the new file a moment (bible §9.6).
var renameTries = []time.Duration{0, 100 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond}

// Finalize gives the complete file its name (bible §9.6): fsync of the data, close, rename of
// .part to the final name, fsync of the folder (Unix), removal of the journal. The caller has
// checked the digest (STO-02).
func (f *File) Finalize() error {
	if _, err := f.Hashes(); err != nil {
		return err
	}
	if f.markClosed() {
		return fmt.Errorf("store: %s is closed", f.name)
	}
	// One fsync: the data. The journal is not needed to finish; it is written only if the
	// rename fails, so that the file stays resumable (its data is durable: invariant J holds).
	if !f.p.NoSync {
		if err := f.part.Sync(); err != nil {
			f.mu.Lock()
			f.closed = false // still open: Close keeps it resumable
			f.mu.Unlock()
			return err
		}
	}
	if err := f.part.Close(); err != nil {
		f.closeJournal()
		return err
	}
	final := filepath.FromSlash(f.name)
	var err error
	if _, lerr := f.root.Lstat(final); lerr == nil && !f.p.Overwrite {
		err = fmt.Errorf("%w: %s", ErrExists, f.name)
	} else {
		for _, wait := range renameTries {
			time.Sleep(wait)
			if err = rename(f.root, f.osName(PartSuffix), final); err == nil {
				break
			}
		}
	}
	if err != nil {
		if nerr := f.notePending(); nerr != nil {
			err = errors.Join(err, nerr)
		}
		f.closeJournal()
		return err
	}
	if runtime.GOOS != "windows" && !f.p.NoSync {
		f.syncDir()
	}
	if f.journal == nil { // finished in one go: it never had one
		return nil
	}
	f.closeJournal()
	return f.root.Remove(f.osName(JournalSuffix))
}

func (f *File) closeJournal() {
	if f.journal != nil {
		_ = f.journal.Close()
	}
}

// syncDir makes the rename durable on Unix: fsync of the folder that holds the file.
func (f *File) syncDir() {
	dir := path.Dir(f.name)
	d, err := f.root.Open(filepath.FromSlash(dir))
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// Discard removes the partial file and its journal: this transfer's only, which Open
// checked (STO-09). The receiver starts the file over (a digest that did not match).
func (f *File) Discard() error {
	f.markClosed()
	f.closeFiles()
	err := f.root.Remove(f.osName(PartSuffix))
	if f.journal != nil {
		if jerr := f.root.Remove(f.osName(JournalSuffix)); err == nil {
			err = jerr
		}
	}
	return err
}
