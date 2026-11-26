package pairing

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
)

// tagsHelperDir names, for the helper process, the device whose tags it prints.
const tagsHelperDir = "MEDXFER_TEST_TAGS_DIR"

// #308: two launches of one device within a period announce the same tags, completing ones
// included. Otherwise the tags that stay across a restart would be the real ones, and their
// number that of the trusted devices.
func TestAnnounceTagsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	if _, err := identity.LoadOrCreate(dir); err != nil {
		t.Fatal(err)
	}
	ts, err := identity.OpenTrustStore(filepath.Join(dir, identity.TrustFile))
	if err != nil {
		t.Fatal(err)
	}
	other := device(t, "o")
	key := make([]byte, tagKeySize)
	key[0] = 7
	if err := ts.Add(identity.Peer{Fingerprint: other.Identity.Fingerprint, TagKey: key}); err != nil {
		t.Fatal(err)
	}
	at := strconv.FormatInt(time.Date(2026, 11, 26, 12, 1, 0, 0, time.UTC).Unix(), 10)
	launch := func() string {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAnnounceTagsHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), tagsHelperDir+"="+dir, "MEDXFER_TEST_TAGS_AT="+at)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("helper: %v\n%s", err, out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if tags, ok := strings.CutPrefix(strings.TrimSpace(line), "tags="); ok {
				return tags
			}
		}
		t.Fatalf("helper printed no tags:\n%s", out)
		return ""
	}
	first, second := launch(), launch()
	if n := len(strings.Split(first, ",")); n != MaxTags {
		t.Fatalf("%d tags, want %d", n, MaxTags)
	}
	if first != second {
		t.Fatalf("a restart within the period changed the tags:\n%s\n%s", first, second)
	}
}

// TestAnnounceTagsHelper prints the tags of the device in tagsHelperDir; it runs alone in a
// process of its own, started by TestAnnounceTagsSurviveARestart.
func TestAnnounceTagsHelper(t *testing.T) {
	dir := os.Getenv(tagsHelperDir)
	if dir == "" {
		t.Skip("helper process of TestAnnounceTagsSurviveARestart")
	}
	id, err := identity.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := identity.OpenTrustStore(filepath.Join(dir, identity.TrustFile))
	if err != nil {
		t.Fatal(err)
	}
	sec, _ := strconv.ParseInt(os.Getenv("MEDXFER_TEST_TAGS_AT"), 10, 64)
	tags := AnnounceTags(ts, id, time.Unix(sec, 0))
	os.Stdout.WriteString("tags=" + strings.Join(tags, ",") + "\n")
}
