package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Medboy224/medXfer/pkg/api"
)

func TestBundleCommand(t *testing.T) {
	api.SetCustomConfigDir(t.TempDir())
	out := filepath.Join(t.TempDir(), "b.zip")
	var stdout bytes.Buffer
	if err := runBundle([]string{"-o", out}, &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Nothing was sent") {
		t.Fatalf("output: %s", stdout.String())
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	found := false
	for _, f := range zr.File {
		found = found || f.Name == "manifest.json"
	}
	if !found {
		t.Fatal("bundle has no manifest.json")
	}

	// Never overwrite an existing file.
	if err := runBundle([]string{"-o", out}, &stdout); err == nil {
		t.Fatal("runBundle overwrote an existing file")
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
	if err := runBundle([]string{"extra"}, &stdout); err == nil {
		t.Fatal("unexpected argument accepted")
	}
}
