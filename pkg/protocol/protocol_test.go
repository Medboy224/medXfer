package protocol

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestMetaSerialization(t *testing.T) {
	original := &Metadata{
		SessionID:   "sess_test_1234",
		FileName:    "ubuntu-24.04-desktop.iso",
		FileSize:    5899345920, // ~5.5 GB
		ChunkSize:   DefaultChunkSize,
		TotalChunks: 1407,
		FileSHA256:  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}

	buf := new(bytes.Buffer)
	if err := SendMeta(buf, original); err != nil {
		t.Fatalf("SendMeta failed: %v", err)
	}

	decoded, err := RecvMeta(buf)
	if err != nil {
		t.Fatalf("RecvMeta failed: %v", err)
	}

	if decoded.SessionID != original.SessionID ||
		decoded.FileName != original.FileName ||
		decoded.FileSize != original.FileSize ||
		decoded.TotalChunks != original.TotalChunks {
		t.Fatalf("Metadata mismatch: expected %+v, got %+v", original, decoded)
	}
}

func TestChunkFramingAndChecksum(t *testing.T) {
	payload := make([]byte, 1024*1024) // 1 MB test payload
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	index := uint32(42)
	offset := uint64(42 * 1024 * 1024)

	buf := new(bytes.Buffer)
	if err := WriteChunk(buf, index, offset, payload); err != nil {
		t.Fatalf("WriteChunk failed: %v", err)
	}

	recvBuf := make([]byte, len(payload))
	header, err := ReadChunk(buf, recvBuf)
	if err != nil {
		t.Fatalf("ReadChunk failed: %v", err)
	}

	if header.Index != index || header.Offset != offset || header.DataLen != uint32(len(payload)) {
		t.Fatalf("ChunkHeader mismatch: %+v", header)
	}

	if !bytes.Equal(payload, recvBuf) {
		t.Fatal("Payload byte content corrupted during transfer")
	}
}

func TestCorruptedChunkDetection(t *testing.T) {
	payload := []byte("critical system file data")
	buf := new(bytes.Buffer)

	if err := WriteChunk(buf, 1, 0, payload); err != nil {
		t.Fatal(err)
	}

	// Tamper with a single byte in the buffer (corrupting data)
	rawBytes := buf.Bytes()
	rawBytes[len(rawBytes)-1] ^= 0xFF // Flip last bit

	tamperedBuf := bytes.NewBuffer(rawBytes)
	recvBuf := make([]byte, len(payload))

	_, err := ReadChunk(tamperedBuf, recvBuf)
	if err != ErrChecksumMismatch {
		t.Fatalf("Expected ErrChecksumMismatch, got: %v", err)
	}
}
