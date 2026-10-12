package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var (
	ErrBadMagic       = errors.New("protocol: bad magic bytes")
	ErrBadVersion     = errors.New("protocol: unsupported version")
	ErrUnexpectedType = errors.New("protocol: unexpected frame type")
	ErrFrameTooLarge  = errors.New("protocol: frame payload exceeds its limit")
)

// Limits maps each frame type accepted at a point of the protocol to its largest payload. A
// type absent from the map is refused: every read is bounded (XFER-01).
type Limits map[byte]uint32

// ReadFrameHeader reads a frame header and checks it against limits: magic, version, type,
// and payload length. It allocates nothing for the payload, so an announced length past the
// limit costs the reader nothing.
func ReadFrameHeader(r io.Reader, limits Limits) (Header, error) {
	var buf [FrameHeaderSize]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return Header{}, err
	}
	if binary.BigEndian.Uint16(buf[0:2]) != MagicBytes {
		return Header{}, ErrBadMagic
	}
	if buf[2] != Version1 {
		return Header{}, fmt.Errorf("%w %d", ErrBadVersion, buf[2])
	}
	h := Header{Type: buf[3], PayloadLen: binary.BigEndian.Uint32(buf[4:8])}
	max, ok := limits[h.Type]
	if !ok {
		return h, fmt.Errorf("%w 0x%02x", ErrUnexpectedType, h.Type)
	}
	if h.PayloadLen > max {
		return h, fmt.Errorf("%w: %d bytes for type 0x%02x, at most %d", ErrFrameTooLarge, h.PayloadLen, h.Type, max)
	}
	return h, nil
}

// ReadFrame reads a whole frame checked by limits. The payload goes into buf when it fits,
// otherwise into a new slice of exactly the payload length, itself bounded by limits.
func ReadFrame(r io.Reader, limits Limits, buf []byte) (Header, []byte, error) {
	h, err := ReadFrameHeader(r, limits)
	if err != nil {
		return h, nil, err
	}
	n := int(h.PayloadLen)
	if cap(buf) < n {
		buf = make([]byte, n)
	}
	buf = buf[:n]
	if _, err := io.ReadFull(r, buf); err != nil {
		return h, nil, err
	}
	return h, buf, nil
}

// EncodeHeader is the wire form of h: the bytes ReadFrameHeader read to obtain it.
func EncodeHeader(h Header) []byte {
	var hdr [FrameHeaderSize]byte
	binary.BigEndian.PutUint16(hdr[0:2], MagicBytes)
	hdr[2] = Version1
	hdr[3] = h.Type
	binary.BigEndian.PutUint32(hdr[4:8], h.PayloadLen)
	return hdr[:]
}

// AppendFrame appends a frame, header then payload, to dst.
func AppendFrame(dst []byte, typ byte, payload []byte) []byte {
	var hdr [FrameHeaderSize]byte
	binary.BigEndian.PutUint16(hdr[0:2], MagicBytes)
	hdr[2] = Version1
	hdr[3] = typ
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	return append(append(dst, hdr[:]...), payload...)
}

// WriteFrame writes a frame in a single Write, so that concurrent writers serialised by the
// caller never interleave a header and a payload.
func WriteFrame(w io.Writer, typ byte, payload []byte) error {
	_, err := w.Write(AppendFrame(make([]byte, 0, FrameHeaderSize+len(payload)), typ, payload))
	return err
}
