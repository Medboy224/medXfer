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
	"syscall"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
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

type nopListener struct{}

func (nopListener) OnStart(string, int64, uint32)    {}
func (nopListener) OnProgress(engine.TransferStats)  {}
func (nopListener) OnChunkFailed(uint32, int, error) {}
func (nopListener) OnComplete(string, time.Duration) {}
func (nopListener) OnError(error)                    {}

// A full disk on the receiver ends the transfer with an error: no hang, no false success.
func TestEngineReportsFullDisk(t *testing.T) {
	restore := engine.SetStorageWrapper(func(f engine.StorageFile, path string) engine.StorageFile {
		if strings.HasSuffix(path, ".medxfer") {
			return f
		}
		return Wrap(f, Plan{NoSpaceAfter: 256 * 1024})
	})
	defer restore()

	src := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(src, bytes.Repeat([]byte("medxfer!"), 128*1024), 0o644); err != nil { // 1 MiB
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ready := make(chan error, 1)
	go func() {
		_ = engine.NewSender(2, 64*1024).ServeAndSendWithRelPathReady(ctx, addr, src, "f.bin", nopListener{}, 0, ready)
	}()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	err = engine.NewReceiver(t.TempDir(), 2).Pull(ctx, addr, nopListener{}, engine.GenerateFileID(src))
	if err == nil {
		t.Fatal("Pull succeeded on a full disk")
	}
	if ctx.Err() != nil {
		t.Fatalf("Pull only stopped at the test timeout: %v", err)
	}
}
