package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Medboy224/medXfer/pkg/bench"
	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

const benchUsage = `Usage:
  xfer bench disk [--dir DIR] [--size 32M] [--json]
      Write SIZE of random data with fsync, read it back, delete it. DIR defaults to the
      current folder: point it at the folder you receive into.
  xfer bench net --listen [ADDR]                    (default ADDR :19990; prints a code)
  xfer bench net --peer HOST:PORT --code CODE [--size 32M] [--json]
      Latency (ping-pong) and throughput in both directions, in memory, no disk.

  xfer bench ladder --listen [ADDR] [--source random|zero|file --file PATH] [--once]
                                                    (default ADDR :19991; prints the command)
  xfer bench ladder --peer HOST:PORT --code CODE --fp KEY [--size 256M] [--streams 4]
                    [--chunk 2] [--sink null|disk] [--dir DIR] [--json]
      The ladder (bible §17.4): R1 raw TCP, R2 + TLS with the key pinned, R3 + medXfer
      frames and SHA-256 (a bench offer, null sink), R4 + disk writes without fsync, R5 +
      the journal and its fsync, R6 the whole transfer (offer, acceptance, digest, rename).
      The listening side sends, the other measures. R4 to R6 write into a temporary folder
      in DIR.

  xfer bench matrix --listen [ADDR] ...             (the same server as ladder --listen)
  xfer bench matrix --peer HOST:PORT --code CODE --fp KEY [--size 256M] [--step R6]
                    [--repeat 1] [--streams-list 1,2,4,8] [--chunks 1,2,4,8] [--dir DIR]
                    [--results bench-results] [--json]
      One step for each number of streams and chunk size (MB). The runs alternate between
      the combinations, round after round: a moment of interference hits them all alike.
      Proposes as defaults the combination within 5 % of the best throughput that needs
      the least memory (streams × 2 × chunk), and saves the results in RESULTS/matrix-*.json.

The net benchmark is raw, unencrypted TCP (step R1 only).
`

// handleBench implements `xfer bench` (DEV-07, base version). It exits with status 1 on error.
func handleBench(args []string) {
	if err := runBench(args, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "[-] xfer bench: %v\n", err)
		os.Exit(1)
	}
}

func runBench(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("missing subcommand\n%s", benchUsage)
	}
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "print JSON")
	sizeStr := fs.String("size", "32M", "bytes to write or send (K, M, G suffixes)")
	dir := fs.String("dir", ".", "folder to test (disk)")
	listen := fs.String("listen", "", "serve network benchmarks on this address (net)")
	peer := fs.String("peer", "", "address of a `xfer bench net --listen` (net)")
	code := fs.String("code", "", "code printed by the listening side (net)")
	once := fs.Bool("once", false, "with --listen, stop after one session")
	fp := fs.String("fp", "", "key of the listening side, printed by it (ladder)")
	streams := fs.Int("streams", 0, "data connections (ladder; default 4)")
	chunkMB := fs.Int("chunk", int(protocol.DefaultChunkSize>>20), "chunk size in MB, 1 to 8 (ladder)")
	source := fs.String("source", "random", "bytes sent: random, zero or file (ladder --listen)")
	file := fs.String("file", "", "with --source file, the file to send (ladder --listen)")
	sink := fs.String("sink", "null", "where R3's chunks go: null or disk (ladder --peer)")
	step := fs.String("step", "R6", "the ladder step each cell measures (matrix)")
	repeat := fs.Int("repeat", 1, "runs per cell, whose median counts (matrix)")
	streamsList := fs.String("streams-list", "1,2,4,8", "numbers of streams (matrix)")
	chunks := fs.String("chunks", "1,2,4,8", "chunk sizes in MB (matrix)")
	results := fs.String("results", "bench-results", "folder of the saved results (matrix)")
	// Accept "--listen" without a value: it then uses the default address.
	sub := normalizeCommand(args[0])
	rest := args[1:]
	for i, a := range rest {
		if (a == "--listen" || a == "-listen") && (i+1 == len(rest) || strings.HasPrefix(rest[i+1], "-")) {
			def := ":19990"
			if sub == "ladder" || sub == "matrix" {
				def = ":19991"
			}
			rest = append(append(append([]string{}, rest[:i]...), a+"="+def), rest[i+1:]...)
			break
		}
	}
	if err := fs.Parse(rest); err != nil {
		return fmt.Errorf("%v\n%s", err, benchUsage)
	}
	size, err := parseSize(*sizeStr)
	if err != nil {
		return err
	}

	switch sub {
	case "matrix":
		sizeSet := false
		fs.Visit(func(f *flag.Flag) { sizeSet = sizeSet || f.Name == "size" })
		if !sizeSet {
			size = 256 << 20
		}
		switch {
		case *listen != "":
			return benchLadderListen(stdout, *listen, *source, *file, *once)
		case *peer != "":
			if *code == "" || *fp == "" {
				return fmt.Errorf("--peer needs the --code and --fp printed by the listening side")
			}
			addr, err := peerAddress(*peer)
			if err != nil {
				return err
			}
			if !strings.Contains(*peer, ":") {
				addr = net.JoinHostPort(strings.TrimSpace(*peer), benchLadderDefaultPort)
			}
			ns, err := parseInts(*streamsList)
			if err != nil {
				return fmt.Errorf("--streams-list: %w", err)
			}
			cs, err := parseInts(*chunks)
			if err != nil {
				return fmt.Errorf("--chunks: %w", err)
			}
			var chunkSizes []uint32
			for _, c := range cs {
				chunkSizes = append(chunkSizes, uint32(c)<<20)
			}
			m := bench.MatrixOptions{Step: strings.ToUpper(*step), Streams: ns, Chunks: chunkSizes, Repeat: *repeat}
			return benchMatrixPeer(stdout, addr, *code, *fp, size, *dir, *results, m, *asJSON)
		}
		return fmt.Errorf("matrix needs --listen or --peer\n%s", benchUsage)
	case "ladder":
		sizeSet := false
		fs.Visit(func(f *flag.Flag) { sizeSet = sizeSet || f.Name == "size" })
		if !sizeSet {
			size = 256 << 20
		}
		switch {
		case *listen != "":
			return benchLadderListen(stdout, *listen, *source, *file, *once)
		case *peer != "":
			if *code == "" || *fp == "" {
				return fmt.Errorf("--peer needs the --code and --fp printed by the listening side")
			}
			if *chunkMB < 1 || *chunkMB > 8 {
				return fmt.Errorf("--chunk must be between 1 and 8 (MB)")
			}
			addr, err := peerAddress(*peer)
			if err != nil {
				return err
			}
			if !strings.Contains(*peer, ":") {
				addr = net.JoinHostPort(strings.TrimSpace(*peer), benchLadderDefaultPort)
			}
			var s transfer.Sink
			switch *sink {
			case "null":
				s = transfer.SinkNull
			case "disk":
				s = transfer.SinkDisk
			default:
				return fmt.Errorf("--sink must be null or disk")
			}
			return benchLadderPeer(stdout, addr, *code, *fp, size, *streams, uint32(*chunkMB)<<20, s, *dir, *asJSON)
		}
		return fmt.Errorf("ladder needs --listen or --peer\n%s", benchUsage)
	case "disk":
		return benchDisk(stdout, *dir, size, *asJSON)
	case "net":
		switch {
		case *listen != "":
			return benchNetListen(stdout, *listen, *once)
		case *peer != "":
			if *code == "" {
				return fmt.Errorf("--peer needs the --code printed by the listening side")
			}
			addr, err := peerAddress(*peer)
			if err != nil {
				return err
			}
			return benchNetPeer(stdout, addr, *code, size, *asJSON)
		}
		return fmt.Errorf("net needs --listen or --peer\n%s", benchUsage)
	}
	return fmt.Errorf("unknown bench subcommand %q\n%s", args[0], benchUsage)
}

func benchDisk(stdout io.Writer, dir string, size int64, asJSON bool) error {
	if size < 4<<20 || size > 512<<20 {
		return fmt.Errorf("disk size must be between 4M and 512M")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	res, err := engine.BenchmarkDisk(abs, size)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(stdout, res)
	}
	fmt.Fprintf(stdout, "Disk benchmark: %s (%s, random data, fsync)\n", abs, diag.FormatBytes(size))
	fmt.Fprintf(stdout, "  write: %8.1f MB/s  (%d ms)\n", res.WriteSpeedMBps, res.WriteDurationMs)
	fmt.Fprintf(stdout, "  read:  %8.1f MB/s  (%d ms, may come from the OS cache)\n", res.ReadSpeedMBps, res.ReadDurationMs)
	fmt.Fprintf(stdout, "  rating: %s\n", res.Rating)
	if res.Warning != "" {
		fmt.Fprintf(stdout, "  warning: %s\n", res.Warning)
	}
	return nil
}

func benchNetListen(stdout io.Writer, addr string, once bool) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	code := diag.NewNetBenchCode()
	port := portOf(ln.Addr())
	fmt.Fprintf(stdout, "Network benchmark server on %s, code %s\n", ln.Addr(), code)
	if host, _, _ := net.SplitHostPort(ln.Addr().String()); host != "" && !net.ParseIP(host).IsUnspecified() {
		fmt.Fprintf(stdout, "On the other device: xfer bench net --peer %s --code %s\n", ln.Addr(), code)
	} else if addrs := lanAddresses(); len(addrs) > 0 {
		fmt.Fprintln(stdout, "On the other device, use the address of the network it shares with this one:")
		for _, a := range addrs {
			fmt.Fprintf(stdout, "  %-24s xfer bench net --peer %s --code %s\n", a.iface+":", net.JoinHostPort(a.ip, port), code)
		}
		if len(addrs) == 1 && addrs[0].iface == "default route" {
			// Android hides its interfaces: the default route may be mobile data, unreachable
			// from a device on this phone's hotspot.
			fmt.Fprintf(stdout, "If the other device is on this phone's hotspot, use this phone's address on the hotspot\n"+
				"instead: the default gateway shown on the other device (Windows: ipconfig; Linux: ip route).\n"+
				"  xfer bench net --peer <gateway>:%s --code %s\n", port, code)
		}
	} else {
		fmt.Fprintf(stdout, "No active network interface found. On the other device: xfer bench net --peer <this-ip>:%s --code %s\n", port, code)
	}
	fmt.Fprintln(stdout, "Raw unencrypted TCP. Ctrl+C to stop.")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return diag.ServeNetBench(ctx, ln, code, once, func(err error) {
		fmt.Fprintf(os.Stderr, "[-] session: %v\n", err)
	})
}

func benchNetPeer(stdout io.Writer, addr, code string, size int64, asJSON bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := diag.RunNetBench(ctx, addr, code, size)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(stdout, res)
	}
	fmt.Fprintf(stdout, "Network benchmark: %s each way, raw unencrypted TCP\n", diag.FormatBytes(size))
	fmt.Fprintf(stdout, "  latency:  min %.3f ms | avg %.3f ms | p95 %.3f ms\n", res.RTTMinMs, res.RTTAvgMs, res.RTTP95Ms)
	fmt.Fprintf(stdout, "  upload:   %8.1f MB/s (this device -> peer)\n", res.UploadMBps)
	fmt.Fprintf(stdout, "  download: %8.1f MB/s (peer -> this device)\n", res.DownloadMBps)
	fmt.Fprintf(stdout, "  link: %s, %s\n", res.LinkType, res.Rating)
	return nil
}

// parseSize reads a byte count with an optional K, M or G suffix (powers of 1024).
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	s = strings.TrimSuffix(strings.TrimSuffix(s, "IB"), "B")
	mult := int64(1)
	if n := len(s); n > 0 {
		switch s[n-1] {
		case 'K':
			mult, s = 1<<10, s[:n-1]
		case 'M':
			mult, s = 1<<20, s[:n-1]
		case 'G':
			mult, s = 1<<30, s[:n-1]
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 || v > (1<<40)/mult {
		return 0, fmt.Errorf("invalid size %q (examples: 4M, 512K, 1G)", s)
	}
	return v * mult, nil
}

const (
	benchNetDefaultPort    = "19990"
	benchLadderDefaultPort = "19991"
)

func benchLadderListen(stdout io.Writer, addr, source, file string, once bool) error {
	id, _, err := loadIdentity(configDir())
	if err != nil {
		return err
	}
	srv, err := bench.NewLadderServer(id)
	if err != nil {
		return err
	}
	switch source {
	case "random":
	case "zero":
		srv.SetSource(transfer.ZeroSource())
	case "file":
		if file == "" {
			return fmt.Errorf("--source file needs --file PATH")
		}
		src, _, err := transfer.FileSource(file)
		if err != nil {
			return err
		}
		srv.SetSource(src)
	default:
		return fmt.Errorf("--source must be random, zero or file")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	port := portOf(ln.Addr())
	args := fmt.Sprintf("--code %s --fp %s", srv.Code, id.Fingerprint)
	fmt.Fprintf(stdout, "Bench ladder server on %s (source: %s)\n", ln.Addr(), source)
	if addrs := lanAddresses(); len(addrs) > 0 {
		fmt.Fprintln(stdout, "On the other device, use the address of the network it shares with this one:")
		for _, a := range addrs {
			fmt.Fprintf(stdout, "  %-24s xfer bench ladder --peer %s %s\n", a.iface+":", net.JoinHostPort(a.ip, port), args)
		}
	} else {
		fmt.Fprintf(stdout, "On the other device: xfer bench ladder --peer <this-ip>:%s %s\n", port, args)
	}
	fmt.Fprintln(stdout, "Ctrl+C to stop.")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	srv.OnError = func(err error) { fmt.Fprintf(os.Stderr, "[-] ladder connection: %v\n", err) }
	return srv.Serve(ctx, ln, once)
}

func benchLadderPeer(stdout io.Writer, addr, code, fp string, size int64, streams int, chunk uint32,
	sink transfer.Sink, dir string, asJSON bool) error {
	key, err := identity.ParseFingerprint(fp)
	if err != nil {
		return fmt.Errorf("--fp: %w", err)
	}
	id, _, err := loadIdentity(configDir())
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	res, err := bench.RunLadder(ctx, bench.LadderOptions{Addr: addr, Code: code, Fingerprint: key, Identity: id,
		Streams: streams, Size: size, ChunkSize: chunk, Dir: abs, Sink: sink})
	if err != nil {
		if res != nil && len(res.Steps) > 0 && !asJSON {
			fmt.Fprint(stdout, bench.FormatLadder(res))
		}
		return err
	}
	if asJSON {
		return writeJSON(stdout, res)
	}
	fmt.Fprint(stdout, bench.FormatLadder(res))
	return nil
}

// benchMatrixPeer runs the matrix against a ladder server, shows each cell as it is measured,
// and saves the result in resultsDir (bible §17.4).
func benchMatrixPeer(stdout io.Writer, addr, code, fp string, size int64, dir, resultsDir string,
	m bench.MatrixOptions, asJSON bool) error {
	key, err := identity.ParseFingerprint(fp)
	if err != nil {
		return fmt.Errorf("--fp: %w", err)
	}
	id, _, err := loadIdentity(configDir())
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	m.LadderOptions = bench.LadderOptions{Addr: addr, Code: code, Fingerprint: key, Identity: id, Size: size, Dir: abs}
	if !asJSON {
		m.OnRun = func(round, streams int, chunk uint32, mbps float64) {
			fmt.Fprintf(stdout, "  round %d/%d: %d streams × %s: %.1f MB/s\n", round, m.Repeat, streams, diag.FormatBytes(int64(chunk)), mbps)
		}
		fmt.Fprintf(stdout, "Bench matrix against %s: step %s, %s per run\n", addr, m.Step, diag.FormatBytes(size))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	res, err := bench.RunMatrix(ctx, m)
	if err != nil {
		return err
	}
	res.Host, _ = os.Hostname()
	res.OS = runtime.GOOS + "/" + runtime.GOARCH
	path, err := saveMatrix(resultsDir, res)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(stdout, res)
	}
	fmt.Fprint(stdout, bench.FormatMatrix(res))
	fmt.Fprintf(stdout, "Saved: %s\n", path)
	return nil
}

// saveMatrix writes res to dir/matrix-<host>-<time>.json.
func saveMatrix(dir string, res *bench.MatrixResult) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	host := res.Host
	if host == "" {
		host = "device"
	}
	// Android names every phone "localhost": the system tells them apart from a PC.
	path := filepath.Join(dir, fmt.Sprintf("matrix-%s-%s-%s.json", host, runtime.GOOS, time.Now().Format("20060102-150405")))
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, append(data, '\n'), 0o644)
}

// parseInts reads a comma-separated list of positive integers.
func parseInts(s string) ([]int, error) {
	var out []int
	for _, f := range strings.Split(s, ",") {
		v, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("invalid list %q (example: 1,2,4,8)", s)
		}
		out = append(out, v)
	}
	return out, nil
}

// peerAddress accepts HOST:PORT, or HOST alone (default port). "10.0.0.5.19990", a common
// typo, gets an explicit correction instead of Go's "missing port in address".
func peerAddress(s string) (string, error) {
	s = strings.TrimSpace(s)
	if _, _, err := net.SplitHostPort(s); err == nil {
		return s, nil
	}
	if ip := net.ParseIP(s); ip != nil {
		return net.JoinHostPort(s, benchNetDefaultPort), nil
	}
	if i := strings.LastIndex(s, "."); i > 0 {
		if ip := net.ParseIP(s[:i]); ip != nil && ip.To4() != nil {
			if _, err := strconv.Atoi(s[i+1:]); err == nil {
				return "", fmt.Errorf("invalid address %q: separate the port with a colon: %s:%s", s, s[:i], s[i+1:])
			}
		}
	}
	if !strings.Contains(s, ":") && s != "" {
		return net.JoinHostPort(s, benchNetDefaultPort), nil // host name without port
	}
	return "", fmt.Errorf("invalid address %q (expected HOST:PORT, for example 192.168.1.20:%s)", s, benchNetDefaultPort)
}

type lanAddress struct{ iface, ip string }

// lanAddresses lists the IPv4 addresses of the interfaces that are up, skipping loopback and
// link-local (169.254.x.x) addresses, which another device cannot reach. Android 11 and later
// forbid listing interfaces (net.Interfaces fails or returns nothing under Termux): the address
// of the default route is then returned instead.
func lanAddresses() []lanAddress {
	out := interfaceAddresses()
	if len(out) == 0 {
		if ip := defaultRouteIP(); ip != "" {
			out = append(out, lanAddress{iface: "default route", ip: ip})
		}
	}
	return out
}

// defaultRouteIP returns the local address the system would use to reach the Internet. A UDP
// "connection" only selects a route: no packet is sent.
func defaultRouteIP() string {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	if a, ok := conn.LocalAddr().(*net.UDPAddr); ok && a.IP.To4() != nil && !a.IP.IsLoopback() {
		return a.IP.To4().String()
	}
	return ""
}

func interfaceAddresses() []lanAddress {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []lanAddress
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || ifc.Flags&net.FlagRunning == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, lanAddress{iface: ifc.Name, ip: ip.String()})
		}
	}
	return out
}

func portOf(a net.Addr) string {
	_, port, _ := net.SplitHostPort(a.String())
	return port
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
