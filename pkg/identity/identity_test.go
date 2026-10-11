package identity

import (
	"crypto/ecdsa"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoadOrCreateIsStable(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint != b.Fingerprint {
		t.Fatalf("fingerprint changed between two loads: %s then %s", a.Fingerprint, b.Fingerprint)
	}
	if _, ok := a.Cert.PrivateKey.(*ecdsa.PrivateKey); !ok {
		t.Fatalf("private key is %T, want ECDSA (ID-01)", a.Cert.PrivateKey)
	}
	if a.Fingerprint != FingerprintOf(a.Leaf) {
		t.Fatal("fingerprint is not SHA-256 of the SubjectPublicKeyInfo (ID-02)")
	}
	if runtime.GOOS != "windows" {
		for _, f := range []string{KeyFile, CertFile} {
			st, err := os.Stat(filepath.Join(dir, f))
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm() != 0o600 {
				t.Errorf("%s has mode %v, want 0600 (ID-01)", f, st.Mode().Perm())
			}
		}
	}
}

// A new certificate on the same key keeps the fingerprint (ID-02).
func TestReissuedCertificateKeepsFingerprint(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, CertFile)); err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(a.Leaf.Raw) == string(b.Leaf.Raw) {
		t.Fatal("certificate was not re-issued")
	}
	if a.Fingerprint != b.Fingerprint {
		t.Fatalf("fingerprint changed with the certificate: %s then %s", a.Fingerprint, b.Fingerprint)
	}
}

// Losing the key means a new identity (ID-06), and the old certificate is not reused.
func TestNewKeyIsNewIdentity(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, KeyFile)); err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint == b.Fingerprint {
		t.Fatal("a new key kept the old fingerprint")
	}
}

func TestCorruptKeyIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, KeyFile), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("junk")}), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(dir); err == nil {
		t.Fatal("a corrupt key was accepted (it must never be silently replaced)")
	}
}

func TestFingerprintForms(t *testing.T) {
	id, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fp := id.Fingerprint
	full := fp.String()
	if len(full) != 52 {
		t.Fatalf("full fingerprint %q has %d characters, want 52", full, len(full))
	}
	short := fp.Short()
	if len(short) != 19 || strings.Count(short, "-") != 3 || !strings.HasPrefix(full, strings.ReplaceAll(short, "-", "")) {
		t.Fatalf("short form %q is not the first 16 characters of %q in 4 groups (ID-03)", short, full)
	}
	for _, in := range []string{full, strings.ToLower(full), full[:4] + "-" + full[4:] + " "} {
		got, err := ParseFingerprint(in)
		if err != nil || got != fp {
			t.Errorf("ParseFingerprint(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseFingerprint(short); err == nil {
		t.Error("a short fingerprint parsed as a full one")
	}
}

func TestTrustStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, TrustFile)
	ts, err := OpenTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts.List()) != 0 {
		t.Fatal("a missing trusted.json is not an empty list")
	}
	a, _ := LoadOrCreate(filepath.Join(dir, "a"))
	b, _ := LoadOrCreate(filepath.Join(dir, "b"))
	if err := ts.Add(Peer{Fingerprint: a.Fingerprint, Alias: "phone", LastAddress: "192.168.1.20:18887"}); err != nil {
		t.Fatal(err)
	}
	if err := ts.Add(Peer{Fingerprint: b.Fingerprint, Alias: "laptop", AddedAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}

	// Reopened from disk.
	ts2, err := OpenTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ts2.Contains(a.Fingerprint) || !ts2.Contains(b.Fingerprint) {
		t.Fatal("trusted devices were not persisted")
	}
	if p, ok := ts2.ByAddress("192.168.1.20:18887"); !ok || p.Fingerprint != a.Fingerprint || p.Alias != "phone" {
		t.Fatalf("ByAddress = %+v, %v", p, ok)
	}
	if l := ts2.List(); len(l) != 2 || l[0].Alias != "phone" {
		t.Fatalf("List is not oldest first: %+v", l)
	}
	if p, err := ts2.Find(b.Fingerprint.Short()); err != nil || p.Fingerprint != b.Fingerprint {
		t.Fatalf("Find(short form) = %+v, %v", p, err)
	}
	if _, err := ts2.Find("A"); err == nil {
		t.Error("a 1-character prefix was accepted")
	}

	// Forget (ID-06).
	if ok, err := ts2.Forget(a.Fingerprint); err != nil || !ok {
		t.Fatalf("Forget = %v, %v", ok, err)
	}
	if ok, _ := ts2.Forget(a.Fingerprint); ok {
		t.Error("forgetting twice reported success")
	}
	ts3, _ := OpenTrustStore(path)
	if ts3.Contains(a.Fingerprint) || !ts3.Contains(b.Fingerprint) {
		t.Fatal("Forget was not persisted, or removed the wrong device")
	}
}

// A crash during a write leaves a temporary file next to trusted.json: the old list stays
// intact and readable (ID-04).
func TestTrustStoreSurvivesInterruptedWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, TrustFile)
	ts, _ := OpenTrustStore(path)
	id, _ := LoadOrCreate(filepath.Join(dir, "id"))
	if err := ts.Add(Peer{Fingerprint: id.Fingerprint, Alias: "kept"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, TrustFile+".tmp-123"), []byte(`{"version":1,"peers":[{"finger`), 0o600); err != nil {
		t.Fatal(err)
	}
	ts2, err := OpenTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := ts2.Get(id.Fingerprint); !ok || p.Alias != "kept" {
		t.Fatalf("list after an interrupted write = %+v, %v", p, ok)
	}
}

func TestTrustStoreRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), TrustFile)
	if err := os.WriteFile(path, []byte(`{"version":2,"peers":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenTrustStore(path); err == nil {
		t.Fatal("version 2 was read as version 1")
	}
}
