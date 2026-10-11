// Command oneproc is a G2A-06 control: the app's loopback (sender and receiver in ONE process,
// one Go runtime) without Flutter and without the C boundary. Comparing it with the app tells
// whether a gap with `xfer send` + `xfer recv` (two processes) comes from the bridge or from
// running both ends in one process.
// Usage: oneproc <file> <out_dir>   prints the receiver's engine time and wall time in ms (the
// engine time stops at the last chunk; the wall time includes the final flush and rename).
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
)

type listener struct {
	done chan time.Duration
	err  chan error
}

func (l *listener) OnStart(string, int64, uint32)        {}
func (l *listener) OnProgress(engine.TransferStats)      {}
func (l *listener) OnChunkFailed(uint32, int, error)     {}
func (l *listener) OnComplete(_ string, d time.Duration) { l.done <- d }
func (l *listener) OnError(err error)                    { l.err <- err }

func main() {
	src, out := os.Args[1], os.Args[2]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const addr = "127.0.0.1:19989"
	ready := make(chan error, 1)
	go func() {
		_ = engine.NewSender(4, 2<<20).ServeAndSendWithRelPathReady(ctx, addr, src, "", nil, 0, ready)
	}()
	if err := <-ready; err != nil {
		fmt.Println("sender:", err)
		os.Exit(1)
	}
	l := &listener{done: make(chan time.Duration, 1), err: make(chan error, 1)}
	start := time.Now()
	go func() { _ = engine.NewReceiver(out, 4).Pull(ctx, addr, l, "") }()
	select {
	case d := <-l.done:
		fmt.Println(d.Milliseconds(), time.Since(start).Milliseconds())
	case err := <-l.err:
		fmt.Println("receiver:", err)
		os.Exit(1)
	}
}
