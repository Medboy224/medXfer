package protocol

import (
	"encoding/json"
	"errors"
)

const (
 MagicBytes uint16 = 0x5846 // "XF"
 Version1 uint16 = 0x01

 FrameHeaderSize = 8  // 2 (Magic) + 1 (Ver) + 1 (Type) + 4 (Length)
 ChunkHeaderSize = 20 // 4 (Index) + 8 (Offset) + 4 (Size) + 4 (CRC32)

 DefaultChunkSize = 4 * 1024 * 1024 // 4 MB chunk slices
)

// Frame Types
const (
 TypeHandshake byte = 0x01
 TypeMeta      byte = 0x02
 TypeChunk     byte = 0x03
 TypeAck       byte = 0x04
 TypeProbe     byte = 0x05
)


var (
 ErrInvalidMagic       = errors.New("invalid protocol magic bytes")
 ErrUnsuportedVersion  = errors.New("unsuported protocol version")
 ErrChecksumMismatch   = errors.New("chunk CRC32 checksum verification failed")
 ErrPayloadTooLarge    = errors.New("payload length exceeds buffer limits")
)

// Metadata represents file details exchanged before transferring chunks
type Metadata struct {
 SessionId     string `json:"session_id"`
 FileName      string `json:"file_name"`
 FileSize      int64  `json:"file_size"`
 ChunkSize     uint32 `json:"chunk_size"`
 TotalChunks   uint32 `json:"total_chunks"` 
 FileSHA256    string `json:"file_sha256,omitempty"`
}
