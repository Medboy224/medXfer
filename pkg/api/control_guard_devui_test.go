//go:build devui

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

var devUIRoutes = []route{
	{"GET", "/api/browse"},
	{"POST", "/api/upload"},
	{"GET", "/api/fs/list"},
	{"POST", "/api/fs/mkdir?dir=x&name=y"},
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

func TestDevUIRoutesWithToken(t *testing.T) {
	s, addr := startGuardServer(t, false)
	good := map[string]string{"Authorization": "Bearer " + s.ControlToken()}
	if code, _ := guardDo(t, "GET", "http://"+addr+"/api/fs/mkdir?dir=x&name=y", good); code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/fs/mkdir = %d; want 405 (no side effect on GET)", code)
	}
	if code, _ := guardDo(t, "GET", "http://"+addr+"/api/fs/list", good); code != http.StatusOK {
		t.Fatalf("/api/fs/list with token = %d; want 200", code)
	}
}

// controlRoutes are the routes behind the control guard; devUIRoutes is empty in builds
// without the dashboard (DEV-19).
var controlRoutes = append([]route{{"GET", "/status"}, {"GET", "/ws"}}, devUIRoutes...)

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

func TestStatusRefusedFromRemote(t *testing.T) {
	server := NewDaemonServer(0, t.TempDir(), "TestStatusHost")
	server.SetWebShareEnabled(true)

	// Remote LAN client: 403 and no secret in the body
	reqRemote := httptest.NewRequest("GET", "/status", nil)
	reqRemote.RemoteAddr = "10.0.0.5:1234"
	wRemote := httptest.NewRecorder()
	server.handleHTTPStatus(wRemote, reqRemote)
	if wRemote.Code != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden for remote client on /status, got %d", wRemote.Code)
	}
	for _, secret := range []string{"web_share_pin", "web_share_token", "pairing_code", "portal_url"} {
		if strings.Contains(wRemote.Body.String(), secret) {
			t.Fatalf("Remote /status response leaks %q: %s", secret, wRemote.Body.String())
		}
	}

	// Localhost client: still served
	reqLocal := httptest.NewRequest("GET", "/status", nil)
	reqLocal.RemoteAddr = "127.0.0.1:1234"
	wLocal := httptest.NewRecorder()
	server.handleHTTPStatus(wLocal, reqLocal)
	if wLocal.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for localhost client on /status, got %d", wLocal.Code)
	}
	if !strings.Contains(wLocal.Body.String(), "web_share_pin") {
		t.Fatalf("Expected local /status to include web_share_pin, got %s", wLocal.Body.String())
	}
}
