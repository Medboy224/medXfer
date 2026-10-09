package api

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// startGuardServer runs a real server with a RANDOM control token (not the shared test token).
func startGuardServer(t *testing.T, disableControl bool) (*DaemonServer, string) {
	t.Helper()
	t.Setenv("MEDXFER_CONTROL_TOKEN", "")
	s := NewDaemonServer(0, t.TempDir(), "GuardHost")
	if s.ControlToken() == testControlToken || len(s.ControlToken()) != 64 {
		t.Fatalf("expected a fresh random 256-bit token, got %q", s.ControlToken())
	}
	if disableControl {
		s.DisableControlSurface()
	}
	ln, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(s.Stop)
	return s, fmt.Sprintf("127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)
}

func guardDo(t *testing.T, method, url string, hdr map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

var controlRoutes = []struct{ method, path string }{
	{"GET", "/status"},
	{"GET", "/api/browse"},
	{"POST", "/api/upload"},
	{"GET", "/api/fs/list"},
	{"POST", "/api/fs/mkdir?dir=x&name=y"},
	{"GET", "/ws"},
}

func TestControlRoutesRequireToken(t *testing.T) {
	s, addr := startGuardServer(t, false)
	for _, r := range controlRoutes {
		if code, _ := guardDo(t, r.method, "http://"+addr+r.path, nil); code != http.StatusUnauthorized {
			t.Errorf("%s %s without token = %d; want 401", r.method, r.path, code)
		}
		bad := map[string]string{"Authorization": "Bearer " + strings.Repeat("0", 64)}
		if code, _ := guardDo(t, r.method, "http://"+addr+r.path, bad); code != http.StatusUnauthorized {
			t.Errorf("%s %s with wrong token = %d; want 401", r.method, r.path, code)
		}
		// The token must not be accepted in the URL (history, logs, Referer).
		sep := "?"
		if strings.Contains(r.path, "?") {
			sep = "&"
		}
		if code, _ := guardDo(t, r.method, "http://"+addr+r.path+sep+"token="+s.ControlToken(), nil); code != http.StatusUnauthorized {
			t.Errorf("%s %s with token in URL = %d; want 401", r.method, r.path, code)
		}
	}
	good := map[string]string{"Authorization": "Bearer " + s.ControlToken()}
	if code, body := guardDo(t, "GET", "http://"+addr+"/status", good); code != http.StatusOK || !strings.Contains(body, "web_share_pin") {
		t.Fatalf("/status with token = %d %s; want 200 with the status", code, body)
	}
	if code, _ := guardDo(t, "GET", "http://"+addr+"/api/fs/list", good); code != http.StatusOK {
		t.Fatalf("/api/fs/list with token = %d; want 200", code)
	}
}

// Anti DNS rebinding (control 5) and anti CSWSH (control 4), even with the right token.
func TestControlRejectsForeignHostAndOrigin(t *testing.T) {
	s, addr := startGuardServer(t, false)
	tok := "Bearer " + s.ControlToken()
	port := addr[strings.LastIndex(addr, ":")+1:]
	cases := []map[string]string{
		{"Authorization": tok, "Host": "evil.example"},
		{"Authorization": tok, "Host": "evil.example:" + port},
		{"Authorization": tok, "Host": "127.0.0.1:1"}, // wrong port
		{"Authorization": tok, "Origin": "https://evil.example"},
		{"Authorization": tok, "Origin": "http://evil.example:" + port},
		{"Authorization": tok, "Origin": "null"},
	}
	for _, h := range cases {
		if code, body := guardDo(t, "GET", "http://"+addr+"/status", h); code != http.StatusForbidden || strings.Contains(body, "web_share_pin") {
			t.Errorf("/status with %v = %d; want 403 without status", h, code)
		}
	}
	// The dashboard's own origin is accepted.
	for _, o := range []string{"http://" + addr, "http://localhost:" + port} {
		if code, _ := guardDo(t, "GET", "http://"+addr+"/status", map[string]string{"Authorization": tok, "Origin": o}); code != http.StatusOK {
			t.Errorf("/status with Origin %s = %d; want 200", o, code)
		}
	}
	// A browser page from another site opening the WebSocket is refused.
	hdr := http.Header{"Origin": {"https://evil.example"}}
	hdr.Set("Sec-WebSocket-Protocol", "medxfer.v1, token."+s.ControlToken())
	if c, resp, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws", hdr); err == nil {
		c.Close()
		t.Fatal("cross-site WebSocket accepted")
	} else if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site WebSocket: %v; want 403", err)
	}
}

func TestControlRejectsRemotePeer(t *testing.T) {
	s := NewDaemonServer(0, t.TempDir(), "RemoteGuard")
	called := false
	h := s.control(func(http.ResponseWriter, *http.Request) { called = true })
	req := httptest.NewRequest("GET", "/status", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Host = fmt.Sprintf("127.0.0.1:%d", s.currentHTTPPort())
	req.Header.Set("Authorization", "Bearer "+s.ControlToken())
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusForbidden || called {
		t.Fatalf("remote peer with token = %d (handler called: %v); want 403", w.Code, called)
	}
}

// Browsers cannot set headers on a WebSocket: the token travels as a subprotocol.
func TestWebSocketTokenForms(t *testing.T) {
	s, addr := startGuardServer(t, false)

	c, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws",
		http.Header{"Authorization": {"Bearer " + s.ControlToken()}})
	if err != nil {
		t.Fatalf("WebSocket with Authorization header: %v", err)
	}
	c.Close()

	browser := websocket.Dialer{Subprotocols: []string{"medxfer.v1", "token." + s.ControlToken()}}
	c, resp, err := browser.Dial("ws://"+addr+"/ws", http.Header{"Origin": {"http://" + addr}})
	if err != nil {
		t.Fatalf("WebSocket with token subprotocol: %v", err)
	}
	defer c.Close()
	// The server must select medxfer.v1, never echo the token back.
	if got := resp.Header.Get("Sec-WebSocket-Protocol"); got != "medxfer.v1" {
		t.Fatalf("selected subprotocol = %q; want medxfer.v1", got)
	}
}

func TestDashboardTokenInjection(t *testing.T) {
	if strings.Count(IndexHTML, controlTokenPlaceholder) != 1 {
		t.Fatal("IndexHTML must contain the token placeholder exactly once")
	}
	s, addr := startGuardServer(t, false)
	if strings.Contains(IndexHTML, s.ControlToken()) {
		t.Fatal("IndexHTML constant contains a real token")
	}

	code, body := guardDo(t, "GET", "http://"+addr+"/", nil)
	if code != http.StatusOK || !strings.Contains(body, s.ControlToken()) || strings.Contains(body, controlTokenPlaceholder) {
		t.Fatalf("dashboard = %d; want 200 with the real token injected", code)
	}
	// Under a foreign Host (DNS rebinding), the page and its token are not served.
	code, body = guardDo(t, "GET", "http://"+addr+"/", map[string]string{"Host": "evil.example"})
	if code != http.StatusForbidden || strings.Contains(body, s.ControlToken()) {
		t.Fatalf("dashboard under foreign Host = %d; want 403 without token", code)
	}
}

func TestHealthAndMkdirMethod(t *testing.T) {
	s, addr := startGuardServer(t, false)
	if code, _ := guardDo(t, "GET", "http://"+addr+"/health", nil); code != http.StatusOK {
		t.Errorf("/health = %d; want 200 without token (D0-4)", code)
	}
	if code, _ := guardDo(t, "GET", "http://"+addr+"/health", map[string]string{"Host": "evil.example"}); code != http.StatusForbidden {
		t.Errorf("/health under foreign Host = %d; want 403", code)
	}
	good := map[string]string{"Authorization": "Bearer " + s.ControlToken()}
	if code, _ := guardDo(t, "GET", "http://"+addr+"/api/fs/mkdir?dir=x&name=y", good); code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/fs/mkdir = %d; want 405 (no side effect on GET)", code)
	}
}

func TestDisableControlSurface(t *testing.T) {
	s, addr := startGuardServer(t, true)
	good := map[string]string{"Authorization": "Bearer " + s.ControlToken()}
	for _, r := range controlRoutes {
		if code, _ := guardDo(t, r.method, "http://"+addr+r.path, good); code != http.StatusNotFound {
			t.Errorf("share-only %s %s = %d; want 404", r.method, r.path, code)
		}
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect || resp.Header.Get("Location") != "/share" {
		t.Fatalf("share-only / = %d → %q; want redirect to /share", resp.StatusCode, resp.Header.Get("Location"))
	}
}
