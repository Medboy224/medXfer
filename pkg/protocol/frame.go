package protocol

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
)

// ReadHeader reads the 8-byte frame from the wire
func ReadHeader(r io.Reader) (Header, error) {
	var buf [FrameHeaderSize]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return Header{}, err
	}
	if binary.BigEndian.Uint16(buf[0:2]) != MagicBytes {
		return Header{}, fmt.Errorf("invalid magic bytes")
	}
	return Header{
		Type:       buf[3],
		PayloadLen: binary.BigEndian.Uint32(buf[4:8]),
	}, nil
}

// ReadHandshakePayload parses the JSON metadata sent by the sender
func ReadHandshakePayload(r io.Reader, payloadLen uint32) (FileMetadata, error) {
	buf := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return FileMetadata{}, err
	}
	var meta FileMetadata
	if err := json.Unmarshal(buf, &meta); err != nil {
		return FileMetadata{}, err
	}
	return meta, nil
}

// WriteHandshake sends the JSON metadata to the receiver
func WriteHandshake(w io.Writer, meta FileMetadata) error {
	payload, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	var buf [FrameHeaderSize]byte
	binary.BigEndian.PutUint16(buf[0:2], MagicBytes)
	buf[2] = Version1
	buf[3] = TypeHandshake
	binary.BigEndian.PutUint32(buf[4:8], uint32(len(payload)))

	if _, err := w.Write(buf[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

// ParseChunkPayload separates the 20-byte chunk metadata from the raw file bytes
func ParseChunkPayload(payload []byte) (ChunkMeta, []byte, error) {
	if len(payload) < ChunkHeaderSize {
		return ChunkMeta{}, nil, fmt.Errorf("payload too small for chunk header")
	}
	meta := ChunkMeta{
		Index:  binary.BigEndian.Uint32(payload[0:4]),
		Offset: binary.BigEndian.Uint64(payload[4:12]),
		Length: binary.BigEndian.Uint32(payload[12:16]),
	}
	expectedCRC := binary.BigEndian.Uint32(payload[16:20])
	data := payload[ChunkHeaderSize:]

	if CalculateChecksum(data) != expectedCRC {
		return ChunkMeta{}, nil, fmt.Errorf("CRC checksum mismatch: data may be corrupted")
	}
	return meta, data, nil
}

// CalculateChecksum generates a rapid CRC32 hash for integrity checks
func CalculateChecksum(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}
