package bench

import (
	"context"
	"net"
	"testing"
	"time"

	ident "github.com/Medboy224/medXfer/pkg/identity"
)

func ladderPair(t *testing.T) (*LadderServer, LadderOptions) {
	t.Helper()
	serverID, err := ident.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clientID, err := ident.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewLadderServer(serverID)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Serve(ctx, ln, false) }()
	t.Cleanup(func() { cancel(); <-done })
	return s, LadderOptions{Addr: ln.Addr().String(), Code: s.Code, Fingerprint: serverID.Fingerprint,
		Identity: clientID, Streams: 2, Size: 8 << 20, ChunkSize: 1 << 20, Dir: t.TempDir()}
}

// DEV-07: the ladder measures R1 to R6 against a server, with the transfer engine from R3 on.
func TestLadderRunsEveryStep(t *testing.T) {
	_, o := ladderPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := RunLadder(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != len(Steps) || res.Steps[0].RatioToR1 != 1 {
		t.Fatalf("steps %+v", res.Steps)
	}
	for _, s := range res.Steps {
		if s.MBps <= 0 || s.Bytes != o.Size {
			t.Errorf("%s: %+v", s.Step, s)
		}
	}
	for _, s := range res.Steps[2:] {
		if s.Bottleneck == nil || s.Bottleneck.Stage == "" {
			t.Errorf("%s has no limiting stage", s.Step)
		}
	}
	t.Log("\n" + FormatLadder(res))
}

func TestLadderRefusesWrongCodeOrKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, o := ladderPair(t)
	o.Code = "wrong"
	if _, err := RunLadder(ctx, o); err == nil {
		t.Fatal("a wrong code was accepted")
	}
	_, o = ladderPair(t)
	other, _ := ident.LoadOrCreate(t.TempDir())
	o.Fingerprint = other.Fingerprint
	res, err := RunLadder(ctx, o)
	if err == nil || len(res.Steps) != 1 { // R1 has no TLS; R2 must refuse the other key
		t.Fatalf("pinned key not enforced: %+v, %v", res, err)
	}
}
