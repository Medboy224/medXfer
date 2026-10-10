//go:build devui

package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestFSEndpoints(t *testing.T) {
	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "SubFolder")
	_ = os.MkdirAll(subDir, 0755)

	server := NewDaemonServer(0, tempDir, "FSTestDevice")
	ln, err := server.Listen(0)
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Stop()

	// 1. Test /api/fs/list
	resp, err := controlGet(fmt.Sprintf("http://127.0.0.1:%d/api/fs/list?dir=%s", port, url.QueryEscape(tempDir)))
	if err != nil {
		t.Fatalf("Failed to GET /api/fs/list: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}
	var fsResp FSListResponse
	if err := json.NewDecoder(resp.Body).Decode(&fsResp); err != nil {
		t.Fatalf("Failed decoding FSListResponse: %v", err)
	}
	if len(fsResp.Dirs) != 1 || fsResp.Dirs[0].Name != "SubFolder" {
		t.Fatalf("Expected SubFolder in dirs, got %+v", fsResp.Dirs)
	}

	// 2. Test /api/fs/mkdir
	mkdirResp, err := controlPost(fmt.Sprintf("http://127.0.0.1:%d/api/fs/mkdir?dir=%s&name=NewTestDir", port, url.QueryEscape(tempDir)))
	if err != nil {
		t.Fatalf("Failed to POST /api/fs/mkdir: %v", err)
	}
	defer mkdirResp.Body.Close()
	if mkdirResp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK from mkdir, got %d", mkdirResp.StatusCode)
	}
	if fi, err := os.Stat(filepath.Join(tempDir, "NewTestDir")); err != nil || !fi.IsDir() {
		t.Fatalf("Directory was not created")
	}
}

func TestRemoteClientAccessRestriction(t *testing.T) {
	tempDir := t.TempDir()
	server := NewDaemonServer(0, tempDir, "TestSecurityHost")

	remoteIP := "10.100.104.139:54321"
	localIP := "127.0.0.1:54321"

	// 1. Remote access to GET / must redirect to /share
	reqRemoteRoot := httptest.NewRequest("GET", "/", nil)
	reqRemoteRoot.RemoteAddr = remoteIP
	wRemoteRoot := httptest.NewRecorder()
	server.handleIndex(wRemoteRoot, reqRemoteRoot)
	if wRemoteRoot.Code != http.StatusTemporaryRedirect {
		t.Fatalf("Expected 307 TemporaryRedirect for remote client on /, got %d", wRemoteRoot.Code)
	}
	if loc := wRemoteRoot.Header().Get("Location"); loc != "/share" {
		t.Fatalf("Expected redirect to /share, got %s", loc)
	}

	// 2. Local access to GET / must succeed with 200 OK
	reqLocalRoot := httptest.NewRequest("GET", "/", nil)
	reqLocalRoot.RemoteAddr = localIP
	reqLocalRoot.Host = fmt.Sprintf("127.0.0.1:%d", server.currentHTTPPort()) // API-04: Host is checked
	wLocalRoot := httptest.NewRecorder()
	server.handleIndex(wLocalRoot, reqLocalRoot)
	if wLocalRoot.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for localhost client on /, got %d", wLocalRoot.Code)
	}

	// 3. Remote access to /api/fs/list must be 403 Forbidden
	reqRemoteFS := httptest.NewRequest("GET", "/api/fs/list", nil)
	reqRemoteFS.RemoteAddr = remoteIP
	wRemoteFS := httptest.NewRecorder()
	server.handleFSList(wRemoteFS, reqRemoteFS)
	if wRemoteFS.Code != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden for remote client on /api/fs/list, got %d", wRemoteFS.Code)
	}

	// 4. Remote access to /api/fs/mkdir must be 403 Forbidden
	reqRemoteMkdir := httptest.NewRequest("POST", "/api/fs/mkdir?dir=/tmp&name=test", nil)
	reqRemoteMkdir.RemoteAddr = remoteIP
	wRemoteMkdir := httptest.NewRecorder()
	server.handleFSMkdir(wRemoteMkdir, reqRemoteMkdir)
	if wRemoteMkdir.Code != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden for remote client on /api/fs/mkdir, got %d", wRemoteMkdir.Code)
	}

	// 5. Remote access to /api/upload must be 403 Forbidden
	reqRemoteUpload := httptest.NewRequest("POST", "/api/upload", nil)
	reqRemoteUpload.RemoteAddr = remoteIP
	wRemoteUpload := httptest.NewRecorder()
	server.handleUpload(wRemoteUpload, reqRemoteUpload)
	if wRemoteUpload.Code != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden for remote client on /api/upload, got %d", wRemoteUpload.Code)
	}

	// 6. Remote access to /api/browse must be 403 Forbidden
	reqRemoteBrowse := httptest.NewRequest("GET", "/api/browse", nil)
	reqRemoteBrowse.RemoteAddr = remoteIP
	wRemoteBrowse := httptest.NewRecorder()
	server.handleBrowse(wRemoteBrowse, reqRemoteBrowse)
	if wRemoteBrowse.Code != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden for remote client on /api/browse, got %d", wRemoteBrowse.Code)
	}

	t.Logf("VERIFIED: Localhost isolation completely prevents remote network devices from accessing host dashboard and filesystem!")
}

func TestLinuxMountDiscovery(t *testing.T) {
	// Test isSystemMount filtering
	if !isSystemMount("/sys/kernel") {
		t.Errorf("Expected /sys/kernel to be identified as system mount")
	}
	if !isSystemMount("/proc/sys") {
		t.Errorf("Expected /proc/sys to be identified as system mount")
	}
	if !isSystemMount("/apex/com.android") {
		t.Errorf("Expected /apex to be identified as system mount")
	}
	if isSystemMount("/media/user/Drive") {
		t.Errorf("Expected /media/user/Drive NOT to be identified as system mount")
	}
	if isSystemMount("/run/media/user/External") {
		t.Errorf("Expected /run/media/user/External NOT to be identified as system mount")
	}
	if isSystemMount("/mnt/external_hdd") {
		t.Errorf("Expected /mnt/external_hdd NOT to be identified as system mount")
	}

	// Verify getQuickDirs contains at least home/downloads or mounts
	qd := getQuickDirs()
	if len(qd) == 0 {
		t.Errorf("Expected getQuickDirs to return at least one quick directory")
	}
}
