package transfer

// Guarantees the first engine's tests checked, now checked on protocol v1 (WP-4.3c-2): the
// paths of an offer, the keys of the data connections, a sender that restarts, a source that
// changed between two attempts, a file already received, a name already taken.

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/securechan"
)

func runOffer(t *testing.T, r *Receiver, addr string, offer protocol.Offer) []ItemResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	results, err := r.Run(ctx, addr, offer)
	if err != nil {
		t.Fatal(err)
	}
	return results
}

// A hostile offer cannot write outside the receive folder: every path unsafe on this system
// fails its item, and nothing appears next to the folder (SafeRelPath).
func TestOfferPathsCannotEscape(t *testing.T) {
	src := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	unsafe := []string{"../evil.bin", "a/../../evil.bin", "/abs/evil.bin", `\abs\evil.bin`, `..\evil.bin`}
	if runtime.GOOS == "windows" {
		// A drive prefix is a plain name on Linux and Android (TestSafeRelPath), a way out on Windows.
		unsafe = append(unsafe, `C:\evil.bin`, "c:evil.bin")
	}
	offer := protocol.Offer{Root: "x", ChunkSize: testChunk}
	var paths []string
	for i, path := range unsafe {
		offer.Items = append(offer.Items, protocol.OfferItem{Item: i, Path: path, Size: 7})
		offer.TotalBytes += 7
		paths = append(paths, src)
	}
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	base := t.TempDir()
	out := filepath.Join(base, "recv")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	offer.TransferID = p.id.String()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	results, _ := p.receiver(out).Run(ctx, p.addr, offer)
	for i, res := range results {
		if res.State != ItemFailed {
			t.Errorf("%q: item %v, want failed", unsafe[i], res.State)
		}
	}
	if entries, _ := os.ReadDir(base); len(entries) != 1 {
		t.Fatalf("something was written next to the receive folder: %v", entries)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("an unsafe path was written into the receive folder: %v", entries)
	}
}

// The data connections are pinned (WP-3.4, CHAN-06): a receiver expecting another sender's
// key, or a device the transfer is not registered for, gets nothing.
func TestDataConnectionsArePinned(t *testing.T) {
	offer, paths, contents := tree(t, []int{testChunk + 1})
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	offer.TransferID = p.id.String()
	stranger := newID(t)
	for name, cfg := range map[string]securechan.Config{
		"other sender key": {Identity: p.peer, Expected: stranger.Fingerprint},
		"other receiver":   {Identity: stranger, Expected: p.device.Fingerprint},
	} {
		out := t.TempDir()
		r := NewReceiver(out, JoinFunc(cfg, p.id))
		r.DialTimeout = time.Second
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := r.Run(ctx, p.addr, offer)
		timedOut := ctx.Err() != nil
		cancel()
		if err == nil || timedOut {
			t.Errorf("%s: Run = %v; want a refused connection before the timeout", name, err)
		}
		// The receiver may have opened the item (folders, a preallocated file) before its
		// connection was refused; none of the sender's bytes may be there.
		_ = filepath.Walk(out, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				if data, _ := os.ReadFile(path); bytes.Contains(data, contents[0][:64]) {
					t.Errorf("%s: data of the sender was received in %s", name, path)
				}
			}
			return nil
		})
	}
}

// #48: a sender that is not there is given up after the connection attempts (XFER-05), not
// after minutes.
func TestUnreachableSenderIsGivenUp(t *testing.T) {
	offer, _, _ := tree(t, []int{10})
	r := NewReceiver(t.TempDir(), JoinFunc(securechan.Config{Identity: newID(t)}, ID{}))
	r.DialTimeout = 500 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := r.Run(ctx, "127.0.0.1:1", offer); err == nil || ctx.Err() != nil {
		t.Fatalf("Run = %v after %v; want an error before the timeout", err, time.Since(start))
	}
}

// XFER-05: the sender stops half way and comes back on the same port; the receiver's
// connections come back too and the file is whole.
func TestReceiverRejoinsARestartedSender(t *testing.T) {
	offer, paths, contents := tree(t, []int{16 * testChunk})
	p := newOfferPair(t)
	offer.TransferID = p.id.String()

	first, err := NewSender(offer, paths)
	if err != nil {
		t.Fatal(err)
	}
	sctx, stop := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { defer close(stopped); _ = first.Serve(sctx, p.tl) }()

	out := t.TempDir()
	r := p.receiver(out)
	r.dial = slowDial
	rec := &recorder{}
	r.Events = rec
	done := make(chan error, 1)
	var results []ItemResult
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var err error
		results, err = r.Run(ctx, p.addr, offer)
		done <- err
	}()

	// Stop the sender once a few chunks arrived, then serve the same transfer again.
	for deadline := time.Now().Add(30 * time.Second); first.Progress().Bytes < 3*testChunk; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the first sender served nothing")
		}
	}
	stop()
	<-stopped
	if first.Progress().Bytes >= offer.TotalBytes {
		t.Fatal("the sender was not stopped half way")
	}
	tl, err := p.h.Register(p.id, p.peer.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	p.tl = tl
	p.serve(t, offer, paths)

	if err := <-done; err != nil || results[0].State != ItemDone {
		t.Fatalf("Run: %+v, %v", results, err)
	}
	checkFiles(t, out, offer, contents, nil)
}

// The source changed between two attempts (same name and size, new content, so a new
// FileID): the partial file of the first attempt is not resumed. The new content is received
// whole under a free name (the stale partial file is left as it is until J4b's journal).
func TestChangedSourceStartsOver(t *testing.T) {
	offer, paths, _ := tree(t, []int{12 * testChunk})
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	out := t.TempDir()

	first := p.receiver(out)
	first.dial = slowDial
	rec := &recorder{}
	first.Events = rec
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for ctx.Err() == nil {
			rec.mu.Lock()
			n := len(rec.progress)
			var got int64
			if n > 0 {
				got = rec.progress[n-1].Bytes
			}
			rec.mu.Unlock()
			if got >= 3*testChunk {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	offer.TransferID = p.id.String()
	if _, err := first.Run(ctx, p.addr, offer); !errors.Is(err, context.Canceled) {
		t.Fatalf("first run: %v, want context.Canceled", err)
	}

	// New content under the same name, served as a new transfer.
	fresh := make([]byte, offer.Items[0].Size)
	_, _ = rand.Read(fresh)
	if err := os.WriteFile(paths[0], fresh, 0o644); err != nil {
		t.Fatal(err)
	}
	offer.Items[0].FileID = "id-changed"
	q := newOfferPair(t)
	q.serve(t, offer, paths)
	offer.TransferID = q.id.String()
	r := NewReceiver(out, JoinFunc(securechan.Config{Identity: q.peer, Expected: q.device.Fingerprint}, q.id))
	res := runOffer(t, r, q.addr, offer)
	if res[0].State != ItemDone {
		t.Fatalf("second run: %+v", res)
	}
	stale := filepath.Join(out, filepath.FromSlash(offer.Items[0].Path))
	if res[0].SavedPath == stale {
		t.Fatal("the new content was written over the partial file of the old one")
	}
	got, err := os.ReadFile(res[0].SavedPath)
	if err != nil || !bytes.Equal(got, fresh) {
		t.Fatalf("the received file is not the new content (%v)", err)
	}
}

// With the skip policy, a file already received whole is not fetched again: the sender
// serves nothing and no copy is made.
func TestCompleteFileIsNotFetchedAgain(t *testing.T) {
	offer, paths, contents := tree(t, []int{3*testChunk + 7})
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	out := t.TempDir()
	offer.TransferID = p.id.String()
	runOffer(t, p.receiver(out), p.addr, offer)

	q := newOfferPair(t)
	again := q.serve(t, offer, paths)
	offer.TransferID = q.id.String()
	r := NewReceiver(out, JoinFunc(securechan.Config{Identity: q.peer, Expected: q.device.Fingerprint}, q.id))
	r.Policy = engine.PolicySkip
	if res := runOffer(t, r, q.addr, offer); res[0].State != ItemDone {
		t.Fatalf("second run: %+v", res)
	}
	if served := again.Progress().Bytes; served != 0 {
		t.Fatalf("the sender served %d bytes for a file already received", served)
	}
	checkFiles(t, out, offer, contents, nil)
	if entries, _ := os.ReadDir(filepath.Dir(filepath.Join(out, filepath.FromSlash(offer.Items[0].Path)))); len(entries) != 1 {
		t.Fatalf("a copy was made: %v", entries)
	}
}

// A file of the user under the offered name is never touched: the default policy saves the
// received file as "name (1).ext".
func TestNameTakenIsRenamed(t *testing.T) {
	offer, paths, contents := tree(t, []int{testChunk + 3})
	out := t.TempDir()
	mine := filepath.Join(out, filepath.FromSlash(offer.Items[0].Path))
	if err := os.MkdirAll(filepath.Dir(mine), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mine, []byte("the user's own file"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	offer.TransferID = p.id.String()
	r := p.receiver(out)
	r.Policy = engine.PolicyAutoRename
	if res := runOffer(t, r, p.addr, offer); res[0].State != ItemDone {
		t.Fatalf("run: %+v", res)
	}
	if got, _ := os.ReadFile(mine); string(got) != "the user's own file" {
		t.Fatalf("the user's file was changed: %q", got)
	}
	ext := filepath.Ext(mine)
	renamed := mine[:len(mine)-len(ext)] + " (1)" + ext
	got, err := os.ReadFile(renamed)
	if err != nil || !bytes.Equal(got, contents[0]) {
		t.Fatalf("received file not saved as %s (%v)", filepath.Base(renamed), err)
	}
}
