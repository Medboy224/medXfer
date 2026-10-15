package api

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
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

type route struct{ method, path string }

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

func TestHealthWithoutToken(t *testing.T) {
	_, addr := startGuardServer(t, false)
	if code, _ := guardDo(t, "GET", "http://"+addr+"/health", nil); code != http.StatusOK {
		t.Errorf("/health = %d; want 200 without token (D0-4)", code)
	}
	if code, _ := guardDo(t, "GET", "http://"+addr+"/health", map[string]string{"Host": "evil.example"}); code != http.StatusForbidden {
		t.Errorf("/health under foreign Host = %d; want 403", code)
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
