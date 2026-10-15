package testkit

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The exit criterion of J6a (WP-6.3): tests drive medXfer in-process. Only the tests of the
// dashboard's own transport may open a control WebSocket.
var wsAllowed = map[string]bool{
	"pkg/api/control_guard_devui_test.go": true, // the seven controls of the loopback surface (API-04)
	"pkg/wsclient/wsclient_test.go":       true, // the WebSocket client itself
}

func TestNoTestOpensAControlWebSocket(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root not found at %s", root)
	}
	ws := regexp.MustCompile(`wsclient\.Dial\(|"ws://|websocket\.DefaultDialer|websocket\.Dialer\{`)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if loc := ws.FindIndex(data); loc != nil && !wsAllowed[rel] && rel != "pkg/testkit/nows_test.go" {
			t.Errorf("%s opens a control WebSocket (%q): drive the daemon in-process (testkit)", rel, data[loc[0]:loc[1]])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
