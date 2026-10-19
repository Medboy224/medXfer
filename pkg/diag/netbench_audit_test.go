package diag

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// slowNetBenchServer answers one session as ServeNetBench does, but waits pause before the
// result of the upload and again before the download: a slow link, whose steps each take
// about pause, in wall-clock time (no load on the machine changes it).
func slowNetBenchServer(t *testing.T, pause time.Duration) string {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		hdr := make([]byte, len(netBenchMagic)+8+8)
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return
		}
		size := int64(binary.BigEndian.Uint64(hdr[len(netBenchMagic)+8:]))
		if _, err := conn.Write([]byte{0}); err != nil {
			return
		}
		ping := make([]byte, 8)
		for range netBenchPings {
			if _, err := io.ReadFull(conn, ping); err != nil {
				return
			}
			if _, err := conn.Write(ping); err != nil {
				return
			}
		}
		if _, err := io.CopyN(io.Discard, conn, size); err != nil {
			return
		}
		time.Sleep(pause)
		if err := binary.Write(conn, binary.BigEndian, int64(pause)); err != nil {
			return
		}
		time.Sleep(pause)
		_ = sendRandom(conn, size)
	}()
	return ln.Addr().String()
}

// Audit §3.8 (#156): each step of the client has its own deadline, as on the server: a slow
// link where each step fits but the whole session does not still gets measured.
func TestNetBenchRenewsItsDeadlineEachStep(t *testing.T) {
	old := netBenchIO
	netBenchIO = 1200 * time.Millisecond
	t.Cleanup(func() { netBenchIO = old })
	// Each step waits 0.8 s, the session 1.6 s: a third of margin either way.
	addr := slowNetBenchServer(t, 800*time.Millisecond)
	if _, err := RunNetBench(context.Background(), addr, "anything", NetBenchMinSize); err != nil {
		t.Fatalf("a session whose steps each fit was cut: %v", err)
	}
}
