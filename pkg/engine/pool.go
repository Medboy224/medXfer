package engine

import (
	"sync"

	"github.com/Medboy224/medXfer/pkg/protocol"
)

// chunkBufferPool provides recycled memory buffers for reading and writing chunk frames.
// This completely eliminates heap allocations and reduces GC pressure to near-zero during
// multi-gigabyte file transfers.
var chunkBufferPool = sync.Pool{
	New: func() interface{} {
		// Maximum chunk frame: FrameHeaderSize (8) + ChunkHeaderSize (20) + up to 4MB chunk data
		b := make([]byte, protocol.FrameHeaderSize+protocol.ChunkHeaderSize+4*1024*1024)
		return &b
	},
}

func getChunkBuffer() *[]byte {
	return chunkBufferPool.Get().(*[]byte)
}

func putChunkBuffer(b *[]byte) {
	chunkBufferPool.Put(b)
}
