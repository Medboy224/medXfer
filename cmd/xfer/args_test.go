package main

import (
	"flag"
	"slices"
	"testing"
)

// Flags may follow the positional arguments; a boolean flag never takes the next argument as
// its value, whatever its name (xfer pair --pin is boolean, xfer share --pin is not).
func TestReorderArgs(t *testing.T) {
	pair := flag.NewFlagSet("pair", flag.ContinueOnError)
	pair.Bool("pin", false, "")
	pair.Bool("once", false, "")
	pair.Int("digits", 6, "")
	share := flag.NewFlagSet("share", flag.ContinueOnError)
	share.String("pin", "", "")
	for _, c := range []struct {
		fs   *flag.FlagSet
		args []string
		want []string
	}{
		{pair, []string{"--pin", "10.0.0.2:18888", "--once"}, []string{"--pin", "--once", "10.0.0.2:18888"}},
		{pair, []string{"--digits", "8", "--pin"}, []string{"--digits", "8", "--pin"}},
		{pair, []string{"host", "--digits=8", "--pin"}, []string{"--digits=8", "--pin", "host"}},
		{pair, []string{"x", "--pin", "--", "-odd"}, []string{"--pin", "--", "x", "-odd"}},
		{share, []string{"file", "--pin", "1234"}, []string{"--pin", "1234", "file"}},
	} {
		if got := reorderArgs(c.fs, c.args); !slices.Equal(got, c.want) {
			t.Errorf("reorderArgs(%s, %q) = %q, want %q", c.fs.Name(), c.args, got, c.want)
		}
	}
}
