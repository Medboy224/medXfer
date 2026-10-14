package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/pairing"
)

const pairUsage = `Usage:
  xfer pair --show [--once] [--port N] [--ip ADDR]   (show a QR code; the other device scans it)
  xfer pair [--once] 'medxfer://v1/pair?...'        (pair with the device that shows the QR code)
  xfer pair --pin [--digits 8] [--once] [--port N]   (show a PIN; with --show, a QR code too)
  xfer pair --pin [--once] HOST[:PORT]               (type the PIN shown by the device at HOST)
  --once: trust the device for this session only, write nothing (PAIR-08)
`

// handlePair implements `xfer pair` (bible §6.1 and §6.2, paths P1 and P2). It exits with
// status 1 on error.
func handlePair(args []string) {
	if err := runPair(context.Background(), os.Stdin, os.Stdout, configDir(), args); err != nil {
		fmt.Fprintf(os.Stderr, "[-] %v\n", err)
		os.Exit(1)
	}
}

// runPair runs `xfer pair`; in gives the PIN typed on the initiator. The PIN is never an
// argument: it would stay in the shell history and the process list.
func runPair(ctx context.Context, in io.Reader, w io.Writer, dir string, args []string) error {
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	show := fs.Bool("show", false, "show a QR code and wait for the other device")
	pin := fs.Bool("pin", false, "pair by PIN: show one, or type the one shown at HOST")
	digits := fs.Int("digits", pairing.PINDigits, "digits of the PIN shown, 6 or 8 (--pin)")
	once := fs.Bool("once", false, "trust for this session only")
	port := fs.Int("port", 0, "TCP port to listen on (--show, --pin; default: any free port)")
	ip := fs.String("ip", "", "address to show (--show, --pin; default: the interface of the default route)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return fmt.Errorf("%v\n%s", err, pairUsage)
	}
	id, trust, err := loadIdentity(dir)
	if err != nil {
		return err
	}
	name, _ := os.Hostname()
	o := pairing.Options{Identity: id, Trust: trust, Once: *once, DeviceName: name}

	if *show || (*pin && fs.NArg() == 0) {
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
		lnPort := ln.Addr().(*net.TCPAddr).Port
		var inv *pairing.Invitation
		if *show {
			if inv, err = pairing.NewInvitation(pairing.TTL); err != nil {
				return err
			}
			uri := inv.URI(id.Fingerprint, host, lnPort).String()
			fmt.Fprintln(w, "Scan this QR code with the other device, or run there:")
			fmt.Fprintf(w, "  xfer pair '%s'\n\n", uri)
			if w == os.Stdout {
				discovery.PrintTerminalQR(uri)
			}
		}
		if *pin {
			addr := net.JoinHostPort(host, strconv.Itoa(lnPort))
			if o.PIN, err = pairing.NewPIN(pairing.PINConfig{Digits: *digits, Show: pinScreen(w, "xfer pair --pin "+addr)}); err != nil {
				return err
			}
		}
		fmt.Fprintf(w, "This device: %s   (valid %d s, for one device)\n", id.Fingerprint.Short(), int(pairing.TTL.Seconds()))
		r, err := pairing.Serve(ctx, ln, inv, o)
		if errors.Is(err, pairing.ErrExpired) {
			return errors.New("the pairing code has expired: run xfer pair again")
		}
		if err != nil {
			return err
		}
		return pairedMessage(w, r, *once)
	}

	if fs.NArg() != 1 {
		return fmt.Errorf("give the URI shown by the other device, or --pin and its address\n%s", pairUsage)
	}
	if *pin {
		addr := fs.Arg(0)
		if _, _, err := net.SplitHostPort(addr); err != nil {
			addr = net.JoinHostPort(addr, strconv.Itoa(defaultPort))
		}
		fmt.Fprintf(w, "PIN shown on the other device: ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		fmt.Fprintf(w, "[*] Connecting to %s...\n", addr)
		r, err := pairing.JoinPIN(ctx, addr, strings.TrimSpace(line), o)
		if errors.Is(err, pairing.ErrRefused) {
			return errors.New("pairing refused: check the PIN (a PIN changes after 3 wrong ones), or the address")
		}
		if err != nil {
			return err
		}
		return pairedMessage(w, r, *once)
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

// pinScreen prints each PIN with the command to type on the other device, and the wait after
// PINs destroyed (PAIR-04). The PIN goes to the screen only, never to a log (bible chapter 13).
func pinScreen(w io.Writer, command string) func(code string, expires time.Time) {
	return func(code string, expires time.Time) {
		if code == "" && expires.IsZero() {
			fmt.Fprintln(w, "[-] Too many wrong PINs: pairing stopped. Try again later.")
			return
		}
		if code == "" {
			fmt.Fprintf(w, "[-] Too many wrong PINs: a new PIN at %s.\n", expires.Format("15:04:05"))
			return
		}
		fmt.Fprintf(w, "Type this PIN on the other device:  %s   (until %s)\n", pairing.FormatPIN(code), expires.Format("15:04:05"))
		fmt.Fprintf(w, "  there: %s\n", command)
	}
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
