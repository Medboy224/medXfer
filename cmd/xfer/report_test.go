package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
)

func TestReportCommand(t *testing.T) {
	dir := t.TempDir()
	r := &diag.Report{SchemaVersion: diag.SchemaVersion, GeneratedAt: time.Now(), AvgSpeedMBps: 40, TotalFiles: 1}
	a, err := diag.SaveReport(dir, r, 0)
	if err != nil {
		t.Fatal(err)
	}
	r2 := *r
	r2.GeneratedAt = r.GeneratedAt.Add(time.Second)
	r2.AvgSpeedMBps = 80
	b, err := diag.SaveReport(dir, &r2, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Keep the test output readable: the commands print to stdout.
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	stdout := os.Stdout
	os.Stdout = devnull
	defer func() { os.Stdout = stdout; devnull.Close() }()

	for _, args := range [][]string{
		{"show", a},
		{"show", "--json", a},
		{"compare", a, b},
		{"compare", a, b, "--json"},
	} {
		if err := runReport(args); err != nil {
			t.Errorf("xfer report %v: %v", args, err)
		}
	}
	for _, args := range [][]string{
		{},
		{"show", filepath.Join(dir, "missing.json")},
		{"compare", a},
		{"frobnicate"},
	} {
		if err := runReport(args); err == nil {
			t.Errorf("xfer report %v succeeded; want an error", args)
		}
	}
}
