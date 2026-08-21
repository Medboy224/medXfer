package protocol

import "errors"

const (
	MagicBytes uint16 = 0x5846 // "XF" (Xfer)
	Version1   uint8  = 0x01

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
	ErrUnsupportedVersion = errors.New("unsupported protocol version")
	ErrChecksumMismatch   = errors.New("chunk CRC32 checksum verification failed")
	ErrPayloadTooLarge    = errors.New("payload length exceeds buffer limit")
)

// Metadata represents file details exchanged before transferring chunks
type Metadata struct {
	SessionID   string `json:"session_id"`
	FileName    string `json:"file_name"`
	FileSize    int64  `json:"file_size"`
	ChunkSize   uint32 `json:"chunk_size"`
	TotalChunks uint32 `json:"total_chunks"`
	FileSHA256  string `json:"file_sha256,omitempty"`
}

// ChunkHeader prefixes every raw data block transmitted across worker streams
type ChunkHeader struct {
	Index    uint32 // Chunk sequence index
	Offset   uint64 // Absolute byte position in destination file
	DataLen  uint32 // Length of binary chunk data
	Checksum uint32 // IEEE CRC32 of Payload
}

// AckPayload confirms chunk receipt or session readiness
type AckPayload struct {
	Index   uint32 `json:"index"`
	Success bool   `json:"success"`
}
