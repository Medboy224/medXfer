package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The schema the interface relies on (API-06): every action the core answers and every event it
// sends, frozen in testdata/schema-v1.json. Adding, removing or renaming one fails this test
// until the golden file is updated on purpose (MEDXFER_UPDATE_GOLDEN=1 go test ./pkg/core), and
// a change that breaks a caller bumps SchemaVersion.
type schema struct {
	Version int      `json:"version"`
	Actions []string `json:"actions"`
	Events  []string `json:"events"`
}

// dynamicEvents are sent with a name held in a variable (pkg/api/transfers.go).
var dynamicEvents = []string{"file_paused", "file_resumed", "file_skipped", "item_completed", "item_failed",
	"transfer_paused", "transfer_resumed"}

func currentSchema(t *testing.T) schema {
	t.Helper()
	files, err := filepath.Glob("../api/*.go")
	if err != nil {
		t.Fatal(err)
	}
	var src strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(data)
	}
	all := src.String()

	handler, err := os.ReadFile("../api/handler.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(handler)
	start := strings.Index(body, "func (s *DaemonServer) dispatch(")
	if start < 0 {
		t.Fatal("dispatch not found")
	}
	body = body[start:]
	body = body[:strings.Index(body, "\n}\n")]
	var actions []string
	for _, m := range regexp.MustCompile(`(?m)^\s*case (.+):`).FindAllStringSubmatch(body, -1) {
		for _, name := range regexp.MustCompile(`"([a-z_]+)"`).FindAllStringSubmatch(m[1], -1) {
			actions = append(actions, name[1])
		}
	}

	var events []string
	for _, m := range regexp.MustCompile(`NewEvent\("([a-z_]+)"`).FindAllStringSubmatch(all, -1) {
		events = append(events, m[1])
	}
	for _, e := range dynamicEvents {
		if !strings.Contains(all, `"`+e+`"`) {
			t.Fatalf("dynamic event %q no longer appears in pkg/api: update dynamicEvents", e)
		}
		events = append(events, e)
	}
	slices.Sort(actions)
	slices.Sort(events)
	return schema{Version: SchemaVersion, Actions: slices.Compact(actions), Events: slices.Compact(events)}
}

func TestSchemaIsFrozen(t *testing.T) {
	got := currentSchema(t)
	path := filepath.Join("testdata", "schema-v1.json")
	if os.Getenv("MEDXFER_UPDATE_GOLDEN") == "1" {
		data, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var want schema
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if want.Version != SchemaVersion {
		t.Fatalf("golden file is schema v%d, the code v%d", want.Version, SchemaVersion)
	}
	diff := func(kind string, want, got []string) {
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("%s %q disappeared: a caller may rely on it (update the golden file on purpose)", kind, w)
			}
		}
		for _, g := range got {
			if !slices.Contains(want, g) {
				t.Errorf("new %s %q: add it to the golden file (MEDXFER_UPDATE_GOLDEN=1)", kind, g)
			}
		}
	}
	diff("action", want.Actions, got.Actions)
	diff("event", want.Events, got.Events)
}
