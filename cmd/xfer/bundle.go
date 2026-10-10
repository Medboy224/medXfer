package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/Medboy224/medXfer/pkg/api"
	"github.com/Medboy224/medXfer/pkg/diag"
)

// startJournal sends the log package and the daemon's events to the diagnostic journal
// (DEV-11) and returns the function that closes it. A journal that cannot be opened only
// costs the diagnosis: the daemon still starts.
func startJournal() (stop func()) {
	j, err := diag.OpenJournal(api.JournalDir(), diag.JournalBudget, nil)
	if err != nil {
		log.Printf("[diag] journal unavailable: %v", err)
		return func() {}
	}
	log.SetOutput(io.MultiWriter(os.Stderr, j))
	api.SetJournal(j)
	j.Printf("start medXfer %s", diag.AppVersion)
	return func() {
		api.SetJournal(nil)
		log.SetOutput(os.Stderr)
		_ = j.Close()
	}
}

const bundleUsage = `Usage:
  xfer bundle [-o FILE.zip]

Writes a diagnostic bundle to attach to a bug report: the newest transfer reports, the
daemon's journal (2 MiB at most), the version and the device type, and settings that identify
no one. It never holds keys, PINs, tokens, file names, paths or user names. Nothing is sent:
share the file yourself if you choose to.
`

// handleBundle implements `xfer bundle` (DEV-11). It exits with status 1 on error.
func handleBundle(args []string) {
	if err := runBundle(args, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "[-] xfer bundle: %v\n", err)
		os.Exit(1)
	}
}

func runBundle(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("bundle", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	out := fs.String("o", "", "output file (default medxfer-bundle-<date>.zip in the current folder)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%v\n%s", err, bundleUsage)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q\n%s", fs.Arg(0), bundleUsage)
	}
	path := *out
	if path == "" {
		path = "medxfer-bundle-" + time.Now().Format("20060102-150405") + ".zip"
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = diag.WriteBundle(f, diag.BundleOptions{
		ReportsDir: api.ReportsDir(),
		JournalDir: api.JournalDir(),
		Config:     api.DiagnosticConfig(),
	})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	abs, _ := filepath.Abs(path)
	fmt.Fprintf(stdout, "Diagnostic bundle written: %s\n", abs)
	fmt.Fprintln(stdout, "It holds no key, PIN, token, file name, path or user name. Nothing was sent.")
	return nil
}
