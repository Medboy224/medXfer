package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// File names inside the configuration directory.
const (
	KeyFile   = "identity.key"
	CertFile  = "identity.crt"
	TrustFile = "trusted.json"
)

// certValidity only matters to TLS stacks that check dates; trust comes from the pinned key.
const certValidity = 10 * 365 * 24 * time.Hour

// Identity is this device's key pair and self-signed certificate.
type Identity struct {
	Cert        tls.Certificate
	Leaf        *x509.Certificate
	Fingerprint Fingerprint
}

// LoadOrCreate returns the identity stored in dir, creating it on first use (ID-01): an ECDSA
// P-256 key (identity.key, PKCS#8 PEM, mode 0600) and a self-signed certificate (identity.crt).
// A missing, unreadable or expired certificate is re-issued from the same key, so the
// fingerprint does not change. A missing key means a new identity (ID-06: a reinstall needs a
// new pairing).
func LoadOrCreate(dir string) (*Identity, error) {
	keyPath, certPath := filepath.Join(dir, KeyFile), filepath.Join(dir, CertFile)
	key, err := loadKey(keyPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
			return nil, err
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return nil, err
		}
		if err := writeFileAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
			return nil, fmt.Errorf("saving identity key: %w", err)
		}
		_ = os.Remove(certPath) // a certificate of an older key must not be reused
	case err != nil:
		return nil, err
	}

	leaf, err := loadCert(certPath, key)
	if err != nil {
		if leaf, err = issueCert(key); err != nil {
			return nil, err
		}
		if err := writeFileAtomic(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw}), 0o600); err != nil {
			return nil, fmt.Errorf("saving identity certificate: %w", err)
		}
	}
	return &Identity{
		Cert:        tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: key, Leaf: leaf},
		Leaf:        leaf,
		Fingerprint: FingerprintOf(leaf),
	}, nil
}

func loadKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%s: not a PEM private key", path)
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	key, ok := k.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%s: not an ECDSA P-256 key", path)
	}
	return key, nil
}

// loadCert returns the stored certificate if it is valid for some time yet and belongs to key.
func loadCert(path string, key *ecdsa.PrivateKey) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("not a PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		return nil, errors.New("certificate of another key")
	}
	if time.Now().Add(30 * 24 * time.Hour).After(cert.NotAfter) {
		return nil, errors.New("certificate expires soon")
	}
	if time.Now().Before(cert.NotBefore) { // the clock went back since it was issued (#156)
		return nil, errors.New("certificate not valid yet")
	}
	return cert, nil
}

func issueCert(key *ecdsa.PrivateKey) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "medxfer"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}
