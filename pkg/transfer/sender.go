package transfer

import (
	"context"
	"crypto/sha256"
	"fmt"
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

	mu     sync.Mutex
	files  map[int]*openFile
	served [][]uint64 // per item, the chunks already counted in bytes

	bytes   atomic.Int64
	streams atomic.Int32
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
	return &Sender{offer: offer, paths: paths, files: map[int]*openFile{}, served: make([][]uint64, len(paths))}, nil
}

// SetEvents sets who gets the progress events.
func (s *Sender) SetEvents(e Events) { s.events = e }

// Progress is the current progress: unique chunks served.
func (s *Sender) Progress() Progress {
	return Progress{Bytes: s.bytes.Load(), Total: s.offer.TotalBytes, Streams: int(s.streams.Load()), Items: len(s.paths)}
}

// Serve answers the receiver's CHUNK_REQ on every connection of ln (the transfer's listener on
// the device's port) until ctx ends. ln is then closed.
func (s *Sender) Serve(ctx context.Context, ln net.Listener) error {
	var wg sync.WaitGroup
	var connsMu sync.Mutex
	conns := map[net.Conn]struct{}{}
	go func() {
		<-ctx.Done()
		ln.Close()
		connsMu.Lock()
		for c := range conns {
			c.Close()
		}
		connsMu.Unlock()
	}()
	go s.reportProgress(ctx)
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
	speed := newSpeedMeter()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p := s.Progress()
			p.SpeedMBps = speed.update(p.Bytes)
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
		limits := protocol.Limits{protocol.TypeChunkRequest: protocol.ChunkRequestLen}
		req := make([]byte, protocol.ChunkRequestLen)
		data := make([]byte, s.offer.ChunkSize)
		for {
			_, p, err := protocol.ReadFrame(c, limits, req)
			if err != nil {
				return
			}
			item, index, err := protocol.ParseChunkRequest(p)
			if err != nil {
				return
			}
			out := <-free
			if out, err = s.chunk(out[:0], data, item, index); err != nil {
				return
			}
			ready <- out
		}
	}()
	for out := range ready {
		_ = c.SetWriteDeadline(time.Now().Add(time.Minute))
		if _, err := c.Write(out); err != nil {
			c.Close() // ends the preparing goroutine's read
			for range ready {
			}
			return
		}
		free <- out
	}
}

// chunk appends to out the answer to a request: the CHUNK, or a CHUNK_ERR.
func (s *Sender) chunk(out, data []byte, item, index uint32) ([]byte, error) {
	if int(item) >= len(s.paths) {
		return protocol.AppendChunkError(out, item, index, protocol.ChunkErrNoSuchChunk), nil
	}
	it := s.offer.Items[item]
	if index >= protocol.Chunks(it.Size, s.offer.ChunkSize) {
		return protocol.AppendChunkError(out, item, index, protocol.ChunkErrNoSuchChunk), nil
	}
	off := int64(index) * int64(s.offer.ChunkSize)
	n := int64(s.offer.ChunkSize)
	if off+n > it.Size {
		n = it.Size - off
	}
	f, err := s.acquire(int(item))
	if err != nil {
		return protocol.AppendChunkError(out, item, index, protocol.ChunkErrRead), nil
	}
	_, err = f.ReadAt(data[:n], off) // an error means fewer bytes than asked (a shorter file too)
	s.release(int(item))
	if err != nil {
		return protocol.AppendChunkError(out, item, index, protocol.ChunkErrRead), nil
	}
	sum := sha256.Sum256(data[:n])
	s.markServed(int(item), index, n)
	return protocol.AppendChunkData(out, item, index, sum, data[:n]), nil
}

// markServed counts a chunk in the progress the first time it is served (#33).
func (s *Sender) markServed(item int, index uint32, n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bits := s.served[item]
	if bits == nil {
		bits = make([]uint64, (protocol.Chunks(s.offer.Items[item].Size, s.offer.ChunkSize)+63)/64)
		s.served[item] = bits
	}
	w, b := index/64, uint64(1)<<(index%64)
	if bits[w]&b == 0 {
		bits[w] |= b
		s.bytes.Add(n)
	}
}

// acquire opens the file of item, or reuses it; release says it is no longer read. The file is
// opened outside the lock, so that one slow open (an antivirus scanning the file) does not hold
// up the other connections. Files nobody reads are closed when more than maxOpenFiles are open.
func (s *Sender) acquire(item int) (*os.File, error) {
	s.mu.Lock()
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
}

// speedMeter smooths the throughput seen through successive byte counts.
type speedMeter struct {
	last  int64
	at    time.Time
	speed float64
}

func newSpeedMeter() *speedMeter { return &speedMeter{at: time.Now()} }

func (m *speedMeter) update(bytes int64) float64 {
	now := time.Now()
	if dt := now.Sub(m.at).Seconds(); dt > 0 {
		inst := float64(bytes-m.last) / (1 << 20) / dt
		if m.speed == 0 {
			m.speed = inst
		} else {
			m.speed = 0.7*m.speed + 0.3*inst
		}
	}
	m.last, m.at = bytes, now
	return m.speed
}
