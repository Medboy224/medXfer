package identity

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// fp is a fingerprint for tests: n in its first bytes.
func fp(n int) Fingerprint {
	var f Fingerprint
	f[0], f[1], f[31] = byte(n>>8), byte(n), 0x5a
	return f
}

// #303, decision R4: two processes share trusted.json, the CLI and a running node or the app.
// What the CLI forgets or renames stays so when the other writes next (a reconnection records an
// address, as touchTrust does), and the other sees it.
func TestTrustStoreSharedByTwoProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), TrustFile)
	daemon, err := OpenTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	a, b := fp(1), fp(2)
	for _, p := range []Peer{{Fingerprint: a, Alias: "A"}, {Fingerprint: b, Alias: "B"}} {
		if err := daemon.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	cli, err := OpenTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Forget(a); err != nil {
		t.Fatal(err)
	}
	p, _ := cli.Get(b)
	p.Alias, p.Renamed = "Mine", true
	if err := cli.Add(p); err != nil {
		t.Fatal(err)
	}

	p, ok := daemon.Get(b) // then the daemon records b's new address
	if !ok {
		t.Fatal("b is no longer trusted")
	}
	p.LastAddress = "192.0.2.8"
	if err := daemon.Add(p); err != nil {
		t.Fatal(err)
	}
	if daemon.Contains(a) {
		t.Fatal("the daemon still trusts a device the CLI forgot")
	}
	again, err := OpenTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Contains(a) {
		t.Fatal("a forgotten device came back in trusted.json")
	}
	if p, _ := again.Get(b); p.Alias != "Mine" || !p.Renamed || p.LastAddress != "192.0.2.8" {
		t.Fatalf("b in trusted.json: %+v; want the CLI's name and the daemon's address", p)
	}
}

// trustHelperEnv names, for the helper process, the trusted.json it adds to.
const trustHelperEnv = "MEDXFER_TEST_TRUST_PATH"

// #303: processes that change trusted.json at the same time lose none of the changes: each
// one is made on the file as it is, under a lock.
func TestTrustStoreConcurrentProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), TrustFile)
	const procs, each = 4, 25
	errs := make(chan error, procs)
	for i := 0; i < procs; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestTrustStoreHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), trustHelperEnv+"="+path, "MEDXFER_TEST_TRUST_FROM="+strconv.Itoa(i*each),
			"MEDXFER_TEST_TRUST_COUNT="+strconv.Itoa(each))
		go func() {
			out, err := cmd.CombinedOutput()
			if err != nil {
				err = fmt.Errorf("%v\n%s", err, out)
			}
			errs <- err
		}()
	}
	for i := 0; i < procs; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	ts, err := OpenTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ts.List()); n != procs*each {
		t.Fatalf("%d devices in trusted.json, want %d: changes were lost", n, procs*each)
	}
}

// TestTrustStoreHelper adds devices to the trusted.json of trustHelperEnv, one by one; it runs in
// processes of its own, started by TestTrustStoreConcurrentProcesses.
func TestTrustStoreHelper(t *testing.T) {
	path := os.Getenv(trustHelperEnv)
	if path == "" {
		t.Skip("helper process of TestTrustStoreConcurrentProcesses")
	}
	from, _ := strconv.Atoi(os.Getenv("MEDXFER_TEST_TRUST_FROM"))
	count, _ := strconv.Atoi(os.Getenv("MEDXFER_TEST_TRUST_COUNT"))
	ts, err := OpenTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := from; i < from+count; i++ {
		if err := ts.Add(Peer{Fingerprint: fp(i + 10), Alias: "d" + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
}

// #303: a Forget on a file that cannot be read fails, but this process stops trusting the
// device at once: trust ends here even when the file cannot say so.
func TestForgetOnUnreadableFileEndsTrustHere(t *testing.T) {
	path := filepath.Join(t.TempDir(), TrustFile)
	ts, err := OpenTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.Add(Peer{Fingerprint: fp(1), Alias: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"peers":[{"finger`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Forget(fp(1)); err == nil {
		t.Fatal("Forget wrote over a file it cannot read")
	}
	if ts.Contains(fp(1)) {
		t.Fatal("the device is still trusted after Forget")
	}
}

// #303: another process's write is seen even with the same size and date, as an atomic write
// makes a new file.
func TestReplacedFileOfSameSizeAndDateIsSeen(t *testing.T) {
	path := filepath.Join(t.TempDir(), TrustFile)
	daemon, err := OpenTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := daemon.Add(Peer{Fingerprint: fp(1), Alias: "A", AddedAt: time.Unix(1, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	cli, err := OpenTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Forget(fp(1)); err != nil {
		t.Fatal(err)
	}
	if err := cli.Add(Peer{Fingerprint: fp(2), Alias: "B", AddedAt: time.Unix(1, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(path); after.Size() != before.Size() {
		t.Fatalf("sizes %d and %d: the test needs the same size", before.Size(), after.Size())
	}
	if daemon.Contains(fp(1)) || !daemon.Contains(fp(2)) {
		t.Fatal("a replaced file of the same size and date was not read again")
	}
}

// #303: a Forget whose write fails is kept by this process, and written by its next change: the
// device does not come back, in memory nor in the file.
func TestFailedForgetIsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), TrustFile)
	ts, err := OpenTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.Add(Peer{Fingerprint: fp(1), Alias: "A"}); err != nil {
		t.Fatal(err)
	}
	old, tries := renameFile, renameTries
	renameFile, renameTries = func(string, string) error { return os.ErrPermission }, []time.Duration{0}
	_, err = ts.Forget(fp(1))
	renameFile, renameTries = old, tries
	if err == nil {
		t.Fatal("the write was refused, yet Forget succeeded")
	}
	if err := ts.Add(Peer{Fingerprint: fp(2), Alias: "B"}); err != nil { // the next change, as a reconnection's
		t.Fatal(err)
	}
	again, err := OpenTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if ts.Contains(fp(1)) || again.Contains(fp(1)) {
		t.Fatalf("the device forgotten came back: here %v, in the file %v", ts.Contains(fp(1)), again.Contains(fp(1)))
	}
}
