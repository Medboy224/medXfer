package main

import (
	"context"
	"testing"
	"time"
)

// answers replaces the terminal for one test: lines is what stdinLines gives.
func answers(t *testing.T, lines <-chan string) {
	t.Helper()
	old := stdinLines
	stdinLines = func() <-chan string { return lines }
	t.Cleanup(func() { stdinLines = old })
}

func typed(line string) <-chan string {
	c := make(chan string, 1)
	c <- line
	return c
}

func closedInput() <-chan string {
	c := make(chan string)
	close(c)
	return c
}

// A [Y/n] question: Enter alone is yes, but no answer is no, without a terminal or with
// nothing typed in time (#301). xfer send -i sent without confirmation 60 s after the
// receiver paired.
func TestYesByDefaultNeedsAnAnswer(t *testing.T) {
	oldWait := trustPromptTimeout
	trustPromptTimeout = 50 * time.Millisecond
	t.Cleanup(func() { trustPromptTimeout = oldWait })
	for _, c := range []struct {
		name string
		in   <-chan string
		want bool
	}{
		{"no terminal", closedInput(), false},
		{"nothing typed in time", make(chan string), false},
		{"Enter", typed("\n"), true},
		{"yes", typed("y\n"), true},
		{"no", typed("n\n"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			answers(t, c.in)
			if got := yesByDefault(context.Background()); got != c.want {
				t.Fatalf("yesByDefault = %v; want %v", got, c.want)
			}
		})
	}
}

// xfer recv asks before downloading: without a terminal the answer is no, where
// `xfer recv </dev/null` accepted everything (#314). --auto-accept accepts without asking.
func TestRecvConfirmation(t *testing.T) {
	answers(t, closedInput())
	if acceptOffer(false) {
		t.Fatal("xfer recv without a terminal accepted the offer")
	}
	answers(t, typed("n\n")) // not read: nothing is asked
	if !acceptOffer(true) {
		t.Fatal("--auto-accept did not accept")
	}
}
