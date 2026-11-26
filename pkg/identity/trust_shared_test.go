package identity

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
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
