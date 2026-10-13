//go:build diag

package api

// The receive folder's disk (STO-07; D9 and D10 of the J4b plan): an offer that does not fit
// is refused before its first byte, a disk that fills up pauses the transfer, and no disk
// probe writes in the receive folder (#36).

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag/faultfs"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/store"
	"github.com/Medboy224/medXfer/pkg/testkit"
)

// sourceFile writes a file of size random bytes to send.
func sourceFile(t *testing.T, size int) (string, []byte) {
	t.Helper()
	data := make([]byte, size)
	_, _ = rand.Read(data)
	path := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, data
}

// D9: the offer tells how much room it needs; accepting it without that room is refused, and
// nothing is written. The offer stays pending: once there is room, it is accepted.
func TestOfferTooBigIsRefusedBeforeAnyByte(t *testing.T) {
	restore := engine.SetFreeSpace(func(string) (int64, error) { return 32 << 20, nil })
	t.Cleanup(restore)
	src, data := sourceFile(t, 1<<20)
	recvDir := t.TempDir()
	pair := testkit.StartPair(t, NewDaemonServer(0, t.TempDir(), "SendNode"), NewDaemonServer(0, recvDir, "RecvNode"))
	pair.Send([]string{src}, testkit.SendOptions{})

	var in IncomingOfferData
	if err := pair.Receiver.WaitEvent("incoming_offer", 0).Decode(&in); err != nil {
		t.Fatal(err)
	}
	if want := int64(1<<20 + 64<<20); in.EnoughSpace || in.FreeBytes != 32<<20 || in.NeededBytes != want {
		t.Fatalf("incoming_offer: enough %v, free %d, needed %d; want false, %d, %d",
			in.EnoughSpace, in.FreeBytes, in.NeededBytes, 32<<20, want)
	}
	id := pair.Receiver.Request("respond_offer", map[string]any{"accept": true})
	reply := pair.Receiver.WaitReply(id, 0)
	var refusal map[string]string
	_ = reply.Decode(&refusal)
	if reply.Event != "action_error" || refusal["code"] != "NO_SPACE" {
		t.Fatalf("accepting an offer too big: %s %s; want action_error NO_SPACE", reply.Event, reply.Data)
	}
	if entries, _ := os.ReadDir(recvDir); len(entries) != 0 {
		t.Fatalf("something was written for a refused offer: %v", entries)
	}

	restore() // room again
	pair.Receiver.Request("respond_offer", map[string]any{"accept": true})
	pair.WaitTransferDone(0)
	if got, _ := os.ReadFile(filepath.Join(recvDir, "f.bin")); !bytes.Equal(got, data) {
		t.Fatal("the offer accepted with room differs")
	}
}

// STO-07: the disk fills up after 1 MiB; the transfer pauses (disk_full) with its partial
// file, and ends once there is room and the user resumes.
func TestDiskFullPausesTheTransfer(t *testing.T) {
	var full atomic.Bool
	full.Store(true)
	t.Cleanup(engine.SetStorageWrapper(func(f engine.StorageFile, path string) engine.StorageFile {
		if !full.Load() || strings.HasSuffix(path, store.JournalSuffix) {
			return f
		}
		return faultfs.Wrap(f, faultfs.Plan{NoSpaceAfter: 1 << 20})
	}))
	src, data := sourceFile(t, 4<<20)
	recvDir := t.TempDir()
	pair := testkit.StartPair(t, NewDaemonServer(0, t.TempDir(), "SendNode"), NewDaemonServer(0, recvDir, "RecvNode"))
	pair.Send([]string{src}, testkit.SendOptions{})
	pair.AcceptOffer(0)

	e := pair.Receiver.WaitEventAny(30*time.Second, "disk_full", "item_failed", "transfer_complete")
	var ev map[string]string
	_ = e.Decode(&ev)
	if e.Event != "disk_full" || ev["code"] != "DISK_FULL" {
		t.Fatalf("on a full disk: %s %s; want disk_full", e.Event, e.Data)
	}
	if _, err := os.Stat(filepath.Join(recvDir, "f.bin"+store.PartSuffix)); err != nil {
		t.Fatalf("the partial file was not kept: %v", err)
	}
	pair.Receiver.ExpectNoEvent("item_failed", 300*time.Millisecond)

	full.Store(false) // room again
	pair.Receiver.Request("resume", nil)
	pair.WaitTransferDone(30 * time.Second)
	if got, _ := os.ReadFile(filepath.Join(recvDir, "f.bin")); !bytes.Equal(got, data) {
		t.Fatal("the file received across a full disk differs")
	}
}

// #36, D10: while a transfer runs, the receive folder holds the transfer's files only. The
// daemon used to write an 8 MiB disk probe there when an offer was accepted.
func TestNoProbeInTheReceiveFolder(t *testing.T) {
	testkit.Throttle(t, 2<<20) // about a second: the folder is watched all along
	src, _ := sourceFile(t, 2<<20)
	recvDir := t.TempDir()
	pair := testkit.StartPair(t, NewDaemonServer(0, t.TempDir(), "SendNode"), NewDaemonServer(0, recvDir, "RecvNode"))
	pair.Send([]string{src}, testkit.SendOptions{})

	stop := make(chan struct{})
	strange := make(chan string, 1)
	go func() {
		allowed := map[string]bool{"f.bin": true, "f.bin" + store.PartSuffix: true, "f.bin" + store.JournalSuffix: true}
		for {
			select {
			case <-stop:
				return
			default:
			}
			entries, _ := os.ReadDir(recvDir)
			for _, e := range entries {
				if !allowed[e.Name()] {
					select {
					case strange <- e.Name():
					default:
					}
					return
				}
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	pair.AcceptOffer(0)
	pair.WaitTransferDone(30 * time.Second)
	close(stop)
	select {
	case name := <-strange:
		t.Fatalf("%q was written in the receive folder during the transfer", name)
	default:
	}
}
