package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DEV-16 / DEV-17: every recorded scenario replays with xfer ctl and its expectations hold.
func TestCtlReplaysGoldenScenarios(t *testing.T) {
	scenarios, _ := filepath.Glob(filepath.Join("testdata", "scenarios", "*.jsonl"))
	if len(scenarios) == 0 {
		t.Fatal("no scenario in testdata/scenarios")
	}
	for _, sc := range scenarios {
		t.Run(filepath.Base(sc), func(t *testing.T) {
			var out bytes.Buffer
			if err := runCtl([]string{"--script", sc, "--linger", "0s"}, nil, &out); err != nil {
				t.Fatalf("scenario failed: %v\nevents:\n%s", err, out.String())
			}
			assertNDJSONEvents(t, out.Bytes())
		})
	}
}

// A failing expectation makes xfer ctl fail and names the line.
func TestCtlFailsOnUnmetExpectation(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fail.jsonl")
	_ = os.WriteFile(script, []byte(`{"expect": {"event": "status"}}
{"expect": {"event": "no_such_event"}, "timeout_ms": 300}
`), 0o644)
	err := runCtl([]string{"--script", script, "--linger", "0s", "--quiet"}, nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "no_such_event") {
		t.Fatalf("runCtl = %v; want an error naming line 2 and the missing event", err)
	}

	for _, bad := range []string{`{"actoin": "get_status"}`, `not json`} {
		_ = os.WriteFile(script, []byte(bad+"\n"), 0o644)
		if err := runCtl([]string{"--script", script, "--linger", "0s", "--quiet"}, nil, &bytes.Buffer{}); err == nil {
			t.Errorf("runCtl accepted the invalid line %s", bad)
		}
	}
}

// Without --script, requests come from stdin and every event goes to stdout as NDJSON.
func TestCtlStdin(t *testing.T) {
	in := strings.NewReader(`{"id": "s1", "action": "get_status"}
{"expect": {"id": "s1"}}
`)
	var out bytes.Buffer
	if err := runCtl([]string{"--linger", "100ms"}, in, &out); err != nil {
		t.Fatal(err)
	}
	events := assertNDJSONEvents(t, out.Bytes())
	found := false
	for _, e := range events {
		found = found || e["id"] == "s1"
	}
	if !found {
		t.Fatalf("no event answering s1 in the output:\n%s", out.String())
	}
}

func TestJSONMatches(t *testing.T) {
	var got any
	_ = json.Unmarshal([]byte(`{"a": 1, "b": "x", "c": {"d": true, "e": [1, 2]}, "f": null}`), &got)
	cases := map[string]bool{
		`{}`:                          true,
		`{"a": 1}`:                    true,
		`{"a": "*", "b": "*"}`:        true,
		`{"c": {"d": true}}`:          true,
		`{"c": {"e": [1, 2]}}`:        true,
		`{"f": null}`:                 true,
		`{"a": 2}`:                    false,
		`{"b": "y"}`:                  false,
		`{"z": "*"}`:                  false,
		`{"c": {"e": [1]}}`:           false,
		`{"c": {"d": "*", "x": "*"}}`: false,
	}
	for pattern, want := range cases {
		var w any
		_ = json.Unmarshal([]byte(pattern), &w)
		if jsonMatches(w, got) != want {
			t.Errorf("jsonMatches(%s) = %v; want %v", pattern, !want, want)
		}
	}
}

// assertNDJSONEvents checks that every output line is a JSON event and returns them.
func assertNDJSONEvents(t *testing.T, out []byte) []map[string]any {
	t.Helper()
	var events []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		var e map[string]any
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil || e["event"] == nil {
			t.Fatalf("output line is not a JSON event: %s", sc.Text())
		}
		events = append(events, e)
	}
	if len(events) == 0 {
		t.Fatal("no event printed")
	}
	return events
}
