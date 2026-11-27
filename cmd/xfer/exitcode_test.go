package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The test binary is also xfer itself: with MEDXFER_TEST_XFER_ARGS set, it runs main with
// those arguments, separated by \x1f.
func TestMain(m *testing.M) {
	if args, ok := os.LookupEnv("MEDXFER_TEST_XFER_ARGS"); ok {
		os.Args = append([]string{"xfer"}, strings.Split(args, "\x1f")...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runXfer runs xfer with args, configDir as its configuration folder and no terminal; it
// returns the exit code and what xfer printed.
func runXfer(t *testing.T, configDir string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "MEDXFER_TEST_XFER_ARGS="+strings.Join(args, "\x1f"), "MEDXFER_CONFIG_DIR="+configDir)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

// A send, a receive or a node that fails exits with a code other than 0, so that a script
// sees it (#314). Each case fails before using the network.
func TestFailuresExitNonZero(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		config string
		args   []string
	}{
		{"send of a missing file", t.TempDir(), []string{"send", filepath.Join(t.TempDir(), "missing")}},
		{"recv of a bad QR code", t.TempDir(), []string{"recv", "medxfer://bad"}},
		{"recv --pin without an address", t.TempDir(), []string{"recv", "--pin"}},
		{"node without its identity", notADir, []string{"node"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if code, out := runXfer(t, c.config, c.args...); code == 0 {
				t.Fatalf("xfer %s exited with 0:\n%s", strings.Join(c.args, " "), out)
			}
		})
	}
}
