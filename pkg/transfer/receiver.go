package transfer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/protocol"
)

// Defaults of the receiver (bible §8.3; parameters, rule R4).
const (
	DefaultWorkers = 4 // data connections (LIM-07: 1 to 8)
	// DefaultDialTimeout bounds each attempt to reach the sender (#48): without it, a
	// firewall that drops the packets would hold a connection attempt for minutes.
	DefaultDialTimeout = 5 * time.Second
	MaxWorkers         = 8
	maxInFlight        = 2 // requests in flight per connection (bible §8.3)
	maxChunkAttempts   = 3 // a chunk whose hash does not match is asked again at most 3 times (XFER-04)
	maxReconnects      = 16
	chunkReadTimeout   = time.Minute
)

var ErrTransferFailed = errors.New("transfer: no data connection could be kept to the sender")

// dialDelays are the waits between attempts to reach the sender: 50 ms up to 1 s, 8 attempts
// in all (XFER-05).
var dialDelays = []time.Duration{
	50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond,
	800 * time.Millisecond, time.Second, time.Second,
}

// Receiver downloads an offer: it opens Workers data connections to the sender's port and
// drives the transfer (bible §8.3). It asks for the smallest missing chunk of the whole offer,
// with at most two requests in flight per connection, so several small files are in flight at
// once and memory stays bounded by Workers × 2 chunks. A chunk is written only once its
// SHA-256 matches the one the sender computed.
type Receiver struct {
	OutDir  string
	Workers int
	Policy  engine.CollisionPolicy
	// Join secures each data connection and names the transfer (JoinFunc).
	Join        func(context.Context, net.Conn) (net.Conn, error)
	DialTimeout time.Duration // 0: DefaultDialTimeout
	Events      Events

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
	r := &Receiver{OutDir: outDir, Workers: DefaultWorkers, Join: join, pausedItems: map[int]bool{}}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// Pause stops requesting chunks; the requests in flight end, the connections stay open.
func (r *Receiver) Pause() {
	r.mu.Lock()
	r.paused = true
	r.mu.Unlock()
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
	workers := r.Workers
	if workers < 1 {
		workers = DefaultWorkers
	}
	if workers > MaxWorkers {
		workers = MaxWorkers
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	run := newRun(r, offer)
	run.workers = workers
	run.stages, _ = r.Events.(StageEvents)
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
	run.closeAll()
	run.emitProgress(0)

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
	r      *Receiver
	offer  protocol.Offer
	items  []*item
	low    int // first item that may still need requests (under r.mu)
	ended  int // items done, skipped or failed (under r.mu)
	start  time.Time
	bytes  atomic.Int64
	conns  atomic.Int32
	evMu   sync.Mutex
	speed  *speedMeter
	chunks uint32 // chunk size

	stages  StageEvents // nil: the stages are not measured
	workers int
}

// hashDelay slows every hash down; tests use it to make the hash the limiting stage (DEV-04).
var hashDelay time.Duration

// stage reports the time a stage took since start, if someone measures (DEV-03).
func (ru *run) stage(name string, parallel int, start time.Time, bytes int64) {
	if ru.stages != nil {
		ru.stages.OnStage(name, parallel, time.Since(start), bytes)
	}
}

// observeSync has dm report its group commits as the fsync stage.
func (ru *run) observeSync(dm *engine.DiskManager) {
	if ru.stages != nil {
		dm.SetSyncObserver(func(d time.Duration) { ru.stages.OnStage(StageFsync, 1, d, 0) })
	}
}

type item struct {
	it       protocol.OfferItem
	total    uint32 // chunks; 0 for an empty file
	next     uint32 // next index to request (under r.mu)
	retries  []uint32
	attempts map[uint32]int
	state    ItemState // under r.mu once ended
	err      error     // under r.mu: why it failed

	// mu: chunks are written under the read lock, in parallel (DiskManager takes care of
	// concurrent writes); opening, finalizing and closing take the write lock.
	mu        sync.RWMutex
	opened    bool
	closed    bool
	safe      string              // the item's path, checked (SafeRelPath)
	dm        *engine.DiskManager // nil until the first verified chunk, unless resuming (#75)
	saved     string
	processed atomic.Uint32 // chunks handled: written, or found on disk

	// The digest (bible §8.3, step 6). hashes[i] is chunk i's SHA-256: from its verified CHUNK
	// (fresh[i]) or read back from disk for a chunk of an earlier session. Distinct chunks are
	// written by distinct goroutines; local is set once they are all handled.
	hashes    [][32]byte
	fresh     []bool
	local     [32]byte
	counted   atomic.Int64 // bytes of this item in the progress, given back if it starts over
	digest    bool         // under r.mu: the DIGEST_REQ waits to be sent
	restarted bool         // under r.mu: started over once after a digest mismatch
}

func newRun(r *Receiver, offer protocol.Offer) *run {
	ru := &run{r: r, offer: offer, start: time.Now(), speed: newSpeedMeter(), chunks: offer.ChunkSize}
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
				if st.digest {
					st.digest = false
					return task{i, protocol.DigestIndex}, gotTask
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
					return task{i, idx}, gotTask
				}
				sched := st.total
				if sched == 0 {
					sched = 1 // an empty file is one request: chunk 0, of 0 bytes
				}
				if st.next < sched {
					st.next++
					return task{i, st.next - 1}, gotTask
				}
			}
		}
		if !block {
			return task{}, nothingNow
		}
		r.cond.Wait()
	}
}

// retry schedules a chunk again. A chunk with a wrong hash counts as an attempt (XFER-04); a
// lost connection does not.
func (ru *run) retry(t task, badHash bool) {
	st := ru.items[t.item]
	r := ru.r
	r.mu.Lock()
	if st.state >= ItemDone {
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
	if st.closed {
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
	onDisk := st.total > 0 && st.dm != nil && st.dm.IsChunkCompleted(t.index)
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
	safe, err := engine.SafeRelPath(st.it.Path)
	if err != nil {
		return err
	}
	res, err := engine.ResolveCollision(r.OutDir, safe, st.it.FileID, st.it.Size, ru.chunks, r.Policy)
	if err != nil {
		return err
	}
	st.opened = true
	st.safe = safe
	st.saved = filepath.Join(r.OutDir, filepath.FromSlash(res.ResolvedName))
	ru.emitItem(ItemEvent{Item: st.it.Item, Path: st.it.Path, Size: st.it.Size, State: ItemStarted})
	if res.IsDuplicate { // the same file is already complete on disk
		ru.bytes.Add(st.it.Size)
		st.closed = true
		ru.endItem(st.it.Item, ItemDone, nil) // ItemDone does not take st.mu
		return nil
	}
	if res.IsResume {
		dm, err := engine.CreateAndPreallocate(r.OutDir, res.ResolvedName, st.it.Size, ru.chunks, st.it.FileID)
		if err != nil {
			return err
		}
		ru.observeSync(dm)
		st.dm = dm
		ru.bytes.Add(dm.GetDownloadedBytes())
		st.counted.Add(dm.GetDownloadedBytes())
	}
	return nil
}

// create makes the item's file on its first verified chunk (#75); an empty item has one, of 0
// bytes, which the sender's answer confirms. Until then a refused connection, a silent or lying sender
// or a cancel leaves nothing behind, and an existing file the policy would overwrite stays
// whole. The name is resolved again, so a file that appeared meanwhile is never emptied.
// Called with st.mu held.
func (ru *run) create(st *item) error {
	r := ru.r
	res, err := engine.ResolveCollision(r.OutDir, st.safe, st.it.FileID, st.it.Size, ru.chunks, r.Policy)
	if err != nil {
		return err
	}
	if res.IsDuplicate || res.IsResume {
		return fmt.Errorf("%q: the destination changed during the transfer", st.it.Path)
	}
	dm, err := engine.CreateAndPreallocate(r.OutDir, res.ResolvedName, st.it.Size, ru.chunks, st.it.FileID)
	if err != nil {
		return err
	}
	ru.observeSync(dm)
	st.dm = dm
	st.saved = filepath.Join(r.OutDir, filepath.FromSlash(res.ResolvedName))
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
	st.mu.RLock()
	if st.dm == nil && !st.closed { // the item's first verified chunk: its file is made now
		st.mu.RUnlock()
		st.mu.Lock()
		var err error
		if st.dm == nil && !st.closed {
			err = ru.create(st)
		}
		st.mu.Unlock()
		if err != nil {
			ru.endItem(t.item, ItemFailed, err)
			return nil
		}
		st.mu.RLock()
	}
	if st.closed || st.dm == nil {
		st.mu.RUnlock()
		return nil // skipped or failed meanwhile
	}
	if st.total > 0 && !st.dm.IsChunkCompleted(t.index) {
		start := time.Now()
		_, err := st.dm.WriteChunkAt(data, int64(t.index)*int64(ru.chunks), t.index)
		ru.stage(StageDiskWrite, ru.workers, start, int64(len(data)))
		if err != nil {
			st.mu.RUnlock()
			ru.endItem(t.item, ItemFailed, err)
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

// complete is called once every chunk of item i is handled. A file of at most one chunk, all
// received now, needs nothing more: its digest follows from that chunk's verified hash. Any
// other file is checked against the sender's digest (bible §8.3, step 6): the receiver makes
// its own, reading back the chunks of an earlier session, then asks for the sender's.
func (ru *run) complete(i int) {
	st := ru.items[i]
	if st.total == 0 || (st.total == 1 && st.fresh[0]) {
		ru.finish(i)
		return
	}
	st.mu.Lock()
	if st.closed || st.dm == nil {
		st.mu.Unlock()
		return
	}
	buf := make([]byte, ru.chunks)
	for index := range st.hashes {
		if st.fresh[index] {
			continue
		}
		off := int64(index) * int64(ru.chunks)
		n := min(int64(ru.chunks), st.it.Size-off)
		if _, err := st.dm.ReadChunkAt(buf[:n], off); err != nil {
			st.mu.Unlock()
			ru.endItem(i, ItemFailed, fmt.Errorf("%q: reading back chunk %d: %w", st.it.Path, index, err))
			return
		}
		st.hashes[index] = sha256.Sum256(buf[:n])
	}
	st.local = protocol.FileDigest(st.hashes)
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
	if st.dm != nil {
		_ = st.dm.Discard()
		st.dm = nil
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
	var err error
	if st.dm != nil {
		err = st.dm.Finalize()
	}
	st.closed = true
	st.mu.Unlock()
	if err != nil {
		ru.endItem(i, ItemFailed, err)
		return
	}
	ru.endItem(i, ItemDone, nil)
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
		if !st.closed && st.dm != nil {
			_ = st.dm.Close()
		}
		st.closed = true
		st.mu.Unlock()
	}
	r := ru.r
	r.mu.Lock()
	if st.state >= ItemDone {
		r.mu.Unlock()
		return
	}
	st.state, st.err = state, err
	ru.ended++
	r.cond.Broadcast()
	r.mu.Unlock()
	ev := ItemEvent{Item: i, Path: st.it.Path, Size: st.it.Size, State: state, Err: err}
	if state == ItemDone {
		ev.SavedPath = st.saved
	}
	ru.emitItem(ev)
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
		if !st.closed && st.dm != nil {
			_ = st.dm.Close()
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
	p := Progress{Bytes: ru.bytes.Load(), Total: ru.offer.TotalBytes, SpeedMBps: speed,
		Streams: int(ru.conns.Load()), ItemsDone: ended, Items: len(ru.items)}
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
			ru.emitProgress(ru.speed.update(ru.bytes.Load()))
		}
	}
}

// connect opens one data connection: dial with a timeout, then Join (TLS and DATA_JOIN), with
// up to 8 attempts (XFER-05).
func (ru *run) connect(ctx context.Context, addr string) (net.Conn, error) {
	r := ru.r
	var lastErr error
	for attempt := 0; attempt <= len(dialDelays); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(dialDelays[attempt-1]):
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
	reconnects := 0
	drop := func() {
		for _, t := range inflight {
			ru.retry(t, false)
		}
		inflight = nil
		if conn != nil {
			stopClosing()
			conn.Close()
			conn = nil
			ru.conns.Add(-1)
		}
	}
	defer drop()

	for {
		if conn == nil {
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
		// The wait starts once the worker can take a chunk: a slow disk or hash holds the
		// buffers back, and must not count as network time (DEV-04).
		readStart := time.Now()
		_ = conn.SetReadDeadline(readStart.Add(chunkReadTimeout))
		h, p, err := protocol.ReadFrame(conn, limits, buf)
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
			ru.verified(t.item, digest)
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
		writes <- received{t: t, buf: buf, data: data, sum: sum}
	}
}
