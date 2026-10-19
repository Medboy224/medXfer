package transfer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// WP-B.0 (STO-16): files an Android system picker gives as open descriptors are sent from
// where they are. Two names alike are numbered as the receiver does; the files stay open
// for the whole transfer, then the sender closes them.
func TestSendFromOpenFiles(t *testing.T) {
	src := t.TempDir()
	contents := map[string][]byte{
		filepath.Join("x", "a.txt"): []byte("first"),
		filepath.Join("y", "A.txt"): []byte("second"),
		"big.bin":                   bytes.Repeat([]byte("medXfer!"), 3*testChunk/8+5),
	}
	names := []string{"x/a.txt", "y/A.txt", "big.bin"}
	var files []*os.File
	for _, n := range names {
		p := filepath.Join(src, filepath.FromSlash(n))
		writeUserFile(t, p, string(contents[filepath.FromSlash(n)]))
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	offer, err := BuildOfferFiles(files, names, testChunk)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.txt", "A (1).txt", "big.bin"}
	for i, it := range offer.Items {
		if it.Path != want[i] {
			t.Fatalf("item %d is %q, want %q", i, it.Path, want[i])
		}
	}

	p := newOfferPair(t)
	offer.TransferID = p.id.String()
	s, err := NewSenderFiles(offer, files)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() { defer close(served); _ = s.Serve(ctx, p.tl) }()
	out := t.TempDir()
	rctx, rcancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer rcancel()
	if _, err := p.receiver(out).Run(rctx, p.addr, offer); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-served
	root := filepath.Join(out, offer.Root)
	for i, it := range offer.Items {
		got, err := os.ReadFile(filepath.Join(root, it.Path))
		if err != nil || !bytes.Equal(got, contents[filepath.FromSlash(names[i])]) {
			t.Fatalf("%s received with %d bytes (%v)", it.Path, len(got), err)
		}
	}
	for _, f := range files {
		if err := f.Close(); !errors.Is(err, os.ErrClosed) { // closed already by the sender
			t.Fatalf("a descriptor is still open after the transfer (%v)", err)
		}
	}
}

// A folder, which a descriptor may also designate, is not something an offer of files holds.
func TestBuildOfferFilesRefusesAFolder(t *testing.T) {
	d, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := BuildOfferFiles([]*os.File{d}, []string{"folder"}, testChunk); !errors.Is(err, ErrNotRegularFile) {
		t.Fatalf("BuildOfferFiles(folder) = %v, want ErrNotRegularFile", err)
	}
}
