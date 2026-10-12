package transfer

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"

	"github.com/Medboy224/medXfer/pkg/protocol"
)

// KindBench marks an offer made to measure (DEV-08): it goes through the same TLS channel, the
// same data connections, frames and hashes as a real transfer, but its bytes come from memory
// and may be thrown away on arrival.
const KindBench = protocol.KindBench

// Sink is where a receiver puts what it receives.
type Sink int

const (
	SinkDisk Sink = iota // the receive folder (the default)
	SinkNull             // nowhere: the chunks are checked, then dropped (DEV-09)
)

// BenchOffer is a bench offer of one synthetic item of size bytes, in chunks of chunkSize.
func BenchOffer(size int64, chunkSize uint32) protocol.Offer {
	return protocol.Offer{
		TransferID: "bench", Root: "bench.bin", Kind: KindBench, ChunkSize: chunkSize, TotalBytes: size,
		Items: []protocol.OfferItem{{Item: 0, Path: "bench.bin", Size: size}},
	}
}

// Sources of a bench sender (DEV-09: --source=zero|random|file).

// ZeroSource reads zeros.
func ZeroSource() io.ReaderAt { return zeroSource{} }

type zeroSource struct{}

func (zeroSource) ReadAt(p []byte, _ int64) (int, error) {
	clear(p)
	return len(p), nil
}

// RandomSource reads random bytes from a 4 MiB block drawn once and repeated: fast enough
// never to be what limits a measure, and incompressible within a chunk.
func RandomSource() (io.ReaderAt, error) {
	block := make([]byte, 4<<20)
	if _, err := rand.Read(block); err != nil {
		return nil, err
	}
	return randomSource(block), nil
}

type randomSource []byte

func (b randomSource) ReadAt(p []byte, off int64) (int, error) {
	n := 0
	for n < len(p) {
		k := copy(p[n:], b[(off+int64(n))%int64(len(b)):])
		n += k
	}
	return n, nil
}

// FileSource reads path, a real file, and returns its size.
func FileSource(path string) (io.ReaderAt, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, fi.Size(), nil
}

// NewSourceSender serves a one-item offer from src instead of a file: a bench offer.
func NewSourceSender(offer protocol.Offer, src io.ReaderAt) (*Sender, error) {
	if len(offer.Items) != 1 {
		return nil, fmt.Errorf("transfer: a source serves one item, the offer has %d", len(offer.Items))
	}
	s, err := NewSender(offer, []string{""})
	if err != nil {
		return nil, err
	}
	s.src = src
	return s, nil
}
