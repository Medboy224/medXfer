package identity

import (
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// writeFileAtomic replaces path with data so that a crash leaves either the old or the new
// content, never a mix (ID-04): temporary file in the same directory, fsync, rename, then
// fsync of the directory where the system supports it.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if err := tmp.Chmod(perm); err != nil && runtime.GOOS != "windows" {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// On Windows an antivirus or an indexer may hold the file a moment: try again, as
	// pkg/store does (bible §9.6, #156).
	for _, wait := range renameTries {
		time.Sleep(wait)
		if err = renameFile(name, path); err == nil {
			break
		}
	}
	if err != nil {
		return err
	}
	ok = true
	syncDir(dir)
	return nil
}

// renameFile renames; tests replace it.
var renameFile = os.Rename

// renameTries are the waits before each attempt to replace the file.
var renameTries = []time.Duration{0, 100 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond}

// syncDir makes a rename durable on Unix. Windows cannot open a directory for fsync; NTFS
// journals the rename itself.
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}
