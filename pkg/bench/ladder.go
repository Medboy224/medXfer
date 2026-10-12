// Package bench measures transfers between two devices with the production code (bible
// §17.4, DEV-07, DEV-08).
package bench

// The bench ladder (bible §17.4, DEV-07). Each step adds one layer to the previous one, so the
// throughput of a step shows what its layer costs:
//
//	R1  raw TCP on N streams, random bytes from memory
//	R2  + TLS 1.3, the server's key pinned
//	R3  + medXfer frames and SHA-256: the transfer engine on a bench offer, null sink (DEV-08)
//	R4  + disk writes, without fsync
//
// R5 (journal and fsync) and R6 (the whole transfer) come with J4b. The listening side sends,
// the other side receives and measures, as `xfer send` and `xfer recv`.

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
	ident "github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

// First byte of a ladder connection, in clear: how it goes on.
const (
	ladderRaw  = 'R' // R1: a hello, then raw bytes
	ladderTLS  = 'T' // R2 to R4: TLS, then a hello
	ladderQuit = 'Q' // the client is done: a server started with once stops
)

// Kind of a TLS connection, in its hello.
const (
	ladderStream byte = 2 // R2: bytes on the connection
	ladderOffer  byte = 3 // R3, R4: a data connection of the bench offer
)

const (
	ladderCodeLen = 16
	ladderIO      = 10 * time.Second // to read a hello
)

var errLadderCode = errors.New("bench ladder: wrong code")

// LadderServer is the sending side of the ladder.
type LadderServer struct {
	Identity *ident.Identity
	Code     string
	OnError  func(error) // errors of one connection; they do not stop the server

	src    io.ReaderAt
	mu     sync.Mutex
	offers map[[2]int64]*connListener // bench senders, by size and chunk size
}

// NewLadderServer returns a server with a new code and a random source in memory.
func NewLadderServer(id *ident.Identity) (*LadderServer, error) {
	src, err := transfer.RandomSource()
	if err != nil {
		return nil, err
	}
	return &LadderServer{Identity: id, Code: diag.NewNetBenchCode(), src: src, offers: map[[2]int64]*connListener{}}, nil
}

// SetSource replaces the random source (DEV-09: --source zero|file). Steps larger than a
// file source fail.
func (s *LadderServer) SetSource(src io.ReaderAt) { s.src = src }

// Serve answers ladder connections on ln until ctx ends or, with once, until a client says
// it is done.
func (s *LadderServer) Serve(ctx context.Context, ln net.Listener, once bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	quit := func() {
		if once {
			cancel()
		}
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go func() {
			if err := s.handle(ctx, c, quit); err != nil && s.OnError != nil {
				s.OnError(err)
			}
		}()
	}
}

func (s *LadderServer) handle(ctx context.Context, c net.Conn, quit func()) error {
	keep := false
	defer func() {
		if !keep {
			_ = c.Close()
		}
	}()
	_ = c.SetDeadline(time.Now().Add(ladderIO))
	var sel [1]byte
	if _, err := io.ReadFull(c, sel[:]); err != nil {
		return err
	}
	switch sel[0] {
	case ladderQuit:
		if err := s.readCode(c); err != nil {
			return err
		}
		quit()
		return nil
	case ladderRaw:
		if err := s.readCode(c); err != nil {
			return err
		}
		size, err := readUint64(c)
		if err != nil {
			return err
		}
		_ = c.SetDeadline(time.Time{})
		return s.send(c, int64(size))
	case ladderTLS:
		tc, err := securechan.Server(ctx, c, securechan.Config{Identity: s.Identity, AllowUnknown: true})
		if err != nil {
			return err
		}
		c = tc
		if err := s.readCode(c); err != nil {
			return err
		}
		var hdr [1 + 8 + 4]byte
		if _, err := io.ReadFull(c, hdr[:]); err != nil {
			return err
		}
		size, chunk := int64(binary.BigEndian.Uint64(hdr[1:9])), binary.BigEndian.Uint32(hdr[9:13])
		_ = c.SetDeadline(time.Time{})
		switch hdr[0] {
		case ladderStream:
			return s.send(c, size)
		case ladderOffer:
			l, err := s.offer(ctx, size, chunk)
			if err != nil {
				return err
			}
			keep = true // the bench sender owns it now
			return l.push(c)
		}
		return fmt.Errorf("bench ladder: unknown kind %d", hdr[0])
	}
	return fmt.Errorf("bench ladder: unknown connection type %q", sel[0])
}

func (s *LadderServer) readCode(r io.Reader) error {
	got := make([]byte, ladderCodeLen)
	if _, err := io.ReadFull(r, got); err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(got, padLadderCode(s.Code)) != 1 {
		return errLadderCode
	}
	return nil
}

// send writes size bytes of the random source to w.
func (s *LadderServer) send(w io.Writer, size int64) error {
	buf := make([]byte, 1<<20)
	for off := int64(0); off < size; {
		n := min(int64(len(buf)), size-off)
		_, _ = s.src.ReadAt(buf[:n], off)
		if _, err := w.Write(buf[:n]); err != nil {
			return err
		}
		off += n
	}
	return nil
}

// offer returns the listener of the bench sender of (size, chunk), started on first use.
func (s *LadderServer) offer(ctx context.Context, size int64, chunk uint32) (*connListener, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := [2]int64{size, int64(chunk)}
	if l := s.offers[key]; l != nil {
		return l, nil
	}
	sender, err := transfer.NewSourceSender(transfer.BenchOffer(size, chunk), s.src)
	if err != nil {
		return nil, err
	}
	l := newConnListener()
	s.offers[key] = l
	go func() { _ = sender.Serve(ctx, l) }()
	return l, nil
}

// connListener hands connections accepted elsewhere to a transfer.Sender.
type connListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newConnListener() *connListener {
	return &connListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *connListener) push(c net.Conn) error {
	select {
	case l.conns <- c:
		return nil
	case <-l.done:
		_ = c.Close()
		return net.ErrClosed
	}
}

func (l *connListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *connListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *connListener) Addr() net.Addr { return &net.TCPAddr{} }

// LadderOptions are the receiving side's settings.
type LadderOptions struct {
	Addr        string            // the server's address
	Code        string            // printed by the server
	Fingerprint ident.Fingerprint // the server's key, pinned from R2 on
	Identity    *ident.Identity   // this device's key, for TLS
	Streams     int               // data connections (default 4)
	Size        int64             // bytes per step
	ChunkSize   uint32            // R3, R4 (default 4 MiB)
	Dir         string            // R4 writes into a temporary folder here (default: the OS's)
	Sink        transfer.Sink     // R3's sink (DEV-09): null by default
}

// LadderStep is the result of one step.
type LadderStep struct {
	Step       string           `json:"step"`
	Layer      string           `json:"layer"`
	Bytes      int64            `json:"bytes"`
	DurationS  float64          `json:"duration_s"`
	MBps       float64          `json:"mbps"`
	RatioToR1  float64          `json:"ratio_to_r1"`
	Bottleneck *diag.Bottleneck `json:"bottleneck,omitempty"` // R3, R4: limiting stage on this side
}

// LadderResult is the whole ladder, printed by `xfer bench ladder --json`.
type LadderResult struct {
	SchemaVersion int          `json:"schema_version"`
	Streams       int          `json:"streams"`
	ChunkSize     uint32       `json:"chunk_size"`
	Steps         []LadderStep `json:"steps"`
}

// RunLadder measures R1 to R4 against a LadderServer.
func RunLadder(ctx context.Context, o LadderOptions) (*LadderResult, error) {
	if o.Streams <= 0 {
		o.Streams = transfer.DefaultWorkers
	}
	if o.ChunkSize == 0 {
		o.ChunkSize = protocol.DefaultChunkSize
	}
	if o.Size < int64(o.Streams) {
		return nil, fmt.Errorf("bench ladder: %d bytes for %d streams", o.Size, o.Streams)
	}
	res := &LadderResult{SchemaVersion: diag.SchemaVersion, Streams: o.Streams, ChunkSize: o.ChunkSize}
	type step struct {
		name, layer string
		run         func() (*diag.Bottleneck, error)
	}
	steps := []step{
		{"R1", "raw TCP", func() (*diag.Bottleneck, error) { return nil, o.streams(ctx, false) }},
		{"R2", "+ TLS 1.3, pinned key", func() (*diag.Bottleneck, error) { return nil, o.streams(ctx, true) }},
		{"R3", "+ frames and SHA-256, null sink", func() (*diag.Bottleneck, error) { return o.offer(ctx, o.Sink, false) }},
		{"R4", "+ disk writes, no fsync", func() (*diag.Bottleneck, error) { return o.offer(ctx, transfer.SinkDisk, true) }},
	}
	for _, st := range steps {
		start := time.Now()
		b, err := st.run()
		if err != nil {
			return res, fmt.Errorf("%s (%s): %w", st.name, st.layer, err)
		}
		d := time.Since(start)
		r := LadderStep{Step: st.name, Layer: st.layer, Bytes: o.Size, DurationS: round3(d.Seconds()),
			MBps: round2(float64(o.Size) / (1 << 20) / d.Seconds()), Bottleneck: b}
		if len(res.Steps) > 0 && res.Steps[0].MBps > 0 {
			r.RatioToR1 = round2(r.MBps / res.Steps[0].MBps)
		} else {
			r.RatioToR1 = 1
		}
		res.Steps = append(res.Steps, r)
	}
	o.quit(ctx)
	return res, nil
}

// dial opens a connection of the ladder, past its first byte.
func (o LadderOptions) dial(ctx context.Context, sel byte) (net.Conn, error) {
	d := net.Dialer{Timeout: transfer.DefaultDialTimeout}
	c, err := d.DialContext(ctx, "tcp4", o.Addr)
	if err != nil {
		return nil, err
	}
	if _, err := c.Write([]byte{sel}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// tls makes c a TLS connection pinned to the server's key and sends the hello of kind.
func (o LadderOptions) tls(ctx context.Context, c net.Conn, kind byte, size int64) (net.Conn, error) {
	tc, err := securechan.Client(ctx, c, securechan.Config{Identity: o.Identity, Expected: o.Fingerprint})
	if err != nil {
		return nil, err
	}
	hello := padLadderCode(o.Code)
	hello = append(hello, kind)
	hello = binary.BigEndian.AppendUint64(hello, uint64(size))
	hello = binary.BigEndian.AppendUint32(hello, o.ChunkSize)
	if _, err := tc.Write(hello); err != nil {
		tc.Close()
		return nil, err
	}
	return tc, nil
}

// streams reads Size bytes over Streams connections at once: R1 raw, R2 with TLS.
func (o LadderOptions) streams(ctx context.Context, secure bool) error {
	errs := make(chan error, o.Streams)
	share := o.Size / int64(o.Streams)
	for i := 0; i < o.Streams; i++ {
		n := share
		if i == o.Streams-1 {
			n = o.Size - share*int64(o.Streams-1)
		}
		go func() { errs <- o.stream(ctx, secure, n) }()
	}
	var first error
	for i := 0; i < o.Streams; i++ {
		if err := <-errs; err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (o LadderOptions) stream(ctx context.Context, secure bool, n int64) error {
	if !secure {
		c, err := o.dial(ctx, ladderRaw)
		if err != nil {
			return err
		}
		defer c.Close()
		hello := binary.BigEndian.AppendUint64(padLadderCode(o.Code), uint64(n))
		if _, err := c.Write(hello); err != nil {
			return err
		}
		return drain(c, n)
	}
	c, err := o.dial(ctx, ladderTLS)
	if err != nil {
		return err
	}
	tc, err := o.tls(ctx, c, ladderStream, n)
	if err != nil {
		c.Close()
		return err
	}
	defer tc.Close()
	return drain(tc, n)
}

func drain(r io.Reader, n int64) error {
	got, err := io.CopyN(io.Discard, r, n)
	if err == nil && got != n {
		err = io.ErrUnexpectedEOF
	}
	return err
}

// ladderEvents measures the stages of a ladder step.
type ladderEvents struct{ *diag.Stages }

func (ladderEvents) OnProgress(transfer.Progress) {}
func (ladderEvents) OnItem(transfer.ItemEvent)    {}

// offer receives the bench offer with the transfer engine: R3 into sink, R4 to disk without
// fsync. It returns the limiting stage on this side.
func (o LadderOptions) offer(ctx context.Context, sink transfer.Sink, noSync bool) (*diag.Bottleneck, error) {
	out, err := os.MkdirTemp(o.Dir, "medxfer-ladder-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(out)
	offer := transfer.BenchOffer(o.Size, o.ChunkSize)
	join := func(ctx context.Context, raw net.Conn) (net.Conn, error) {
		if _, err := raw.Write([]byte{ladderTLS}); err != nil {
			return nil, err
		}
		return o.tls(ctx, raw, ladderOffer, o.Size)
	}
	r := transfer.NewReceiver(out, join)
	r.SenderKey = o.Fingerprint
	r.Workers, r.Sink, r.NoSync = o.Streams, sink, noSync
	ev := ladderEvents{diag.NewStages()}
	r.Events = ev
	results, err := r.Run(ctx, o.Addr, offer)
	if err != nil {
		return nil, err
	}
	if results[0].State != transfer.ItemDone {
		return nil, fmt.Errorf("bench offer %v: %v", results[0].State, results[0].Err)
	}
	b := ev.Bottleneck()
	return &b, nil
}

// quit tells a server started with once that the ladder is over.
func (o LadderOptions) quit(ctx context.Context) {
	if c, err := o.dial(ctx, ladderQuit); err == nil {
		_, _ = c.Write(padLadderCode(o.Code))
		_ = c.Close()
	}
}

func padLadderCode(code string) []byte {
	b := make([]byte, ladderCodeLen)
	copy(b, code)
	return b
}

func readUint64(r io.Reader) (uint64, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b[:]), nil
}

// FormatLadder renders a ladder result as text.
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func FormatLadder(r *LadderResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Bench ladder: %d streams, chunk %s\n", r.Streams, diag.FormatBytes(int64(r.ChunkSize))))
	for _, s := range r.Steps {
		line := fmt.Sprintf("  %s %-34s %8.1f MB/s  %4.0f %% of R1", s.Step, s.Layer, s.MBps, s.RatioToR1*100)
		if s.Bottleneck != nil && s.Bottleneck.Stage != "" {
			line += fmt.Sprintf("  (limiting: %s)", s.Bottleneck.Stage)
		}
		sb.WriteString(line + "\n")
	}
	return sb.String()
}
