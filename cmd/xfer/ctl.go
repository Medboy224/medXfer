package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/api"
	"github.com/Medboy224/medXfer/pkg/wsclient"
)

const ctlUsage = `Usage:
  xfer ctl [--script scenario.jsonl] [--out DIR] [--config DIR] [--name NAME] [--linger 1s] [--quiet]

Starts a daemon of its own (random port, random control token, temporary folders unless
--out / --config are given), reads one JSON object per line and prints every daemon event as
one JSON line on stdout (NDJSON). Input lines:
  {"id": "r1", "action": "get_status", "payload": {...}}      send a request
  {"expect": {"event": "status", "id": "r1", "data": {...}}, "timeout_ms": 5000}
                                                             wait for a matching event
  {"sleep_ms": 200}                                          pause
  {"comment": "..."}                                         ignored, like empty lines
In "data", "*" matches any value; objects match when the listed keys match.
${outdir} and ${configdir} are replaced by the daemon's folders (JSON-escaped).
An expect that fails ends xfer ctl with status 1.
`

// ctlLine is one input line of xfer ctl.
type ctlLine struct {
	ID        string          `json:"id,omitempty"`
	Action    string          `json:"action,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Expect    *ctlExpect      `json:"expect,omitempty"`
	TimeoutMS int             `json:"timeout_ms,omitempty"`
	SleepMS   int             `json:"sleep_ms,omitempty"`
	Comment   string          `json:"comment,omitempty"`
}

type ctlExpect struct {
	Event string          `json:"event,omitempty"`
	ID    string          `json:"id,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

const ctlDefaultExpectTimeout = 10 * time.Second

// handleCtl implements `xfer ctl` (DEV-16, DEV-17). It exits with status 1 on error.
func handleCtl(args []string) {
	if err := runCtl(args, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "[-] xfer ctl: %v\n", err)
		os.Exit(1)
	}
}

func runCtl(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("ctl", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	script := fs.String("script", "", "replay this scenario instead of reading stdin")
	outDir := fs.String("out", "", "receive folder (default: temporary)")
	configDir := fs.String("config", "", "config folder (default: temporary)")
	name := fs.String("name", "medxfer-ctl", "device name of the daemon")
	linger := fs.Duration("linger", time.Second, "keep printing events this long after the last input line")
	quiet := fs.Bool("quiet", false, "do not print events")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%v\n%s", err, ctlUsage)
	}

	in := stdin
	if *script != "" {
		f, err := os.Open(*script)
		if err != nil {
			return err
		}
		defer f.Close()
		in = f
	}

	for _, dir := range []*string{outDir, configDir} {
		if *dir == "" {
			tmp, err := os.MkdirTemp("", "medxfer-ctl-*")
			if err != nil {
				return err
			}
			defer os.RemoveAll(tmp)
			*dir = tmp
		}
	}
	api.SetCustomConfigDir(*configDir)

	srv := api.NewDaemonServer(0, *outDir, *name)
	ln, err := srv.Listen(0)
	if err != nil {
		return err
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Stop()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	client, err := wsclient.Dial(ctx, "127.0.0.1:"+port, srv.ControlToken())
	cancel()
	if err != nil {
		return err
	}
	defer client.Close()

	var outMu sync.Mutex
	if !*quiet {
		enc := json.NewEncoder(stdout)
		client.Watch(func(e wsclient.Event) {
			outMu.Lock()
			_ = enc.Encode(e)
			outMu.Unlock()
		})
	}

	vars := strings.NewReplacer("${outdir}", jsonEscape(*outDir), "${configdir}", jsonEscape(*configDir))
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if err := runCtlLine(client, vars.Replace(text)); err != nil {
			return fmt.Errorf("line %d: %w", lineNo, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	time.Sleep(*linger)
	return nil
}

func runCtlLine(client *wsclient.Client, text string) error {
	var line ctlLine
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&line); err != nil {
		return fmt.Errorf("invalid line %s: %w", text, err)
	}
	switch {
	case line.Action != "":
		var payload any
		if len(line.Payload) > 0 {
			payload = line.Payload
		}
		_, err := client.Send(line.ID, line.Action, payload)
		return err
	case line.Expect != nil:
		return ctlExpectEvent(client, line.Expect, line.TimeoutMS)
	case line.SleepMS > 0:
		time.Sleep(time.Duration(line.SleepMS) * time.Millisecond)
	}
	return nil
}

func ctlExpectEvent(client *wsclient.Client, want *ctlExpect, timeoutMS int) error {
	var wantData any
	if len(want.Data) > 0 {
		if err := json.Unmarshal(want.Data, &wantData); err != nil {
			return fmt.Errorf("expect: invalid data: %w", err)
		}
	}
	timeout := ctlDefaultExpectTimeout
	if timeoutMS > 0 {
		timeout = time.Duration(timeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, err := client.WaitFor(ctx, func(e wsclient.Event) bool {
		if (want.Event != "" && e.Event != want.Event) || (want.ID != "" && e.ID != want.ID) {
			return false
		}
		if wantData == nil {
			return true
		}
		var got any
		return json.Unmarshal(e.Data, &got) == nil && jsonMatches(wantData, got)
	})
	if errors.Is(err, context.DeadlineExceeded) {
		w, _ := json.Marshal(want)
		return fmt.Errorf("expect %s: no matching event within %v", w, timeout)
	}
	return err
}

// jsonMatches reports whether got matches the pattern want: "*" matches any value, an
// object matches when every key of want is in got and matches, other values must be equal.
func jsonMatches(want, got any) bool {
	switch w := want.(type) {
	case string:
		if w == "*" {
			return true
		}
		g, ok := got.(string)
		return ok && g == w
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for k, wv := range w {
			gv, present := g[k]
			if !present || !jsonMatches(wv, gv) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for i := range w {
			if !jsonMatches(w[i], g[i]) {
				return false
			}
		}
		return true
	default: // numbers, booleans, null
		return want == got
	}
}

// jsonEscape returns s escaped for use inside a JSON string (Windows paths hold backslashes).
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
