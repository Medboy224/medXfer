package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Medboy224/medXfer/pkg/api"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
)

const trustUsage = `Usage:
  xfer id                               (this device's fingerprint)
  xfer trust list                       (trusted devices)
  xfer trust forget <fingerprint>       (full fingerprint, or enough of it to be unique)
`

// configDir is where the identity and the trusted devices live: next to config.json.
func configDir() string { return filepath.Dir(api.GetConfigFilePath()) }

// handleID implements `xfer id` (ID-03). It exits with status 1 on error.
func handleID(args []string) {
	if err := runID(os.Stdout, configDir()); err != nil {
		fmt.Fprintf(os.Stderr, "[-] %v\n", err)
		os.Exit(1)
	}
}

func runID(w io.Writer, dir string) error {
	id, err := identity.LoadOrCreate(dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Fingerprint: %s\n", id.Fingerprint.Short())
	fmt.Fprintf(w, "Full:        %s\n", id.Fingerprint)
	fmt.Fprintf(w, "Stored in:   %s\n", filepath.Join(dir, identity.KeyFile))
	return nil
}

// handleTrust implements `xfer trust list|forget` (ID-06). It exits with status 1 on error.
func handleTrust(args []string) {
	if err := runTrust(os.Stdout, configDir(), args); err != nil {
		fmt.Fprintf(os.Stderr, "[-] %v\n", err)
		os.Exit(1)
	}
}

func runTrust(w io.Writer, dir string, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(w, trustUsage)
		return fmt.Errorf("missing subcommand")
	}
	ts, err := identity.OpenTrustStore(filepath.Join(dir, identity.TrustFile))
	if err != nil {
		return err
	}
	switch normalizeCommand(args[0]) {
	case "list", "ls":
		peers := ts.List()
		if len(peers) == 0 {
			fmt.Fprintln(w, "No trusted device.")
			return nil
		}
		for _, p := range peers {
			alias := p.Alias
			if alias == "" {
				alias = "(no alias)"
			}
			fmt.Fprintf(w, "%s  %-20s  added %s", p.Fingerprint.Short(), alias, p.AddedAt.Local().Format("2006-01-02 15:04"))
			if p.LastAddress != "" {
				fmt.Fprintf(w, "  last seen at %s", p.LastAddress)
			}
			fmt.Fprintln(w)
		}
		return nil
	case "forget", "rm", "remove":
		if len(args) != 2 {
			return fmt.Errorf("forget needs one fingerprint\n%s", trustUsage)
		}
		p, err := ts.Find(args[1])
		if err != nil {
			return err
		}
		if _, err := ts.Forget(p.Fingerprint); err != nil {
			return err
		}
		fmt.Fprintf(w, "Forgotten: %s %s. Pair again to trust it.\n", p.Fingerprint.Short(), p.Alias)
		return nil
	}
	return fmt.Errorf("unknown trust subcommand %q\n%s", args[0], trustUsage)
}

// loadIdentity returns this device's identity and trusted devices, stored in dir.
func loadIdentity(dir string) (*identity.Identity, *identity.TrustStore, error) {
	id, err := identity.LoadOrCreate(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("device identity: %w", err)
	}
	ts, err := identity.OpenTrustStore(filepath.Join(dir, identity.TrustFile))
	if err != nil {
		return nil, nil, fmt.Errorf("trusted devices: %w", err)
	}
	return id, ts, nil
}

// dialSecure opens a TCP connection with the radio wake-up strategy of session.DialPeer, then
// the secure channel. There is no plaintext fallback (CHAN-05).
func dialSecure(addr string, cfg securechan.Config) (*securechan.Conn, error) {
	raw, err := session.DialPeer(addr)
	if err != nil {
		return nil, err
	}
	return securechan.Client(context.Background(), raw, cfg)
}
