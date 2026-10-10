//go:build !devui

package api

import "net/http"

// DashboardAvailable reports whether this build serves the web dashboard (DEV-19).
const DashboardAvailable = false

func (s *DaemonServer) registerDevUIRoutes(*http.ServeMux) {}

func (s *DaemonServer) serveDashboard(w http.ResponseWriter) {
	http.Error(w, "The medXfer web dashboard is a development tool, not part of this build "+
		"(build with -tags devui). Web Share, if enabled, is at /share.", http.StatusNotFound)
}
