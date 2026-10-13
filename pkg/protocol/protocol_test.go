package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"runtime"
	"testing"
)

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
		"type":      {frameHeader(TypeChunkData, 2), ErrUnexpectedType},
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
	f.Add(append(frameHeader(TypeChunkRequest, 8), 0, 0, 0, 1, 0, 0, 0, 7))
	f.Add([]byte("MX"))
	limits := Limits{TypeControl: 4096, TypeChunkRequest: ChunkRequestLen, TypeDataJoin: DataJoinLen}
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
		"kind":       func(o *Offer) { o.Kind = "mystery" },
		// #75: two items on one file, or a file that is the folder of another item.
		"same path":            func(o *Offer) { o.Items[1].Path = o.Items[0].Path },
		"same path, backslash": func(o *Offer) { o.Items[0].Path = "dir/b.txt"; o.Items[1].Path = `dir\b.txt` },
		"file is a folder":     func(o *Offer) { o.Items[0].Path = "dir" },
		"same path, case":      func(o *Offer) { o.Items[0].Path = "DIR/B.TXT" }, // STO-14
		"chunk high":           func(o *Offer) { o.ChunkSize = MaxChunkSize + 1 },
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
				o.Items[i] = OfferItem{Item: i, Path: fmt.Sprintf("%s%05d", long, i)}
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

// DIGEST_REQ and DIGEST round trip; the digest depends on every chunk hash, their order and
// their number (bible §8.3).
func TestDigestFrames(t *testing.T) {
	limits := Limits{TypeDigestRequest: DigestRequestLen, TypeDigest: DigestLen}
	h, p, err := ReadFrame(bytes.NewReader(AppendDigestRequest(nil, 7)), limits, nil)
	if err != nil || h.Type != TypeDigestRequest {
		t.Fatal(h, err)
	}
	if item, err := ParseDigestRequest(p); err != nil || item != 7 {
		t.Fatal(item, err)
	}
	d := FileDigest([][32]byte{{1}, {2}})
	h, p, err = ReadFrame(bytes.NewReader(AppendDigest(nil, 9, d)), limits, nil)
	if err != nil || h.Type != TypeDigest {
		t.Fatal(h, err)
	}
	if item, got, err := ParseDigest(p); err != nil || item != 9 || got != d {
		t.Fatal(item, err)
	}
	for name, other := range map[string][32]byte{
		"order":  FileDigest([][32]byte{{2}, {1}}),
		"number": FileDigest([][32]byte{{1}, {2}, {}}),
		"empty":  FileDigest(nil),
	} {
		if other == d {
			t.Errorf("%s: same digest", name)
		}
	}
}

func TestDoneFrame(t *testing.T) {
	for _, status := range []byte{DoneComplete, DonePartial} {
		h, p, err := ReadFrame(bytes.NewReader(AppendDone(nil, status)), Limits{TypeDone: DoneLen}, nil)
		if err != nil || h.Type != TypeDone {
			t.Fatal(h, err)
		}
		if got, err := ParseDone(p); err != nil || got != status {
			t.Fatalf("status %d read as %d (%v)", status, got, err)
		}
	}
	if _, err := ParseDone(nil); err == nil {
		t.Fatal("an empty DONE was accepted")
	}
}
