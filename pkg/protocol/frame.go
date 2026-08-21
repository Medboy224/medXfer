package protocol

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
)

// WriteRawFrame serializes a basic typed frame with a generic payload
func WriteRawFrame(w io.Writer, frameType byte, payload []byte) error {
	header := make([]byte, FrameHeaderSize)
	binary.BigEndian.PutUint16(header[0:2], MagicBytes)
	header[2] = Version1
	header[3] = frameType
	binary.BigEndian.PutUint32(header[4:8], uint32(len(payload)))

	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("failed to write frame header: %w", err)
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("failed to write frame payload: %w", err)
		}
	}
	return nil
}

// ReadFrameHeader reads and validates the initial 8-byte frame header
func ReadFrameHeader(r io.Reader) (frameType byte, length uint32, err error) {
	header := make([]byte, FrameHeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, 0, err
	}

	magic := binary.BigEndian.Uint16(header[0:2])
	if magic != MagicBytes {
		return 0, 0, ErrInvalidMagic
	}

	version := header[2]
	if version != Version1 {
		return 0, 0, ErrUnsupportedVersion
	}

	frameType = header[3]
	length = binary.BigEndian.Uint32(header[4:8])
	return frameType, length, nil
}

// SendMeta encodes metadata as JSON and streams it across the control socket
func SendMeta(w io.Writer, meta *Metadata) error {
	payload, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	return WriteRawFrame(w, TypeMeta, payload)
}

// RecvMeta reads and parses metadata JSON from a control connection
func RecvMeta(r io.Reader) (*Metadata, error) {
	frameType, length, err := ReadFrameHeader(r)
	if err != nil {
		return nil, err
	}
	if frameType != TypeMeta {
		return nil, fmt.Errorf("expected TypeMeta (0x02), received 0x%02x", frameType)
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("failed to read metadata payload: %w", err)
	}

	var meta Metadata
	if err := json.Unmarshal(payload, &meta); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata JSON: %w", err)
	}
	return &meta, nil
}

// WriteChunk streams a chunk header and binary payload over a worker socket
func WriteChunk(w io.Writer, index uint32, offset uint64, data []byte) error {
	checksum := crc32.ChecksumIEEE(data)
	payloadLen := uint32(ChunkHeaderSize + len(data))

	// 1. Write Frame Envelope
	frameHeader := make([]byte, FrameHeaderSize)
	binary.BigEndian.PutUint16(frameHeader[0:2], MagicBytes)
	frameHeader[2] = Version1
	frameHeader[3] = TypeChunk
	binary.BigEndian.PutUint32(frameHeader[4:8], payloadLen)

	if _, err := w.Write(frameHeader); err != nil {
		return err
	}

	// 2. Write Chunk Metadata Header
	chunkHeader := make([]byte, ChunkHeaderSize)
	binary.BigEndian.PutUint32(chunkHeader[0:4], index)
	binary.BigEndian.PutUint64(chunkHeader[4:12], offset)
	binary.BigEndian.PutUint32(chunkHeader[12:16], uint32(len(data)))
	binary.BigEndian.PutUint32(chunkHeader[16:20], checksum)

	if _, err := w.Write(chunkHeader); err != nil {
		return err
	}

	// 3. Stream Raw Chunk Bytes
	_, err := w.Write(data)
	return err
}

// ReadChunk parses a chunk frame from the stream and validates the checksum
func ReadChunk(r io.Reader, buf []byte) (ChunkHeader, error) {
	var ch ChunkHeader

	frameType, payloadLen, err := ReadFrameHeader(r)
	if err != nil {
		return ch, err
	}
	if frameType != TypeChunk {
		return ch, fmt.Errorf("expected TypeChunk (0x03), got 0x%02x", frameType)
	}

	// Read 20-byte chunk header
	headerBuf := make([]byte, ChunkHeaderSize)
	if _, err := io.ReadFull(r, headerBuf); err != nil {
		return ch, fmt.Errorf("failed to read chunk header: %w", err)
	}

	ch.Index = binary.BigEndian.Uint32(headerBuf[0:4])
	ch.Offset = binary.BigEndian.Uint64(headerBuf[4:12])
	ch.DataLen = binary.BigEndian.Uint32(headerBuf[12:16])
	ch.Checksum = binary.BigEndian.Uint32(headerBuf[16:20])

	if ch.DataLen > uint32(len(buf)) {
		return ch, ErrPayloadTooLarge
	}
	if payloadLen != ChunkHeaderSize+ch.DataLen {
		return ch, fmt.Errorf("frame payload length mismatch")
	}

	// Read actual data payload into provided buffer
	targetSlice := buf[:ch.DataLen]
	if _, err := io.ReadFull(r, targetSlice); err != nil {
		return ch, fmt.Errorf("failed to read chunk data: %w", err)
	}

	// Validate integrity
	if crc32.ChecksumIEEE(targetSlice) != ch.Checksum {
		return ch, ErrChecksumMismatch
	}

	return ch, nil
}
