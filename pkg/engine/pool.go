package engine

import (
	"sync"
	"sync/atomic"

	"github.com/Medboy224/medXfer/pkg/protocol"
)

// chunkBufferPool provides recycled memory buffers for reading and writing chunk frames.
// This completely eliminates heap allocations and reduces GC pressure to near-zero during
// multi-gigabyte file transfers.
var chunkBufferPool = sync.Pool{
	New: func() interface{} {
		// Maximum chunk frame: FrameHeaderSize (8) + ChunkHeaderSize (20) + up to 8MB chunk data
		b := make([]byte, protocol.FrameHeaderSize+protocol.ChunkHeaderSize+8*1024*1024)
		return &b
	},
}

// buffersInUse / buffersPeak count borrowed buffers; tests use them to check the
// receiver's memory bound (STO-08).
var buffersInUse, buffersPeak atomic.Int64

func getChunkBuffer() *[]byte {
	n := buffersInUse.Add(1)
	for {
		p := buffersPeak.Load()
		if n <= p || buffersPeak.CompareAndSwap(p, n) {
			break
		}
	}
	return chunkBufferPool.Get().(*[]byte)
}

func putChunkBuffer(b *[]byte) {
	buffersInUse.Add(-1)
	chunkBufferPool.Put(b)
}
