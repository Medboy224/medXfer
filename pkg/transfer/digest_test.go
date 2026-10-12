package transfer

// The end of an item (WP-4.4, bible §8.3): the receiver compares the digest of its chunk
// hashes with the sender's, and the sender reports a source that changed (XFER-07).

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/protocol"
)

// withMTimes puts the modification time of each source in the offer, as BuildOffer does.
func withMTimes(t *testing.T, offer protocol.Offer, paths []string) protocol.Offer {
	t.Helper()
	offer.Items = append([]protocol.OfferItem(nil), offer.Items...)
	for i, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		offer.Items[i].MTime = fi.ModTime().UnixNano()
	}
	return offer
}

func touch(t *testing.T, path string) {
	t.Helper()
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

func sourceChanged(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SOURCE_CHANGED")
}

// XFER-07: a source whose date no longer matches the offer is refused when the sender opens
// it; nothing is written (#75).
func TestSourceChangedBeforeSending(t *testing.T) {
	offer, paths, _ := tree(t, []int{3 * testChunk})
	offer = withMTimes(t, offer, paths)
	touch(t, paths[0])
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	out := t.TempDir()
	offer.TransferID = p.id.String()
	res := runOffer(t, p.receiver(out), p.addr, offer)
	if res[0].State != ItemFailed || !sourceChanged(res[0].Err) {
		t.Fatalf("result %+v, want failed with SOURCE_CHANGED", res[0])
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("the receive folder is not empty: %v", entries)
	}
}

// XFER-07: a source changed while it is being sent is caught before the digest: the item fails
// with SOURCE_CHANGED instead of mixing two versions.
func TestSourceChangedDuringSending(t *testing.T) {
	offer, paths, _ := tree(t, []int{12 * testChunk})
	offer = withMTimes(t, offer, paths)
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	out := t.TempDir()
	offer.TransferID = p.id.String()
	r := p.receiver(out)
	v := newValve(3 * testChunk) // the sender has the file open, most chunks are still to come
	r.dial = v.dial
	done := make(chan []ItemResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		res, _ := r.Run(ctx, p.addr, offer)
		done <- res
	}()
	v.wait(t)
	touch(t, paths[0])
	v.open()
	res := <-done
	if res[0].State != ItemFailed || !sourceChanged(res[0].Err) {
		t.Fatalf("result %+v, want failed with SOURCE_CHANGED", res[0])
	}
}

// Bible §8.3: a partial file altered on disk between two sessions is caught by the digest and
// received again from zero; the file ends up whole.
func TestTamperedPartialFileStartsOver(t *testing.T) {
	offer, paths, contents := tree(t, []int{12 * testChunk})
	offer = withMTimes(t, offer, paths)
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	out := t.TempDir()
	offer.TransferID = p.id.String()

	first := p.receiver(out)
	first.Workers = 1 // chunks 0, 1, 2 in order: chunk 0 is surely on disk
	v := newValve(3 * testChunk)
	first.dial = v.dial
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-v.reached; cancel() }()
	if _, err := first.Run(ctx, p.addr, offer); !errors.Is(err, context.Canceled) {
		t.Fatalf("first run: %v, want context.Canceled", err)
	}

	// The first session left a partial file to resume: without the digest, the second would
	// not ask for those chunks again.
	it := offer.Items[0]
	if resumed, _ := engine.PeekResumeOffset(out, it.Path, it.FileID, it.Size, offer.ChunkSize); resumed < testChunk {
		t.Fatalf("the first session left %d bytes to resume; the test needs chunk 0 at least", resumed)
	}
	// Chunk 0, already on disk and marked done in the state, is altered.
	part := filepath.Join(out, filepath.FromSlash(it.Path))
	f, err := os.OpenFile(part, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("tampered"), 100); err != nil {
		t.Fatal(err)
	}
	f.Close()

	q := newOfferPair(t)
	again := q.serve(t, offer, paths)
	offer.TransferID = q.id.String()
	r := q.receiver(out)
	if res := runOffer(t, r, q.addr, offer); res[0].State != ItemDone {
		t.Fatalf("second run: %+v", res[0])
	}
	checkFiles(t, out, offer, contents, nil)
	if served := again.Progress().Bytes; served != offer.TotalBytes {
		t.Fatalf("the second sender served %d bytes; the file had to start over (%d)", served, offer.TotalBytes)
	}
}

// A sender whose digest never matches: the item starts over once, then fails.
func TestDigestMismatchTwiceFails(t *testing.T) {
	offer, _, contents := tree(t, []int{3 * testChunk})
	p := newOfferPair(t)
	offer.TransferID = p.id.String()
	var digests atomic.Int32
	go func() {
		for {
			c, err := p.tl.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				limits := protocol.Limits{
					protocol.TypeChunkRequest:  protocol.ChunkRequestLen,
					protocol.TypeDigestRequest: protocol.DigestRequestLen,
				}
				for {
					h, pl, err := protocol.ReadFrame(c, limits, nil)
					if err != nil {
						return
					}
					var out []byte
					if h.Type == protocol.TypeDigestRequest {
						item, _ := protocol.ParseDigestRequest(pl)
						digests.Add(1)
						out = protocol.AppendDigest(nil, item, [32]byte{1}) // never the right one
					} else {
						item, index, _ := protocol.ParseChunkRequest(pl)
						data := contents[item]
						off := int(index) * int(offer.ChunkSize)
						chunk := data[off:min(off+int(offer.ChunkSize), len(data))]
						out = protocol.AppendChunkData(nil, item, index, sha256.Sum256(chunk), chunk)
					}
					if _, err := c.Write(out); err != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { p.tl.Close() })
	res := runOffer(t, p.receiver(t.TempDir()), p.addr, offer)
	if res[0].State != ItemFailed || res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "digest") {
		t.Fatalf("result %+v, want failed on the digest", res[0])
	}
	if n := digests.Load(); n != 2 {
		t.Fatalf("%d digests asked; want 2 (the file starts over once)", n)
	}
}
