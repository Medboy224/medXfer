//go:build !devui

package api

import (
	"net/http"
	"strings"
	"testing"
)

var devUIRoutes []route

// DEV-19: without the devui tag there is no dashboard page, no file picker, no upload drop
// zone and no folder browser, even with a valid control token.
func TestDashboardAbsentWithoutDevUI(t *testing.T) {
	s, addr := startGuardServer(t, false)
	code, body := guardDo(t, "GET", "http://"+addr+"/", nil)
	if code != http.StatusNotFound || strings.Contains(body, s.ControlToken()) || !strings.Contains(body, "devui") {
		t.Fatalf("GET / = %d %q; want 404 explaining the devui build, without token", code, body)
	}
	good := map[string]string{"Authorization": "Bearer " + s.ControlToken()}
	for _, r := range []route{{"GET", "/api/browse"}, {"POST", "/api/upload"}, {"GET", "/api/fs/list"}, {"POST", "/api/fs/mkdir?dir=x&name=y"}} {
		if code, _ := guardDo(t, r.method, "http://"+addr+r.path, good); code != http.StatusNotFound {
			t.Errorf("%s %s = %d; want 404 in a build without devui", r.method, r.path, code)
		}
	}
}
