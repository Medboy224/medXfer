package transfer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/store"
)

// Defaults of the receiver (bible §8.3; parameters, rule R4).
const (
	DefaultWorkers = 4 // data connections (LIM-07: 1 to 8)
	// DefaultDialTimeout bounds each attempt to reach the sender (#48): without it, a
	// firewall that drops the packets would hold a connection attempt for minutes.
	DefaultDialTimeout = 5 * time.Second
	MaxWorkers         = 8
	maxInFlight        = 2  // requests in flight per connection (bible §8.3)
	maxChunkAttempts   = 3  // a chunk whose hash does not match is asked again at most 3 times (XFER-04)
	maxReconnects      = 16 // connections in a row without a chunk received
	chunkReadTimeout   = time.Minute
)

var ErrTransferFailed = errors.New("transfer: no data connection could be kept to the sender")

// Small files (WP-4.11, bench-results/README.md). The files received in one go in the
// transfer's own folder are made durable together (store.Batch): the batch is flushed at 256
// files or 64 MiB, 2 s after its first file, or once nothing else is left to receive
// (parameters, rule R4). An offer of small files (16 items or more, 1 MiB at most on average)
// uses SmallFileWorkers data connections, unless Receiver.Workers is set: more files in
// flight hide the cost of each.
const (
	batchFiles       = 256
	batchBytes       = store.DefaultCommitBytes
	batchWait        = store.DefaultCommitInterval
	SmallFileWorkers = MaxWorkers
)

// smallFiles reports whether offer is mostly small files.
func smallFiles(o protocol.Offer) bool {
	return len(o.Items) >= 16 && o.TotalBytes <= int64(len(o.Items))<<20
}

// chooseWorkers is the number of data connections for offer: n, within 1 to MaxWorkers, or
// for n = 0, DefaultWorkers, or SmallFileWorkers for an offer of small files.
func chooseWorkers(n int, offer protocol.Offer) int {
	switch {
	case n < 1 && smallFiles(offer):
		return SmallFileWorkers
	case n < 1:
		return DefaultWorkers
	case n > MaxWorkers:
		return MaxWorkers
	}
	return n
}

// dialDelays are the waits between attempts to reach the sender: 50 ms up to 1 s, 8 attempts
// in all (XFER-05).
var dialDelays = []time.Duration{
	50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond,
	800 * time.Millisecond, time.Second, time.Second,
}

// DefaultReconnectWindow is how long a receiver goes on trying to reach a sender it was
// receiving from, after XFER-05's attempts: a phone's Wi-Fi gone for a minute or two (WP-B.8).
// reconnectEvery is the wait between those further attempts: with MaxWorkers connections,
// fewer handshakes a minute than the sender's gate allows a known address (LIM-01, #93), so
// that a sender that lost the transfer does not block this address. Parameters (rule R4).
const DefaultReconnectWindow = 2 * time.Minute

var reconnectEvery = 5 * time.Second

// Receiver downloads an offer: it opens Workers data connections to the sender's port and
// drives the transfer (bible §8.3). It asks for the smallest missing chunk of the whole offer,
// with at most two requests in flight per connection, so several small files are in flight at
// once and memory stays bounded by Workers × 2 chunks. A chunk is written only once its
// SHA-256 matches the one the sender computed.
type Receiver struct {
	OutDir string
	// Workers is the number of data connections, 1 to MaxWorkers; 0 chooses: DefaultWorkers,
	// or SmallFileWorkers for an offer of small files.
	Workers int
	Policy  store.Policy
	// SenderKey is the sender's key, part of the resume key of every file (D2): a partial file
	// is resumed only for the same sender, path, size and date.
	SenderKey identity.Fingerprint
	// VerifyFull checks every chunk of a partial file on resume, not only the last 8 (STO-06).
	VerifyFull bool
	// Join secures each data connection and names the transfer (JoinFunc).
	Join        func(context.Context, net.Conn) (net.Conn, error)
	DialTimeout time.Duration // 0: DefaultDialTimeout
	// ReconnectWindow: once data came, the sender is tried again that long after XFER-05's
	// attempts (0: DefaultReconnectWindow; negative: not at all). A sender never reached is
	// given up at once: a firewall or a wrong address shows without waiting.
	ReconnectWindow time.Duration
	Events          Events
	// Sink is where the chunks go; SinkNull checks and drops them (a bench offer, DEV-09).
	Sink Sink
	// NoSync writes without fsync: never durable, for the bench ladder's step R4 only.
	NoSync bool

	dial func(ctx context.Context, network, addr string) (net.Conn, error) // tests

	mu          sync.Mutex
	cond        *sync.Cond
	paused      bool
	pausedItems map[int]bool
	skips       []int // asked before Run started
	run         *run
}

// NewReceiver returns a receiver that saves into outDir and secures its connections with join.
func NewReceiver(outDir string, join func(context.Context, net.Conn) (net.Conn, error)) *Receiver {
	r := &Receiver{OutDir: outDir, Join: join, pausedItems: map[int]bool{}}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// Pause stops requesting chunks, at once: the chunks in flight are dropped, and asked again on
// Resume. Letting them arrive kept both bars moving for seconds on a slow link (up to Workers
// x 2 chunks after the pause). The connections reconnect on Resume.
func (r *Receiver) Pause() {
	r.mu.Lock()
	r.paused = true
	ru := r.run
	r.mu.Unlock()
	if ru != nil {
		ru.interrupt()
	}
}

// Resume starts requesting chunks again.
func (r *Receiver) Resume() {
	r.mu.Lock()
	r.paused = false
	r.cond.Broadcast()
	r.mu.Unlock()
}

// PauseItem stops requesting the chunks of one item; the others go on.
func (r *Receiver) PauseItem(i int) {
	r.mu.Lock()
	r.pausedItems[i] = true
	r.mu.Unlock()
}

// ResumeItem requests the chunks of a paused item again.
func (r *Receiver) ResumeItem(i int) {
	r.mu.Lock()
	delete(r.pausedItems, i)
	r.cond.Broadcast()
	r.mu.Unlock()
}

// SkipItem gives up one item: nothing more is requested for it, and what was received stays
// on disk with its state, resumable later.
func (r *Receiver) SkipItem(i int) {
	r.mu.Lock()
	run := r.run
	if run == nil {
		r.skips = append(r.skips, i)
	}
	r.mu.Unlock()
	if run != nil {
		run.endItem(i, ItemSkipped, nil)
	}
}

// Run downloads offer from addr, the sender's port, and returns how each item ended. Its
// error is for the transfer as a whole: an invalid offer, ctx ended, or no connection left.
func (r *Receiver) Run(ctx context.Context, addr string, offer protocol.Offer) ([]ItemResult, error) {
	if err := offer.Validate(); err != nil {
		return nil, err
	}
	workers := chooseWorkers(r.Workers, offer)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	run := newRun(r, offer)
	run.workers = workers
	run.stages, _ = r.Events.(StageEvents)
	if r.Sink != SinkNull { // every file operation goes through the destination's root (STO-13)
		if err := os.MkdirAll(r.OutDir, 0o755); err != nil {
			return nil, err
		}
		root, err := os.OpenRoot(r.OutDir)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		run.root = root
		place, err := run.findRoot()
		if err != nil {
			return nil, err
		}
		if place.Skip { // "skip": the folder is there, the whole offer is refused (D5)
			for i := range run.items {
				run.endItem(i, ItemSkipped, fmt.Errorf("the folder %q is already there", place.Name))
			}
		}
		if run.inRoot {
			run.batch = store.NewBatch(root) // nil where the system has no syncfs
		}
	}
	r.mu.Lock()
	r.run = run
	skips := r.skips
	r.skips = nil
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.run = nil
		r.mu.Unlock()
	}()
	for _, i := range skips {
		run.endItem(i, ItemSkipped, nil)
	}
	go func() {
		<-ctx.Done()
		r.mu.Lock()
		r.cond.Broadcast()
		r.mu.Unlock()
	}()
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		run.reportProgress(ctx)
	}()

	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		go func() { errs <- run.worker(ctx, addr) }()
	}
	var firstErr error
	for w := 0; w < workers; w++ {
		if err := <-errs; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	cancel()
	<-progressDone
	run.flushBatch(true) // the complete files get their names, even on a cancel
	run.closeAll()
	run.emitProgress(0)
	if run.inRoot && run.allEnded() && !run.partial() {
		_ = store.UnmarkRoot(run.root, run.rootDir, run.rootKey) // every file is there
	}

	results := run.results()
	if run.allEnded() {
		return results, nil
	}
	if err := ctx.Err(); firstErr == nil && err != nil {
		firstErr = err
	}
	if firstErr == nil {
		firstErr = ErrTransferFailed
	}
	return results, firstErr
}

// run is the state of one Run.
type run struct {
	r     *Receiver
	offer protocol.Offer
	items []*item
	low   int // first item that may still need requests (under r.mu)
	ended int // items done, skipped or failed (under r.mu)
	// active are the items started and not ended: their progress (WP-B.0).
	activeMu sync.Mutex
	active   map[int]struct{}
	start    time.Time
	bytes    atomic.Int64
	conns    atomic.Int32
	reached  atomic.Bool // a data connection was opened in this run (ReconnectWindow)
	evMu     sync.Mutex
	speed    *speedMeter
	// received counts the bytes read from the data connections: the speed. bytes also counts
	// what a resume finds on disk and the files already there, which arrive in no time.
	received atomic.Int64
	chunks   uint32 // chunk size

	stages  StageEvents // nil: the stages are not measured
	workers int
	root    *os.Root // the destination; nil with the null sink

	// live are the data connections open: a pause interrupts their reads (Pause).
	liveMu sync.Mutex
	live   map[net.Conn]struct{}

	// The transfer's root folder (STO-10): inRoot unless it is a single file. Its items go to
	// rootDir/<path without strip>; the folder and its marker are made with the first file.
	inRoot   bool
	rootBase string
	strip    string
	rootDir  string
	rootKey  store.Key
	markOnce sync.Once
	markErr  error

	// The batch of files received in one go (WP-4.11). Lock order: st.mu, batchMu, r.mu.
	batchMu    sync.Mutex
	batch      *store.Batch // nil: each file is finalized alone
	batched    []int        // the items in batch
	batchTimer *time.Timer
}

// rootName is a name the offer proposes for its root folder, if it is one safe component;
// "medXfer" otherwise.
func rootName(proposed string) string {
	if safe, err := engine.SafeRelPath(proposed); err == nil && !strings.Contains(safe, "/") {
		return safe
	}
	return "medXfer"
}

// planRoot decides where the items of the offer go (STO-10, decision D5). A single file
// without a folder goes to the destination itself. Otherwise the items go to a root folder:
// the folder they all are in (a folder sent), or the root the offer proposes (several paths).
func (ru *run) planRoot(offer protocol.Offer) {
	items := offer.Items
	if len(items) == 1 && !strings.ContainsAny(items[0].Path, `/\`) {
		return
	}
	top := ""
	for i, it := range items {
		p := strings.ReplaceAll(it.Path, `\`, "/")
		j := strings.IndexByte(p, '/')
		if j <= 0 || (i > 0 && p[:j] != top) {
			top = ""
			break
		}
		top = p[:j]
	}
	ru.inRoot = true
	if top != "" && rootName(top) == top {
		ru.rootBase, ru.strip = top, top+"/"
	} else {
		ru.rootBase = rootName(offer.Root)
	}
	ru.rootKey = store.RootKey(ru.r.SenderKey, ru.rootBase, len(items), offer.TotalBytes)
}

// findRoot plans and resolves the transfer's root folder, without writing anything. A single
// file has none: the zero RootPlace.
func (ru *run) findRoot() (store.RootPlace, error) {
	ru.planRoot(ru.offer)
	if !ru.inRoot {
		return store.RootPlace{}, nil
	}
	place, err := store.ResolveRoot(ru.root, ru.rootBase, ru.rootKey, ru.r.Policy)
	if err != nil {
		return place, err
	}
	ru.rootDir = place.Name
	return place, nil
}

// policy is the collision policy of an item. In the transfer's own folder, a final file is
// one of this transfer, received in an earlier session: done. Nothing of the user is there to
// rename or overwrite.
func (ru *run) policy() store.Policy {
	if ru.inRoot {
		return store.PolicySkip
	}
	return ru.r.Policy
}

// itemName is where item path (checked by SafeRelPath) goes in the destination.
func (ru *run) itemName(safe string) string {
	if !ru.inRoot {
		return safe
	}
	return ru.rootDir + "/" + strings.TrimPrefix(safe, ru.strip)
}

// markRoot makes the root folder and its marker, once, before the first file is written.
func (ru *run) markRoot() error {
	if !ru.inRoot {
		return nil
	}
	ru.markOnce.Do(func() { ru.markErr = store.MarkRoot(ru.root, ru.rootDir, ru.rootKey) })
	return ru.markErr
}

// hashDelay slows every hash down; tests use it to make the hash the limiting stage (DEV-04).
var hashDelay time.Duration

// stage reports the time a stage took since start, if someone measures (DEV-03).
func (ru *run) stage(name string, parallel int, start time.Time, bytes int64) {
	if ru.stages != nil {
		ru.stages.OnStage(name, parallel, time.Since(start), bytes)
	}
}

// params are the storage parameters of item st. Its resume key ties a partial file to this
// sender, path, size and date (D2); the group commits are measured as the fsync stage.
func (ru *run) params(st *item, overwrite bool) store.Params {
	r := ru.r
	p := store.Params{
		Key:  store.ResumeKey(r.SenderKey, st.it.Path, st.it.Size, st.it.MTime),
		Size: st.it.Size, ChunkSize: ru.chunks,
		VerifyFull: r.VerifyFull, NoSync: r.NoSync, Overwrite: overwrite,
		Owned: ru.inRoot, // the root folder is this transfer's only (STO-10)
	}
	if ru.stages != nil {
		p.OnSync = func(d time.Duration) { ru.stages.OnStage(StageFsync, 1, d, 0) }
	}
	return p
}

type item struct {
	it       protocol.OfferItem
	total    uint32 // chunks; 0 for an empty file
	next     uint32 // next index to request (under r.mu)
	retries  []uint32
	attempts map[uint32]int
	state    ItemState // under r.mu once ended
	err      error     // under r.mu: why it failed

	// mu: chunks are written under the read lock, in parallel (store.File takes care of
	// concurrent writes); opening, finalizing and closing take the write lock.
	mu        sync.RWMutex
	opened    bool
	started   bool // ItemStarted was emitted
	closed    bool
	safe      string      // the item's path, checked (SafeRelPath)
	place     store.Place // where it goes, found by open
	f         *store.File // nil until the first verified chunk, unless resuming (#75)
	saved     string
	processed atomic.Uint32 // chunks handled: written, or found on disk

	// The digest (bible §8.3, step 6). hashes[i] is chunk i's SHA-256 from its verified CHUNK
	// (fresh[i]); the chunks of an earlier session keep the hash of their journal. Distinct
	// chunks are written by distinct goroutines; local is set once they are all handled.
	hashes    [][32]byte
	fresh     []bool
	local     [32]byte
	counted   atomic.Int64 // bytes of this item in the progress, given back if it starts over
	digest    bool         // under r.mu: the DIGEST_REQ waits to be sent
	restarted bool         // under r.mu: started over once after a digest mismatch
	// gen changes when the item is set aside on a full disk (under st.mu and r.mu): the
	// answers to the tasks of an earlier generation are dropped, every chunk is asked again.
	gen atomic.Uint32
}

func newRun(r *Receiver, offer protocol.Offer) *run {
	ru := &run{r: r, offer: offer, start: time.Now(), speed: newSpeedMeter(), chunks: offer.ChunkSize, active: map[int]struct{}{},
		live: map[net.Conn]struct{}{}}
	ru.items = make([]*item, len(offer.Items))
	for i, it := range offer.Items {
		total := protocol.Chunks(it.Size, offer.ChunkSize)
		ru.items[i] = &item{it: it, total: total, attempts: map[uint32]int{},
			hashes: make([][32]byte, total), fresh: make([]bool, total)}
	}
	return ru
}

type task struct {
	item  int
	index uint32
	gen   uint32 // the item's generation when the task was scheduled
}

type schedStatus int

const (
	gotTask schedStatus = iota
	nothingNow
	allEnded
)

// next is the scheduler: the smallest chunk of the offer still to request. With block, it
// waits for one (pause, items all in flight) until there is one or the transfer is over.
func (ru *run) next(ctx context.Context, block bool) (task, schedStatus) {
	r := ru.r
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		if ru.ended == len(ru.items) {
			return task{}, allEnded
		}
		if ctx.Err() != nil {
			return task{}, allEnded
		}
		if !r.paused {
			for i := ru.low; i < len(ru.items); i++ {
				st := ru.items[i]
				if st.state >= ItemDone {
					if i == ru.low {
						ru.low++
					}
					continue
				}
				if r.pausedItems[i] {
					continue
				}
				gen := st.gen.Load()
				if st.digest {
					st.digest = false
					return task{i, protocol.DigestIndex, gen}, gotTask
				}
				if n := len(st.retries); n > 0 {
					min := 0
					for k, idx := range st.retries {
						if idx < st.retries[min] {
							min = k
						}
					}
					idx := st.retries[min]
					st.retries = append(st.retries[:min], st.retries[min+1:]...)
					return task{i, idx, gen}, gotTask
				}
				sched := st.total
				if sched == 0 {
					sched = 1 // an empty file is one request: chunk 0, of 0 bytes
				}
				if st.next < sched {
					st.next++
					return task{i, st.next - 1, gen}, gotTask
				}
			}
		}
		if !block {
			return task{}, nothingNow
		}
		r.cond.Wait()
	}
}

// interrupt ends the reads waiting on every data connection: their chunks in flight are asked
// again (Pause).
func (ru *run) interrupt() {
	ru.liveMu.Lock()
	defer ru.liveMu.Unlock()
	for c := range ru.live {
		_ = c.SetReadDeadline(time.Now())
	}
}

func (ru *run) track(c net.Conn, open bool) {
	ru.liveMu.Lock()
	defer ru.liveMu.Unlock()
	if open {
		ru.live[c] = struct{}{}
	} else {
		delete(ru.live, c)
	}
}

func (ru *run) paused() bool {
	ru.r.mu.Lock()
	defer ru.r.mu.Unlock()
	return ru.r.paused
}

// waitResumed waits while the transfer is paused, and reports whether it was.
func (ru *run) waitResumed(ctx context.Context) bool {
	r := ru.r
	r.mu.Lock()
	defer r.mu.Unlock()
	was := r.paused
	for r.paused && ctx.Err() == nil {
		r.cond.Wait()
	}
	return was
}

// retry schedules a chunk again. A chunk with a wrong hash counts as an attempt (XFER-04); a
// lost connection does not.
func (ru *run) retry(t task, badHash bool) {
	st := ru.items[t.item]
	r := ru.r
	r.mu.Lock()
	if st.state >= ItemDone || t.gen != st.gen.Load() {
		r.mu.Unlock()
		return
	}
	if badHash {
		st.attempts[t.index]++
		if st.attempts[t.index] >= maxChunkAttempts {
			r.mu.Unlock()
			ru.endItem(t.item, ItemFailed, fmt.Errorf("chunk %d of %q: the data never matched its SHA-256", t.index, st.it.Path))
			return
		}
	}
	st.retries = append(st.retries, t.index)
	r.cond.Broadcast()
	r.mu.Unlock()
}

// prepare opens the item's file if needed and handles what needs no request: an item that
// already ended, an empty file, a chunk already on disk (resume). It reports whether the task
// is done with.
func (ru *run) prepare(t task) bool {
	st := ru.items[t.item]
	st.mu.Lock()
	if st.closed || t.gen != st.gen.Load() {
		st.mu.Unlock()
		return true
	}
	if !st.opened {
		if err := ru.open(st); err != nil {
			st.mu.Unlock()
			ru.endItem(t.item, ItemFailed, err)
			return true
		}
		if st.closed { // already complete on disk
			st.mu.Unlock()
			return true
		}
	}
	// An empty item is requested too: the sender's answer confirms it before it is made (#75).
	onDisk := st.total > 0 && st.f != nil && st.f.Has(t.index)
	st.mu.Unlock()
	if onDisk {
		if ru.countChunk(st) {
			ru.complete(t.item)
		}
		return true
	}
	return false
}

// open finds where the item goes before its first request, and writes nothing (#75): a safe
// path, then the same file already complete, the partial file of a resume (reopened, it
// exists already), or a free name for later. Called with st.mu held.
func (ru *run) open(st *item) error {
	r := ru.r
	if r.Sink == SinkNull { // nothing goes to disk: no name to find
		st.opened = true
		ru.emitItem(ItemEvent{Item: st.it.Item, Path: st.it.Path, Size: st.it.Size, State: ItemStarted})
		return nil
	}
	safe, err := engine.SafeRelPath(st.it.Path)
	if err != nil {
		return err
	}
	place, err := store.Resolve(ru.root, ru.itemName(safe), ru.params(st, false), ru.policy())
	if err != nil {
		return err
	}
	st.opened = true
	st.safe = safe
	st.place = place
	st.saved = filepath.Join(r.OutDir, filepath.FromSlash(place.Name))
	if !st.started { // once: an item set aside on a full disk is opened again
		st.started = true
		ru.activeMu.Lock()
		ru.active[st.it.Item] = struct{}{} // in progress until it ends (WP-B.0)
		ru.activeMu.Unlock()
		ru.emitItem(ItemEvent{Item: st.it.Item, Path: st.it.Path, Size: st.it.Size, State: ItemStarted})
	}
	if place.Done { // PolicySkip: a file of that name is already there
		ru.bytes.Add(st.it.Size)
		st.closed = true
		ru.endItem(st.it.Item, ItemDone, nil) // ItemDone does not take st.mu
		return nil
	}
	// An item set aside on a full disk still counts what it received, until now: it counts
	// again only what is durable.
	var durable int64
	if place.Resume { // its own partial file, which exists already
		f, err := store.Open(ru.root, place.Name, ru.params(st, place.Overwrite))
		if err != nil {
			return err
		}
		st.f = f
		durable = f.Resumed()
	}
	ru.bytes.Add(durable - st.counted.Swap(durable))
	return nil
}

// create makes the item's .part and journal on its first verified chunk (#75); an empty item
// has one, of 0 bytes, which the sender's answer confirms. Until then a refused connection, a
// silent or lying sender or a cancel leaves nothing behind. A file of the user is never
// touched before the end: the bytes go to the .part, and Finalize does not replace a file
// that took the name meanwhile unless the policy is to overwrite (STO-01). Called with st.mu
// held.
func (ru *run) create(st *item) error {
	if err := ru.markRoot(); err != nil {
		return err
	}
	f, err := store.Open(ru.root, st.place.Name, ru.params(st, st.place.Overwrite))
	if err != nil {
		return err
	}
	st.f = f
	return nil
}

// countChunk records one more chunk of st handled; it reports whether that was the last (true
// once: every chunk is handled once). An empty file is one task.
func (ru *run) countChunk(st *item) bool {
	target := st.total
	if target == 0 {
		target = 1
	}
	return st.processed.Add(1) == target
}

// write stores a verified chunk and keeps its hash. Called by the worker that received it.
func (ru *run) write(t task, data []byte, sum [32]byte) error {
	st := ru.items[t.item]
	if ru.r.Sink == SinkNull { // checked, then dropped: only its hash is kept, for the digest
		st.mu.RLock()
		first := !st.closed && (st.total == 0 || !st.fresh[t.index]) // an empty item: chunk 0, of 0 bytes
		if first && st.total > 0 {
			st.hashes[t.index], st.fresh[t.index] = sum, true
			ru.bytes.Add(int64(len(data)))
			st.counted.Add(int64(len(data)))
		}
		st.mu.RUnlock()
		if first && ru.countChunk(st) {
			ru.complete(t.item)
		}
		return nil
	}
	st.mu.RLock()
	if t.gen != st.gen.Load() {
		st.mu.RUnlock()
		return nil // set aside on a full disk meanwhile: asked again
	}
	if st.f == nil && !st.closed { // the item's first verified chunk: its file is made now
		st.mu.RUnlock()
		st.mu.Lock()
		var err error
		if st.f == nil && !st.closed && t.gen == st.gen.Load() {
			err = ru.create(st)
		}
		st.mu.Unlock()
		if err != nil {
			ru.writeFailed(t, err)
			return nil
		}
		st.mu.RLock()
	}
	if st.closed || st.f == nil || t.gen != st.gen.Load() {
		st.mu.RUnlock()
		return nil // skipped, failed or set aside meanwhile
	}
	if st.total > 0 && !st.f.Has(t.index) {
		start := time.Now()
		err := st.f.WriteChunk(t.index, data, sum)
		ru.stage(StageDiskWrite, ru.workers, start, int64(len(data)))
		if err == nil {
			err = st.f.CommitIfDue() // its fsync is measured as the fsync stage
		}
		if err != nil {
			st.mu.RUnlock()
			ru.writeFailed(t, err)
			return nil
		}
		ru.bytes.Add(int64(len(data)))
		st.counted.Add(int64(len(data)))
	}
	if t.index < st.total {
		st.hashes[t.index], st.fresh[t.index] = sum, true
	}
	st.mu.RUnlock()
	if ru.countChunk(st) {
		ru.complete(t.item)
	}
	return nil
}

// writeFailed ends the item whose chunk t could not be written, unless the disk is full: then
// the transfer pauses (STO-07).
func (ru *run) writeFailed(t task, err error) {
	if engine.IsDiskFull(err) {
		ru.diskFull(t.item, t.gen, err)
		return
	}
	ru.endItem(t.item, ItemFailed, err)
}

// diskFull pauses the transfer when the disk is full (STO-07, bible §9.7) instead of failing
// it. Item i is set aside: its file is closed with its journal, and opened again once the
// user resumes, so that only what is durable counts; every other chunk is asked again. The
// progress keeps what it received until then (open sets it right). The other items keep
// their files: a write of theirs that fails sets them aside too.
func (ru *run) diskFull(i int, gen uint32, err error) {
	st := ru.items[i]
	r := ru.r
	st.mu.Lock()
	r.mu.Lock()
	// A write of an earlier generation, which failed late, says nothing new: the user may
	// have resumed since.
	aside := st.state < ItemDone && !st.closed && gen == st.gen.Load()
	first := false
	if aside {
		st.gen.Add(1)
		st.next, st.retries, st.digest = 0, nil, false
		first = !r.paused
		r.paused = true
	}
	r.mu.Unlock()
	if aside {
		if st.f != nil {
			_ = st.f.Suspend()
			st.f = nil
		}
		st.opened = false
		st.processed.Store(0)
		for k := range st.fresh {
			st.fresh[k] = false
		}
	}
	st.mu.Unlock()
	if first {
		if ev, ok := r.Events.(DiskFullEvents); ok {
			ru.evMu.Lock()
			ev.OnDiskFull(err)
			ru.evMu.Unlock()
		}
	}
}

// complete is called once every chunk of item i is handled. A file of at most one chunk, all
// received now, needs nothing more: its digest follows from that chunk's verified hash. Any
// other file is checked against the sender's digest (bible §8.3, step 6): the receiver makes
// its own from the hashes it checked, those of an earlier session coming from the journal
// (verified on resume, STO-06), then asks for the sender's.
func (ru *run) complete(i int) {
	st := ru.items[i]
	if st.total == 0 || (st.total == 1 && st.fresh[0]) {
		ru.finish(i)
		return
	}
	st.mu.Lock()
	if st.closed || (st.f == nil && ru.r.Sink != SinkNull) {
		st.mu.Unlock()
		return
	}
	hashes := st.hashes // the null sink: every chunk was checked in this session
	if st.f != nil {
		h, err := st.f.Hashes()
		if err != nil {
			st.mu.Unlock()
			ru.endItem(i, ItemFailed, fmt.Errorf("%q: %w", st.it.Path, err))
			return
		}
		hashes = h
	}
	st.local = protocol.FileDigest(hashes)
	st.mu.Unlock()
	r := ru.r
	r.mu.Lock()
	if st.state < ItemDone {
		st.digest = true
		r.cond.Broadcast()
	}
	r.mu.Unlock()
}

// verified ends item i once the sender's digest is known: the same as ours, the file is
// finished; another, it starts over.
func (ru *run) verified(i int, digest [32]byte) {
	st := ru.items[i]
	st.mu.RLock()
	same := digest == st.local
	st.mu.RUnlock()
	if same {
		ru.finish(i)
		return
	}
	ru.startOver(i)
}

// startOver takes item i from zero after its digest did not match (bible §8.3): the partial
// file and its state are removed and every chunk is requested again, once. A second mismatch
// fails the item.
func (ru *run) startOver(i int) {
	st := ru.items[i]
	r := ru.r
	r.mu.Lock()
	again, ended := st.restarted, st.state >= ItemDone
	st.restarted = true
	r.mu.Unlock()
	if ended {
		return
	}
	if again {
		ru.endItem(i, ItemFailed, fmt.Errorf("%q: the received file does not match the sender's, twice (digest)", st.it.Path))
		return
	}
	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		return
	}
	if st.f != nil {
		_ = st.f.Discard() // this transfer's partial file only (STO-09)
		st.f = nil
	}
	st.processed.Store(0)
	for k := range st.fresh {
		st.fresh[k] = false
	}
	ru.bytes.Add(-st.counted.Swap(0))
	st.mu.Unlock()
	r.mu.Lock()
	st.next, st.retries, st.attempts = 0, nil, map[uint32]int{}
	r.cond.Broadcast()
	r.mu.Unlock()
}

// sendDone tells the sender on conn that every item ended (bible §8.3, step 7; D8): DONE, with
// a partial status if some item was skipped or failed. Never after a cancel.
func (ru *run) sendDone(ctx context.Context, conn net.Conn) {
	if conn == nil || ctx.Err() != nil || !ru.allEnded() {
		return
	}
	status := protocol.DoneComplete
	if ru.partial() {
		status = protocol.DonePartial
	}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = conn.Write(protocol.AppendDone(nil, status))
}

// partial reports whether some item was skipped or failed.
func (ru *run) partial() bool {
	ru.r.mu.Lock()
	defer ru.r.mu.Unlock()
	for _, st := range ru.items {
		if st.state != ItemDone {
			return true
		}
	}
	return false
}

// chunkErr is the error of an item the sender answered with CHUNK_ERR code.
func chunkErr(path string, code uint16) error {
	switch code {
	case protocol.ChunkErrSourceChanged:
		return fmt.Errorf("the source of %q changed during the transfer (SOURCE_CHANGED)", path)
	case protocol.ChunkErrNoSuchChunk:
		return fmt.Errorf("the sender does not offer that part of %q", path)
	}
	return fmt.Errorf("the sender could not read %q", path)
}

// finish makes a complete item durable and gives it its final name.
func (ru *run) finish(i int) {
	st := ru.items[i]
	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		return
	}
	if st.f != nil && st.f.Batchable() && ru.addToBatch(i, st.f) {
		st.closed = true // sealed: the batch gives it its name
		st.mu.Unlock()
		ru.flushBatch(false)
		return
	}
	var err error
	if st.f != nil {
		err = st.f.Finalize()
	}
	if engine.IsDiskFull(err) {
		gen := st.gen.Load()
		st.mu.Unlock()
		ru.diskFull(i, gen, err)
		return
	}
	st.closed = true
	st.mu.Unlock()
	if err != nil {
		ru.endItem(i, ItemFailed, err)
		return
	}
	ru.endItem(i, ItemDone, nil)
}

// addToBatch seals f, complete, into the batch, if there is one. Called with st.mu held.
func (ru *run) addToBatch(i int, f *store.File) bool {
	ru.batchMu.Lock()
	defer ru.batchMu.Unlock()
	if ru.batch == nil || ru.batch.Add(f) != nil {
		return false
	}
	ru.batched = append(ru.batched, i)
	if len(ru.batched) == 1 {
		ru.batchTimer = time.AfterFunc(batchWait, func() { ru.flushBatch(true) })
	}
	return true
}

// flushBatch makes the files of the batch durable and gives them their names, if it is due:
// force, 256 files, 64 MiB, or nothing else left to receive. Their items then end.
func (ru *run) flushBatch(force bool) {
	ru.batchMu.Lock()
	if ru.batch == nil || ru.batch.Len() == 0 {
		ru.batchMu.Unlock()
		return
	}
	if !force && ru.batch.Len() < batchFiles && ru.batch.Bytes() < batchBytes && !ru.onlyBatchedLeft() {
		ru.batchMu.Unlock()
		return
	}
	b, items := ru.batch, ru.batched
	ru.batch, ru.batched = store.NewBatch(ru.root), nil
	if ru.batchTimer != nil {
		ru.batchTimer.Stop()
		ru.batchTimer = nil
	}
	ru.batchMu.Unlock()
	for k, err := range b.Flush() {
		i := items[k] // sealed and closed by the flush: no item lock to take (markEnded)
		if err != nil {
			ru.markEnded(i, ItemFailed, fmt.Errorf("%q: %w", ru.items[i].it.Path, err))
		} else {
			ru.markEnded(i, ItemDone, nil)
		}
	}
}

// onlyBatchedLeft reports whether every item not ended is in the batch. Called with batchMu
// held.
func (ru *run) onlyBatchedLeft() bool {
	ru.r.mu.Lock()
	left := len(ru.items) - ru.ended
	ru.r.mu.Unlock()
	return left <= len(ru.batched)
}

// endItem records how an item ended (once) and wakes the scheduler. A skipped or failed
// item's file is closed with its state: it can be resumed later.
func (ru *run) endItem(i int, state ItemState, err error) {
	if i < 0 || i >= len(ru.items) {
		return
	}
	st := ru.items[i]
	if state != ItemDone {
		st.mu.Lock()
		if !st.closed && st.f != nil {
			_ = st.f.Close()
		}
		st.closed = true
		st.mu.Unlock()
	}
	ru.markEnded(i, state, err)
}

// markEnded records how item i ended (once), wakes the scheduler and tells the events; then the
// batch is flushed if only its files are left. It takes no item lock: a flush ends its items
// with it, while open may hold the lock of another item.
func (ru *run) markEnded(i int, state ItemState, err error) {
	st := ru.items[i]
	r := ru.r
	r.mu.Lock()
	if st.state >= ItemDone {
		r.mu.Unlock()
		return
	}
	st.state, st.err = state, err
	ru.ended++
	ru.activeMu.Lock()
	delete(ru.active, i)
	ru.activeMu.Unlock()
	r.cond.Broadcast()
	r.mu.Unlock()
	ev := ItemEvent{Item: i, Path: st.it.Path, Size: st.it.Size, State: state, Err: err}
	if state == ItemDone {
		ev.SavedPath = st.saved
	}
	ru.emitItem(ev)
	ru.flushBatch(false) // the files left may all be in the batch
}

func (ru *run) allEnded() bool {
	ru.r.mu.Lock()
	defer ru.r.mu.Unlock()
	return ru.ended == len(ru.items)
}

// closeAll closes the files of the items still open (cancel, lost sender): their state stays,
// for a later resume.
func (ru *run) closeAll() {
	for _, st := range ru.items {
		st.mu.Lock()
		if !st.closed && st.f != nil {
			_ = st.f.Close()
			st.closed = true
		}
		st.mu.Unlock()
	}
}

func (ru *run) results() []ItemResult {
	ru.r.mu.Lock()
	defer ru.r.mu.Unlock()
	out := make([]ItemResult, len(ru.items))
	for i, st := range ru.items {
		out[i] = ItemResult{Item: i, State: st.state, Err: st.err}
		if st.state == ItemDone {
			out[i].SavedPath = st.saved
		}
	}
	return out
}

func (ru *run) emitItem(ev ItemEvent) {
	if ru.r.Events == nil {
		return
	}
	ru.evMu.Lock()
	defer ru.evMu.Unlock()
	ru.r.Events.OnItem(ev)
}

func (ru *run) emitProgress(speed float64) {
	if ru.r.Events == nil {
		return
	}
	ru.r.mu.Lock()
	ended := ru.ended
	ru.r.mu.Unlock()
	ru.activeMu.Lock()
	active := activeOf(ru.active, func(i int) (int64, int64) { return ru.items[i].counted.Load(), ru.items[i].it.Size })
	ru.activeMu.Unlock()
	p := Progress{Bytes: ru.bytes.Load(), Total: ru.offer.TotalBytes, SpeedMBps: speed,
		Streams: int(ru.conns.Load()), ItemsDone: ended, Items: len(ru.items), Active: active}
	ru.evMu.Lock()
	defer ru.evMu.Unlock()
	ru.r.Events.OnProgress(p)
}

func (ru *run) reportProgress(ctx context.Context) {
	t := time.NewTicker(progressEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ru.emitProgress(ru.speed.update(ru.received.Load()))
		}
	}
}

// connect opens one data connection: dial with a timeout, then Join (TLS and DATA_JOIN), with
// up to 8 attempts (XFER-05). Once this run had a connection, one more every reconnectEvery
// for ReconnectWindow: the sender's network may come back (WP-B.8).
func (ru *run) connect(ctx context.Context, addr string) (net.Conn, error) {
	r := ru.r
	var lastErr error
	var deadline time.Time // of the further attempts, set when they start
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			delay := reconnectEvery
			if attempt <= len(dialDelays) {
				delay = dialDelays[attempt-1]
			} else {
				if deadline.IsZero() {
					window := r.ReconnectWindow
					if window == 0 {
						window = DefaultReconnectWindow
					}
					if window < 0 || !ru.reached.Load() {
						break
					}
					deadline = time.Now().Add(window)
				}
				if time.Now().After(deadline) {
					break
				}
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		timeout := r.DialTimeout
		if timeout <= 0 {
			timeout = DefaultDialTimeout
		}
		dctx, cancel := context.WithTimeout(ctx, timeout)
		var raw net.Conn
		var err error
		if r.dial != nil {
			raw, err = r.dial(dctx, "tcp4", addr)
		} else {
			var d net.Dialer
			raw, err = d.DialContext(dctx, "tcp4", addr)
		}
		cancel()
		if err == nil {
			raw = engine.WrapDialed(raw)
			var c net.Conn
			if c, err = r.Join(ctx, raw); err == nil {
				ru.reached.Store(true)
				return c, nil
			}
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("%w: %v", ErrTransferFailed, lastErr)
}

// received is a chunk read from the network, waiting to be checked and written.
type received struct {
	t    task
	buf  []byte // the whole buffer, given back once written
	data []byte
	sum  [32]byte
}

// worker keeps one data connection busy: up to two requests in flight. Each answer is handed
// to the worker's writer, which checks it against its SHA-256 and writes it while the next one
// is read: the network never waits for the disk. A worker holds two chunk buffers: memory
// stays bounded by Workers × 2 chunks (bible §8.3).
func (ru *run) worker(ctx context.Context, addr string) error {
	limits := protocol.Limits{
		protocol.TypeChunkData:  uint32(protocol.ChunkDataHeaderLen) + ru.chunks,
		protocol.TypeChunkError: protocol.ChunkErrorLen,
		protocol.TypeDigest:     protocol.DigestLen,
	}
	free := make(chan []byte, 2)
	for i := 0; i < 2; i++ {
		free <- make([]byte, protocol.ChunkDataHeaderLen+int(ru.chunks))
	}
	writes := make(chan received, 1)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for rc := range writes {
			start := time.Now()
			if hashDelay > 0 {
				time.Sleep(hashDelay)
			}
			sum := sha256.Sum256(rc.data)
			ru.stage(StageHash, ru.workers, start, int64(len(rc.data)))
			if sum != rc.sum {
				ru.retry(rc.t, true) // XFER-04: never written
			} else {
				_ = ru.write(rc.t, rc.data, rc.sum)
			}
			free <- rc.buf
		}
	}()
	defer func() {
		close(writes)
		<-writerDone
	}()
	var out []byte
	var conn net.Conn
	var stopClosing func() bool
	var inflight []task
	reconnects := 0 // in a row, without a chunk received in between (audit 14, #156)
	drop := func() {
		for _, t := range inflight {
			ru.retry(t, false)
		}
		inflight = nil
		if conn != nil {
			stopClosing()
			ru.track(conn, false)
			conn.Close()
			conn = nil
			ru.conns.Add(-1)
		}
	}
	defer drop()

	for {
		if conn == nil {
			// Paused: no connection meanwhile. The pause cut the last one, not a dead sender.
			if ru.waitResumed(ctx) {
				reconnects = 0
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if reconnects >= maxReconnects {
				return ErrTransferFailed
			}
			if ru.allEnded() {
				return nil
			}
			c, err := ru.connect(ctx, addr)
			if err != nil {
				return err
			}
			conn = c
			ru.track(c, true)
			// A cancel closes the connection: a read waiting for a silent sender ends at once.
			stopClosing = context.AfterFunc(ctx, func() { c.Close() })
			reconnects++
			ru.conns.Add(1)
		}
	fill:
		for len(inflight) < maxInFlight {
			t, status := ru.next(ctx, len(inflight) == 0)
			switch status {
			case allEnded:
				if len(inflight) == 0 {
					ru.sendDone(ctx, conn)
					return nil
				}
				break fill
			case nothingNow:
				break fill
			}
			if t.index == protocol.DigestIndex {
				out = protocol.AppendDigestRequest(out[:0], uint32(t.item))
			} else {
				if ru.prepare(t) {
					continue
				}
				out = protocol.AppendChunkRequest(out[:0], uint32(t.item), t.index)
			}
			inflight = append(inflight, t)
			_ = conn.SetWriteDeadline(time.Now().Add(chunkReadTimeout))
			if _, err := conn.Write(out); err != nil {
				drop()
				break fill
			}
		}
		if conn == nil || len(inflight) == 0 {
			continue
		}
		var buf []byte
		select {
		case buf = <-free:
		case <-ctx.Done():
			return ctx.Err()
		}
		if ru.paused() { // paused meanwhile: the requests in flight are asked again on resume
			free <- buf
			drop()
			continue
		}
		// The wait starts once the worker can take a chunk: a slow disk or hash holds the
		// buffers back, and must not count as network time (DEV-04).
		readStart := time.Now()
		_ = conn.SetReadDeadline(readStart.Add(chunkReadTimeout))
		h, p, err := protocol.ReadFrame(countingReader{conn, &ru.received}, limits, buf)
		if err != nil {
			free <- buf
			drop()
			continue
		}
		t := inflight[0]
		inflight = inflight[1:]
		if h.Type == protocol.TypeChunkError {
			_, _, code, _ := protocol.ParseChunkError(p)
			free <- buf
			ru.endItem(t.item, ItemFailed, chunkErr(ru.items[t.item].it.Path, code))
			continue
		}
		if t.index == protocol.DigestIndex {
			item, digest, err := protocol.ParseDigest(p)
			free <- buf
			if err != nil || h.Type != protocol.TypeDigest || int(item) != t.item {
				inflight = append([]task{t}, inflight...)
				drop()
				continue
			}
			if t.gen == ru.items[t.item].gen.Load() {
				ru.verified(t.item, digest)
			}
			continue
		}
		item, index, sum, data, err := protocol.ParseChunkData(p)
		ru.stage(StageNetWait, ru.workers, readStart, int64(len(data)))
		if err != nil || h.Type != protocol.TypeChunkData || int(item) != t.item || index != t.index {
			free <- buf
			inflight = append([]task{t}, inflight...)
			drop() // a peer that answers out of order is not trusted further on this connection
			continue
		}
		reconnects = 0 // the connection carries data: a cut later is not a dead sender
		writes <- received{t: t, buf: buf, data: data, sum: sum}
	}
}
