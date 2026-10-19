package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

// Audit §3.4 (#156): xfer send says it waits for the receiver only once the QR code and the
// PIN are both on screen: startOneShotPairing returns after them.
func TestSendShowsQRAndPINBeforeWaiting(t *testing.T) {
	sender := newTestIdentity(t)
	old := localID
	localID = sender
	t.Cleanup(func() { localID = old })
	hub, addr := startTestHub(t, sender)
	base, _ := transfer.NewID()
	send := newOneShotSend(hub, base)
	msg := session.Message{DataPort: portNum(t, addr), TransferID: base.String()}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	out := make(chan string, 1)
	go func() { b, _ := io.ReadAll(r); out <- string(b) }()
	ctx, cancel := context.WithCancel(context.Background())
	startOneShotPairing(ctx, send, portNum(t, addr), msg, nil, nil, false, false)
	_, _ = w.WriteString("<WAITING>")
	os.Stdout = stdout
	cancel()
	w.Close()
	got := <-out
	qr, wait := strings.Index(got, "xfer recv '"), strings.Index(got, "<WAITING>")
	if qr < 0 || qr > wait {
		t.Fatalf("the QR code came after the wait began:\n%s", got)
	}
	if pin := strings.Index(got, "PIN"); pin < 0 || pin > wait {
		t.Fatalf("the PIN came after the wait began:\n%s", got)
	}
}
