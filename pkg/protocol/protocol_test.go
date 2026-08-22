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
		FileSize:    5899345920,
		ChunkSize:   DefaultChunkSize,
		TotalChunks: 1407,
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
		decoded.FileSize != original.FileSize {
		t.Fatalf("Metadata mismatch: expected %+v, got %+v", original, decoded)
	}
}

func TestChunkFramingAndChecksum(t *testing.T) {
	payload := make([]byte, 1024*1024)
	_, _ = rand.Read(payload)

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
		t.Fatal("Payload content corrupted during transfer")
	}
}
