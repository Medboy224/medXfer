package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/Medboy224/medXfer/pkg/engine"
)

func TestBenchDisk(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := runBench([]string{"disk", "--dir", dir, "--size", "4M", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var res engine.DiskBenchResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("--json output is not a DiskBenchResult: %v\n%s", err, out.String())
	}
	if res.SizeBytes != 4<<20 || res.WriteSpeedMBps <= 0 || res.Rating == "" {
		t.Fatalf("implausible result: %+v", res)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("the benchmark left files behind: %v", entries)
	}

	out.Reset()
	if err := runBench([]string{"disk", "--dir", dir, "--size", "4M"}, &out); err != nil || !strings.Contains(out.String(), "write:") {
		t.Fatalf("text output: %v\n%s", err, out.String())
	}
	for _, args := range [][]string{
		{"disk", "--dir", dir, "--size", "1M"},   // below 4M
		{"disk", "--dir", dir, "--size", "1G"},   // above 512M
		{"disk", "--dir", dir, "--size", "lots"}, // not a size
		{"net"},                                  // neither --listen nor --peer
		{"net", "--peer", "127.0.0.1:1"},         // no code
		{"cpu"},
		{},
	} {
		if err := runBench(args, &out); err == nil {
			t.Errorf("xfer bench %v succeeded; want an error", args)
		}
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{"512": 512, "4K": 4 << 10, "32M": 32 << 20, "1g": 1 << 30, "8MiB": 8 << 20, "16MB": 16 << 20}
	for in, want := range cases {
		if got, err := parseSize(in); err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0", "-1M", "M", "1T", "abc"} {
		if _, err := parseSize(bad); err == nil {
			t.Errorf("parseSize(%q) accepted", bad)
		}
	}
}

// The addresses shown to the other device are reachable ones: IPv4, no loopback, no 169.254.
func TestLANAddresses(t *testing.T) {
	for _, a := range lanAddresses() {
		ip := net.ParseIP(a.ip)
		if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || a.iface == "" {
			t.Errorf("unsuitable address %+v", a)
		}
	}
}
