package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// #313: config.json is replaced in one go, never truncated then written: a reader never sees it
// empty or cut, as a power loss in the middle of a write would have left it, and the next start
// would then fall back to the defaults without a word.
func TestSaveConfigIsAtomic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not replace a file a reader holds open: the reader would stall the writer")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Config{DeviceName: "Laptop", DownloadDir: strings.Repeat("d", 256<<10)}
	if err := SaveConfigTo(path, cfg); err != nil {
		t.Fatal(err)
	}
	stop, seen := make(chan struct{}), make(chan int, 1)
	go func() {
		for {
			select {
			case <-stop:
				close(seen)
				return
			default:
			}
			data, err := os.ReadFile(path)
			var c Config
			if err != nil || json.Unmarshal(data, &c) != nil || c.DownloadDir != cfg.DownloadDir {
				seen <- len(data)
				return
			}
		}
	}()
	for i := 0; i < 300; i++ {
		if err := SaveConfigTo(path, cfg); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	if n, cut := <-seen; cut {
		t.Fatalf("a reader saw config.json cut: %d bytes of %d", n, len(cfg.DownloadDir))
	}
}
