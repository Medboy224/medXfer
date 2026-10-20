package api

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
)

// startGuardServer runs a real server on a random port.
func startGuardServer(t *testing.T) (*DaemonServer, string) {
	t.Helper()
	s := NewDaemonServer(0, t.TempDir(), "GuardHost")
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
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

type route struct{ method, path string }

func TestHealthWithoutToken(t *testing.T) {
	_, addr := startGuardServer(t)
	if code, _ := guardDo(t, "GET", "http://"+addr+"/health", nil); code != http.StatusOK {
		t.Errorf("/health = %d; want 200 without token (D0-4)", code)
	}
	if code, _ := guardDo(t, "GET", "http://"+addr+"/health", map[string]string{"Host": "evil.example"}); code != http.StatusForbidden {
		t.Errorf("/health under foreign Host = %d; want 403", code)
	}
}

// API-01, DEV-19-R: the web dashboard and its control surface were removed at gate G7: no
// /status, no control WebSocket, no file picker, upload or folder browser; "/" leads to the
// Web Share portal.
func TestNoDashboardNorControlSurface(t *testing.T) {
	_, addr := startGuardServer(t)
	for _, r := range []route{
		{"GET", "/status"}, {"GET", "/ws"}, {"GET", "/api/browse"}, {"POST", "/api/upload"},
		{"GET", "/api/fs/list"}, {"POST", "/api/fs/mkdir?dir=x&name=y"},
	} {
		if code, _ := guardDo(t, r.method, "http://"+addr+r.path, nil); code != http.StatusNotFound {
			t.Errorf("%s %s = %d; want 404", r.method, r.path, code)
		}
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect || resp.Header.Get("Location") != "/share" {
		t.Fatalf("GET / = %d → %q; want a redirect to /share", resp.StatusCode, resp.Header.Get("Location"))
	}
}
