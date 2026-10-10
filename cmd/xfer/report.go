package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Medboy224/medXfer/pkg/api"
	"github.com/Medboy224/medXfer/pkg/diag"
)

const reportUsage = `Usage:
  xfer report show [--json] [report.json]       (latest saved report when no file is given)
  xfer report compare [--json] a.json b.json    (differences of b against a)
  xfer report list                              (saved reports, oldest first)
`

// handleReport implements `xfer report` (DEV-06). It exits with status 1 on error.
func handleReport(args []string) {
	if err := runReport(args); err != nil {
		fmt.Fprintf(os.Stderr, "[-] %v\n", err)
		os.Exit(1)
	}
}

func runReport(args []string) error {
	if len(args) == 0 {
		fmt.Print(reportUsage)
		return fmt.Errorf("missing subcommand")
	}
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON instead of text")
	if err := fs.Parse(reorderArgs(args[1:])); err != nil {
		return err
	}
	files := fs.Args()

	switch normalizeCommand(args[0]) {
	case "show":
		path, err := reportPath(files)
		if err != nil {
			return err
		}
		r, err := diag.LoadReport(path)
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(r)
		}
		fmt.Println(diag.FormatText(r))
		fmt.Println("File:", path)
		return nil

	case "compare":
		if len(files) != 2 {
			return fmt.Errorf("compare needs two report files\n%s", reportUsage)
		}
		a, err := diag.LoadReport(files[0])
		if err != nil {
			return err
		}
		b, err := diag.LoadReport(files[1])
		if err != nil {
			return err
		}
		c := diag.Compare(a, b)
		if *asJSON {
			return printJSON(c)
		}
		fmt.Printf("A: %s\nB: %s\n\n", files[0], files[1])
		fmt.Print(diag.FormatComparison(c))
		return nil

	case "list":
		paths, err := diag.ListReports(api.ReportsDir())
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if len(paths) == 0 {
			fmt.Println("No saved report in", api.ReportsDir())
		}
		for _, p := range paths {
			fmt.Println(p)
		}
		return nil
	}
	fmt.Print(reportUsage)
	return fmt.Errorf("unknown report subcommand %q", args[0])
}

// reportPath returns the given file, or the newest report saved by the daemon.
func reportPath(files []string) (string, error) {
	switch len(files) {
	case 1:
		return files[0], nil
	case 0:
		paths, err := diag.ListReports(api.ReportsDir())
		if err != nil || len(paths) == 0 {
			return "", fmt.Errorf("no saved report in %s; give a report file", api.ReportsDir())
		}
		return paths[len(paths)-1], nil
	}
	return "", fmt.Errorf("show takes one report file\n%s", reportUsage)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
