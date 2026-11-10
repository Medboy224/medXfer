package webshare

import (
	"archive/zip"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/diag"
)

// recorder keeps what the session tells the host and logs, for the tests to read.
type recorder struct {
	mu     sync.Mutex
	events []Event
	logs   []string
}

func (r *recorder) event(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

func (r *recorder) kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		out = append(out, e.Kind)
	}
	return out
}

func (r *recorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, l := range r.logs {
		b.WriteString(l + "\n")
	}
	for _, e := range r.events {
		fmt.Fprintf(&b, "%s %v\n", e.Kind, e.Data)
	}
	return b.String()
}

// guest is a browser of the tests: it trusts this session's certificate only.
type guest struct {
	t      *testing.T
	s      *Session
	client *http.Client
	base   string
}

func startGuest(t *testing.T, opts Options) (*Session, *guest, *recorder) {
	t.Helper()
	rec := &recorder{}
	opts.Addr = "127.0.0.1"
	if opts.Port == 0 {
		opts.Port = -1
	}
	opts.Events = rec.event
	opts.Logf = rec.logf
	s, err := start(opts, time.Now, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	cert, err := x509.ParseCertificate(s.srv.TLSConfig.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	t.Cleanup(tr.CloseIdleConnections)
	return s, &guest{t: t, s: s, client: &http.Client{Transport: tr}, base: fmt.Sprintf("https://127.0.0.1:%d", s.Port())}, rec
}

// do sends a request; token "" sends no Authorization.
func (g *guest) do(method, path, token string, body io.Reader, header ...string) *http.Response {
	g.t.Helper()
	req, err := http.NewRequest(method, g.base+path, body)
	if err != nil {
		g.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := g.client.Do(req)
	if err != nil {
		g.t.Fatal(err)
	}
	g.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (g *guest) unlock(pin string) (int, map[string]any) {
	g.t.Helper()
	resp := g.do("POST", "/api/unlock", "", strings.NewReader(`{"pin":"`+pin+`"}`))
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func readAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func waitDone(t *testing.T, s *Session) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the session did not end")
	}
}

func wrongPIN(s *Session) string {
	if s.PIN() == "000000" {
		return "000001"
	}
	return "000000"
}

// WEB-02, bible §11.2: the QR code's address carries the token in its fragment; a 128-bit token
// and a 6-digit PIN, drawn anew for each session.
func TestSessionSecrets(t *testing.T) {
	a, _, _ := startGuest(t, Options{})
	b, _, _ := startGuest(t, Options{})
	if !regexp.MustCompile(`^https://127\.0\.0\.1:\d+/s#t=[0-9a-f]{32}$`).MatchString(a.URL()) || !strings.HasSuffix(a.URL(), a.Token()) {
		t.Fatalf("URL %q", a.URL())
	}
	if !regexp.MustCompile(`^\d{6}$`).MatchString(a.PIN()) {
		t.Fatalf("PIN %q", a.PIN())
	}
	if a.Token() == b.Token() || a.Fingerprint() == b.Fingerprint() {
		t.Fatal("two sessions share a token or a certificate")
	}
}

// Bible §11.2, decision D6: a self-signed ECDSA P-256 certificate per session, for the address
// served, its SHA-256 fingerprint given to the host as browsers show it.
func TestSessionCertificate(t *testing.T) {
	s, _, _ := startGuest(t, Options{})
	der := s.srv.TLSConfig.Certificates[0].Certificate[0]
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		t.Fatalf("key %T", cert.PublicKey)
	}
	if len(cert.IPAddresses) != 1 || !cert.IPAddresses[0].Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("addresses %v", cert.IPAddresses)
	}
	sum := sha256.Sum256(der)
	if s.Fingerprint() != fingerprint(sum[:]) || len(s.Fingerprint()) != 95 {
		t.Fatalf("fingerprint %q", s.Fingerprint())
	}
}

// WEB-05: the policy and the headers on every response, the page's script and style served as
// files; WEB-02: no cookie, ever.
func TestHeadersEverywhere(t *testing.T) {
	s, g, _ := startGuest(t, Options{})
	for _, path := range []string{"/s", "/s/app.js", "/s/app.css", "/api/items", "/api/session", "/nothing"} {
		resp := g.do("GET", path, "", nil)
		h := resp.Header
		if h.Get("Content-Security-Policy") != CSP || h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: headers %v", path, h)
		}
		if strings.HasPrefix(path, "/api/") && h.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control %q", path, h.Get("Cache-Control"))
		}
		if len(resp.Cookies()) > 0 {
			t.Errorf("%s: cookies %v", path, resp.Cookies())
		}
	}
	page := string(readAll(t, g.do("GET", "/s", "", nil).Body))
	if !strings.Contains(page, `src="/s/app.js"`) || strings.Contains(page, "<script>") || strings.Contains(page, "<style") {
		t.Fatalf("the page has inline code:\n%s", page)
	}
	if !strings.Contains(CSP, "script-src 'self'") || strings.Contains(CSP, "unsafe") {
		t.Fatalf("CSP %q", CSP)
	}
	_ = s
}

// WEB-02: the token is accepted in Authorization only; never in the URL, never in a cookie.
func TestBearerOnly(t *testing.T) {
	s, g, _ := startGuest(t, Options{})
	for name, resp := range map[string]*http.Response{
		"none":     g.do("GET", "/api/items", "", nil),
		"query":    g.do("GET", "/api/items?token="+s.Token(), "", nil),
		"cookie":   g.do("GET", "/api/items", "", nil, "Cookie", "medxfer_token="+s.Token()),
		"wrong":    g.do("GET", "/api/items", strings.Repeat("0", 32), nil),
		"x-header": g.do("GET", "/api/items", "", nil, "X-Share-Token", s.Token()),
	} {
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: %d", name, resp.StatusCode)
		}
	}
	if resp := g.do("GET", "/api/items", s.Token(), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("the token: %d", resp.StatusCode)
	}
}

// WEB-03, WEB-10: the PIN gives the token; MaxPINTries wrong ones per session end it, and the
// host is told; a right one costs no try.
func TestUnlockTries(t *testing.T) {
	s, g, rec := startGuest(t, Options{})
	for left := MaxPINTries - 1; left >= 2; left-- {
		code, m := g.unlock(wrongPIN(s))
		if code != http.StatusUnauthorized || m["error"] != "invalid_pin" || m["tries_left"] != float64(left) {
			t.Fatalf("wrong PIN: %d %v, want %d left", code, m, left)
		}
	}
	code, m := g.unlock(" " + s.PIN() + " ")
	if code != http.StatusOK || m["token"] != s.Token() {
		t.Fatalf("right PIN: %d %v", code, m)
	}
	if code, m = g.unlock(wrongPIN(s)); code != http.StatusUnauthorized || m["tries_left"] != float64(1) {
		t.Fatalf("after the right PIN: %d %v", code, m)
	}
	if code, m = g.unlock(wrongPIN(s)); code != http.StatusGone || m["error"] != "session_closed" {
		t.Fatalf("the last try: %d %v", code, m)
	}
	waitDone(t, s)
	if s.Closed() != ClosedPINAttempts || !strings.Contains(rec.text(), "session_closed map[reason:pin_attempts]") {
		t.Fatalf("closed %q, events %v", s.Closed(), rec.kinds())
	}
}

// WEB-03: tries in parallel are counted before the comparison: never more than MaxPINTries.
func TestUnlockParallel(t *testing.T) {
	s, g, _ := startGuest(t, Options{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[int]int{}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", g.base+"/api/unlock", strings.NewReader(`{"pin":"`+wrongPIN(s)+`"}`))
			resp, err := g.client.Do(req)
			code := 0 // refused at the door: the session already ended
			if err == nil {
				code = resp.StatusCode
				resp.Body.Close()
			}
			mu.Lock()
			codes[code]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if codes[http.StatusUnauthorized] > MaxPINTries-1 || codes[http.StatusOK] != 0 {
		t.Fatalf("answers %v", codes)
	}
	waitDone(t, s)
}

// WEB-10: without a transfer, the session ends at its expiry; a transfer running puts it off,
// and its end gives the guest the full TTL again.
func TestSessionExpiry(t *testing.T) {
	s, _, rec := startGuest(t, Options{TTL: 150 * time.Millisecond})
	waitDone(t, s)
	if s.Closed() != ClosedExpired || !strings.Contains(rec.text(), "reason:expired") {
		t.Fatalf("closed %q, events %v", s.Closed(), rec.kinds())
	}

	s, _, _ = startGuest(t, Options{TTL: 150 * time.Millisecond})
	s.mu.Lock()
	s.recheck = 50 * time.Millisecond
	s.mu.Unlock()
	s.begin()
	time.Sleep(400 * time.Millisecond)
	if s.Closed() != "" {
		t.Fatal("expired during a transfer")
	}
	s.finish()
	if left := time.Until(s.Expires()); left < 100*time.Millisecond {
		t.Fatalf("after the transfer, %v left", left)
	}
	waitDone(t, s)
}

// WEB-01, WEB-10: closed, the session's listener is gone, and its routes answer nothing more.
func TestCloseRemovesListener(t *testing.T) {
	s, g, rec := startGuest(t, Options{})
	addr := fmt.Sprintf("127.0.0.1:%d", s.Port())
	s.Close()
	waitDone(t, s)
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			break
		}
		c.Close()
		if time.Now().After(deadline) {
			t.Fatal("the listener is still open")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(rec.text(), "reason:stopped") {
		t.Fatalf("events %v", rec.kinds())
	}
	_ = g
}

// Decision D5: the port asked when it is free, another when it is busy.
func TestPortTakenFallsBack(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	s, _, _ := startGuest(t, Options{Port: port})
	if s.Port() == port || s.Port() == 0 {
		t.Fatalf("port %d with %d busy", s.Port(), port)
	}
}

// WEB-01: the session serves one interface's address, never all of them.
func TestStartNeedsAnAddress(t *testing.T) {
	for _, addr := range []string{"", "0.0.0.0", "::", "not an address"} {
		if s, err := Start(Options{Addr: addr}); err == nil {
			s.Close()
			t.Errorf("%q accepted", addr)
		}
	}
}

// WEB-08: bounded headers and bodies.
func TestLimits(t *testing.T) {
	s, g, _ := startGuest(t, Options{})
	if s.srv.ReadHeaderTimeout <= 0 || s.srv.MaxHeaderBytes != 16<<10 {
		t.Fatalf("server %v %d", s.srv.ReadHeaderTimeout, s.srv.MaxHeaderBytes)
	}
	big := `{"pin":"` + strings.Repeat("1", 64<<10) + `"}`
	if resp := g.do("POST", "/api/unlock", "", strings.NewReader(big)); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("a big body: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", g.base+"/s", nil)
	req.Header.Set("X-Big", strings.Repeat("a", 64<<10))
	if resp, err := g.client.Do(req); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
			t.Fatalf("big headers: %d", resp.StatusCode)
		}
	}
}

// WEB-07: what the host offers, named by ID: a file (resumable by Range) and a folder in zip.
func TestItemsByID(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "Relevé été.pdf")
	data := bytes.Repeat([]byte("medXfer "), 4096)
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(dir, "Photos")
	if err := os.MkdirAll(filepath.Join(folder, "2026"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(folder, "a.jpg"), []byte("aaaa"), 0o644)
	_ = os.WriteFile(filepath.Join(folder, "2026", "b.jpg"), []byte("bbbbbb"), 0o644)
	s, g, rec := startGuest(t, Options{Paths: []string{file, folder}})

	var items []Item
	if err := json.NewDecoder(g.do("GET", "/api/items", s.Token(), nil).Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Name != "Relevé été.pdf" || items[0].Size != int64(len(data)) || items[0].Folder ||
		items[1].Name != "Photos" || items[1].Size != 10 || !items[1].Folder {
		t.Fatalf("items %+v", items)
	}

	resp := g.do("GET", "/api/items/"+items[0].ID, s.Token(), nil)
	if got := readAll(t, resp.Body); !bytes.Equal(got, data) {
		t.Fatalf("file: %d bytes", len(got))
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, "filename*=utf-8''Relev%C3%A9%20%C3%A9t%C3%A9.pdf") {
		t.Fatalf("Content-Disposition %q", cd)
	}
	resp = g.do("GET", "/api/items/"+items[0].ID, s.Token(), nil, "Range", "bytes=100-")
	if got := readAll(t, resp.Body); resp.StatusCode != http.StatusPartialContent || !bytes.Equal(got, data[100:]) {
		t.Fatalf("resumed: %d, %d bytes", resp.StatusCode, len(got))
	}

	zipped := readAll(t, g.do("GET", "/api/items/"+items[1].ID, s.Token(), nil).Body)
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "Photos/2026/b.jpg,Photos/a.jpg" {
		t.Fatalf("zip %v", names)
	}

	for _, id := range []string{"9", "../etc", "Relevé été.pdf"} {
		if resp := g.do("GET", "/api/items/"+id, s.Token(), nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("item %q: %d", id, resp.StatusCode)
		}
	}
	if !strings.Contains(strings.Join(rec.kinds(), ","), "download_started,download_done") {
		t.Fatalf("events %v", rec.kinds())
	}
}

// WEB-09: with PinIP, the first address authenticated is the only one let in.
func TestPinIP(t *testing.T) {
	s, _, _ := startGuest(t, Options{PinIP: true})
	h := s.handler()
	from := func(ip string) int {
		r := httptest.NewRequest("GET", "/api/items", nil)
		r.RemoteAddr = ip + ":50000"
		r.Header.Set("Authorization", "Bearer "+s.Token())
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if a, b, c := from("192.0.2.10"), from("192.0.2.11"), from("192.0.2.10"); a != 200 || b != 403 || c != 200 {
		t.Fatalf("answers %d %d %d", a, b, c)
	}
	s2, _, _ := startGuest(t, Options{})
	h = s2.handler()
	s = s2
	if a, b := from("192.0.2.10"), from("192.0.2.11"); a != 200 || b != 200 {
		t.Fatalf("without PinIP: %d %d", a, b)
	}
}

// WEB-04: neither the token nor the PIN, nor a PIN tried, in the logs or the events; the
// process's redactor masks both.
func TestNoSecretsOutside(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(file, []byte("x"), 0o644)
	s, g, rec := startGuest(t, Options{Paths: []string{file}})
	g.unlock(wrongPIN(s))
	g.unlock(s.PIN())
	readAll(t, g.do("GET", "/api/items/0", s.Token(), nil).Body)
	// A browser refusing the certificate: the server logs the handshake, nothing more.
	if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", s.Port())); err == nil {
		_, _ = c.Write([]byte("GET /s?t=" + s.Token() + " HTTP/1.1\r\nHost: x\r\n\r\n"))
		_, _ = io.ReadAll(c)
		c.Close()
	}
	s.Close()
	waitDone(t, s)
	text := rec.text()
	for _, secret := range []string{s.Token(), s.PIN(), wrongPIN(s)} {
		if strings.Contains(text, secret) {
			t.Fatalf("%q in the logs or events:\n%s", secret, text)
		}
	}
	if got := diag.Secrets.Redact("t=" + s.Token() + " pin " + s.PIN()); strings.Contains(got, s.Token()) || strings.Contains(got, s.PIN()) {
		t.Fatalf("not masked: %s", got)
	}
}
