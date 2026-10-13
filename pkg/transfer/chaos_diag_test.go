//go:build diag

package transfer

// Chaos (bible §14.2; WP-4.10 of the J4b plan). Each iteration receives a small folder (small
// files, an empty one, a file of several chunks) into a destination that already holds files
// of the user, injects ONE fault drawn from its seed, then resumes until the end:
//
//   - kill: the receiver, a process of its own, stops dead in the middle of the transfer
//     (os.Exit in a read: nothing is closed, as with kill -9);
//   - netcut: data connections cut in the middle of a chunk (faultconn);
//   - diskfull: the disk fills up, the transfer pauses, then goes on (faultfs);
//   - torn: a write torn in half (faultfs);
//   - powercut: fsync lies, then the power goes: what the partial files never really synced is
//     scrambled (faultfs).
//
// After the fault, then after the end, it checks that every final file equals its source,
// that nothing is outside the destination, and that the user's files are untouched; at the
// end, that no partial file, journal or marker is left.
//
// 50 iterations by default (CI); MEDXFER_CHAOS_ITER=1000 locally. MEDXFER_CHAOS_SEED replays
// a failed seed: the faults and their parameters follow from it (not the timing).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag/faultconn"
	"github.com/Medboy224/medXfer/pkg/diag/faultfs"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/store"
)

const (
	chaosSeed  = 20261013
	chaosIters = 50
)

var chaosFaults = []string{"kill", "netcut", "diskfull", "torn", "powercut"}

// chaosEnv is an iteration's: the two devices, shared by the iterations, and the sender's
// hub, its own. A hub limits failed handshakes per address (LIM-01: 5 a minute), which the
// faults of an iteration cause (at most 4: one per data connection); shared by iterations
// half a second apart, they would add up and lock the receiver out.
type chaosEnv struct {
	device, peer *identity.Identity
	peerDir      string
	h            *Hub
	addr         string
}

func envInt(t *testing.T, name string, def uint64) uint64 {
	t.Helper()
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		t.Fatalf("%s=%q: %v", name, s, err)
	}
	return v
}

func TestChaos(t *testing.T) {
	seed := envInt(t, "MEDXFER_CHAOS_SEED", chaosSeed)
	iters := envInt(t, "MEDXFER_CHAOS_ITER", chaosIters)
	device := newID(t)
	peerDir := t.TempDir()
	peer, err := identity.LoadOrCreate(peerDir)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for i := uint64(0); i < iters; i++ {
		s := seed + i
		var fault string
		ok := t.Run(fmt.Sprintf("seed=%d", s), func(t *testing.T) {
			h, addr := startHub(t, device)
			env := &chaosEnv{device: device, peer: peer, peerDir: peerDir, h: h, addr: addr}
			env.iteration(t, s, &fault)
		})
		counts[fault]++
		if !ok {
			t.Fatalf("chaos failed at seed %d (%s): replay with MEDXFER_CHAOS_SEED=%d MEDXFER_CHAOS_ITER=1", s, fault, s)
		}
	}
	t.Logf("%d iterations from seed %d: %v", iters, seed, counts)
}

// chaosBatch is the folder an iteration sends, and what each item holds.
type chaosBatch struct {
	offer    protocol.Offer
	files    []string
	contents map[string][]byte // by item path ("folder/...")
}

func makeBatch(t *testing.T, rng *rand.Rand) chaosBatch {
	t.Helper()
	folder := filepath.Join(t.TempDir(), "folder")
	fill := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(rng.Uint32())
		}
		return b
	}
	sizes := map[string]int{"empty.bin": 0, filepath.Join("sub", "big.bin"): (3+rng.IntN(4))*testChunk + rng.IntN(testChunk)}
	for k := 0; k < 1+rng.IntN(6); k++ {
		sizes[fmt.Sprintf("s%02d.bin", k)] = 1 + rng.IntN(4000)
	}
	for name, size := range sizes {
		writeUserFile(t, filepath.Join(folder, name), string(fill(size)))
	}
	offer, files, err := BuildOffer([]string{folder}, testChunk)
	if err != nil {
		t.Fatal(err)
	}
	b := chaosBatch{offer: offer, files: files, contents: map[string][]byte{}}
	for i, it := range offer.Items {
		data, err := os.ReadFile(files[i])
		if err != nil {
			t.Fatal(err)
		}
		b.contents[it.Path] = data
	}
	return b
}

// serve registers a new transfer of the batch for the peer and serves it.
func (e *chaosEnv) serve(t *testing.T, b chaosBatch) protocol.Offer {
	t.Helper()
	id, _ := NewID()
	tl, err := e.h.Register(id, e.peer.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	offer := b.offer
	offer.TransferID = id.String()
	s, err := NewSender(offer, b.files)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Serve(ctx, tl) }()
	t.Cleanup(func() { cancel(); <-done; tl.Close() })
	return offer
}

func (e *chaosEnv) receiver(out string, offer protocol.Offer) *Receiver {
	id, _ := ParseID(offer.TransferID)
	r := NewReceiver(out, JoinFunc(securechan.Config{Identity: e.peer, Expected: e.device.Fingerprint}, id))
	r.SenderKey = e.device.Fingerprint
	return r
}

func runFor(r *Receiver, addr string, offer protocol.Offer, within time.Duration) ([]ItemResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	return r.Run(ctx, addr, offer)
}

// iteration runs one seed; fault says which.
func (e *chaosEnv) iteration(t *testing.T, seed uint64, fault *string) {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	b := makeBatch(t, rng)
	parent := t.TempDir()
	out := filepath.Join(parent, "out")
	user := map[string]string{"keep.txt": "the user's file", filepath.Join("folder", "mine.txt"): "the user's folder"}
	for name, content := range user {
		writeUserFile(t, filepath.Join(out, name), content)
	}
	*fault = chaosFaults[rng.IntN(len(chaosFaults))]
	budget := 1 + rng.Int64N(b.offer.TotalBytes) // bytes before a kill or a power cut
	t.Logf("fault %s, budget %d of %d bytes, %d items", *fault, budget, b.offer.TotalBytes, len(b.offer.Items))

	switch *fault {
	case "kill":
		e.kill(t, b, out, budget)
	case "netcut":
		e.netcut(t, b, out, rng)
	case "diskfull":
		e.diskFull(t, b, out, rng)
	case "torn":
		e.torn(t, b, out, rng)
	case "powercut":
		e.powerCut(t, b, out, budget)
	}
	checkChaos(t, parent, out, user, b, false)

	// Resume until the end, unless the fault let it end: a few clean sessions, each a new
	// transfer of the same files. A complete root folder is the user's: a new transfer of the
	// same files would rightly go to "folder (2)".
	done := complete(out, b)
	for attempt := 0; attempt < 4 && !done; attempt++ {
		offer := e.serve(t, b)
		res, err := runFor(e.receiver(out, offer), e.addr, offer, time.Minute)
		done = err == nil
		for _, r := range res {
			done = done && r.State == ItemDone
		}
		if !done {
			t.Logf("resume %d: %v %+v", attempt+1, err, res)
		}
	}
	if !done {
		t.Fatal("the transfer never completed after the fault")
	}
	checkChaos(t, parent, out, user, b, true)
}

// complete reports whether every file of b is in its root folder, and its marker gone.
func complete(out string, b chaosBatch) bool {
	root := filepath.Join(out, "folder (1)")
	if _, err := os.Stat(filepath.Join(root, store.MarkerName)); err == nil {
		return false
	}
	for item := range b.contents {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(item, "folder/")))); err != nil {
			return false
		}
	}
	return true
}

// checkChaos checks the destination: nothing outside it, the user's files untouched, every
// final file of the transfer complete. With end, every file is there and nothing partial is
// left.
func checkChaos(t *testing.T, parent, out string, user map[string]string, b chaosBatch, end bool) {
	t.Helper()
	if entries, _ := os.ReadDir(parent); len(entries) != 1 || entries[0].Name() != "out" {
		t.Fatalf("something outside the destination: %v", entries)
	}
	for name, content := range user {
		if got := readFile(t, filepath.Join(out, name)); got != content {
			t.Fatalf("the user's %s was changed: %q", name, got)
		}
	}
	root := filepath.Join(out, "folder (1)")
	seen := map[string]bool{}
	err := filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(out, path)
		if _, ok := user[rel]; ok {
			return nil
		}
		name := d.Name()
		partial := strings.HasSuffix(name, store.PartSuffix) || strings.HasSuffix(name, store.JournalSuffix) || name == store.MarkerName
		if partial {
			if end {
				return fmt.Errorf("%s left after the transfer completed", rel)
			}
			return nil
		}
		inRoot, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(inRoot, "..") {
			return fmt.Errorf("%s: a file outside the transfer's root folder", rel)
		}
		item := "folder/" + filepath.ToSlash(inRoot)
		want, ok := b.contents[item]
		if !ok {
			return fmt.Errorf("%s: not a file of the transfer", rel)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, want) {
			return fmt.Errorf("%s has its final name but differs from the source (%d bytes, want %d)", rel, len(got), len(want))
		}
		seen[item] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if end && len(seen) != len(b.contents) {
		t.Fatalf("%d of %d files received", len(seen), len(b.contents))
	}
}

// kill receives in a process of its own, which stops dead once budget bytes came in.
func (e *chaosEnv) kill(t *testing.T, b chaosBatch, out string, budget int64) {
	offer := e.serve(t, b)
	cfg := chaosChild{Addr: e.addr, IdentityDir: e.peerDir, Device: e.device.Fingerprint.String(),
		Offer: offer, Out: out, Budget: budget}
	data, _ := json.Marshal(cfg)
	path := filepath.Join(t.TempDir(), "child.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestChaosChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), "MEDXFER_CHAOS_CHILD="+path)
	outb, err := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 && code != chaosKilled {
		t.Fatalf("the receiver process ended with %d (%v):\n%s", code, err, outb)
	}
}

// chaosKilled is the exit status of a receiver process stopped in the middle.
const chaosKilled = 3

type chaosChild struct {
	Addr, IdentityDir, Device, Out string
	Offer                          protocol.Offer
	Budget                         int64
}

// TestChaosChild is the receiver of a "kill" iteration, in a process of its own, started by
// TestChaos: it exits in the middle of a read once its budget came in. Nothing is closed,
// committed nor renamed: the files are as a kill -9 leaves them.
func TestChaosChild(t *testing.T) {
	path := os.Getenv("MEDXFER_CHAOS_CHILD")
	if path == "" {
		t.Skip("run by TestChaos")
	}
	var cfg chaosChild
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &cfg)
	}
	if err != nil {
		t.Fatal(err)
	}
	peer, err := identity.LoadOrCreate(cfg.IdentityDir)
	if err != nil {
		t.Fatal(err)
	}
	device, err := identity.ParseFingerprint(cfg.Device)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := ParseID(cfg.Offer.TransferID)
	r := NewReceiver(cfg.Out, JoinFunc(securechan.Config{Identity: peer, Expected: device}, id))
	r.SenderKey = device
	var read atomic.Int64
	r.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		c, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &budgetConn{Conn: c, read: &read, budget: cfg.Budget}, nil
	}
	_, _ = runFor(r, cfg.Addr, cfg.Offer, time.Minute)
}

// budgetConn ends the process once its connections read budget bytes in all.
type budgetConn struct {
	net.Conn
	read   *atomic.Int64
	budget int64
}

func (c *budgetConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if c.read.Add(int64(n)) >= c.budget {
		os.Exit(chaosKilled)
	}
	return n, err
}

// netcut cuts the first one to three data connections after a few bytes each, in the middle of
// a chunk; the receiver reconnects.
func (e *chaosEnv) netcut(t *testing.T, b chaosBatch, out string, rng *rand.Rand) {
	cuts := int32(1 + rng.IntN(3))
	after := 1 + rng.Int64N(b.offer.TotalBytes/2+1)
	var n atomic.Int32
	restore := engine.SetConnWrapper(func(role engine.ConnRole, c net.Conn) net.Conn {
		if role != engine.ConnDialed || n.Add(1) > cuts {
			return c
		}
		return faultconn.Wrap(c, faultconn.Plan{CutAfter: after, Reset: rng.IntN(2) == 0})
	})
	defer restore()
	offer := e.serve(t, b)
	_, _ = runFor(e.receiver(out, offer), e.addr, offer, 30*time.Second)
}

// fullDiskEvents tells when the disk filled up.
type fullDiskEvents struct {
	recorder
	full chan struct{}
	once sync.Once
}

func (f *fullDiskEvents) OnDiskFull(error) { f.once.Do(func() { close(f.full) }) }

// diskFull fills the disk once a partial file has room bytes; the transfer pauses, then goes
// on once there is room.
func (e *chaosEnv) diskFull(t *testing.T, b chaosBatch, out string, rng *rand.Rand) {
	room := 1 + rng.Int64N(4*testChunk)
	var full atomic.Bool
	full.Store(true)
	restore := engine.SetStorageWrapper(func(f engine.StorageFile, path string) engine.StorageFile {
		if !full.Load() || !strings.HasSuffix(path, store.PartSuffix) {
			return f
		}
		return faultfs.Wrap(f, faultfs.Plan{NoSpaceAfter: room})
	})
	defer restore()
	offer := e.serve(t, b)
	r := e.receiver(out, offer)
	ev := &fullDiskEvents{full: make(chan struct{})}
	r.Events = ev
	ended := make(chan struct{})
	go func() { defer close(ended); _, _ = runFor(r, e.addr, offer, 30*time.Second) }()
	select {
	case <-ev.full:
		full.Store(false)
		r.Resume()
	case <-ended:
	}
	<-ended
}

// torn tears one write of each partial file that crosses at bytes; its item fails.
func (e *chaosEnv) torn(t *testing.T, b chaosBatch, out string, rng *rand.Rand) {
	at := 1 + rng.Int64N(3*testChunk)
	restore := engine.SetStorageWrapper(func(f engine.StorageFile, path string) engine.StorageFile {
		if !strings.HasSuffix(path, store.PartSuffix) {
			return f
		}
		return faultfs.Wrap(f, faultfs.Plan{TornWriteAt: at})
	})
	defer restore()
	offer := e.serve(t, b)
	_, _ = runFor(e.receiver(out, offer), e.addr, offer, 30*time.Second)
}

// powerCut makes the partial files' fsync lie, stops the transfer once budget bytes came in,
// then scrambles what they never really synced: the power went. A file already renamed is
// left as it is: its fsync lied before its rename, a loss no receiver can see (a known limit:
// a lying disk is beyond what medXfer can protect).
func (e *chaosEnv) powerCut(t *testing.T, b chaosBatch, out string, budget int64) {
	type wrapped struct {
		f    *faultfs.File
		path string
	}
	var mu sync.Mutex
	var files []wrapped
	restore := engine.SetStorageWrapper(func(f engine.StorageFile, path string) engine.StorageFile {
		if !strings.HasSuffix(path, store.PartSuffix) {
			return f
		}
		w := faultfs.Wrap(f, faultfs.Plan{LyingSync: true})
		mu.Lock()
		files = append(files, wrapped{w, filepath.Join(out, path)})
		mu.Unlock()
		return w
	})
	offer := e.serve(t, b)
	r := e.receiver(out, offer)
	v := newValve(budget)
	r.dial = v.dial
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	go func() {
		select {
		case <-v.reached:
			cancel()
		case <-ctx.Done():
		}
	}()
	_, _ = r.Run(ctx, e.addr, offer)
	cancel()
	restore()
	garbage := []byte("lost in the power cut")
	for _, w := range files {
		f, err := os.OpenFile(w.path, os.O_WRONLY, 0)
		if err != nil {
			continue // renamed, or never made
		}
		for _, rg := range w.f.Unsynced() {
			for off := rg[0]; off < rg[1]; off += int64(len(garbage)) {
				_, _ = f.WriteAt(garbage[:min(int64(len(garbage)), rg[1]-off)], off)
			}
		}
		f.Close()
	}
}
