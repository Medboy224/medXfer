package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"runtime"
	"testing"
)

func TestCalculateChecksum(t *testing.T) {
	data := []byte("medXfer test data")
	crc := CalculateChecksum(data)
	if crc == 0 {
		t.Fatal("Expected non-zero checksum")
	}
}

func TestHandshakeReadWrite(t *testing.T) {
	meta := FileMetadata{
		FileName:  "test_movie.mp4",
		FileSize:  1024576,
		ChunkSize: 2097152,
	}

	var buf bytes.Buffer
	err := WriteHandshake(&buf, meta)
	if err != nil {
		t.Fatalf("WriteHandshake failed: %v", err)
	}

	header, err := ReadFrameHeader(&buf, Limits{TypeHandshake: MaxHandshake, TypeResume: ResumeLen})
	if err != nil {
		t.Fatalf("ReadFrameHeader failed: %v", err)
	}

	if header.Type != TypeHandshake {
		t.Fatalf("Expected TypeHandshake, got %v", header.Type)
	}

	parsedMeta, err := ReadHandshakePayload(&buf, header.PayloadLen)
	if err != nil {
		t.Fatalf("ReadHandshakePayload failed: %v", err)
	}

	if parsedMeta.FileName != meta.FileName || parsedMeta.FileSize != meta.FileSize {
		t.Fatalf("Metadata mismatch. Got: %+v, Want: %+v", parsedMeta, meta)
	}
}

func TestParseChunkPayload(t *testing.T) {
	data := []byte("dummy chunk data")
	payload := make([]byte, ChunkHeaderSize+len(data))

	// Mock 20-byte Chunk Header
	binary.BigEndian.PutUint32(payload[0:4], 5)                         // Index
	binary.BigEndian.PutUint64(payload[4:12], 4096)                     // Offset
	binary.BigEndian.PutUint32(payload[12:16], uint32(len(data)))       // Length
	binary.BigEndian.PutUint32(payload[16:20], CalculateChecksum(data)) // CRC32

	// Append data
	copy(payload[ChunkHeaderSize:], data)

	meta, parsedData, err := ParseChunkPayload(payload)
	if err != nil {
		t.Fatalf("ParseChunkPayload failed: %v", err)
	}

	if meta.Index != 5 || meta.Offset != 4096 {
		t.Fatalf("Chunk meta mismatch")
	}

	if string(parsedData) != string(data) {
		t.Fatalf("Chunk data mismatch")
	}
}

func TestTypeResumeFrame(t *testing.T) {
	var buf bytes.Buffer
	var frame [16]byte
	binary.BigEndian.PutUint16(frame[0:2], MagicBytes)
	frame[2] = Version1
	frame[3] = TypeResume
	binary.BigEndian.PutUint32(frame[4:8], 8)
	binary.BigEndian.PutUint64(frame[8:16], 52428800)

	buf.Write(frame[:])

	header, err := ReadFrameHeader(&buf, Limits{TypeHandshake: MaxHandshake, TypeResume: ResumeLen})
	if err != nil {
		t.Fatalf("ReadFrameHeader failed: %v", err)
	}
	if header.Type != TypeResume {
		t.Fatalf("Expected TypeResume, got %v", header.Type)
	}
	if header.PayloadLen != 8 {
		t.Fatalf("Expected payload length 8, got %d", header.PayloadLen)
	}

	payload := make([]byte, 8)
	if _, err := buf.Read(payload); err != nil {
		t.Fatalf("Read payload failed: %v", err)
	}
	offset := binary.BigEndian.Uint64(payload)
	if offset != 52428800 {
		t.Fatalf("Expected offset 52428800, got %d", offset)
	}
}

func frameHeader(typ byte, n uint32) []byte {
	var h [FrameHeaderSize]byte
	binary.BigEndian.PutUint16(h[0:2], MagicBytes)
	h[2] = Version1
	h[3] = typ
	binary.BigEndian.PutUint32(h[4:8], n)
	return h[:]
}

var controlOnly = Limits{TypeControl: MaxControl}

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, TypeControl, []byte(`{"type":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	h, payload, err := ReadFrame(&buf, controlOnly, nil)
	if err != nil || h.Type != TypeControl || string(payload) != `{"type":"hello"}` {
		t.Fatalf("got %+v %q %v", h, payload, err)
	}
}

// XFER-01, LIM-04: a CONTROL frame announced past 1 MiB is refused from its header alone,
// before the payload is read or any memory is set aside for it.
func TestOversizedFrameIsRefusedBeforeAllocating(t *testing.T) {
	frame := frameHeader(TypeControl, MaxControl+1) // no payload follows
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := 0; i < 50; i++ {
		_, _, err := ReadFrame(bytes.NewReader(frame), controlOnly, nil)
		if !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("err = %v, want ErrFrameTooLarge", err)
		}
	}
	runtime.ReadMemStats(&after)
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 64<<10 {
		t.Fatalf("refusing 50 oversized frames allocated %d bytes", grown)
	}
	// Exactly at the limit is accepted.
	full := append(frameHeader(TypeControl, MaxControl), make([]byte, MaxControl)...)
	if _, p, err := ReadFrame(bytes.NewReader(full), controlOnly, nil); err != nil || len(p) != MaxControl {
		t.Fatalf("frame at the limit: %d bytes, %v", len(p), err)
	}
}

func TestBadFramesAreRefused(t *testing.T) {
	badMagic := frameHeader(TypeControl, 2)
	badMagic[0] = 'X'
	badVersion := frameHeader(TypeControl, 2)
	badVersion[2] = 2
	for name, c := range map[string]struct {
		frame []byte
		want  error
	}{
		"magic":     {badMagic, ErrBadMagic},
		"version":   {badVersion, ErrBadVersion},
		"type":      {frameHeader(TypeChunk, 2), ErrUnexpectedType},
		"truncated": {append(frameHeader(TypeControl, 10), "short"...), io.ErrUnexpectedEOF},
		"no header": {[]byte{0x4D, 0x58, 1}, io.ErrUnexpectedEOF},
	} {
		if _, _, err := ReadFrame(bytes.NewReader(c.frame), controlOnly, nil); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
}

// Native fuzzing of the frame reader (bible §14.2): whatever the bytes, it never panics and
// never returns more than the limit of the type it accepted.
func FuzzReadFrame(f *testing.F) {
	f.Add(append(frameHeader(TypeControl, 2), "{}"...))
	f.Add(frameHeader(TypeControl, MaxControl+1))
	f.Add(append(frameHeader(TypeRequest, 4), 0, 0, 0, 7))
	f.Add([]byte("MX"))
	limits := Limits{TypeControl: 4096, TypeRequest: RequestLen, TypeResume: ResumeLen}
	f.Fuzz(func(t *testing.T, data []byte) {
		h, payload, err := ReadFrame(bytes.NewReader(data), limits, nil)
		if err != nil {
			return
		}
		max, ok := limits[h.Type]
		if !ok || uint32(len(payload)) > max || uint32(len(payload)) != h.PayloadLen {
			t.Fatalf("accepted %+v with %d bytes", h, len(payload))
		}
	})
}

func validOffer() Offer {
	return Offer{TransferID: "t", ChunkSize: DefaultChunkSize, TotalBytes: 15,
		Items: []OfferItem{{Item: 0, Path: "a.txt", Size: 5}, {Item: 1, Path: "dir/b.txt", Size: 10}}}
}

func TestOfferValidate(t *testing.T) {
	o := validOffer()
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Offer){
		"total":      func(o *Offer) { o.TotalBytes++ },
		"numbering":  func(o *Offer) { o.Items[1].Item = 5 },
		"negative":   func(o *Offer) { o.Items[0].Size = -1; o.TotalBytes = 9 },
		"empty path": func(o *Offer) { o.Items[0].Path = "" },
		"chunk low":  func(o *Offer) { o.ChunkSize = MinChunkSize - 1 },
		"chunk high": func(o *Offer) { o.ChunkSize = MaxChunkSize + 1 },
		"items": func(o *Offer) { // LIM-05: 100 001 items
			o.Items = make([]OfferItem, MaxOfferItems+1)
			for i := range o.Items {
				o.Items[i] = OfferItem{Item: i, Path: "f"}
			}
			o.TotalBytes = 0
		},
		"manifest": func(o *Offer) { // LIM-05: more than 16 MiB of manifest
			long := string(bytes.Repeat([]byte("p"), 1000))
			o.Items = make([]OfferItem, 20_000)
			for i := range o.Items {
				o.Items[i] = OfferItem{Item: i, Path: long}
			}
			o.TotalBytes = 0
		},
	} {
		o := validOffer()
		mutate(&o)
		if err := o.Validate(); !errors.Is(err, ErrBadOffer) {
			t.Errorf("%s: %v, want ErrBadOffer", name, err)
		}
	}
}

func TestChunkFrames(t *testing.T) {
	var sum [32]byte
	sum[0], sum[31] = 7, 9
	frame := AppendChunkData(nil, 3, 41, sum, []byte("payload"))
	h, p, err := ReadFrame(bytes.NewReader(frame), Limits{TypeChunkData: ChunkDataHeaderLen + 16}, nil)
	if err != nil || h.Type != TypeChunkData {
		t.Fatal(h, err)
	}
	item, index, got, data, err := ParseChunkData(p)
	if err != nil || item != 3 || index != 41 || got != sum || string(data) != "payload" {
		t.Fatalf("CHUNK: %d %d %v %q %v", item, index, got, data, err)
	}
	p[10]++ // the announced length no longer matches
	if _, _, _, _, err := ParseChunkData(p); err == nil {
		t.Fatal("a CHUNK with a wrong length was accepted")
	}

	_, p, _ = ReadFrame(bytes.NewReader(AppendChunkRequest(nil, 2, 99)), Limits{TypeChunkRequest: ChunkRequestLen}, nil)
	if it, ix, err := ParseChunkRequest(p); err != nil || it != 2 || ix != 99 {
		t.Fatalf("CHUNK_REQ: %d %d %v", it, ix, err)
	}
	_, p, _ = ReadFrame(bytes.NewReader(AppendChunkError(nil, 1, 4, ChunkErrRead)), Limits{TypeChunkError: ChunkErrorLen}, nil)
	if it, ix, code, err := ParseChunkError(p); err != nil || it != 1 || ix != 4 || code != ChunkErrRead {
		t.Fatalf("CHUNK_ERR: %d %d %d %v", it, ix, code, err)
	}
}
