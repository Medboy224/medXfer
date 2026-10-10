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
	"strconv"
	"strings"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/engine"
)

const benchUsage = `Usage:
  xfer bench disk [--dir DIR] [--size 32M] [--json]
      Write SIZE of random data with fsync, read it back, delete it. DIR defaults to the
      current folder: point it at the folder you receive into.
  xfer bench net --listen [ADDR]                    (default ADDR :19990; prints a code)
  xfer bench net --peer HOST:PORT --code CODE [--size 32M] [--json]
      Latency (ping-pong) and throughput in both directions, in memory, no disk.

The network benchmark is raw, unencrypted TCP (step R1): the encrypted measurement comes
with the secure channel (J3).
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
	// Accept "--listen" without a value: it then uses the default address.
	sub := normalizeCommand(args[0])
	rest := args[1:]
	for i, a := range rest {
		if (a == "--listen" || a == "-listen") && (i+1 == len(rest) || strings.HasPrefix(rest[i+1], "-")) {
			rest = append(append(append([]string{}, rest[:i]...), a+"=:19990"), rest[i+1:]...)
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
			return benchNetPeer(stdout, *peer, *code, size, *asJSON)
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
