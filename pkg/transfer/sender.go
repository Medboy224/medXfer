package transfer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Medboy224/medXfer/pkg/protocol"
)

// maxOpenFiles bounds the files a sender keeps open at once; others are reopened when asked.
const maxOpenFiles = 64

// progressEvery is the period of progress events (B7: at most 10 per second).
const progressEvery = 200 * time.Millisecond

// Sender serves the chunks of every item of an offer, in whatever order the receiver asks
// (bible §8.3: the receiver drives). Each CHUNK carries the SHA-256 of its data.
type Sender struct {
	offer  protocol.Offer
	paths  []string
	events Events
	stages StageEvents // nil: the stages are not measured (DEV-03)
	src    io.ReaderAt // a bench offer's source, instead of the item's file (DEV-09)

	mu        sync.Mutex
	files     map[int]*openFile
	fixed     map[int]*os.File // files open already (NewSenderFiles): never opened, closed at the end
	served    [][]uint64       // per item, the chunks already counted in bytes
	hashes    [][][32]byte     // per item, the SHA-256 of the chunks served, for the digest
	itemBytes map[int]int64    // per item, the bytes counted in served
	active    map[int]struct{} // items served in part: the progress of each (WP-B.0)
	ahead     map[int]*ahead   // per item resumed, the chunks hashed ahead for the digest
	aheadNext chan int         // items to hash ahead, in the order they were resumed
	quit      chan struct{}    // closed once Serve's context ends

	digestReads atomic.Int64 // chunks the digest had to read itself (tests)

	bytes    atomic.Int64
	wire     atomic.Int64 // bytes written to the data connections: the speed
	streams  atomic.Int32
	finished atomic.Bool // the receiver sent DONE
	partial  atomic.Bool
}

type openFile struct {
	f     *os.File      // nil while being opened
	refs  int           // connections reading it
	ready chan struct{} // closed once the open is over
}

// NewSender serves offer; paths[i] is the file of item i.
func NewSender(offer protocol.Offer, paths []string) (*Sender, error) {
	if len(paths) != len(offer.Items) {
		return nil, fmt.Errorf("transfer: %d paths for %d items", len(paths), len(offer.Items))
	}
	if err := offer.Validate(); err != nil {
		return nil, err
	}
	// The sender keeps its own items: the caller may reuse or change its offer afterwards.
	offer.Items = append([]protocol.OfferItem(nil), offer.Items...)
	return &Sender{offer: offer, paths: paths, files: map[int]*openFile{},
		served: make([][]uint64, len(paths)), hashes: make([][][32]byte, len(paths)),
		itemBytes: map[int]int64{}, active: map[int]struct{}{}, ahead: map[int]*ahead{},
		aheadNext: make(chan int, len(paths)), quit: make(chan struct{})}, nil
}

// ahead is a resumed item's chunks that the receiver already had: not served by this sender,
// so the digest would read and hash them at the very end, after 100 % (WP-B.8: seconds on a
// phone for a large file). They are hashed while the rest goes (hashAhead).
type ahead struct {
	upTo   uint32     // chunks [0, upTo): those before the first one asked for
	hashes [][32]byte // a chunk served meanwhile keeps its own hash (servedHash)
	ok     bool       // all of them hashed
	done   chan struct{}
}

// SetEvents sets who gets the progress events, and the stage times if it wants them.
func (s *Sender) SetEvents(e Events) {
	s.events = e
	s.stages, _ = e.(StageEvents)
}

// stage reports the time a stage took since start, if someone measures (DEV-03). Every
// connection runs each stage: the parallelism is the number of connections.
func (s *Sender) stage(name string, start time.Time, bytes int64) {
	if s.stages != nil {
		s.stages.OnStage(name, max(1, int(s.streams.Load())), time.Since(start), bytes)
	}
}

// Progress is the current progress: unique chunks served.
func (s *Sender) Progress() Progress {
	s.mu.Lock()
	active := activeOf(s.active, func(i int) (int64, int64) { return s.itemBytes[i], s.offer.Items[i].Size })
	s.mu.Unlock()
	return Progress{Bytes: s.bytes.Load(), Total: s.offer.TotalBytes, Streams: int(s.streams.Load()), Items: len(s.paths),
		Finished: s.finished.Load(), Partial: s.partial.Load(), Active: active}
}

// Serve answers the receiver's CHUNK_REQ on every connection of ln (the transfer's listener on
// the device's port) until ctx ends. ln is then closed.
func (s *Sender) Serve(ctx context.Context, ln net.Listener) error {
	var wg sync.WaitGroup
	var connsMu sync.Mutex
	conns := map[net.Conn]struct{}{}
	go func() {
		<-ctx.Done()
		close(s.quit)
		ln.Close()
		connsMu.Lock()
		for c := range conns {
			c.Close()
		}
		connsMu.Unlock()
	}()
	go s.reportProgress(ctx)
	go s.hashAhead(ctx)
	defer s.closeFiles()
	for {
		c, err := ln.Accept()
		if err != nil {
			wg.Wait()
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		connsMu.Lock()
		conns[c] = struct{}{}
		connsMu.Unlock()
		wg.Add(1)
		s.streams.Add(1)
		go func() {
			defer wg.Done()
			defer s.streams.Add(-1)
			defer func() {
				connsMu.Lock()
				delete(conns, c)
				connsMu.Unlock()
				c.Close()
			}()
			s.serveConn(c)
		}()
	}
}

func (s *Sender) reportProgress(ctx context.Context) {
	if s.events == nil {
		return
	}
	t := time.NewTicker(progressEvery)
	defer t.Stop()
	speed := newSenderSpeedMeter()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p := s.Progress()
			p.SpeedMBps = speed.update(s.wire.Load())
			s.events.OnProgress(p)
		}
	}
}

// serveConn answers the requests of one data connection. The receiver keeps two requests in
// flight: the next chunk is read and hashed while the previous one is being written to the
// network. Two frame buffers per connection.
func (s *Sender) serveConn(c net.Conn) {
	frameLen := protocol.FrameHeaderSize + protocol.ChunkDataHeaderLen + int(s.offer.ChunkSize)
	free := make(chan []byte, 2)
	for i := 0; i < 2; i++ {
		free <- make([]byte, 0, frameLen)
	}
	ready := make(chan []byte, 1)
	go func() { // prepares the answers, in the order of the requests
		defer close(ready)
		limits := protocol.Limits{
			protocol.TypeChunkRequest:  protocol.ChunkRequestLen,
			protocol.TypeDigestRequest: protocol.DigestRequestLen,
			protocol.TypeDone:          protocol.DoneLen,
		}
		req := make([]byte, protocol.ChunkRequestLen)
		data := make([]byte, s.offer.ChunkSize)
		for {
			h, p, err := protocol.ReadFrame(c, limits, req)
			if err != nil {
				return
			}
			if h.Type == protocol.TypeDone {
				status, err := protocol.ParseDone(p)
				if err == nil {
					s.receiverDone(status)
				}
				return // the receiver is done with this connection
			}
			out := <-free
			if h.Type == protocol.TypeDigestRequest {
				item, err := protocol.ParseDigestRequest(p)
				if err != nil {
					return
				}
				out = s.digest(out[:0], data, item)
			} else {
				item, index, err := protocol.ParseChunkRequest(p)
				if err != nil {
					return
				}
				out = s.chunk(out[:0], data, item, index)
			}
			ready <- out
		}
	}()
	for out := range ready {
		start := time.Now()
		_ = c.SetWriteDeadline(start.Add(time.Minute))
		_, err := writeCounted(c, out, &s.wire)
		s.stage(StageNetWrite, start, int64(len(out)))
		if err != nil {
			c.Close() // ends the preparing goroutine's read
			for range ready {
			}
			return
		}
		free <- out
	}
}

// receiverDone records the receiver's DONE (once, whatever the connection) and reports it at
// once.
func (s *Sender) receiverDone(status byte) {
	if !s.finished.CompareAndSwap(false, true) {
		return
	}
	s.partial.Store(status != protocol.DoneComplete)
	if s.events != nil {
		s.events.OnProgress(s.Progress())
	}
}

// errSourceChanged: the file no longer has the size or date of the offer (XFER-07).
var errSourceChanged = errors.New("transfer: the source file changed")

// reader returns what item reads from: its file, to release once read, or the bench source
// (f is then nil).
func (s *Sender) reader(item int) (r io.ReaderAt, f *os.File, err error) {
	if s.src != nil {
		return s.src, nil, nil
	}
	if f, err = s.acquire(item); err != nil {
		return nil, nil, err
	}
	return f, f, nil
}

// unchanged reports whether item's open file still has the size and date of the offer. A
// bench source (f nil) does not change.
func (s *Sender) unchanged(item int, f *os.File) bool {
	if f == nil {
		return true
	}
	it := s.offer.Items[item]
	fi, err := f.Stat()
	return err == nil && fi.Size() == it.Size && (it.MTime == 0 || fi.ModTime().UnixNano() == it.MTime)
}

// readErr is the CHUNK_ERR code of a failed open or read of item's file.
func (s *Sender) readErr(item int, f *os.File, err error) uint16 {
	if errors.Is(err, errSourceChanged) || (f != nil && !s.unchanged(item, f)) {
		return protocol.ChunkErrSourceChanged
	}
	return protocol.ChunkErrRead
}

// span is the offset and length of chunk index of an item of size bytes.
func (s *Sender) span(size int64, index uint32) (int64, int64) {
	off := int64(index) * int64(s.offer.ChunkSize)
	return off, min(int64(s.offer.ChunkSize), size-off)
}

// chunk appends to out the answer to a request: the CHUNK, or a CHUNK_ERR.
func (s *Sender) chunk(out, data []byte, item, index uint32) []byte {
	if int(item) >= len(s.paths) {
		return protocol.AppendChunkError(out, item, index, protocol.ChunkErrNoSuchChunk)
	}
	it := s.offer.Items[item]
	if index >= max(1, protocol.Chunks(it.Size, s.offer.ChunkSize)) { // an empty item is chunk 0, of 0 bytes
		return protocol.AppendChunkError(out, item, index, protocol.ChunkErrNoSuchChunk)
	}
	off, n := s.span(it.Size, index)
	r, f, err := s.reader(int(item))
	if err != nil {
		return protocol.AppendChunkError(out, item, index, s.readErr(int(item), nil, err))
	}
	start := time.Now()
	_, err = r.ReadAt(data[:n], off) // an error means fewer bytes than asked (a shorter file too)
	s.stage(StageDiskRead, start, n)
	if f != nil {
		defer s.release(int(item))
	}
	if err != nil {
		return protocol.AppendChunkError(out, item, index, s.readErr(int(item), f, err))
	}
	start = time.Now()
	sum := sha256.Sum256(data[:n])
	s.stage(StageHash, start, n)
	s.markServed(int(item), index, n, sum)
	return protocol.AppendChunkData(out, item, index, sum, data[:n])
}

// digest appends to out the answer to a DIGEST_REQ: the digest of the item's chunk hashes
// (bible §8.3), or a CHUNK_ERR at DigestIndex. The chunks served keep their hash; the others
// (received in an earlier session) are read and hashed now. The source is checked before and
// after: a file changed during the transfer gives SOURCE_CHANGED (XFER-07).
func (s *Sender) digest(out, data []byte, item uint32) []byte {
	fail := func(code uint16) []byte {
		return protocol.AppendChunkError(out, item, protocol.DigestIndex, code)
	}
	if int(item) >= len(s.paths) {
		return fail(protocol.ChunkErrNoSuchChunk)
	}
	i := int(item)
	r, f, err := s.reader(i)
	if err != nil {
		return fail(s.readErr(i, nil, err))
	}
	if f != nil {
		defer s.release(i)
	}
	if !s.unchanged(i, f) {
		return fail(protocol.ChunkErrSourceChanged)
	}
	it := s.offer.Items[i]
	pre := s.hashedAhead(i)
	hashes := make([][32]byte, protocol.Chunks(it.Size, s.offer.ChunkSize))
	for index := range hashes {
		if h, ok := s.servedHash(i, uint32(index)); ok {
			hashes[index] = h
			continue
		}
		if pre != nil && uint32(index) < pre.upTo {
			hashes[index] = pre.hashes[index]
			continue
		}
		off, n := s.span(it.Size, uint32(index))
		if _, err := r.ReadAt(data[:n], off); err != nil {
			return fail(s.readErr(i, f, err))
		}
		s.digestReads.Add(1)
		hashes[index] = sha256.Sum256(data[:n])
	}
	if !s.unchanged(i, f) {
		return fail(protocol.ChunkErrSourceChanged)
	}
	return protocol.AppendDigest(out, item, protocol.FileDigest(hashes))
}

// markServed counts a chunk in the progress the first time it is served (#33), and keeps its
// hash for the digest.
func (s *Sender) markServed(item int, index uint32, n int64, sum [32]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bits := s.served[item]
	if bits == nil {
		chunks := max(1, protocol.Chunks(s.offer.Items[item].Size, s.offer.ChunkSize))
		bits = make([]uint64, (chunks+63)/64)
		s.served[item] = bits
		s.hashes[item] = make([][32]byte, chunks)
		// The first chunk asked for is not the first: a resume, the receiver has those before.
		if index > 0 {
			s.ahead[item] = &ahead{upTo: index, hashes: make([][32]byte, index), done: make(chan struct{})}
			s.aheadNext <- item // never blocks: one per item at most
		}
	}
	w, b := index/64, uint64(1)<<(index%64)
	if bits[w]&b == 0 {
		bits[w] |= b
		s.bytes.Add(n)
		s.itemBytes[item] += n
		if s.itemBytes[item] < s.offer.Items[item].Size {
			s.active[item] = struct{}{}
		} else {
			delete(s.active, item) // all of it served
		}
	}
	s.hashes[item][index] = sum
}

// hashAhead hashes the chunks a resumed item's receiver already had (ahead), one item after
// the other, while the rest is served: the digest at the end then reads nothing. One goroutine,
// one buffer: the network is slower than a read and a hash.
func (s *Sender) hashAhead(ctx context.Context) {
	data := make([]byte, s.offer.ChunkSize)
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-s.aheadNext:
			s.mu.Lock()
			a := s.ahead[item]
			s.mu.Unlock()
			a.ok = s.hashChunks(ctx, item, a, data)
			close(a.done)
		}
	}
}

// hashChunks hashes a's chunks of item, those not served meanwhile; false if it could not.
func (s *Sender) hashChunks(ctx context.Context, item int, a *ahead, data []byte) bool {
	r, f, err := s.reader(item)
	if err != nil {
		return false
	}
	if f != nil {
		defer s.release(item)
	}
	size := s.offer.Items[item].Size
	for index := range a.upTo {
		if ctx.Err() != nil {
			return false
		}
		if _, ok := s.servedHash(item, index); ok {
			continue
		}
		off, n := s.span(size, index)
		if _, err := r.ReadAt(data[:n], off); err != nil {
			return false
		}
		a.hashes[index] = sha256.Sum256(data[:n])
	}
	return true
}

// hashedAhead returns the chunks of item hashed ahead, waiting for them if they are being
// hashed: sooner than reading them again. Nil if there are none.
func (s *Sender) hashedAhead(item int) *ahead {
	s.mu.Lock()
	a := s.ahead[item]
	s.mu.Unlock()
	if a == nil {
		return nil
	}
	select {
	case <-a.done:
	case <-s.quit:
		return nil
	}
	if !a.ok {
		return nil
	}
	return a
}

// servedHash returns the hash of a chunk served in this session.
func (s *Sender) servedHash(item int, index uint32) ([32]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bits := s.served[item]
	if bits == nil || bits[index/64]&(uint64(1)<<(index%64)) == 0 {
		return [32]byte{}, false
	}
	return s.hashes[item][index], true
}

// acquire opens the file of item, or reuses it; release says it is no longer read. The file is
// opened outside the lock, so that one slow open (an antivirus scanning the file) does not hold
// up the other connections. Files nobody reads are closed when more than maxOpenFiles are open.
func (s *Sender) acquire(item int) (*os.File, error) {
	s.mu.Lock()
	if f, ok := s.fixed[item]; ok {
		s.mu.Unlock()
		if !s.unchanged(item, f) {
			return nil, errSourceChanged
		}
		return f, nil
	}
	for {
		of, ok := s.files[item]
		if !ok {
			break
		}
		if of.f != nil {
			of.refs++
			s.mu.Unlock()
			return of.f, nil
		}
		ready := of.ready // another connection is opening it
		s.mu.Unlock()
		<-ready
		s.mu.Lock()
	}
	of := &openFile{ready: make(chan struct{})}
	s.files[item] = of
	s.mu.Unlock()

	f, err := os.Open(s.paths[item])
	if err == nil && !s.unchanged(item, f) {
		f.Close()
		f, err = nil, errSourceChanged
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	close(of.ready)
	if err != nil {
		delete(s.files, item)
		return nil, err
	}
	of.f, of.refs = f, 1
	if len(s.files) > maxOpenFiles {
		for i, o := range s.files {
			if i != item && o.f != nil && o.refs == 0 {
				o.f.Close()
				delete(s.files, i)
				break
			}
		}
	}
	return f, nil
}

func (s *Sender) release(item int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if of, ok := s.files[item]; ok && of.refs > 0 {
		of.refs--
	}
}

func (s *Sender) closeFiles() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, of := range s.files {
		if of.f != nil {
			of.f.Close()
		}
		delete(s.files, i)
	}
	for i, f := range s.fixed { // the transfer is over: its descriptors with it
		f.Close()
		delete(s.fixed, i)
	}
}
