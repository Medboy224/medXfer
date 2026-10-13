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
		done <- runPair(context.Background(), strings.NewReader(""), &shown, showDir, []string{"--show", "--ip", "127.0.0.1"})
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
	if err := runPair(context.Background(), strings.NewReader(""), &joined, joinDir, []string{uri}); err != nil {
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
	for _, args := range [][]string{{}, {"not-a-uri"}, {"--show", "medxfer://v1/pair"}, {"--pin", "a", "b"}, {"--pin", "--digits", "4"}} {
		if err := runPair(context.Background(), strings.NewReader(""), &out, t.TempDir(), args); err == nil {
			t.Errorf("xfer pair %v succeeded", args)
		}
	}
}

// waitFor returns the first match of re in the shower's output.
func waitFor(t *testing.T, out *syncBuffer, re *regexp.Regexp) []string {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; {
		if m := re.FindStringSubmatch(out.String()); m != nil {
			return m
		}
		if time.Now().After(deadline) {
			t.Fatalf("not shown (%s):\n%s", re, out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// xfer pair --pin on one side, xfer pair --pin HOST:PORT with the PIN typed on the other: a
// wrong PIN is refused, the right one pairs both (bible §6.2).
func TestPairCommandPIN(t *testing.T) {
	showDir, joinDir := t.TempDir(), t.TempDir()
	var shown syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- runPair(context.Background(), strings.NewReader(""), &shown, showDir, []string{"--pin", "--ip", "127.0.0.1"})
	}()
	m := waitFor(t, &shown, regexp.MustCompile(`PIN on the other device:\s+(\d{3} \d{3})[^\n]*\n\s+there: xfer pair --pin (\S+)`))
	pin, addr := m[1], m[2]
	bad := []byte(strings.ReplaceAll(pin, " ", ""))
	bad[0] = '0' + (bad[0]-'0'+1)%10
	var out bytes.Buffer
	if err := runPair(context.Background(), strings.NewReader(string(bad)+"\n"), &out, t.TempDir(), []string{"--pin", addr}); err == nil {
		t.Fatal("a wrong PIN paired")
	}
	if err := runPair(context.Background(), strings.NewReader(pin+"\n"), &out, joinDir, []string{"--pin", addr}); err != nil {
		t.Fatalf("join: %v\n%s", err, out.String())
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
	if strings.Contains(out.String(), pin) {
		t.Fatal("the initiator printed the PIN back")
	}
}
