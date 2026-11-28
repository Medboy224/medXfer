package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/api"
	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

// xfer send, recv and node save the report of their transfer, as the daemon does (#304,
// DEV-05): `xfer report` only saw the application's transfers.
func TestCLITransfersSaveTheirReports(t *testing.T) {
	api.SetCustomConfigDir(t.TempDir())
	t.Cleanup(func() { api.SetCustomConfigDir("") })
	sender, _ := testDevice(t)
	receiver, _ := testDevice(t)
	hub, addr := startTestHub(t, sender)
	base, _ := transfer.NewID()
	send := newOneShotSend(hub, base)
	if err := send.register(pairing.Result{Fingerprint: receiver.Fingerprint}); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "a.bin")
	if err := os.WriteFile(src, bytes.Repeat([]byte("medXfer "), 300_000), 0o644); err != nil {
		t.Fatal(err)
	}
	offer, files := fileOffer(t, src, base)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sent := make(chan error, 1)
	go func() {
		sctx, scancel := context.WithCancel(ctx)
		defer scancel()
		complete, err := serveOffer(sctx, scancel, offer, files, send.listener(sctx))
		if err == nil && !complete {
			err = errors.New("the receiver did not take every file")
		}
		sent <- err
	}()
	cfg := securechan.Config{Identity: receiver, Expected: sender.Fingerprint}
	if err := receiveOffer(ctx, addr, t.TempDir(), 2, offer, cfg, base); err != nil {
		t.Fatal(err)
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}

	paths, err := diag.ListReports(api.ReportsDir())
	if err != nil || len(paths) != 2 {
		t.Fatalf("%d reports (%v); want the sender's and the receiver's", len(paths), err)
	}
	for _, p := range paths {
		r, err := diag.LoadReport(p)
		if err != nil {
			t.Fatal(err)
		}
		if r.Outcome != diag.OutcomeCompleted || r.TotalBytes != offer.TotalBytes || len(r.Stages) == 0 {
			t.Errorf("%s: outcome %q, %d bytes, %d stages; want completed, %d bytes, its stages",
				filepath.Base(p), r.Outcome, r.TotalBytes, len(r.Stages), offer.TotalBytes)
		}
	}
}
