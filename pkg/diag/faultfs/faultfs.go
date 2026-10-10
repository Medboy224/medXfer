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

	"github.com/Medboy224/medXfer/pkg/engine"
)

// ErrNoSpace wraps syscall.ENOSPC, as a full disk would report it.
var ErrNoSpace = fmt.Errorf("faultfs: %w", syscall.ENOSPC)

// ErrTorn is returned by the write that Plan.TornWriteAt tears.
var ErrTorn = errors.New("faultfs: torn write")

// Plan describes the faults of one file. The zero Plan changes nothing.
type Plan struct {
	NoSpaceAfter int64 // once this many bytes are written, writes fail with ErrNoSpace; 0 = never
	TornWriteAt  int64 // the write that crosses this many bytes writes only its first half, then fails; 0 = never
	LyingSync    bool  // Sync reports success without syncing
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
}

// Wrap returns f with the faults of plan applied.
func Wrap(f engine.StorageFile, plan Plan) *File {
	return &File{StorageFile: f, plan: plan}
}

func (f *File) WriteAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	written := f.written
	tear := f.plan.TornWriteAt > 0 && !f.torn && written+int64(len(p)) > f.plan.TornWriteAt
	if tear {
		f.torn = true
	}
	f.mu.Unlock()

	if limit := f.plan.NoSpaceAfter; limit > 0 && written+int64(len(p)) > limit {
		room := limit - written
		if room <= 0 {
			return 0, ErrNoSpace
		}
		n, err := f.write(p[:room], off)
		if err != nil {
			return n, err
		}
		return n, ErrNoSpace
	}
	if tear {
		n, err := f.write(p[:len(p)/2], off)
		if err != nil {
			return n, err
		}
		return n, ErrTorn
	}
	return f.write(p, off)
}

func (f *File) write(p []byte, off int64) (int, error) {
	n, err := f.StorageFile.WriteAt(p, off)
	f.mu.Lock()
	f.written += int64(n)
	f.mu.Unlock()
	return n, err
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
	return f.StorageFile.Sync()
}

// Stats returns the bytes written, the Sync calls, and how many of them really synced.
func (f *File) Stats() (written int64, syncCalls, realSyncs int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.written, f.syncCalls, f.realSyncs
}
