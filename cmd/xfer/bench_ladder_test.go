package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Medboy224/medXfer/pkg/api"
	"github.com/Medboy224/medXfer/pkg/bench"
	"github.com/Medboy224/medXfer/pkg/identity"
)

// ladderServer starts a ladder server; it returns its address, code and key.
func ladderServer(t *testing.T) (string, string, string) {
	t.Helper()
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
	return ln.Addr().String(), srv.Code, serverID.Fingerprint.String()
}

// xfer bench ladder --peer measures R1 to R6 against a listening side and prints JSON.
func TestBenchLadderPeer(t *testing.T) {
	addr, code, fp := ladderServer(t)
	var out bytes.Buffer
	args := []string{"ladder", "--peer", addr, "--code", code, "--fp", fp,
		"--size", "4M", "--streams", "2", "--chunk", "1", "--dir", t.TempDir(), "--json"}
	if err := runBench(args, &out); err != nil {
		t.Fatal(err)
	}
	var res bench.LadderResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res.Steps) != len(bench.Steps) || res.Streams != 2 {
		t.Fatalf("output %s (%v)", out.String(), err)
	}
}

// xfer bench matrix --peer measures each combination, prints JSON and saves it in --results.
func TestBenchMatrixPeer(t *testing.T) {
	addr, code, fp := ladderServer(t)
	results := t.TempDir()
	var out bytes.Buffer
	args := []string{"matrix", "--peer", addr, "--code", code, "--fp", fp, "--size", "4M",
		"--streams-list", "1,2", "--chunks", "1", "--dir", t.TempDir(), "--results", results, "--json"}
	if err := runBench(args, &out); err != nil {
		t.Fatal(err)
	}
	var res bench.MatrixResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res.Cells) != 2 || res.Step != "R6" {
		t.Fatalf("output %s (%v)", out.String(), err)
	}
	saved, _ := filepath.Glob(filepath.Join(results, "matrix-*.json"))
	if len(saved) != 1 {
		t.Fatalf("saved results: %v", saved)
	}
	var again bench.MatrixResult
	if data, err := os.ReadFile(saved[0]); err != nil || json.Unmarshal(data, &again) != nil || len(again.Cells) != 2 {
		t.Fatalf("saved file does not hold the matrix: %v", err)
	}
}

func TestBenchLadderArguments(t *testing.T) {
	for _, args := range [][]string{
		{"ladder"},
		{"ladder", "--peer", "127.0.0.1:1"}, // no code, no key
		{"ladder", "--peer", "127.0.0.1:1", "--code", "c", "--fp", "x", "--chunk", "9"}, // chunk too big
		{"ladder", "--peer", "127.0.0.1:1", "--code", "c", "--fp", "x", "--sink", "tape"},
		{"ladder", "--peer", "127.0.0.1:1", "--code", "c", "--fp", "not-a-key"},
		{"matrix"},
		{"matrix", "--peer", "127.0.0.1:1"}, // no code, no key
		{"matrix", "--peer", "127.0.0.1:1", "--code", "c", "--fp", "x", "--streams-list", "1,two"},
		{"matrix", "--peer", "127.0.0.1:1", "--code", "c", "--fp", "x", "--chunks", "0"},
	} {
		if err := runBench(args, &bytes.Buffer{}); err == nil {
			t.Errorf("%s: accepted", strings.Join(args, " "))
		}
	}
}
