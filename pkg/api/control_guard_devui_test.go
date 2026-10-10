//go:build devui

package api

import (
	"net/http"
	"strings"
	"testing"
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
