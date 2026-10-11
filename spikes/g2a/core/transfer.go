package main

// S4: the real engine behind the boundary (plan §2 and §5, G2A-06 and G2A-07). send_file and
// receive_file follow the same path as `xfer send` and `xfer recv` (engine.Sender, Receiver
// and the discovery server), so a transfer between the app and the CLI uses one protocol.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/engine"
)

// transferListener turns engine callbacks into core events; progress is limited to 10 events
// per second (B7).
type transferListener struct {
	c        *coreState
	id, role string
	start    time.Time
	cancel   context.CancelFunc // sender: stops serving once everything is sent

	mu       sync.Mutex
	lastProg time.Time
	done     chan struct{}
	once     sync.Once
}

func newTransferListener(c *coreState, id, role string) *transferListener {
	return &transferListener{c: c, id: id, role: role, start: time.Now(), done: make(chan struct{})}
}

func (l *transferListener) OnStart(fileName string, fileSize int64, totalChunks uint32) {
	l.c.emit("transfer_started", map[string]any{
		"transfer_id": l.id, "role": l.role, "file_name": fileName, "file_size": fileSize, "chunks": totalChunks,
	})
}

func (l *transferListener) OnProgress(s engine.TransferStats) {
	// The engine's sender never reports completion; like `xfer send` (cliListener), it is done
	// when every byte has been served.
	if l.role == "send" && s.TotalBytes > 0 && s.BytesTransferred >= s.TotalBytes {
		l.OnComplete("", time.Since(l.start))
		if l.cancel != nil {
			l.cancel()
		}
		return
	}
	l.mu.Lock()
	if time.Since(l.lastProg) < 100*time.Millisecond {
		l.mu.Unlock()
		return
	}
	l.lastProg = time.Now()
	l.mu.Unlock()
	l.c.emit("transfer_progress", map[string]any{
		"transfer_id": l.id, "role": l.role, "bytes": s.BytesTransferred, "total": s.TotalBytes,
		"mbps": s.SpeedMBps, "streams": s.ActiveStreams, "bottleneck": s.Bottleneck,
	})
}

func (l *transferListener) OnChunkFailed(uint32, int, error) {}

func (l *transferListener) OnComplete(savePath string, d time.Duration) {
	l.once.Do(func() {
		l.c.emit("transfer_done", map[string]any{
			"transfer_id": l.id, "role": l.role, "path": savePath,
			"engine_ms": d.Milliseconds(), "wall_ms": time.Since(l.start).Milliseconds(),
		})
		close(l.done)
	})
}

func (l *transferListener) OnError(err error) {
	l.once.Do(func() {
		l.c.emit("transfer_error", map[string]any{"transfer_id": l.id, "role": l.role, "error": err.Error()})
		close(l.done)
	})
}

var transferSeq struct {
	sync.Mutex
	n int
}

func nextTransferID(role string) string {
	transferSeq.Lock()
	defer transferSeq.Unlock()
	transferSeq.n++
	return fmt.Sprintf("%s-%d", role, transferSeq.n)
}

// transferContext ends when the core stops.
func (c *coreState) transferContext() (context.Context, context.CancelFunc, error) {
	stopCh, started := c.running()
	if !started {
		return nil, nil, fmt.Errorf("core not started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel, nil
}

func init() {
	// send_file {path, port, workers, chunk_mb}: serves one file like `xfer send`, with the
	// discovery server so that `xfer recv -ip <host>:<port>` finds the offer.
	handlers["send_file"] = func(c *coreState, p json.RawMessage) (map[string]any, error) {
		args := struct {
			Path    string `json:"path"`
			Port    int    `json:"port"`
			Workers int    `json:"workers"`
			ChunkMB int    `json:"chunk_mb"`
		}{Port: 19990, Workers: 4, ChunkMB: 2}
		if err := json.Unmarshal(p, &args); err != nil || args.Path == "" {
			return nil, fmt.Errorf("payload must be {path, port?, workers?, chunk_mb?}")
		}
		info, err := os.Stat(args.Path)
		if err != nil {
			return nil, err
		}
		ctx, cancel, err := c.transferContext()
		if err != nil {
			return nil, err
		}
		id := nextTransferID("send")
		offer := &discovery.TransferOffer{
			FileName: filepath.Base(args.Path), FileSize: info.Size(), FileID: engine.GenerateFileIDFromInfo(info),
		}
		code, _, _ := discovery.GeneratePairingCode(discovery.GetPrimaryLocalIP())
		disc := discovery.NewDiscoveryServer("sender", args.Port, offer, "medxfer-g2a")
		disc.SetPairingCode(code) // the discovery server hides the offer from a receiver without it
		disc.Start(ctx)

		bindAddr := fmt.Sprintf("0.0.0.0:%d", args.Port)
		ready := make(chan error, 1)
		l := newTransferListener(c, id, "send")
		l.cancel = cancel
		c.goSafe("send_file", func() {
			defer cancel()
			sender := engine.NewSender(args.Workers, uint32(args.ChunkMB)<<20)
			err := sender.ServeAndSendWithRelPathReady(ctx, bindAddr, args.Path, "", l, 0, ready)
			if err != nil && ctx.Err() == nil {
				l.OnError(err)
			}
		})
		select { // the listener is bound before the call returns: a receiver can dial at once
		case err := <-ready:
			if err != nil {
				return nil, err
			}
		case <-time.After(5 * time.Second):
			return nil, fmt.Errorf("sender not ready after 5 s")
		}
		return map[string]any{"transfer_id": id, "port": args.Port, "file_id": offer.FileID, "size": info.Size(), "pairing_code": code}, nil
	}

	// receive_file {addr, out_dir, workers, file_id}: pulls one file like `xfer recv -ip`.
	handlers["receive_file"] = func(c *coreState, p json.RawMessage) (map[string]any, error) {
		args := struct {
			Addr    string `json:"addr"`
			OutDir  string `json:"out_dir"`
			Workers int    `json:"workers"`
			FileID  string `json:"file_id"`
		}{Workers: 4}
		if err := json.Unmarshal(p, &args); err != nil || args.Addr == "" {
			return nil, fmt.Errorf("payload must be {addr, out_dir?, workers?, file_id?}")
		}
		if _, _, err := net.SplitHostPort(args.Addr); err != nil {
			return nil, fmt.Errorf("addr must be host:port: %w", err)
		}
		if args.OutDir == "" {
			args.OutDir = c.cfg.DownloadDir // B6: paths come from the caller
		}
		if err := os.MkdirAll(args.OutDir, 0o755); err != nil {
			return nil, err
		}
		ctx, cancel, err := c.transferContext()
		if err != nil {
			return nil, err
		}
		id := nextTransferID("recv")
		l := newTransferListener(c, id, "recv")
		c.goSafe("receive_file", func() {
			defer cancel()
			err := engine.NewReceiver(args.OutDir, args.Workers).Pull(ctx, args.Addr, l, args.FileID)
			if err != nil && ctx.Err() == nil {
				l.OnError(err)
			}
		})
		return map[string]any{"transfer_id": id}, nil
	}

	// get_network: the local IPv4 addresses, so that the other device knows where to connect.
	handlers["get_network"] = func(*coreState, json.RawMessage) (map[string]any, error) {
		var addrs []string
		if ifs, err := net.InterfaceAddrs(); err == nil { // may be refused on Android
			for _, a := range ifs {
				if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
					addrs = append(addrs, ipn.IP.String())
				}
			}
		}
		route := ""
		if conn, err := net.Dial("udp4", "8.8.8.8:80"); err == nil { // selects a route, sends nothing
			route = conn.LocalAddr().(*net.UDPAddr).IP.String()
			conn.Close()
		}
		return map[string]any{"interfaces": addrs, "default_route_ip": route, "primary": discovery.GetPrimaryLocalIP()}, nil
	}

	// make_file {path, size_mb}: pseudo-random test data (incompressible), written on the device
	// so that 1 GiB need not be copied to the phone.
	handlers["make_file"] = func(_ *coreState, p json.RawMessage) (map[string]any, error) {
		var args struct {
			Path   string `json:"path"`
			SizeMB int    `json:"size_mb"`
		}
		if err := json.Unmarshal(p, &args); err != nil || args.Path == "" || args.SizeMB <= 0 {
			return nil, fmt.Errorf("payload must be {path, size_mb>0}")
		}
		start := time.Now()
		f, err := os.Create(args.Path)
		if err != nil {
			return nil, err
		}
		rng := rand.NewChaCha8([32]byte{'g', '2', 'a'})
		buf := make([]byte, 4<<20)
		for written := 0; written < args.SizeMB<<20; written += len(buf) {
			_, _ = rng.Read(buf)
			if _, err := f.Write(buf[:min(len(buf), args.SizeMB<<20-written)]); err != nil {
				f.Close()
				return nil, err
			}
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		return map[string]any{"path": args.Path, "ms": time.Since(start).Milliseconds()}, nil
	}

	// sha256 {path}: hash for the end-to-end check of G2A-06 and G2A-07.
	handlers["sha256"] = func(_ *coreState, p json.RawMessage) (map[string]any, error) {
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(p, &args); err != nil || args.Path == "" {
			return nil, fmt.Errorf("payload must be {path}")
		}
		f, err := os.Open(args.Path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		h := sha256.New()
		n, err := io.Copy(h, f)
		if err != nil {
			return nil, err
		}
		return map[string]any{"sha256": hex.EncodeToString(h.Sum(nil)), "size": n}, nil
	}

	// remove {path}: test clean-up (a 1 GiB file per run).
	handlers["remove"] = func(_ *coreState, p json.RawMessage) (map[string]any, error) {
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(p, &args); err != nil || args.Path == "" {
			return nil, fmt.Errorf("payload must be {path}")
		}
		return nil, os.RemoveAll(args.Path)
	}
}
