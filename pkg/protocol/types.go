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

// Protocol v1 (bible §8.2). TypeControl carries the JSON messages of a session; the data
// frame types of v1 come with the lots that use them (WP-4.2, WP-4.3).
const (
	TypeControl  byte = 0x10
	TypeDataJoin byte = 0x20 // first frame of a data connection: the transfer it joins (CHAN-06)

	// DataJoinLen is the payload of DATA_JOIN: a 128-bit transfer id.
	DataJoinLen = 16

	// MaxControl is the default bound of a CONTROL payload (LIM-04). Like every limit it is
	// a parameter (rule R4): session.Channel.SetMaxControl.
	MaxControl = 1 << 20
)

// Bounds of the first engine's data frames, which WP-4.3 replaces. Until then every one of
// them is read within these bounds (XFER-01).
const (
	MaxHandshake = 64 << 10 // file metadata JSON
	ResumeLen    = 40       // file id (32) and offset (8)
	RequestLen   = 4        // chunk index
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
