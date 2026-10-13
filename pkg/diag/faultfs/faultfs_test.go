//go:build diag

package faultfs

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

func tempFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestNoSpaceAfter(t *testing.T) {
	f := Wrap(tempFile(t), Plan{NoSpaceAfter: 100})
	if n, err := f.WriteAt(make([]byte, 60), 0); n != 60 || err != nil {
		t.Fatalf("first write = %d, %v; want 60, nil", n, err)
	}
	if n, err := f.WriteAt(make([]byte, 60), 60); n != 40 || !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("second write = %d, %v; want 40, ENOSPC", n, err)
	}
	if n, err := f.WriteAt([]byte{1}, 100); n != 0 || !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("write on a full disk = %d, %v; want 0, ENOSPC", n, err)
	}
	if written, _, _ := f.Stats(); written != 100 {
		t.Fatalf("written = %d; want 100", written)
	}
}

func TestTornWrite(t *testing.T) {
	raw := tempFile(t)
	f := Wrap(raw, Plan{TornWriteAt: 50})
	data := bytes.Repeat([]byte{0xAB}, 100)
	if n, err := f.WriteAt(data, 0); n != 50 || !errors.Is(err, ErrTorn) {
		t.Fatalf("torn write = %d, %v; want 50, ErrTorn", n, err)
	}
	got := make([]byte, 100)
	n, _ := raw.ReadAt(got, 0)
	if n != 50 || !bytes.Equal(got[:50], data[:50]) {
		t.Fatalf("file holds %d bytes; want the first 50 of the write", n)
	}
	if n, err := f.WriteAt(data, 0); n != 100 || err != nil {
		t.Fatalf("write after the tear = %d, %v; want 100, nil (one tear only)", n, err)
	}
}

// Writes at the same time share the room left: together they never write past the limit.
func TestNoSpaceIsSharedByConcurrentWrites(t *testing.T) {
	f := Wrap(tempFile(t), Plan{NoSpaceAfter: 250})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = f.WriteAt(make([]byte, 100), int64(i)*100)
		}(i)
	}
	wg.Wait()
	if written, _, _ := f.Stats(); written != 250 {
		t.Fatalf("%d bytes written by concurrent writes; want exactly the limit, 250", written)
	}
}

// A real Sync makes what was written durable; a lying one does not: a power loss may take it
// back.
func TestUnsynced(t *testing.T) {
	f := Wrap(tempFile(t), Plan{})
	_, _ = f.WriteAt(make([]byte, 10), 0)
	_, _ = f.WriteAt(make([]byte, 5), 20)
	if got := f.Unsynced(); len(got) != 2 || got[0] != [2]int64{0, 10} || got[1] != [2]int64{20, 25} {
		t.Fatalf("unsynced %v", got)
	}
	if err := f.Sync(); err != nil || len(f.Unsynced()) != 0 {
		t.Fatalf("after a real sync: %v, %v", f.Unsynced(), err)
	}
	liar := Wrap(tempFile(t), Plan{LyingSync: true})
	_, _ = liar.WriteAt(make([]byte, 10), 0)
	_ = liar.Sync()
	if got := liar.Unsynced(); len(got) != 1 {
		t.Fatalf("a lying sync made %v durable", got)
	}
}

func TestLyingSync(t *testing.T) {
	f := Wrap(tempFile(t), Plan{LyingSync: true})
	for i := 0; i < 3; i++ {
		if err := f.Sync(); err != nil {
			t.Fatal(err)
		}
	}
	if _, calls, real := f.Stats(); calls != 3 || real != 0 {
		t.Fatalf("Sync calls %d, real %d; want 3, 0", calls, real)
	}
}

// diskFull is a receiver's events that cancel the transfer once the disk is full.
type diskFull struct {
	cancel   context.CancelFunc
	reported chan error
}

func (d *diskFull) OnProgress(transfer.Progress) {}
func (d *diskFull) OnItem(transfer.ItemEvent)    {}
func (d *diskFull) OnDiskFull(err error) {
	d.reported <- err
	d.cancel()
}

// A full disk on the receiver pauses the transfer and says so (STO-07): no hang, no false
// success. The pkg/transfer tests resume it.
func TestEngineReportsFullDisk(t *testing.T) {
	restore := engine.SetStorageWrapper(func(f engine.StorageFile, path string) engine.StorageFile {
		if strings.HasSuffix(path, ".part.state") {
			return f
		}
		return Wrap(f, Plan{NoSpaceAfter: 256 * 1024})
	})
	defer restore()

	src := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(src, bytes.Repeat([]byte("medxfer!"), 128*1024), 0o644); err != nil { // 1 MiB
		t.Fatal(err)
	}
	offer, files, err := transfer.BuildOffer([]string{src}, protocol.MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	offer.TransferID = "loopback"
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sender, err := transfer.NewSender(offer, files)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { _ = sender.Serve(ctx, ln) }()

	plain := func(_ context.Context, c net.Conn) (net.Conn, error) { return c, nil }
	rctx, rcancel := context.WithCancel(ctx)
	defer rcancel()
	r := transfer.NewReceiver(t.TempDir(), plain)
	ev := &diskFull{cancel: rcancel, reported: make(chan error, 1)}
	r.Events = ev
	results, err := r.Run(rctx, ln.Addr().String(), offer)
	if err == nil && results[0].State == transfer.ItemDone {
		t.Fatal("the transfer succeeded on a full disk")
	}
	if ctx.Err() != nil {
		t.Fatalf("the transfer only stopped at the test timeout: %v", err)
	}
	select {
	case err := <-ev.reported:
		if !errors.Is(err, syscall.ENOSPC) {
			t.Fatalf("OnDiskFull(%v); want ENOSPC", err)
		}
	default:
		t.Fatal("the full disk was not reported")
	}
}
