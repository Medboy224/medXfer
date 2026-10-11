package main

import (
	"bytes"
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
)

// syncBuffer is written by the shower's goroutine and read by the test.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// xfer pair --show on one side, xfer pair <uri> on the other: both trust each other.
func TestPairCommand(t *testing.T) {
	showDir, joinDir := t.TempDir(), t.TempDir()
	var shown syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- runPair(context.Background(), &shown, showDir, []string{"--show", "--ip", "127.0.0.1"})
	}()
	re := regexp.MustCompile(`xfer pair '(medxfer://[^']+)'`)
	var uri string
	for deadline := time.Now().Add(5 * time.Second); uri == ""; {
		if m := re.FindStringSubmatch(shown.String()); m != nil {
			uri = m[1]
		} else if time.Now().After(deadline) {
			t.Fatalf("no URI shown:\n%s", shown.String())
		} else {
			time.Sleep(10 * time.Millisecond)
		}
	}
	var joined bytes.Buffer
	if err := runPair(context.Background(), &joined, joinDir, []string{uri}); err != nil {
		t.Fatalf("join: %v\n%s", err, joined.String())
	}
	if err := <-done; err != nil {
		t.Fatalf("show: %v", err)
	}
	for _, c := range []struct{ self, other string }{{showDir, joinDir}, {joinDir, showDir}} {
		ts, _ := identity.OpenTrustStore(filepath.Join(c.self, identity.TrustFile))
		other, _ := identity.LoadOrCreate(c.other)
		if !ts.Contains(other.Fingerprint) {
			t.Fatalf("%s does not trust %s", c.self, c.other)
		}
	}
	if !strings.Contains(joined.String(), "Paired with") {
		t.Fatalf("join output:\n%s", joined.String())
	}
}

func TestPairCommandUsage(t *testing.T) {
	var out bytes.Buffer
	for _, args := range [][]string{{}, {"not-a-uri"}, {"--show", "medxfer://v1/pair"}} {
		if err := runPair(context.Background(), &out, t.TempDir(), args); err == nil {
			t.Errorf("xfer pair %v succeeded", args)
		}
	}
}
