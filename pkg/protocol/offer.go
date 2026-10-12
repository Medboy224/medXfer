package protocol

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// OfferItem is one file of an offer (bible §8.3).
type OfferItem struct {
	Item  int    `json:"item"`
	Path  string `json:"path"` // relative, "/"-separated; the receiver checks it (SafeRelPath)
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime,omitempty"` // unix nanoseconds: a changed source is detected (WP-4.4)
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
	// Count is the number of items when the offer comes in pages (decision D2): the head
	// carries the first items, offer_items messages the others. 0: all items are here.
	Count int `json:"count,omitempty"`
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

// KindBench marks an offer made to measure (DEV-08): synthetic bytes, possibly dropped on
// arrival. An offer of no kind is a transfer of files.
const KindBench = "bench"

// Chunks is the number of chunks of an item of size bytes.
func Chunks(size int64, chunkSize uint32) uint32 {
	if size <= 0 {
		return 0
	}
	return uint32((size + int64(chunkSize) - 1) / int64(chunkSize))
}

// Validate checks what a receiver must check before accepting an offer: its size limits
// (LIM-05, LIM-06), item numbering (0 to n-1, in order), sizes and totals, chunk size, and
// that no two items aim at the same file and no file is the folder of another (#75).
func (o *Offer) Validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrBadOffer, fmt.Sprintf(format, a...))
	}
	if len(o.Items) > MaxOfferItems {
		return bad("%d items, at most %d", len(o.Items), MaxOfferItems)
	}
	if o.Kind != "" && o.Kind != KindBench {
		return bad("unknown kind %q", o.Kind)
	}
	if o.ChunkSize < MinChunkSize || o.ChunkSize > MaxChunkSize {
		return bad("chunk size %d outside [%d, %d]", o.ChunkSize, MinChunkSize, MaxChunkSize)
	}
	var total, manifest int64
	files := make(map[string]bool, len(o.Items))
	dirs := map[string]bool{}
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
		// The receiver reads both separators alike, and Windows, macOS and Android's shared
		// storage do not tell "A.txt" from "a.txt": such paths would land on one file (STO-14).
		key := strings.ToLower(strings.ReplaceAll(it.Path, `\`, "/"))
		if files[key] {
			return bad("item %d repeats the path of another item (letter case aside)", i)
		}
		files[key] = true
		for j := strings.LastIndexByte(key, '/'); j > 0; j = strings.LastIndexByte(key[:j], '/') {
			dirs[key[:j]] = true
		}
		total += it.Size
		manifest += int64(len(it.Path) + 64)
	}
	for d := range dirs {
		if files[d] {
			return bad("%q is both a file and the folder of other items", d)
		}
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
	// The end of an item (decision D7 of the J4a plan): the receiver asks for the digest of
	// the sender's chunk hashes and compares it with its own (bible §8.3, step 6).
	TypeDigestRequest byte = 0x24 // DIGEST_REQ: item u32
	TypeDigest        byte = 0x25 // DIGEST: item u32, digest (32 bytes)
	// The end of the transfer (decision D8 of the J4a plan; bible §8.3, step 7): the receiver
	// says every item ended, so the sender stops waiting.
	TypeDone byte = 0x26 // DONE: status u8

	ChunkRequestLen    = 8
	ChunkDataHeaderLen = 44
	ChunkErrorLen      = 10
	DigestRequestLen   = 4
	DigestLen          = 36
	DoneLen            = 1

	// DigestIndex is the index of a CHUNK_ERR that answers a DIGEST_REQ.
	DigestIndex uint32 = 0xFFFFFFFF
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

// AppendDigestRequest appends a DIGEST_REQ frame to dst.
func AppendDigestRequest(dst []byte, item uint32) []byte {
	var p [DigestRequestLen]byte
	binary.BigEndian.PutUint32(p[:], item)
	return AppendFrame(dst, TypeDigestRequest, p[:])
}

// ParseDigestRequest reads the payload of a DIGEST_REQ.
func ParseDigestRequest(p []byte) (item uint32, err error) {
	if len(p) != DigestRequestLen {
		return 0, fmt.Errorf("protocol: DIGEST_REQ of %d bytes", len(p))
	}
	return binary.BigEndian.Uint32(p), nil
}

// AppendDigest appends a DIGEST frame to dst.
func AppendDigest(dst []byte, item uint32, digest [32]byte) []byte {
	var p [DigestLen]byte
	binary.BigEndian.PutUint32(p[0:4], item)
	copy(p[4:], digest[:])
	return AppendFrame(dst, TypeDigest, p[:])
}

// ParseDigest reads the payload of a DIGEST.
func ParseDigest(p []byte) (item uint32, digest [32]byte, err error) {
	if len(p) != DigestLen {
		return 0, digest, fmt.Errorf("protocol: DIGEST of %d bytes", len(p))
	}
	copy(digest[:], p[4:])
	return binary.BigEndian.Uint32(p[0:4]), digest, nil
}

// Status of a DONE.
const (
	DoneComplete byte = 0 // every item was received
	DonePartial  byte = 1 // some items were skipped or failed
)

// AppendDone appends a DONE frame to dst.
func AppendDone(dst []byte, status byte) []byte { return AppendFrame(dst, TypeDone, []byte{status}) }

// ParseDone reads the payload of a DONE.
func ParseDone(p []byte) (status byte, err error) {
	if len(p) != DoneLen {
		return 0, fmt.Errorf("protocol: DONE of %d bytes", len(p))
	}
	return p[0], nil
}

// FileDigest is the digest of an item from the SHA-256 of its n chunks, in order:
// SHA-256("medxfer-v1-filedigest" ‖ n (u32, big-endian) ‖ hash_0 ‖ … ‖ hash_{n-1}) (bible §8.3).
func FileDigest(hashes [][32]byte) [32]byte {
	h := sha256.New()
	h.Write([]byte("medxfer-v1-filedigest"))
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(hashes)))
	h.Write(n[:])
	for i := range hashes {
		h.Write(hashes[i][:])
	}
	var d [32]byte
	copy(d[:], h.Sum(nil))
	return d
}

// Split cuts the offer into a head and pages whose items each take at most maxBytes of JSON,
// so that every message fits in a CONTROL frame (LIM-04; decision D2). The head carries the
// offer's fields, Count and the first items.
func (o Offer) Split(maxBytes int) (Offer, [][]OfferItem) {
	var pages [][]OfferItem
	var page []OfferItem
	size := 0
	for _, it := range o.Items {
		b, _ := json.Marshal(it)
		if len(page) > 0 && size+len(b)+1 > maxBytes {
			pages = append(pages, page)
			page, size = nil, 0
		}
		page = append(page, it)
		size += len(b) + 1
	}
	if len(page) > 0 || len(pages) == 0 {
		pages = append(pages, page)
	}
	head := o
	head.Items = pages[0]
	head.Count = 0
	if len(pages) > 1 {
		head.Count = len(o.Items)
	}
	return head, pages[1:]
}

// Complete reports whether every item of a paged offer has arrived.
func (o *Offer) Complete() bool { return o.Count == 0 || len(o.Items) >= o.Count }

// AddPage appends a page of items received after the head: the numbering must go on, and the
// offer may not grow past Count or MaxOfferItems (LIM-05).
func (o *Offer) AddPage(items []OfferItem) error {
	if o.Count <= 0 || o.Count > MaxOfferItems || len(o.Items)+len(items) > o.Count {
		return fmt.Errorf("%w: more items than announced (%d)", ErrBadOffer, o.Count)
	}
	for _, it := range items {
		if it.Item != len(o.Items) {
			return fmt.Errorf("%w: item %d arrives as %d", ErrBadOffer, len(o.Items), it.Item)
		}
		o.Items = append(o.Items, it)
	}
	return nil
}
