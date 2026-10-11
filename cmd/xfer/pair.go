package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/pairing"
)

const pairUsage = `Usage:
  xfer pair --show [--once] [--port N] [--ip ADDR]   (show a QR code; the other device scans it)
  xfer pair [--once] 'medxfer://v1/pair?...'        (pair with the device that shows the QR code)
  --once: trust the device for this session only, write nothing (PAIR-08)
`

// handlePair implements `xfer pair` (bible §6.1, path P1). It exits with status 1 on error.
func handlePair(args []string) {
	if err := runPair(context.Background(), os.Stdout, configDir(), args); err != nil {
		fmt.Fprintf(os.Stderr, "[-] %v\n", err)
		os.Exit(1)
	}
}

func runPair(ctx context.Context, w io.Writer, dir string, args []string) error {
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	show := fs.Bool("show", false, "show a QR code and wait for the other device")
	once := fs.Bool("once", false, "trust for this session only")
	port := fs.Int("port", 0, "TCP port to listen on (--show; default: any free port)")
	ip := fs.String("ip", "", "address to put in the QR code (--show; default: the interface of the default route)")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return fmt.Errorf("%v\n%s", err, pairUsage)
	}
	id, trust, err := loadIdentity(dir)
	if err != nil {
		return err
	}
	name, _ := os.Hostname()
	o := pairing.Options{Identity: id, Trust: trust, Once: *once, DeviceName: name}

	if *show {
		if fs.NArg() != 0 {
			return fmt.Errorf("--show takes no URI\n%s", pairUsage)
		}
		host := *ip
		if host == "" {
			host = discovery.GetPrimaryLocalIP() // the default route's interface (#19)
		}
		ln, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", *port))
		if err != nil {
			return err
		}
		defer ln.Close()
		inv, err := pairing.NewInvitation(pairing.TTL)
		if err != nil {
			return err
		}
		uri := inv.URI(id.Fingerprint, host, ln.Addr().(*net.TCPAddr).Port).String()
		fmt.Fprintln(w, "Scan this QR code with the other device, or run there:")
		fmt.Fprintf(w, "  xfer pair '%s'\n\n", uri)
		if w == os.Stdout {
			discovery.PrintTerminalQR(uri)
		}
		fmt.Fprintf(w, "This device: %s   (valid %d s, for one device)\n", id.Fingerprint.Short(), int(pairing.TTL.Seconds()))
		r, err := pairing.Serve(ctx, ln, inv, o)
		if errors.Is(err, pairing.ErrExpired) {
			return errors.New("the QR code has expired: run xfer pair --show again")
		}
		if err != nil {
			return err
		}
		return pairedMessage(w, r, *once)
	}

	if fs.NArg() != 1 {
		return fmt.Errorf("give the URI shown by the other device\n%s", pairUsage)
	}
	u, err := pairing.Parse(fs.Arg(0), time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "[*] Connecting to %s (key %s)...\n", u.Addr(), u.Fingerprint.Short())
	r, err := pairing.Join(ctx, u, o)
	if err != nil {
		return err
	}
	return pairedMessage(w, r, *once)
}

func pairedMessage(w io.Writer, r pairing.Result, once bool) error {
	name := r.Name
	if name == "" {
		name = r.Address
	}
	if once {
		fmt.Fprintf(w, "[+] Paired with '%s' (key %s) for this session.\n", name, r.Fingerprint.Short())
	} else {
		fmt.Fprintf(w, "[+] Paired with '%s' (key %s): now a trusted device (xfer trust list).\n", name, r.Fingerprint.Short())
	}
	return nil
}
