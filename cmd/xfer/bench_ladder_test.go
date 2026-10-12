package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/Medboy224/medXfer/pkg/api"
	"github.com/Medboy224/medXfer/pkg/bench"
	"github.com/Medboy224/medXfer/pkg/identity"
)

// xfer bench ladder --peer measures R1 to R4 against a listening side and prints JSON.
func TestBenchLadderPeer(t *testing.T) {
	api.SetCustomConfigDir(t.TempDir())
	t.Cleanup(func() { api.SetCustomConfigDir("") })
	serverID, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := bench.NewLadderServer(serverID)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Serve(ctx, ln, false) }()

	var out bytes.Buffer
	args := []string{"ladder", "--peer", ln.Addr().String(), "--code", srv.Code, "--fp", serverID.Fingerprint.String(),
		"--size", "4M", "--streams", "2", "--chunk", "1", "--dir", t.TempDir(), "--json"}
	if err := runBench(args, &out); err != nil {
		t.Fatal(err)
	}
	var res bench.LadderResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res.Steps) != 4 || res.Streams != 2 {
		t.Fatalf("output %s (%v)", out.String(), err)
	}
}

func TestBenchLadderArguments(t *testing.T) {
	for _, args := range [][]string{
		{"ladder"},
		{"ladder", "--peer", "127.0.0.1:1"}, // no code, no key
		{"ladder", "--peer", "127.0.0.1:1", "--code", "c", "--fp", "x", "--chunk", "9"}, // chunk too big
		{"ladder", "--peer", "127.0.0.1:1", "--code", "c", "--fp", "x", "--sink", "tape"},
		{"ladder", "--peer", "127.0.0.1:1", "--code", "c", "--fp", "not-a-key"},
	} {
		if err := runBench(args, &bytes.Buffer{}); err == nil {
			t.Errorf("%s: accepted", strings.Join(args, " "))
		}
	}
}
