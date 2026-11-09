package diag

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

// The application's log: its last MaxAppLog bytes, from a whole line; none, no app.log.
func TestBundleAppLog(t *testing.T) {
	read := func(log string) (string, bool) {
		t.Helper()
		var buf bytes.Buffer
		if err := WriteBundle(&buf, BundleOptions{AppLog: log}); err != nil {
			t.Fatal(err)
		}
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range zr.File {
			if f.Name != "app.log" {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(rc)
			rc.Close()
			return string(data), true
		}
		return "", false
	}
	if _, ok := read(""); ok {
		t.Fatal("an empty log gave app.log")
	}
	line := "ble: seen 11:11, -60 dBm\n"
	long := "first line, cut off\n" + strings.Repeat(line, MaxAppLog/len(line)+10) + "hotspot: failed refused (aborted)\n"
	got, ok := read(long)
	if !ok {
		t.Fatal("no app.log")
	}
	if len(got) > MaxAppLog || !strings.HasPrefix(got, "ble: seen") || strings.Contains(got, "first line") {
		t.Fatalf("app.log: %d bytes, starting %q", len(got), got[:40])
	}
	if !strings.HasSuffix(got, "hotspot: failed refused (aborted)\n") {
		t.Fatal("app.log lost its last line")
	}
}
