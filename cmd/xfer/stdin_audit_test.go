package main

import (
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
)

// Audit §3.4 (#156): the list of senders ends when the standard input is closed (a pipe that
// ends, a script), instead of searching the network again and again.
func TestSenderListEndsOnEOF(t *testing.T) {
	t.Setenv("MEDXFER_CONFIG_DIR", t.TempDir())
	closed := make(chan string)
	close(closed)
	oldStdin, oldDiscover := stdinLines, discoverPeers
	stdinLines = func() <-chan string { return closed }
	searches := 0
	discoverPeers = func(time.Duration) ([]discovery.Peer, error) { searches++; return nil, nil }
	t.Cleanup(func() { stdinLines, discoverPeers = oldStdin, oldDiscover })
	done := make(chan *discovery.Peer, 1)
	go func() { done <- selectOneShotSender() }()
	select {
	case p := <-done:
		if p != nil {
			t.Fatalf("a sender chosen from a closed input: %+v", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("still asking after the input closed (%d searches)", searches)
	}
}
