package protocol

const (
	MagicBytes      uint16 = 0x4D58 // 'MX'
	Version1        byte   = 0x01
	TypeHandshake   byte   = 0x01
	TypeRequest     byte   = 0x02
	TypeChunk       byte   = 0x03
	TypeResume      byte   = 0x04
	FrameHeaderSize        = 8  // Untyped to prevent uint32/int math errors
	ChunkHeaderSize        = 20 // Untyped to prevent uint32/int math errors
)

type Header struct {
	Type       byte
	PayloadLen uint32
}

type FileMetadata struct {
	FileName  string `json:"fileName"`
	FileSize  int64  `json:"fileSize"`
	ChunkSize uint32 `json:"chunkSize"`
	FileID    string `json:"fileID,omitempty"`
}

type ChunkMeta struct {
	Index  uint32
	Offset uint64
	Length uint32
}
