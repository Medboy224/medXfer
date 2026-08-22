package protocol

import "errors"

const (
	MagicBytes uint16 = 0x5846 // "XF"
	Version1   uint8  = 0x01

	FrameHeaderSize = 8
	ChunkHeaderSize = 20

	DefaultChunkSize = 4 * 1024 * 1024 // 4 MB
)

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

type Metadata struct {
	SessionID   string `json:"session_id"`
	FileName    string `json:"file_name"`
	FileSize    int64  `json:"file_size"`
	ChunkSize   uint32 `json:"chunk_size"`
	TotalChunks uint32 `json:"total_chunks"`
	FileSHA256  string `json:"file_sha256,omitempty"`
}

type ChunkHeader struct {
	Index    uint32
	Offset   uint64
	DataLen  uint32
	Checksum uint32
}
