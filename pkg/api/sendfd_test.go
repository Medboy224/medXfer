package api

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/testkit"
)

// WP-B.0 (STO-16): send takes files open already, by descriptor, as an Android system picker
// gives them; they arrive whole, under the names given.
func TestSendByDescriptor(t *testing.T) {
	src := t.TempDir()
	want := map[string][]byte{"photo.jpg": bytes.Repeat([]byte("jpg"), 1000), "note.txt": []byte("hello")}
	var items []map[string]any
	for _, name := range []string{"photo.jpg", "note.txt"} {
		p := filepath.Join(src, "picked-"+name)
		_ = os.WriteFile(p, want[name], 0o644)
		items = append(items, map[string]any{"fd": openFD(t, p), "name": name})
	}
	recvDir := t.TempDir()
	pair := testkit.StartPair(t, NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Sender"),
		NewDaemonServerAt(t.TempDir(), 0, recvDir, "Receiver"))
	pair.Sender.Request("send", map[string]any{"items": items, "target_ip": pair.Receiver.PeerAddr()})
	pair.AcceptOffer(0)
	pair.WaitTransferDone(30 * time.Second)
	// The sender's side ends too: its screen leaves "sending" (seen on the phone, WP-B.4).
	pair.Sender.WaitEvent("transfer_complete", 10*time.Second)

	entries, _ := os.ReadDir(recvDir)
	if len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("receive folder holds %v, want the transfer's folder", entries)
	}
	for name, data := range want {
		got, err := os.ReadFile(filepath.Join(recvDir, entries[0].Name(), name))
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%s: %d bytes (%v)", name, len(got), err)
		}
	}
}

// Paths and descriptors together are refused, and the descriptors closed.
func TestSendRefusesPathsAndItems(t *testing.T) {
	_, err := sourceOf(SendPayload{Paths: []string{"x"}, Items: []SendItem{{FD: openFD(t, os.Args[0]), Name: "a"}}})
	if err == nil {
		t.Fatal("paths and items together were accepted")
	}
}
