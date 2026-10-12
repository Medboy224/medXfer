package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// OfferItem is one file of an offer (bible §8.3).
type OfferItem struct {
	Item   int    `json:"item"`
	Path   string `json:"path"` // relative, "/"-separated; the receiver checks it (SafeRelPath)
	Size   int64  `json:"size"`
	MTime  int64  `json:"mtime,omitempty"`   // unix nanoseconds: a changed source is detected (WP-4.4)
	FileID string `json:"file_id,omitempty"` // resume key of the current storage, until J4b
}

// Offer covers a file, several files or a folder in one transfer (bible §8.3): the receiver
// requests the chunks of every item on the same data connections.
type Offer struct {
	TransferID string      `json:"transfer_id"`
	Root       string      `json:"root,omitempty"` // what the user sees: the file or folder name
	Items      []OfferItem `json:"items"`
	TotalBytes int64       `json:"total_bytes"`
	ChunkSize  uint32      `json:"chunk_size"`
	Kind       string      `json:"kind,omitempty"` // "" or "bench" (DEV-08)
}

// Limits of an offer (LIM-05) and of the chunk size (XFER-02, plan J4a D4). All are
// parameters (rule R4); these are the defaults.
const (
	MaxOfferItems    = 100_000
	MaxManifestBytes = 16 << 20
	MinChunkSize     = 256 << 10
	MaxChunkSize     = 8 << 20
	DefaultChunkSize = 4 << 20
	maxPathBytes     = 1024 // LIM-06
)

var ErrBadOffer = errors.New("protocol: invalid offer")

// Chunks is the number of chunks of an item of size bytes.
func Chunks(size int64, chunkSize uint32) uint32 {
	if size <= 0 {
		return 0
	}
	return uint32((size + int64(chunkSize) - 1) / int64(chunkSize))
}

// Validate checks what a receiver must check before accepting an offer: its size limits
// (LIM-05, LIM-06), item numbering (0 to n-1, in order), sizes and totals, chunk size.
func (o *Offer) Validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrBadOffer, fmt.Sprintf(format, a...))
	}
	if len(o.Items) > MaxOfferItems {
		return bad("%d items, at most %d", len(o.Items), MaxOfferItems)
	}
	if o.ChunkSize < MinChunkSize || o.ChunkSize > MaxChunkSize {
		return bad("chunk size %d outside [%d, %d]", o.ChunkSize, MinChunkSize, MaxChunkSize)
	}
	var total, manifest int64
	for i, it := range o.Items {
		if it.Item != i {
			return bad("item %d is numbered %d", i, it.Item)
		}
		if it.Size < 0 {
			return bad("item %d has a negative size", i)
		}
		if it.Path == "" || len(it.Path) > maxPathBytes {
			return bad("item %d has an empty or too long path", i)
		}
		if Chunks(it.Size, o.ChunkSize) > 1<<24 {
			return bad("item %d has too many chunks", i)
		}
		total += it.Size
		manifest += int64(len(it.Path) + len(it.FileID) + 64)
	}
	if manifest > MaxManifestBytes {
		return bad("manifest of about %d bytes, at most %d", manifest, MaxManifestBytes)
	}
	if total != o.TotalBytes {
		return bad("total %d bytes, items add up to %d", o.TotalBytes, total)
	}
	return nil
}

// Data frames of protocol v1 (bible §8.2).
const (
	TypeChunkRequest byte = 0x21 // CHUNK_REQ: item u32, index u32
	TypeChunkData    byte = 0x22 // CHUNK: item u32, index u32, length u32, sha256, then the data
	TypeChunkError   byte = 0x23 // CHUNK_ERR: item u32, index u32, code u16

	ChunkRequestLen    = 8
	ChunkDataHeaderLen = 44
	ChunkErrorLen      = 10
)

// Codes of CHUNK_ERR.
const (
	ChunkErrRead          uint16 = 1 // READ_ERROR
	ChunkErrSourceChanged uint16 = 2 // SOURCE_CHANGED (WP-4.4)
	ChunkErrNoSuchChunk   uint16 = 3 // the item or chunk is not in the offer
)

// AppendChunkRequest appends a CHUNK_REQ frame to dst.
func AppendChunkRequest(dst []byte, item, index uint32) []byte {
	var p [ChunkRequestLen]byte
	binary.BigEndian.PutUint32(p[0:4], item)
	binary.BigEndian.PutUint32(p[4:8], index)
	return AppendFrame(dst, TypeChunkRequest, p[:])
}

// ParseChunkRequest reads the payload of a CHUNK_REQ.
func ParseChunkRequest(p []byte) (item, index uint32, err error) {
	if len(p) != ChunkRequestLen {
		return 0, 0, fmt.Errorf("%w: CHUNK_REQ of %d bytes", ErrFrameTooLarge, len(p))
	}
	return binary.BigEndian.Uint32(p[0:4]), binary.BigEndian.Uint32(p[4:8]), nil
}

// AppendChunkData appends a CHUNK frame (header, chunk header, data) to dst.
func AppendChunkData(dst []byte, item, index uint32, sum [32]byte, data []byte) []byte {
	var h [FrameHeaderSize + ChunkDataHeaderLen]byte
	copy(h[:], EncodeHeader(Header{Type: TypeChunkData, PayloadLen: uint32(ChunkDataHeaderLen + len(data))}))
	c := h[FrameHeaderSize:]
	binary.BigEndian.PutUint32(c[0:4], item)
	binary.BigEndian.PutUint32(c[4:8], index)
	binary.BigEndian.PutUint32(c[8:12], uint32(len(data)))
	copy(c[12:44], sum[:])
	return append(append(dst, h[:]...), data...)
}

// ParseChunkData reads the payload of a CHUNK: its data is p's tail, not a copy.
func ParseChunkData(p []byte) (item, index uint32, sum [32]byte, data []byte, err error) {
	if len(p) < ChunkDataHeaderLen {
		return 0, 0, sum, nil, fmt.Errorf("protocol: CHUNK of %d bytes", len(p))
	}
	item = binary.BigEndian.Uint32(p[0:4])
	index = binary.BigEndian.Uint32(p[4:8])
	n := binary.BigEndian.Uint32(p[8:12])
	copy(sum[:], p[12:44])
	data = p[ChunkDataHeaderLen:]
	if int(n) != len(data) {
		return 0, 0, sum, nil, fmt.Errorf("protocol: CHUNK announces %d bytes, carries %d", n, len(data))
	}
	return item, index, sum, data, nil
}

// AppendChunkError appends a CHUNK_ERR frame to dst.
func AppendChunkError(dst []byte, item, index uint32, code uint16) []byte {
	var p [ChunkErrorLen]byte
	binary.BigEndian.PutUint32(p[0:4], item)
	binary.BigEndian.PutUint32(p[4:8], index)
	binary.BigEndian.PutUint16(p[8:10], code)
	return AppendFrame(dst, TypeChunkError, p[:])
}

// ParseChunkError reads the payload of a CHUNK_ERR.
func ParseChunkError(p []byte) (item, index uint32, code uint16, err error) {
	if len(p) != ChunkErrorLen {
		return 0, 0, 0, fmt.Errorf("protocol: CHUNK_ERR of %d bytes", len(p))
	}
	return binary.BigEndian.Uint32(p[0:4]), binary.BigEndian.Uint32(p[4:8]), binary.BigEndian.Uint16(p[8:10]), nil
}
