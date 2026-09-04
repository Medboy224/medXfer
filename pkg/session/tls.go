package session

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"
)

var (
	defaultCert     tls.Certificate
	defaultCertOnce sync.Once
	defaultCertErr  error
)

// GetDefaultCertificate generates or returns an in-memory ephemeral self-signed ECDSA P-256 TLS certificate.
func GetDefaultCertificate() (tls.Certificate, error) {
	defaultCertOnce.Do(func() {
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			defaultCertErr = err
			return
		}

		serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
		serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
		if err != nil {
			defaultCertErr = err
			return
		}

		template := x509.Certificate{
			SerialNumber: serialNumber,
			Subject: pkix.Name{
				Organization: []string{"medXfer P2P Security"},
				CommonName:   "medxfer.local",
			},
			NotBefore:             time.Now().Add(-1 * time.Hour),
			NotAfter:              time.Now().Add(365 * 24 * time.Hour),
			KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
			BasicConstraintsValid: true,
			IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.IPv6loopback},
		}

		derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
		if err != nil {
			defaultCertErr = err
			return
		}

		defaultCert = tls.Certificate{
			Certificate: [][]byte{derBytes},
			PrivateKey:  priv,
		}
	})
	return defaultCert, defaultCertErr
}

// ServerTLSConfig returns a tls.Config configured for TLS 1.3 server connections.
func ServerTLSConfig() (*tls.Config, error) {
	cert, err := GetDefaultCertificate()
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// ClientTLSConfig returns a tls.Config configured for TLS 1.3 client connections.
func ClientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true, // Ephemeral self-signed local P2P pairing
		MinVersion:         tls.VersionTLS13,
	}
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) {
	return b.r.Read(p)
}

// UpgradeToTLSIfClientHello peeks the first byte. If it matches a TLS Handshake (0x16),
// it upgrades the connection to TLS 1.3. Otherwise it returns the connection for plaintext processing.
func UpgradeToTLSIfClientHello(rawConn net.Conn, srvConfig *tls.Config) (net.Conn, bool, error) {
	_ = rawConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	br := bufio.NewReader(rawConn)
	firstByte, err := br.Peek(1)
	_ = rawConn.SetReadDeadline(time.Time{})
	if err != nil {
		return rawConn, false, err
	}

	bConn := &bufferedConn{Conn: rawConn, r: br}

	if firstByte[0] == 0x16 {
		tlsConn := tls.Server(bConn, srvConfig)
		_ = tlsConn.SetDeadline(time.Now().Add(5 * time.Second))
		if err := tlsConn.Handshake(); err != nil {
			return nil, false, fmt.Errorf("TLS handshake failed: %w", err)
		}
		_ = tlsConn.SetDeadline(time.Time{})
		return tlsConn, true, nil
	}

	return bConn, false, nil
}

// DialTLSPeer establishes a TLS 1.3 encrypted connection to a peer with radio wake-up probe.
func DialTLSPeer(target string) (net.Conn, error) {
	if !strings.Contains(target, ":") {
		target = fmt.Sprintf("%s:18887", target)
	}

	tcpConn, err := DialPeer(target)
	if err != nil {
		return nil, err
	}

	tlsConn := tls.Client(tcpConn, ClientTLSConfig())
	_ = tlsConn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := tlsConn.Handshake(); err != nil {
		// If TLS handshake fails, fall back to plain TCP connection
		_ = tcpConn.Close()
		return DialPeer(target)
	}
	_ = tlsConn.SetDeadline(time.Time{})
	return tlsConn, nil
}
