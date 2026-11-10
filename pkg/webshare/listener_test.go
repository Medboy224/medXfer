package webshare

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A guest who typed the address without "https://" (real test of 10 November 2026): sent to
// the page in HTTPS, never told « Client sent an HTTP request to an HTTPS server ».
func TestPlainHTTPRedirected(t *testing.T) {
	s, g, _ := startGuest(t, Options{})
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", s.Port()), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = fmt.Fprintf(c, "GET /s HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n", s.Port())
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("https://127.0.0.1:%d/s", s.Port())
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != want {
		t.Fatalf("plain HTTP: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// HTTPS still works beside it.
	if resp := g.do("GET", "/s", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTPS after: %d", resp.StatusCode)
	}
	// A client that says nothing holds nothing open for long.
	idle, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", s.Port()))
	if err != nil {
		t.Fatal(err)
	}
	idle.Close()
	if resp := g.do("GET", "/s", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTPS after an idle client: %d", resp.StatusCode)
	}
}

// HTTP/1.1 even for a browser that offers HTTP/2: large uploads go faster without Go's HTTP/2
// flow control.
func TestHTTP11Only(t *testing.T) {
	s, _, _ := startGuest(t, Options{})
	cert, _ := x509.ParseCertificate(s.cert.Certificate[0])
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}, ForceAttemptHTTP2: true}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr}).Get(fmt.Sprintf("https://127.0.0.1:%d/s", s.Port()))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Proto != "HTTP/1.1" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("proto %s, %s", resp.Proto, resp.Header.Get("Content-Type"))
	}
}
