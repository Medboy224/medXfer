package transfer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/securechan"
)

const testChunk = protocol.MinChunkSize // 256 KiB: several chunks without big files

// tree writes files of the given sizes under a fresh folder and returns the offer, the full
// paths and the contents.
func tree(t *testing.T, sizes []int) (protocol.Offer, []string, [][]byte) {
	t.Helper()
	root := t.TempDir()
	offer := protocol.Offer{Root: "folder", ChunkSize: testChunk}
	var paths []string
	var contents [][]byte
	for i, size := range sizes {
		data := make([]byte, size)
		_, _ = rand.Read(data)
		rel := fmt.Sprintf("folder/d%d/f%04d.bin", i%7, i)
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatal(err)
		}
		offer.Items = append(offer.Items, protocol.OfferItem{Item: i, Path: rel, Size: int64(size)})
		offer.TotalBytes += int64(size)
		paths = append(paths, full)
		contents = append(contents, data)
	}
	return offer, paths, contents
}

// offerPair registers a transfer on a hub and returns the transfer's listener, the receiving
// device and the address to pull from.
type offerPair struct {
	device, peer *identity.Identity
	h            *Hub
	tl           *Listener
	addr         string
	id           ID
}

func newOfferPair(t *testing.T) *offerPair {
	t.Helper()
	device, peer := newID(t), newID(t)
	h, addr := startHub(t, device)
	id, _ := NewID()
	tl, err := h.Register(id, peer.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	return &offerPair{device: device, peer: peer, h: h, tl: tl, addr: addr, id: id}
}

func (p *offerPair) serve(t *testing.T, offer protocol.Offer, paths []string) *Sender {
	t.Helper()
	offer.TransferID = p.id.String()
	s, err := NewSender(offer, paths)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Serve(ctx, p.tl) }()
	t.Cleanup(func() { cancel(); <-done })
	return s
}

func (p *offerPair) receiver(out string) *Receiver {
	r := NewReceiver(out, JoinFunc(securechan.Config{Identity: p.peer, Expected: p.device.Fingerprint}, p.id))
	r.SenderKey = p.device.Fingerprint // part of the resume key (D2)
	return r
}

// again is a new transfer between the same two devices, on the same hub: its partial files
// are resumed (the resume key holds the sender's key, D2).
func (p *offerPair) again(t *testing.T) *offerPair {
	t.Helper()
	id, _ := NewID()
	tl, err := p.h.Register(id, p.peer.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	return &offerPair{device: p.device, peer: p.peer, h: p.h, tl: tl, addr: p.addr, id: id}
}

// recorder keeps the events of a receiver.
type recorder struct {
	mu       sync.Mutex
	items    []ItemEvent
	progress []Progress
	onItem   func(ItemEvent)
}

func (r *recorder) OnProgress(p Progress) {
	r.mu.Lock()
	r.progress = append(r.progress, p)
	r.mu.Unlock()
}

func (r *recorder) OnItem(e ItemEvent) {
	r.mu.Lock()
	r.items = append(r.items, e)
	f := r.onItem
	r.mu.Unlock()
	if f != nil {
		f(e)
	}
}

func checkFiles(t *testing.T, out string, offer protocol.Offer, contents [][]byte, only map[int]bool) {
	t.Helper()
	for i, it := range offer.Items {
		if only != nil && !only[i] {
			continue
		}
		got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(it.Path)))
		if err != nil || !bytes.Equal(got, contents[i]) {
			t.Fatalf("item %d (%s) differs: %v", i, it.Path, err)
		}
	}
}

// A folder of many files of every awkward size in ONE offer: empty, one byte, around a chunk
// boundary, several chunks. Each chunk is checked against its SHA-256; progress never goes
// past the total, on either side (#33).
func TestOfferOfManyFiles(t *testing.T) {
	base := []int{0, 1, 1000, testChunk - 1, testChunk, testChunk + 1, 3*testChunk + 5}
	var sizes []int
	for i := 0; i < 300; i++ {
		s := base[i%len(base)]
		if i%len(base) == 2 {
			s += i // small files of different sizes
		}
		sizes = append(sizes, s)
	}
	offer, paths, contents := tree(t, sizes)
	p := newOfferPair(t)
	sender := p.serve(t, offer, paths)
	out := t.TempDir()
	r := p.receiver(out)
	rec := &recorder{}
	r.Events = rec
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	results, err := r.Run(ctx, p.addr, offer)
	if err != nil {
		t.Fatal(err)
	}
	for _, res := range results {
		if res.State != ItemDone {
			t.Fatalf("item %d: %v", res.Item, res.State)
		}
	}
	checkFiles(t, out, offer, contents, nil)
	for _, pr := range rec.progress {
		if pr.Bytes > pr.Total {
			t.Fatalf("receiver progress past the total: %+v", pr)
		}
	}
	if last := rec.progress[len(rec.progress)-1]; last.Bytes != offer.TotalBytes || last.ItemsDone != len(sizes) {
		t.Fatalf("final progress %+v, want %d bytes and %d items", last, offer.TotalBytes, len(sizes))
	}
	if got := sender.Progress().Bytes; got != offer.TotalBytes {
		t.Fatalf("sender progress %d, want %d", got, offer.TotalBytes)
	}
}

// #33: a chunk served twice (a request repeated after a lost connection) is counted once.
func TestSenderCountsEachChunkOnce(t *testing.T) {
	offer, paths, _ := tree(t, []int{2*testChunk + 10})
	p := newOfferPair(t)
	sender := p.serve(t, offer, paths)
	raw, err := net.Dial("tcp4", p.addr)
	if err != nil {
		t.Fatal(err)
	}
	c, err := JoinFunc(securechan.Config{Identity: p.peer, Expected: p.device.Fingerprint}, p.id)(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	limits := protocol.Limits{protocol.TypeChunkData: protocol.ChunkDataHeaderLen + testChunk}
	for _, index := range []uint32{0, 0, 2, 0} {
		if _, err := c.Write(protocol.AppendChunkRequest(nil, 0, index)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := protocol.ReadFrame(c, limits, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := sender.Progress().Bytes, int64(testChunk+10); got != want {
		t.Fatalf("sender counted %d bytes, want %d (chunks 0 and 2, once each)", got, want)
	}
}

// A transfer stopped half way resumes: the second run asks only for what is missing.
func TestOfferResumes(t *testing.T) {
	offer, paths, contents := tree(t, []int{24 * testChunk})
	p := newOfferPair(t)
	sender := p.serve(t, offer, paths)
	out := t.TempDir()

	first := p.receiver(out)
	v := newValve(4 * testChunk)
	first.dial = v.dial
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-v.reached; cancel() }()
	if _, err := first.Run(ctx, p.addr, offer); !errors.Is(err, context.Canceled) {
		t.Fatalf("first run: %v, want context.Canceled", err)
	}
	servedBefore := sender.Progress().Bytes
	if servedBefore >= offer.TotalBytes {
		t.Fatal("the first run was not stopped half way")
	}

	second := p.receiver(out)
	rec2 := &recorder{}
	second.Events = rec2
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Minute)
	defer cancel2()
	results, err := second.Run(ctx2, p.addr, offer)
	if err != nil || results[0].State != ItemDone {
		t.Fatalf("second run: %+v, %v", results, err)
	}
	checkFiles(t, out, offer, contents, nil)
}

// Per item: a paused item is not requested while the others are; skipping it ends the
// transfer. The whole transfer can be paused too.
func TestOfferPauseAndSkipItem(t *testing.T) {
	offer, paths, contents := tree(t, []int{testChunk + 1, 2 * testChunk, 1000})
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	out := t.TempDir()
	r := p.receiver(out)
	r.PauseItem(1)
	rec := &recorder{}
	var once sync.Once
	rec.onItem = func(e ItemEvent) {
		rec.mu.Lock()
		done := 0
		for _, ev := range rec.items {
			if ev.State == ItemDone {
				done++
			}
		}
		rec.mu.Unlock()
		if done == 2 {
			once.Do(func() { go r.SkipItem(1) })
		}
	}
	r.Events = rec
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	results, err := r.Run(ctx, p.addr, offer)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].State != ItemDone || results[1].State != ItemSkipped || results[2].State != ItemDone {
		t.Fatalf("results %+v", results)
	}
	checkFiles(t, out, offer, contents, map[int]bool{0: true, 2: true})
	if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(offer.Items[1].Path))); err == nil {
		t.Fatal("the paused item was requested")
	}
}

func TestOfferPauseWholeTransfer(t *testing.T) {
	offer, paths, contents := tree(t, []int{3 * testChunk})
	p := newOfferPair(t)
	p.serve(t, offer, paths)
	out := t.TempDir()
	r := p.receiver(out)
	r.Pause()
	rec := &recorder{}
	r.Events = rec
	done := make(chan error, 1)
	go func() {
		_, err := r.Run(context.Background(), p.addr, offer)
		done <- err
	}()
	time.Sleep(400 * time.Millisecond)
	rec.mu.Lock()
	started := len(rec.items)
	rec.mu.Unlock()
	if started != 0 {
		t.Fatal("a paused transfer requested chunks")
	}
	r.Resume()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the transfer did not finish after Resume")
	}
	checkFiles(t, out, offer, contents, nil)
}

// lyingSender answers every request of the transfer, but with a wrong SHA-256 for the first
// bad answers to chunk 0 of item 0.
// digestOf is the digest of data in chunks of chunkSize (bible §8.3).
func digestOf(data []byte, chunkSize uint32) [32]byte {
	hashes := make([][32]byte, protocol.Chunks(int64(len(data)), chunkSize))
	for i := range hashes {
		off := i * int(chunkSize)
		hashes[i] = sha256.Sum256(data[off:min(off+int(chunkSize), len(data))])
	}
	return protocol.FileDigest(hashes)
}

func lyingSender(t *testing.T, tl net.Listener, offer protocol.Offer, contents [][]byte, bad int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var mu sync.Mutex
	lies := 0
	go func() {
		<-ctx.Done()
		tl.Close()
	}()
	go func() {
		for {
			c, err := tl.Accept()
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
					h, p, err := protocol.ReadFrame(c, limits, nil)
					if err != nil {
						return
					}
					if h.Type == protocol.TypeDigestRequest { // the true digest of the content
						item, _ := protocol.ParseDigestRequest(p)
						if _, err := c.Write(protocol.AppendDigest(nil, item, digestOf(contents[item], offer.ChunkSize))); err != nil {
							return
						}
						continue
					}
					item, index, _ := protocol.ParseChunkRequest(p)
					data := contents[item]
					off := int(index) * int(offer.ChunkSize)
					end := off + int(offer.ChunkSize)
					if end > len(data) {
						end = len(data)
					}
					sum := sha256.Sum256(data[off:end])
					mu.Lock()
					if item == 0 && index == 0 && lies < bad {
						lies++
						sum[0] ^= 0xff
					}
					mu.Unlock()
					if _, err := c.Write(protocol.AppendChunkData(nil, item, index, sum, data[off:end])); err != nil {
						return
					}
				}
			}()
		}
	}()
}

// XFER-04: a chunk whose data does not match its SHA-256 is never written; it is asked again,
// and the item fails after 3 wrong answers.
func TestBadHashIsNeverWritten(t *testing.T) {
	for _, c := range []struct {
		lies int
		want ItemState
	}{{2, ItemDone}, {3, ItemFailed}} {
		offer, _, contents := tree(t, []int{2*testChunk + 3})
		p := newOfferPair(t)
		offer.TransferID = p.id.String()
		lyingSender(t, p.tl, offer, contents, c.lies)
		out := t.TempDir()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		results, err := p.receiver(out).Run(ctx, p.addr, offer)
		cancel()
		if err != nil && c.want == ItemDone {
			t.Fatal(err)
		}
		if results[0].State != c.want {
			t.Fatalf("%d wrong answers: item %v, want %v", c.lies, results[0].State, c.want)
		}
		if c.want == ItemDone {
			checkFiles(t, out, offer, contents, nil)
		}
	}
}

// LIM-05: an offer past the limits is refused before anything is requested.
func TestOfferPastTheLimitsIsRefused(t *testing.T) {
	offer := protocol.Offer{ChunkSize: testChunk, Items: make([]protocol.OfferItem, protocol.MaxOfferItems+1)}
	for i := range offer.Items {
		offer.Items[i] = protocol.OfferItem{Item: i, Path: "f"}
	}
	r := NewReceiver(t.TempDir(), nil)
	if _, err := r.Run(context.Background(), "127.0.0.1:1", offer); !errors.Is(err, protocol.ErrBadOffer) {
		t.Fatalf("Run = %v, want ErrBadOffer", err)
	}
}
