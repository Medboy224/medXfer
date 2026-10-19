package identity

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Audit §3.5 (#156): a certificate not valid yet (the clock went back since it was issued)
// is issued again; the fingerprint, which is the key's, stays.
func TestCertificateFromTheFutureIsReissued(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	key, err := loadKey(filepath.Join(dir, KeyFile))
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(48 * time.Hour)
	tmpl := x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "medxfer"},
		NotBefore: future, NotAfter: future.Add(certValidity), BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, CertFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b.Leaf.NotBefore.After(time.Now()) {
		t.Fatalf("a certificate valid from %v was kept", b.Leaf.NotBefore)
	}
	if a.Fingerprint != b.Fingerprint {
		t.Fatal("the fingerprint changed with the certificate")
	}
}

// Audit §3.5 (#156): replacing a file is tried again a moment later when the system holds it
// (an antivirus under Windows), as pkg/store does.
func TestAtomicWriteRetriesTheRename(t *testing.T) {
	fails := 2
	old := renameFile
	renameFile = func(from, to string) error {
		if fails > 0 {
			fails--
			return errors.New("the file is held by another process")
		}
		return os.Rename(from, to)
	}
	t.Cleanup(func() { renameFile = old })
	path := filepath.Join(t.TempDir(), "trusted.json")
	if err := writeFileAtomic(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("a rename refused twice was not tried again: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "{}" {
		t.Fatalf("content %q", b)
	}
}
