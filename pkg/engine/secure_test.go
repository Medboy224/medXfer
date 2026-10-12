package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/securechan"
)

func newID(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// serverSecure and clientSecure pin the data connections of a transfer to the peer's key.
func serverSecure(self *identity.Identity, peer identity.Fingerprint) SecureFunc {
	return func(ctx context.Context, c net.Conn) (net.Conn, error) {
		return securechan.Server(ctx, c, securechan.Config{Identity: self, Expected: peer})
	}
}

func clientSecure(self *identity.Identity, peer identity.Fingerprint) SecureFunc {
	return func(ctx context.Context, c net.Conn) (net.Conn, error) {
		return securechan.Client(ctx, c, securechan.Config{Identity: self, Expected: peer})
	}
}

// startSecureSender serves path; the receiver is expected to present receiverFP.
func startSecureSender(t *testing.T, path string, sender *identity.Identity, receiverFP identity.Fingerprint) (string, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	s := NewSender(2, 512*1024)
	s.SetSecure(serverSecure(sender, receiverFP))
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(done)
		_ = s.ServeAndSendWithRelPathReady(ctx, addr, path, "", nil, 0, ready)
	}()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); <-done })
	return addr, cancel, done
}

func randomFile(t *testing.T, size int) (string, []byte) {
	t.Helper()
	data := make([]byte, size)
	_, _ = rand.Read(data)
	path := filepath.Join(t.TempDir(), "secret.bin")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, data
}

// WP-3.4: a transfer whose data connections are TLS, pinned on both sides.
func TestSecureTransfer(t *testing.T) {
	a, b := newID(t), newID(t)
	path, data := randomFile(t, 3<<20)
	addr, _, _ := startSecureSender(t, path, a, b.Fingerprint)

	out := t.TempDir()
	r := NewReceiver(out, 2)
	r.SetSecure(clientSecure(b, a.Fingerprint))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.Pull(ctx, addr, nil, ""); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(out, "secret.bin"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("received file differs (%v)", err)
	}
}

// A receiver with another key (not the transfer's peer) gets nothing.
func TestSecureTransferRefusesOtherKey(t *testing.T) {
	a, b, intruder := newID(t), newID(t), newID(t)
	path, _ := randomFile(t, 1<<20)
	addr, _, _ := startSecureSender(t, path, a, b.Fingerprint)

	out := t.TempDir()
	r := NewReceiver(out, 2)
	r.SetSecure(clientSecure(intruder, a.Fingerprint))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.Pull(ctx, addr, nil, ""); err == nil {
		t.Fatal("a receiver with another key downloaded the file")
	}
	if _, err := os.Stat(filepath.Join(out, "secret.bin")); err == nil {
		t.Fatal("the file exists on the intruder's side")
	}
}

// The receiver pins the sender's key: a sender with another key is refused.
func TestSecureTransferRefusesWrongSender(t *testing.T) {
	a, b, impostor := newID(t), newID(t), newID(t)
	path, _ := randomFile(t, 1<<20)
	addr, _, _ := startSecureSender(t, path, impostor, b.Fingerprint)

	r := NewReceiver(t.TempDir(), 2)
	r.SetSecure(clientSecure(b, a.Fingerprint))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.Pull(ctx, addr, nil, ""); err == nil {
		t.Fatal("the receiver accepted a sender with another key")
	}
}

// A plaintext receiver is refused by a secure sender.
func TestSecureSenderRefusesPlaintext(t *testing.T) {
	a, b := newID(t), newID(t)
	path, _ := randomFile(t, 1<<20)
	addr, _, _ := startSecureSender(t, path, a, b.Fingerprint)

	r := NewReceiver(t.TempDir(), 2) // no SetSecure
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.Pull(ctx, addr, nil, ""); err == nil {
		t.Fatal("a plaintext receiver downloaded from a secure sender")
	}
}

// 20 secure transfers in a row leave no goroutine behind (leak check of G2a, §4.3).
func TestSecureTransfersLeaveNoGoroutine(t *testing.T) {
	a, b := newID(t), newID(t)
	path, _ := randomFile(t, 256<<10)
	before := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		addr, cancel, done := startSecureSender(t, path, a, b.Fingerprint)
		r := NewReceiver(t.TempDir(), 2)
		r.SetSecure(clientSecure(b, a.Fingerprint))
		ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		if err := r.Pull(ctx, addr, nil, ""); err != nil {
			t.Fatalf("transfer %d: %v", i, err)
		}
		stop()
		cancel()
		<-done
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+2 {
		t.Fatalf("%d goroutines after 20 transfers, %d before", n, before)
	}
}

// #48: a sender that does not answer (a firewall dropping packets) is given up after 8
// attempts, each bounded by the dial timeout, instead of the system's delay for each.
func TestPullGivesUpOnUnreachableSender(t *testing.T) {
	r := NewReceiver(t.TempDir(), 1)
	r.SetDialTimeout(100 * time.Millisecond)
	attempts := 0
	r.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		attempts++
		<-ctx.Done() // a black hole: nothing ever answers
		return nil, ctx.Err()
	}
	start := time.Now()
	err := r.Pull(context.Background(), "192.0.2.1:18888", nil, "")
	if err == nil {
		t.Fatal("Pull reached a sender that does not exist")
	}
	if attempts != 8 {
		t.Fatalf("%d attempts, want 8 (XFER-05)", attempts)
	}
	// 8 × 100 ms of attempts + 3.55 s of waits between them.
	if d := time.Since(start); d > 6*time.Second {
		t.Fatalf("giving up took %v", d)
	}
}
