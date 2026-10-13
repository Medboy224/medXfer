//go:build diag

// Package faultfs wraps the receiver's storage files with deterministic faults (DEV-10):
// disk full after N bytes, torn write, fsync that lies. It exists only in builds with the
// "diag" tag; CI checks that it never reaches a production binary.
//
// Install a plan for every file opened by the engine with engine.SetStorageWrapper:
//
//	restore := engine.SetStorageWrapper(func(f engine.StorageFile, path string) engine.StorageFile {
//		return faultfs.Wrap(f, plan)
//	})
//	defer restore()
package faultfs

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
)

// ErrNoSpace wraps syscall.ENOSPC, as a full disk would report it.
var ErrNoSpace = fmt.Errorf("faultfs: %w", syscall.ENOSPC)

// ErrTorn is returned by the write that Plan.TornWriteAt tears.
var ErrTorn = errors.New("faultfs: torn write")

// Plan describes the faults of one file. The zero Plan changes nothing.
type Plan struct {
	NoSpaceAfter int64         // once this many bytes are written, writes fail with ErrNoSpace; 0 = never
	TornWriteAt  int64         // the write that crosses this many bytes writes only its first half, then fails; 0 = never
	LyingSync    bool          // Sync reports success without syncing
	WriteDelay   time.Duration // every write waits this long first: a slow disk (DEV-04)
}

// File is a storage file with faults. Its counters let a test check what happened.
type File struct {
	engine.StorageFile
	plan Plan

	mu        sync.Mutex
	written   int64
	torn      bool
	syncCalls int
	realSyncs int
	unsynced  [][2]int64 // [offset, end) of the writes since the last real Sync
}

// Wrap returns f with the faults of plan applied.
func Wrap(f engine.StorageFile, plan Plan) *File {
	return &File{StorageFile: f, plan: plan}
}

// WriteAt applies the plan. Concurrent writes are counted in the order they reserve their
// bytes, under the lock: two writes at once never both fit in the last room (#75).
func (f *File) WriteAt(p []byte, off int64) (int, error) {
	if f.plan.WriteDelay > 0 {
		time.Sleep(f.plan.WriteDelay)
	}
	f.mu.Lock()
	written := f.written
	tear := f.plan.TornWriteAt > 0 && !f.torn && written+int64(len(p)) > f.plan.TornWriteAt
	if tear {
		f.torn = true
	}
	var room int64 = -1 // -1: no limit for this write
	if limit := f.plan.NoSpaceAfter; limit > 0 && written+int64(len(p)) > limit {
		room = max(limit-written, 0)
	}
	reserved := int64(len(p))
	if room >= 0 {
		reserved = room
	}
	f.written += reserved // given back below if the write falls short
	f.mu.Unlock()

	if room >= 0 {
		if room == 0 {
			return 0, ErrNoSpace
		}
		n, err := f.reserved(p[:room], off, reserved)
		if err != nil {
			return n, err
		}
		return n, ErrNoSpace
	}
	if tear {
		f.mu.Lock()
		f.written -= reserved // a torn write counts what it writes
		f.mu.Unlock()
		n, err := f.write(p[:len(p)/2], off)
		if err != nil {
			return n, err
		}
		return n, ErrTorn
	}
	return f.reserved(p, off, reserved)
}

// reserved writes p, whose bytes were already counted as reserved, and gives back the part
// not written.
func (f *File) reserved(p []byte, off int64, reserved int64) (int, error) {
	n, err := f.StorageFile.WriteAt(p, off)
	f.mu.Lock()
	if short := reserved - int64(n); short > 0 {
		f.written -= short
	}
	f.noteUnsynced(off, n)
	f.mu.Unlock()
	return n, err
}

func (f *File) write(p []byte, off int64) (int, error) {
	n, err := f.StorageFile.WriteAt(p, off)
	f.mu.Lock()
	f.written += int64(n)
	f.noteUnsynced(off, n)
	f.mu.Unlock()
	return n, err
}

// noteUnsynced records n bytes written at off, not durable until a real Sync. Called with
// f.mu held.
func (f *File) noteUnsynced(off int64, n int) {
	if n > 0 {
		f.unsynced = append(f.unsynced, [2]int64{off, off + int64(n)})
	}
}

// Unsynced returns the ranges [offset, end) written since the last real Sync: what a power
// loss may take back, a lying Sync included.
func (f *File) Unsynced() [][2]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]int64(nil), f.unsynced...)
}

func (f *File) Sync() error {
	f.mu.Lock()
	f.syncCalls++
	lie := f.plan.LyingSync
	if !lie {
		f.realSyncs++
	}
	f.mu.Unlock()
	if lie {
		return nil
	}
	f.mu.Lock()
	pending := len(f.unsynced) // writes that end after this point stay unsynced
	f.mu.Unlock()
	if err := f.StorageFile.Sync(); err != nil {
		return err
	}
	f.mu.Lock()
	f.unsynced = f.unsynced[pending:]
	f.mu.Unlock()
	return nil
}

// Stats returns the bytes written, the Sync calls, and how many of them really synced.
func (f *File) Stats() (written int64, syncCalls, realSyncs int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.written, f.syncCalls, f.realSyncs
}
