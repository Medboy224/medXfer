package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/manifest"
)

func TestTarStreamEndToEnd(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// 1. Create 50 nested test files
	var paths []string
	expectedContents := make(map[string][]byte)

	for i := 0; i < 50; i++ {
		subDir := fmt.Sprintf("folder_%d", i%5)
		_ = os.MkdirAll(filepath.Join(srcDir, subDir), 0755)
		fileName := filepath.Join(subDir, fmt.Sprintf("file_%d.txt", i))
		fullPath := filepath.Join(srcDir, fileName)

		content := bytes.Repeat([]byte(fmt.Sprintf("data-content-%d\n", i)), 100*(i+1))
		if err := os.WriteFile(fullPath, content, 0644); err != nil {
			t.Fatalf("Failed writing file: %v", err)
		}
		paths = append(paths, fullPath)
		expectedContents[filepath.ToSlash(fileName)] = content
	}

	m, err := manifest.Build([]string{srcDir})
	if err != nil {
		t.Fatalf("Failed to build manifest: %v", err)
	}

	// 2. Set up in-memory pipe
	pr, pw := io.Pipe()

	errChan := make(chan error, 2)
	go func() {
		err := StreamTar(context.Background(), pw, m, nil)
		_ = pw.CloseWithError(err)
		errChan <- err
	}()

	go func() {
		err := ExtractTar(context.Background(), pr, dstDir, m.TotalBytes, m.TotalFiles, nil)
		_ = pr.CloseWithError(err)
		errChan <- err
	}()

	// Wait for both sides to complete
	for i := 0; i < 2; i++ {
		if err := <-errChan; err != nil {
			t.Fatalf("Tar streaming failed: %v", err)
		}
	}

	// 3. Verify all 50 files extracted bit-for-bit
	for _, item := range m.Items {
		extractedPath := filepath.Join(dstDir, filepath.FromSlash(item.RelPath))
		actual, err := os.ReadFile(extractedPath)
		if err != nil {
			t.Fatalf("Missing extracted file '%s': %v", item.RelPath, err)
		}
		expected, err := os.ReadFile(item.FullPath)
		if err != nil {
			t.Fatalf("Failed reading source file '%s': %v", item.FullPath, err)
		}
		if !bytes.Equal(actual, expected) {
			t.Fatalf("Content mismatch for file '%s': expected %d bytes, got %d", item.RelPath, len(expected), len(actual))
		}
	}
}

func TestPathTraversalProtection(t *testing.T) {
	dstDir := t.TempDir()

	// Malicious tar stream containing "../evil.txt"
	var buf bytes.Buffer
	m := &manifest.Manifest{
		RootName:   "evil",
		TotalFiles: 1,
		TotalBytes: 10,
		Items: []manifest.Item{
			{
				RelPath:  "../evil.txt",
				FullPath: filepath.Join(t.TempDir(), "evil.txt"),
				Size:     10,
			},
		},
	}
	_ = os.WriteFile(m.Items[0].FullPath, []byte("evil data!"), 0644)

	_ = StreamTar(context.Background(), &buf, m, nil)

	// Extraction must reject the traversal attempt
	err := ExtractTar(context.Background(), &buf, dstDir, 10, 1, nil)
	if err == nil {
		t.Fatal("Expected security error for path traversal, but extraction succeeded")
	}
	t.Logf("Correctly rejected malicious path with error: %v", err)
}

func TestTarStreamOverNetwork(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create test files
	var paths []string
	for i := 0; i < 20; i++ {
		p := filepath.Join(srcDir, fmt.Sprintf("doc_%d.dat", i))
		_ = os.WriteFile(p, bytes.Repeat([]byte{byte(i)}, 10240), 0644)
		paths = append(paths, p)
	}

	m, err := manifest.Build(paths)
	if err != nil {
		t.Fatalf("Manifest build failed: %v", err)
	}

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	doneChan := make(chan error, 2)

	// Sender
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			doneChan <- err
			return
		}
		defer conn.Close()
		doneChan <- StreamTar(context.Background(), conn, m, nil)
	}()

	// Receiver
	go func() {
		conn, err := net.Dial("tcp4", ln.Addr().String())
		if err != nil {
			doneChan <- err
			return
		}
		defer conn.Close()
		doneChan <- ExtractTar(context.Background(), conn, dstDir, m.TotalBytes, m.TotalFiles, nil)
	}()

	for i := 0; i < 2; i++ {
		select {
		case err := <-doneChan:
			if err != nil {
				t.Fatalf("Network tar stream failed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for tar stream over network")
		}
	}

	// Verify count
	entries, err := os.ReadDir(dstDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 20 {
		t.Fatalf("Expected 20 extracted files, got %d", len(entries))
	}
}
