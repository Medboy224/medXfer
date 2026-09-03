package protocol

import (
	"bytes"
	"encoding/binary"
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

	header, err := ReadHeader(&buf)
	if err != nil {
		t.Fatalf("ReadHeader failed: %v", err)
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

	header, err := ReadHeader(&buf)
	if err != nil {
		t.Fatalf("ReadHeader failed: %v", err)
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
