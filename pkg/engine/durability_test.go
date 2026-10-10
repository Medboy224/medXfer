package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/protocol"
)

// memFile is an in-memory fileHandle that models the OS page cache: writes land in
// vol (volatile) and only reach dur (durable) on Sync. A power loss keeps dur only.
// Hooks run under the file lock, so their order matches the order of the copies.
type memFile struct {
	mu      sync.Mutex
	vol     []byte
	dur     []byte
	onWrite func(off int64, p []byte)
	onSync  func()
	syncErr error
}

func (f *memFile) WriteAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if end := int(off) + len(p); end > len(f.vol) {
		f.vol = append(f.vol, make([]byte, end-len(f.vol))...)
	}
	copy(f.vol[off:], p)
	if f.onWrite != nil {
		f.onWrite(off, p)
	}
	return len(p), nil
}

func (f *memFile) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if off >= int64(len(f.vol)) {
		return 0, io.EOF
	}
	n := copy(p, f.vol[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *memFile) Sync() error {
	if f.syncErr != nil {
		return f.syncErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dur = append([]byte(nil), f.vol...)
	if f.onSync != nil {
		f.onSync()
	}
	return nil
}

func (f *memFile) Close() error { return nil }

func newMemDisk(t *testing.T, id string, totalChunks int, fileSize int64) (*DiskManager, *memFile, *memFile) {
	t.Helper()
	header := append([]byte(id), make([]byte, totalChunks)...)
	data := &memFile{}
	state := &memFile{vol: header, dur: append([]byte(nil), header...)}
	dir := t.TempDir()
	dm := newDiskManager(data, state, filepath.Join(dir, "f.bin"), filepath.Join(dir, "f.bin.medxfer"),
		fileSize, make([]bool, totalChunks), 0)
	return dm, data, state
}

func chunkContent(i, size int) []byte {
	return bytes.Repeat([]byte{byte(i*7 + 1)}, size)
}

// Invariant J (STO-03), with background commits: (a) every chunk the state file declares
// was synced after its last write; (b) crash at every sync point, reload what was durable,
// and check every chunk declared complete has its correct bytes.
func TestDurabilityOrdering(t *testing.T) {
	const chunk, total = 64 * 1024, 10
	fileSize := int64(total*chunk - 1000)
	id := strings.Repeat("i", 32)
	dm, data, state := newMemDisk(t, id, total, fileSize)
	dm.commitBytes = 3 * chunk

	var evMu sync.Mutex
	seq, lastDataSync := 0, -1
	lastWrite := make([]int, total)
	var violation string
	data.onWrite = func(off int64, p []byte) {
		evMu.Lock()
		seq++
		lastWrite[off/chunk] = seq
		evMu.Unlock()
	}
	type snapshot struct{ data, state []byte }
	var snaps []snapshot
	// Syncs are serialized by commitMu, so the other file's dur is stable while we copy it.
	take := func() {
		snaps = append(snaps, snapshot{append([]byte(nil), data.dur...), append([]byte(nil), state.dur...)})
	}
	data.onSync = func() {
		evMu.Lock()
		seq++
		lastDataSync = seq
		take()
		evMu.Unlock()
	}
	state.onWrite = func(off int64, p []byte) {
		evMu.Lock()
		for i, b := range p {
			if b == 1 && lastWrite[i] > lastDataSync && violation == "" {
				violation = fmt.Sprintf("chunk %d declared before its data was synced", i)
			}
		}
		evMu.Unlock()
	}
	state.onSync = func() {
		evMu.Lock()
		take()
		evMu.Unlock()
	}

	for i := 0; i < total; i++ {
		size := chunk
		if i == total-1 {
			size = int(fileSize) - i*chunk
		}
		if _, err := dm.WriteChunkAt(chunkContent(i, size), int64(i*chunk), uint32(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := dm.Close(); err != nil {
		t.Fatal(err)
	}
	if violation != "" {
		t.Fatal(violation)
	}

	declared := 0
	for k, s := range snaps {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "f.bin"), s.data, 0o644)
		_ = os.WriteFile(filepath.Join(dir, "f.bin.medxfer"), s.state, 0o644)
		reloaded, err := CreateAndPreallocate(dir, "f.bin", fileSize, chunk, id)
		if err != nil {
			t.Fatalf("snapshot %d: reload: %v", k, err)
		}
		for i := 0; i < total; i++ {
			if !reloaded.IsChunkCompleted(uint32(i)) {
				continue
			}
			declared++
			size := chunk
			if i == total-1 {
				size = int(fileSize) - i*chunk
			}
			got := make([]byte, size)
			n, _ := reloaded.ReadChunkAt(got, int64(i*chunk))
			if n != size || !bytes.Equal(got, chunkContent(i, size)) {
				t.Fatalf("snapshot %d: chunk %d declared complete but its bytes were lost", k, i)
			}
		}
		_ = reloaded.Close()
	}
	if declared == 0 {
		t.Fatal("no snapshot declared any chunk: the test proves nothing")
	}
}

func TestCommitByBytesAndByTime(t *testing.T) {
	dm, _, state := newMemDisk(t, strings.Repeat("i", 32), 4, 4*64*1024)
	clock := time.Unix(1000, 0)
	dm.now = func() time.Time { return clock }
	dm.lastCommit = clock
	dm.commitBytes = 100
	commits := 0
	state.onSync = func() { commits++ }

	write := func(i int, n int) {
		t.Helper()
		if _, err := dm.WriteChunkAt(make([]byte, n), int64(i*64*1024), uint32(i)); err != nil {
			t.Fatal(err)
		}
		dm.commitWG.Wait() // commits run in the background
	}
	write(0, 60)
	if commits != 0 {
		t.Fatalf("commit after 60 bytes; want none")
	}
	write(1, 60)
	if commits != 1 {
		t.Fatalf("commits = %d after 120 bytes; want 1 (byte threshold)", commits)
	}
	write(2, 1)
	if commits != 1 {
		t.Fatalf("commits = %d; want 1 before the interval elapses", commits)
	}
	clock = clock.Add(2 * time.Second)
	write(3, 1)
	if commits != 2 {
		t.Fatalf("commits = %d after 2 s; want 2 (time threshold)", commits)
	}
}

type countListener struct {
	mu        sync.Mutex
	completes int
	errs      int
}

func (l *countListener) OnStart(string, int64, uint32)    {}
func (l *countListener) OnProgress(TransferStats)         {}
func (l *countListener) OnChunkFailed(uint32, int, error) {}
func (l *countListener) OnComplete(string, time.Duration) { l.mu.Lock(); l.completes++; l.mu.Unlock() }
func (l *countListener) OnError(error)                    { l.mu.Lock(); l.errs++; l.mu.Unlock() }

func TestFinalizeSyncFailureKeepsState(t *testing.T) {
	dm, data, _ := newMemDisk(t, strings.Repeat("i", 32), 1, 10)
	if err := os.WriteFile(dm.statePath, []byte("state"), 0o644); err != nil {
		t.Fatal(err)
	}
	data.syncErr = errors.New("disk unplugged")

	l := &countListener{}
	err := NewReceiver(t.TempDir(), 1).finalize(dm, l, 0)
	if err == nil {
		t.Fatal("finalize succeeded although the data sync failed")
	}
	if _, statErr := os.Stat(dm.statePath); statErr != nil {
		t.Fatalf("state file removed after a failed sync: %v", statErr)
	}
	if l.completes != 0 || l.errs != 1 {
		t.Fatalf("listener: %d complete, %d errors; want 0 and 1", l.completes, l.errs)
	}
}

type closeTrackingFile struct {
	*memFile
	closed bool
}

func (f *closeTrackingFile) Close() error { f.closed = true; return nil }

// #32: the data file is closed when Finalize returns, so the received file can be moved or
// deleted at once (Windows refuses both on an open file).
func TestFinalizeClosesDataFileBeforeReturning(t *testing.T) {
	dir := t.TempDir()
	data := &closeTrackingFile{memFile: &memFile{}}
	state := &memFile{vol: []byte(strings.Repeat("i", 32) + "\x00")}
	dm := newDiskManager(data, state, filepath.Join(dir, "f.bin"), filepath.Join(dir, "f.bin.medxfer"),
		10, make([]bool, 1), 0)
	if err := dm.Finalize(); err != nil {
		t.Fatal(err)
	}
	if !data.closed {
		t.Fatal("data file still open after Finalize returned")
	}

	// Same check on a real file: rename and delete right after Finalize.
	real, err := CreateAndPreallocate(dir, "real.bin", 64*1024, 64*1024, strings.Repeat("r", 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := real.WriteChunkAt(chunkContent(0, 64*1024), 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := real.Finalize(); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(dir, "moved.bin")
	if err := os.Rename(filepath.Join(dir, "real.bin"), moved); err != nil {
		t.Fatalf("cannot move the received file right after Finalize: %v", err)
	}
	if err := os.Remove(moved); err != nil {
		t.Fatalf("cannot delete the received file right after Finalize: %v", err)
	}
}

func TestDispatcherNoDeadlockAfterRetries(t *testing.T) {
	td := &taskDispatcher{}
	td.Push(chunkTask{index: 7}) // two workers failed: 7 then 0 pushed back
	td.Push(chunkTask{index: 0})

	if task, ok, _ := td.PopWithin(5); !ok || task.index != 0 {
		t.Fatalf("PopWithin(5) = %d, %v; want chunk 0", task.index, ok)
	}
	if _, ok, blocked := td.PopWithin(5); ok || !blocked {
		t.Fatalf("PopWithin(5) = ok %v, blocked %v; want blocked on chunk 7", ok, blocked)
	}
	if task, ok, _ := td.PopWithin(8); !ok || task.index != 7 {
		t.Fatalf("PopWithin(8) = %d, %v; want chunk 7", task.index, ok)
	}
	if _, ok, blocked := td.PopWithin(100); ok || blocked {
		t.Fatalf("empty dispatcher: ok %v, blocked %v; want neither", ok, blocked)
	}
}

// gatedSender serves chunks like the real sender but holds chunk 0 until gate closes.
func gatedSender(t *testing.T, chunk int, content func(int) []byte, gate <-chan struct{}) string {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				out := make([]byte, protocol.FrameHeaderSize+protocol.ChunkHeaderSize+chunk)
				for {
					h, err := protocol.ReadHeader(c)
					if err != nil {
						return
					}
					payload := make([]byte, h.PayloadLen)
					if _, err := io.ReadFull(c, payload); err != nil {
						return
					}
					if h.Type != protocol.TypeRequest {
						continue // resume frame
					}
					idx := int(payload[0])<<24 | int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
					if idx == 0 {
						<-gate
					}
					if writeContiguousChunk(c, out, uint32(idx), uint64(idx*chunk), content(idx)) != nil {
						return
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}

func TestValidateMeta(t *testing.T) {
	cases := []struct {
		meta protocol.FileMetadata
		ok   bool
	}{
		{protocol.FileMetadata{FileSize: 1 << 30, ChunkSize: 4 << 20}, true},
		{protocol.FileMetadata{FileSize: 0, ChunkSize: 64 << 10}, true},
		{protocol.FileMetadata{FileSize: 1 << 40, ChunkSize: 1}, false},        // tiny chunks: millions of tasks
		{protocol.FileMetadata{FileSize: -1, ChunkSize: 4 << 20}, false},       // negative size
		{protocol.FileMetadata{FileSize: 10, ChunkSize: 16 << 20}, false},      // above buffer size
		{protocol.FileMetadata{FileSize: 1 << 40, ChunkSize: 64 << 10}, false}, // > 2^21 chunks
	}
	for _, c := range cases {
		if err := validateMeta(c.meta); (err == nil) != c.ok {
			t.Errorf("validateMeta(%+v) = %v; want ok=%v", c.meta, err, c.ok)
		}
	}
	// Rejected before any allocation or disk access.
	err := NewReceiver(t.TempDir(), 1).PullWithMetadata(context.Background(), "127.0.0.1:1", nil,
		protocol.FileMetadata{FileName: "x", FileSize: 1 << 40, ChunkSize: 1}, "")
	if err == nil {
		t.Fatal("PullWithMetadata accepted ChunkSize=1 for a 1 TiB file")
	}
}

func TestSenderClampsChunkSize(t *testing.T) {
	for in, want := range map[uint32]uint32{
		16 << 20: 4 << 20, // would overflow the 8 MiB buffer and panic when serving
		1024:     4 << 20,
		1 << 20:  1 << 20,
		8 << 20:  8 << 20,
	} {
		if got := NewSender(4, in).chunkSize; got != want {
			t.Errorf("NewSender(4, %d).chunkSize = %d; want %d", in, got, want)
		}
	}
}
