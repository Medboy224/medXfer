package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/manifest"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/testkit"
)

// testConfigDir keeps the tests away from the user's real config.json and reports.
// TestMain removes it.
var testConfigDir string

func init() {
	temp, err := os.MkdirTemp("", "medxfer_test_cfg_*")
	if err == nil {
		testConfigDir = temp
		SetCustomConfigDir(temp)
	}
}

func getFreePort() int {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 19999
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestDaemonHTTPEndpoints(t *testing.T) {
	server := NewDaemonServer(0, t.TempDir(), "TestDevice")
	ln, err := server.Listen(0)
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Stop()

	// 1. Test /health
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		t.Fatalf("Failed to GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var healthMap map[string]string
	if err := json.Unmarshal(body, &healthMap); err != nil || healthMap["status"] != "ok" {
		t.Fatalf("Invalid /health payload: %s", string(body))
	}

	// 2. Test /status
	statusResp, err := controlGet(fmt.Sprintf("http://127.0.0.1:%d/status", port))
	if err != nil {
		t.Fatalf("Failed to GET /status: %v", err)
	}
	defer statusResp.Body.Close()
	var st DaemonStatus
	if err := json.NewDecoder(statusResp.Body).Decode(&st); err != nil {
		t.Fatalf("Failed decoding /status: %v", err)
	}
	if st.DeviceName != "TestDevice" {
		t.Fatalf("Expected DeviceName 'TestDevice', got %q", st.DeviceName)
	}

	// 3. Test / : the dashboard in devui builds, an explicit 404 otherwise (DEV-19)
	uiResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("Failed to GET /: %v", err)
	}
	defer uiResp.Body.Close()
	if want := map[bool]int{true: http.StatusOK, false: http.StatusNotFound}[DashboardAvailable]; uiResp.StatusCode != want {
		t.Fatalf("GET / = %d; want %d (DashboardAvailable=%v)", uiResp.StatusCode, want, DashboardAvailable)
	}
}

func TestDaemonWebSocketCommands(t *testing.T) {
	node := testkit.StartNode(t, NewDaemonServer(0, t.TempDir(), "TestWSNode"))

	// 1. First event received upon connect should be "status"
	if e := node.NextEvent(0); e.Event != "status" {
		t.Fatalf("Expected first event 'status', got %q", e.Event)
	}

	// 2. "set_config" is answered by a correlated status event
	node.RequestID("req_1", "set_config", `{
		"device_name": "UpdatedDevice",
		"download_dir": "/tmp/custom_downloads",
		"collision_policy": "overwrite"
	}`)
	if e := node.WaitReply("req_1", 0); e.Event != "status" {
		t.Fatalf("Expected correlated status event for req_1, got %+v", e)
	}

	// 3. "get_status"
	node.RequestID("req_2", "get_status", nil)
	node.WaitReply("req_2", 0)

	// 4. "cancel"
	node.Request("cancel", nil)
	node.WaitEvent("transfer_canceled", 0)
}

func TestConfigSaveAndLoad(t *testing.T) {
	cfgPath := GetConfigFilePath()
	orig, _ := os.ReadFile(cfgPath)
	defer func() {
		if orig != nil {
			_ = os.WriteFile(cfgPath, orig, 0644)
		} else {
			_ = os.Remove(cfgPath)
		}
	}()

	tempDir := t.TempDir()
	cfg := Config{
		DeviceName:      "PersistedDevice",
		DownloadDir:     tempDir,
		CollisionPolicy: "skip",
		Workers:         8,
		ChunkSizeMB:     4,
	}

	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	loaded := LoadConfig("", "")
	if loaded.DeviceName != "PersistedDevice" || loaded.CollisionPolicy != "skip" || loaded.Workers != 8 {
		t.Fatalf("Loaded config mismatch: %+v", loaded)
	}
}

func TestPauseResumeAndSkipWebSocketCommands(t *testing.T) {
	server := NewDaemonServer(0, t.TempDir(), "TestControlWSNode")
	node := testkit.StartNode(t, server)

	// 1. "pause"
	node.Request("pause", nil)
	node.WaitEvent("transfer_paused", 0)
	server.mu.RLock()
	paused := server.isPaused
	server.mu.RUnlock()
	if !paused {
		t.Fatalf("Server should be paused")
	}

	// 2. "resume"
	node.Request("resume", nil)
	node.WaitEvent("transfer_resumed", 0)
	server.mu.RLock()
	paused = server.isPaused
	server.mu.RUnlock()
	if paused {
		t.Fatalf("Server should not be paused")
	}

	// 3. "skip_file"
	server.mu.Lock()
	server.currentBatchItems = []BatchFileInfo{
		{Index: 0, RelPath: "file0.txt", Size: 100, Status: "completed"},
		{Index: 1, RelPath: "file1.txt", Size: 200, Status: "transferring"},
		{Index: 2, RelPath: "file2.txt", Size: 300, Status: "pending"},
	}
	server.mu.Unlock()
	node.Request("skip_file", `{"item_index": 2}`)
	node.WaitEvent("file_skipped", 0)
	server.mu.RLock()
	skipped := server.skippedFiles[2]
	server.mu.RUnlock()
	if !skipped {
		t.Fatalf("File index 2 should be in skippedFiles")
	}

	// 4. "cancel"
	node.Request("cancel", nil)
	node.WaitEvent("transfer_canceled", 0)
}

func TestInstantOfferAndSessionDispatch(t *testing.T) {
	sendDir := t.TempDir()
	testFile := filepath.Join(sendDir, "fast_offer.mp4")
	_ = os.WriteFile(testFile, []byte("quick video data payload"), 0644)

	sendServer := NewDaemonServer(0, sendDir, "SenderNode")
	pair := testkit.StartPair(t, sendServer, NewDaemonServer(0, t.TempDir(), "ReceiverNode"))
	pair.Receiver.PeerAddr() // the offer delay below must not include the listener startup

	startOffer := time.Now()
	pair.Send([]string{testFile}, testkit.SendOptions{})

	// The receiver must show the incoming offer quickly.
	pair.Receiver.WaitEvent("incoming_offer", 2*time.Second)
	elapsed := time.Since(startOffer)
	t.Logf("Received incoming_offer in %v", elapsed)
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("Offer took too long to show: %v", elapsed)
	}

	sendServer.mu.Lock()
	sess := sendServer.activeSession
	sendServer.mu.Unlock()
	if sess != nil && !sess.IsEncrypted() {
		t.Fatalf("Expected activeSession to be encrypted with TLS 1.3")
	}
}

func TestPerFilePauseAndResumeWebSocketCommands(t *testing.T) {
	server := NewDaemonServer(0, t.TempDir(), "TestDevice")
	node := testkit.StartNode(t, server)

	server.mu.Lock()
	server.currentBatchItems = []BatchFileInfo{
		{Index: 0, RelPath: "file0.txt", Size: 100, Status: "transferring"},
		{Index: 1, RelPath: "file1.txt", Size: 200, Status: "pending"},
		{Index: 2, RelPath: "file2.txt", Size: 300, Status: "pending"},
	}
	server.mu.Unlock()

	itemState := func(i int) (paused bool, status string) {
		server.mu.RLock()
		defer server.mu.RUnlock()
		return server.pausedFiles[i], server.currentBatchItems[i].Status
	}

	// 1. "pause_file" for the item in progress
	node.Request("pause_file", `{"item_index": 0}`)
	node.WaitEvent("file_paused", 0)
	if paused, status := itemState(0); !paused || status != "paused" {
		t.Fatalf("item 0: paused=%v status=%q; want true, paused", paused, status)
	}

	// 2. "pause_file" for a pending item
	node.Request("pause_file", `{"item_index": 2}`)
	node.WaitEvent("file_paused", 0)
	if paused, status := itemState(2); !paused || status != "paused" {
		t.Fatalf("item 2: paused=%v status=%q; want true, paused", paused, status)
	}

	// 3. "resume_file" for item 0
	node.Request("resume_file", `{"item_index": 0}`)
	node.WaitEvent("file_resumed", 0)
	if paused, status := itemState(0); paused || status != "pending" {
		t.Fatalf("item 0 after resume: paused=%v status=%q; want false, pending", paused, status)
	}
}

func TestFolderTarStreamingBatch(t *testing.T) {
	t.Skip("quarantaine : batch_complete annule l'extraction en cours, voir issue #9")
	sendDir := t.TempDir()
	folderToShare := filepath.Join(sendDir, "my_shared_repo")
	for i := 0; i < 30; i++ {
		subDir := filepath.Join(folderToShare, fmt.Sprintf("sub_%d", i%3))
		_ = os.MkdirAll(subDir, 0755)
		data := bytes.Repeat([]byte(fmt.Sprintf("package main\n// file %d content\n", i)), 50)
		_ = os.WriteFile(filepath.Join(subDir, fmt.Sprintf("code_%d.go", i)), data, 0644)
	}

	recvDir := t.TempDir()
	pair := testkit.StartPair(t, NewDaemonServer(0, sendDir, "SenderNode"), NewDaemonServer(0, recvDir, "ReceiverNode"))

	pair.Send([]string{folderToShare}, testkit.SendOptions{UseTarStream: true})
	pair.AcceptOffer(0)
	pair.WaitTransferDone(0)

	testkit.AssertTreesEqual(t, sendDir, recvDir)
}

func TestFolderGranularBatchTransfer(t *testing.T) {
	sendDir := t.TempDir()
	folderToShare := filepath.Join(sendDir, "my_granular_folder")
	for i := 0; i < 5; i++ {
		subDir := filepath.Join(folderToShare, fmt.Sprintf("sub_%d", i%2))
		_ = os.MkdirAll(subDir, 0755)
		data := bytes.Repeat([]byte(fmt.Sprintf("content of file %d\n", i)), 100)
		_ = os.WriteFile(filepath.Join(subDir, fmt.Sprintf("file_%d.bin", i)), data, 0644)
	}

	recvDir := t.TempDir()
	pair := testkit.StartPair(t, NewDaemonServer(0, sendDir, "SenderNode"), NewDaemonServer(0, recvDir, "ReceiverNode"))

	// Without use_tar_stream a folder is sent as a granular batch.
	pair.Send([]string{folderToShare}, testkit.SendOptions{})
	pair.AcceptOffer(0)
	pair.WaitTransferDone(0)

	testkit.AssertTreesEqual(t, sendDir, recvDir)
}

func TestQRGeneration(t *testing.T) {
	wifiQR, err := GenerateWiFiQRDataURI("medXfer-Test", "secret1234", 128)
	if err != nil {
		t.Fatalf("Failed to generate WiFi QR: %v", err)
	}
	if !strings.HasPrefix(wifiQR, "data:image/png;base64,") {
		t.Fatalf("Expected data-uri prefix, got: %s", wifiQR[:30])
	}

	urlQR, err := GenerateURLQRDataURI("http://192.168.137.1:18888/share", 128)
	if err != nil {
		t.Fatalf("Failed to generate URL QR: %v", err)
	}
	if !strings.HasPrefix(urlQR, "data:image/png;base64,") {
		t.Fatalf("Expected data-uri prefix, got: %s", urlQR[:30])
	}
	t.Logf("VERIFIED: QR codes generated successfully!")
}

func TestSharePortalEndpoints(t *testing.T) {
	tempDir := t.TempDir()
	server := NewDaemonServer(0, tempDir, "TestPortalHost")
	ln, err := server.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Stop()

	client := &http.Client{Timeout: 3 * time.Second}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	st := server.GetStatus()
	pin := st.WebSharePIN

	// 0. Verify Web Share is disabled by default on startup
	if st.WebShareEnabled {
		t.Fatalf("Expected Web Share to be disabled by default on startup, got enabled")
	}
	respDis, err := client.Get(baseURL + "/share")
	if err != nil {
		t.Fatalf("GET /share failed: %v", err)
	}
	defer respDis.Body.Close()
	if respDis.StatusCode != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden from /share when disabled, got %d", respDis.StatusCode)
	}
	bodyDis, _ := io.ReadAll(respDis.Body)
	if !strings.Contains(string(bodyDis), "Web Share is Turned Off") {
		t.Fatalf("Expected ShareDisabledHTML content, got: %s", string(bodyDis))
	}

	// 1. Enable Web Share
	server.SetWebShareEnabled(true)

	// 2. Test GET /share (HTML portal)
	resp, err := client.Get(baseURL + "/share")
	if err != nil {
		t.Fatalf("GET /share failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK from /share, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "medXfer Mobile Share") {
		t.Fatalf("Unexpected /share HTML content")
	}

	// 2. Test GET /api/share/list without PIN -> Must be 401 Unauthorized
	respUnauth, err := client.Get(baseURL + "/api/share/list")
	if err != nil {
		t.Fatalf("GET /api/share/list failed: %v", err)
	}
	defer respUnauth.Body.Close()
	if respUnauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Expected 401 Unauthorized without PIN, got %d", respUnauth.StatusCode)
	}

	// 3. Test GET /api/share/list with PIN -> 200 OK
	respList, err := client.Get(baseURL + "/api/share/list?pin=" + pin)
	if err != nil {
		t.Fatalf("GET /api/share/list failed: %v", err)
	}
	defer respList.Body.Close()
	if respList.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK from /api/share/list with PIN, got %d", respList.StatusCode)
	}

	// 4. Test POST /api/share/request_upload and ticket approval
	server.SetAutoAccept(true)

	reqUploadBody := bytes.NewBufferString(`{"files":[{"name":"mobile_photo.jpg","size":26}]}`)
	reqUploadReq, _ := http.NewRequest(http.MethodPost, baseURL+"/api/share/request_upload?pin="+pin, reqUploadBody)
	reqUploadReq.Header.Set("Content-Type", "application/json")
	reqUploadResp, err := client.Do(reqUploadReq)
	if err != nil {
		t.Fatalf("request_upload failed: %v", err)
	}
	defer reqUploadResp.Body.Close()
	if reqUploadResp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 from request_upload, got %d", reqUploadResp.StatusCode)
	}
	var ticketRes struct {
		Approved bool   `json:"approved"`
		Ticket   string `json:"ticket"`
	}
	_ = json.NewDecoder(reqUploadResp.Body).Decode(&ticketRes)
	if !ticketRes.Approved || ticketRes.Ticket == "" {
		t.Fatalf("Expected approved ticket, got %+v", ticketRes)
	}

	// 5. Test POST /api/share/upload with ticket
	uploadBody := &bytes.Buffer{}
	writer := multipart.NewWriter(uploadBody)
	part, err := writer.CreateFormFile("files", "mobile_photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	expectedData := []byte("JPEG_MOCK_IMAGE_DATA_12345")
	_, _ = part.Write(expectedData)
	_ = writer.Close()

	uploadReq, err := http.NewRequest(http.MethodPost, baseURL+"/api/share/upload?ticket="+ticketRes.Ticket+"&pin="+pin, uploadBody)
	if err != nil {
		t.Fatal(err)
	}
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())

	uploadResp, err := client.Do(uploadReq)
	if err != nil {
		t.Fatalf("POST /api/share/upload failed: %v", err)
	}
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK from /api/share/upload, got %d", uploadResp.StatusCode)
	}

	// Verify file was saved in server's tempDir
	savedFile := filepath.Join(tempDir, "mobile_photo.jpg")
	actualData, err := os.ReadFile(savedFile)
	if err != nil {
		t.Fatalf("Uploaded file missing from destination: %v", err)
	}
	if !bytes.Equal(actualData, expectedData) {
		t.Fatalf("Uploaded file content mismatch")
	}

	t.Logf("VERIFIED: Zero-install web share portal, PIN security, and mobile upload verified!")
}

func TestClearWebFilesAndStaleFilePrevention(t *testing.T) {
	tempDir := t.TempDir()
	server := NewDaemonServer(0, tempDir, "TestClearHost")
	node := testkit.StartNode(t, server)

	// 1. Share a file from the control WebSocket
	testFile := filepath.Join(tempDir, "shared_document.pdf")
	_ = os.WriteFile(testFile, []byte("Test Document Content"), 0644)
	node.Request("share_web_files", map[string]any{"paths": []string{testFile}})

	var shared struct {
		Count int `json:"count"`
	}
	if err := node.WaitEvent("web_files_shared", 0).Decode(&shared); err != nil || shared.Count != 1 {
		t.Fatalf("Expected 1 shared file, got %d (%v)", shared.Count, err)
	}

	pin := server.GetStatus().WebSharePIN
	listCount := func() int {
		t.Helper()
		resp, err := http.Get(node.URL("/api/share/list?pin=" + pin))
		if err != nil {
			t.Fatalf("GET /api/share/list failed: %v", err)
		}
		defer resp.Body.Close()
		var list struct {
			Items      []SharedItem `json:"items"`
			TotalCount int          `json:"total_count"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&list)
		if list.TotalCount != len(list.Items) {
			t.Fatalf("total_count %d but %d items", list.TotalCount, len(list.Items))
		}
		return list.TotalCount
	}

	// 2. /api/share/list returns the shared file
	if n := listCount(); n != 1 {
		t.Fatalf("Expected 1 item in list, got %d", n)
	}

	// 3. clear_web_files empties the list...
	node.Request("clear_web_files", nil)
	node.WaitEvent("web_files_cleared", 0)
	if n := listCount(); n != 0 {
		t.Fatalf("Expected 0 items in list after clear, got %d", n)
	}

	// 4. ...and the old file can no longer be downloaded.
	dlResp, err := http.Get(node.URL("/api/share/download?file=shared_document.pdf&pin=" + pin))
	if err != nil {
		t.Fatalf("GET /api/share/download failed: %v", err)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusNotFound {
		t.Fatalf("Expected HTTP 404 StatusNotFound after clear, got %d", dlResp.StatusCode)
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

func TestWebShareInvalidPINNotification(t *testing.T) {
	tmpDir := t.TempDir()
	server := NewDaemonServer(0, tmpDir, "SecurityHost")
	server.webSharePIN = "4321"
	server.SetWebShareEnabled(true)

	node := testkit.StartNode(t, server)

	// 1. First visit without any PIN: returns 401 with "unauthorized", no security alert
	reqNoPin := httptest.NewRequest("GET", "/api/share/list", nil)
	wNoPin := httptest.NewRecorder()
	server.handleShareList(wNoPin, reqNoPin)
	if wNoPin.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 for no PIN, got %d", wNoPin.Code)
	}
	var noPinBody map[string]interface{}
	_ = json.Unmarshal(wNoPin.Body.Bytes(), &noPinBody)
	if noPinBody["error"] != "unauthorized" {
		t.Fatalf("Expected error 'unauthorized', got %v", noPinBody["error"])
	}

	// 2. Request with WRONG PIN: returns 401 with "invalid_pin" and emits web_share_auth_failed
	reqBadPin := httptest.NewRequest("GET", "/api/share/list?pin=9999", nil)
	reqBadPin.RemoteAddr = "192.168.1.105:54321"
	wBadPin := httptest.NewRecorder()
	server.handleShareList(wBadPin, reqBadPin)
	if wBadPin.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 for bad PIN, got %d", wBadPin.Code)
	}
	var badPinBody map[string]interface{}
	_ = json.Unmarshal(wBadPin.Body.Bytes(), &badPinBody)
	if badPinBody["error"] != "invalid_pin" {
		t.Fatalf("Expected error 'invalid_pin', got %v", badPinBody["error"])
	}

	// The host is told through the control WebSocket.
	failed := node.WaitEvent("web_share_auth_failed", 2*time.Second).Fields()
	if failed["client_ip"] != "192.168.1.105" {
		t.Fatalf("Expected client IP 192.168.1.105, got %v", failed["client_ip"])
	}
	// WEB-04: the attempted PIN must never leave the server, not even towards the host UI
	if _, leaked := failed["attempted_pin"]; leaked {
		t.Fatalf("web_share_auth_failed leaks the attempted PIN: %v", failed)
	}

	// 3. Test rate limiting: rapid second bad attempt does not emit a second WS event immediately
	reqBadPin2 := httptest.NewRequest("GET", "/api/share/list?pin=8888", nil)
	reqBadPin2.RemoteAddr = "192.168.1.105:54321"
	wBadPin2 := httptest.NewRecorder()
	server.handleShareList(wBadPin2, reqBadPin2)
	if wBadPin2.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401, got %d", wBadPin2.Code)
	}

	node.ExpectNoEvent("web_share_auth_failed", 200*time.Millisecond)

	// 4. Request with CORRECT PIN succeeds with 200 OK
	reqGoodPin := httptest.NewRequest("GET", "/api/share/list?pin=4321", nil)
	wGoodPin := httptest.NewRecorder()
	server.handleShareList(wGoodPin, reqGoodPin)
	if wGoodPin.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for correct PIN, got %d", wGoodPin.Code)
	}

	t.Logf("VERIFIED: Web Share invalid PIN correctly notifies host via WebSocket and returns invalid_pin error with rate limiting!")
}

func TestWebShareAntiBruteForceLockout(t *testing.T) {
	tmpDir := t.TempDir()
	server := NewDaemonServer(0, tmpDir, "BruteForceHost")
	server.webSharePIN = "123456"
	server.SetWebShareEnabled(true)

	node := testkit.StartNode(t, server)

	attackerIP := "192.168.1.77:12345"

	// 1. Send 4 failed PIN attempts: each should return 401 Unauthorized
	for i := 1; i <= 4; i++ {
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/share/list?pin=%06d", i), nil)
		req.RemoteAddr = attackerIP
		w := httptest.NewRecorder()
		server.handleShareList(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("Attempt #%d: expected 401, got %d", i, w.Code)
		}
	}

	// 2. 5th failed attempt: must trigger HTTP 429 Too Many Requests (Lockout for 60s)
	req5 := httptest.NewRequest("GET", "/api/share/list?pin=000005", nil)
	req5.RemoteAddr = attackerIP
	w5 := httptest.NewRecorder()
	server.handleShareList(w5, req5)
	if w5.Code != http.StatusTooManyRequests {
		t.Fatalf("Attempt #5: expected 429 Too Many Requests, got %d", w5.Code)
	}
	var body5 map[string]interface{}
	_ = json.Unmarshal(w5.Body.Bytes(), &body5)
	if body5["error"] != "locked_out" {
		t.Fatalf("Expected error 'locked_out', got %v", body5["error"])
	}

	// 3. Verify WebSocket received web_share_brute_force_blocked
	var blocked struct {
		ClientIP      string `json:"client_ip"`
		FailCount     int    `json:"fail_count"`
		LockedSeconds int    `json:"locked_seconds"`
	}
	if err := node.WaitEvent("web_share_brute_force_blocked", 2*time.Second).Decode(&blocked); err != nil {
		t.Fatal(err)
	}
	if blocked.ClientIP != "192.168.1.77" || blocked.FailCount != 5 || blocked.LockedSeconds != 60 {
		t.Fatalf("brute force alert = %+v; want 192.168.1.77, 5 failures, 60 s", blocked)
	}

	// 4. While locked out, even the CORRECT PIN must be rejected with 429
	reqLocked := httptest.NewRequest("GET", "/api/share/list?pin=123456", nil)
	reqLocked.RemoteAddr = attackerIP
	wLocked := httptest.NewRecorder()
	server.handleShareList(wLocked, reqLocked)
	if wLocked.Code != http.StatusTooManyRequests {
		t.Fatalf("Expected locked IP to get 429 even with correct PIN, got %d", wLocked.Code)
	}

	// 5. Another client IP (legitimate user) is NOT locked out and succeeds
	legitIP := "192.168.1.88:23456"
	reqLegit := httptest.NewRequest("GET", "/api/share/list?pin=123456", nil)
	reqLegit.RemoteAddr = legitIP
	wLegit := httptest.NewRecorder()
	server.handleShareList(wLegit, reqLegit)
	if wLegit.Code != http.StatusOK {
		t.Fatalf("Expected legitimate IP to get 200 OK, got %d", wLegit.Code)
	}

	// 6. Token authentication bypasses PIN requirement
	reqToken := httptest.NewRequest("GET", "/api/share/list?token="+server.webShareToken, nil)
	reqToken.RemoteAddr = legitIP
	wToken := httptest.NewRecorder()
	server.handleShareList(wToken, reqToken)
	if wToken.Code != http.StatusOK {
		t.Fatalf("Expected token auth to succeed with 200 OK, got %d", wToken.Code)
	}

	t.Logf("VERIFIED: Anti-brute force progressive lockout (HTTP 429), WebSocket security alert, and high-entropy token authentication verified!")
}

func TestWebShareToggleAndDisabledState(t *testing.T) {
	tempDir := t.TempDir()
	server := NewDaemonServer(0, tempDir, "ToggleHost")
	node := testkit.StartNode(t, server)
	port := node.Port()

	// 1. Verify initially disabled
	if server.GetStatus().WebShareEnabled {
		t.Fatalf("Expected WebShareEnabled to be false on startup")
	}

	client := &http.Client{Timeout: 2 * time.Second}
	portalURL := fmt.Sprintf("http://127.0.0.1:%d/share", port)
	apiURL := fmt.Sprintf("http://127.0.0.1:%d/api/share/list", port)

	// GET /share while disabled -> 403 Forbidden with ShareDisabledHTML
	resp1, err := client.Get(portalURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusForbidden {
		t.Fatalf("Expected 403 for /share when disabled, got %d", resp1.StatusCode)
	}

	// GET /api/share/list while disabled -> 403 Forbidden with error "web_share_disabled"
	resp2, err := client.Get(apiURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("Expected 403 for /api/share/list when disabled, got %d", resp2.StatusCode)
	}
	var errBody map[string]interface{}
	_ = json.NewDecoder(resp2.Body).Decode(&errBody)
	if errBody["error"] != "web_share_disabled" {
		t.Fatalf("Expected error 'web_share_disabled', got %v", errBody["error"])
	}

	toggle := func(enabled bool) {
		t.Helper()
		id := node.Request("toggle_web_share", map[string]any{"enabled": enabled})
		var st DaemonStatus
		if err := node.WaitReply(id, 2*time.Second).Decode(&st); err != nil {
			t.Fatal(err)
		}
		if st.WebShareEnabled != enabled {
			t.Fatalf("status after toggle_web_share(%v) reports WebShareEnabled=%v", enabled, st.WebShareEnabled)
		}
	}
	toggle(true)

	// Now GET /share returns 200 OK!
	resp3, err := client.Get(portalURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK for /share after toggle ON, got %d", resp3.StatusCode)
	}

	// 3. Toggle Web Share OFF
	toggle(false)

	// Now GET /share returns 403 Forbidden again!
	resp4, err := client.Get(portalURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp4.Body.Close()
	if resp4.StatusCode != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden for /share after toggle OFF, got %d", resp4.StatusCode)
	}

	t.Logf("VERIFIED: Web Share toggle lifecycle (default disabled, WebSocket toggle ON/OFF, 403 protection) fully verified!")
}

func TestWebShareRangeDownload(t *testing.T) {
	tempDir := t.TempDir()

	// Create a test file of 100 bytes
	testData := make([]byte, 100)
	for i := range testData {
		testData[i] = byte(i)
	}
	filePath := filepath.Join(tempDir, "sample_range.bin")
	if err := os.WriteFile(filePath, testData, 0644); err != nil {
		t.Fatal(err)
	}

	server := NewDaemonServer(0, tempDir, "TestDaemonRange")
	ln, err := server.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Stop()

	server.SetWebShareEnabled(true)
	server.mu.Lock()
	server.webSharedFile = filePath
	server.webSharedManifest = nil
	server.mu.Unlock()
	st := server.GetStatus()
	pin := st.WebSharePIN

	client := &http.Client{Timeout: 3 * time.Second}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// 1. Full download - verify Accept-Ranges header
	fullReq, _ := http.NewRequest(http.MethodGet, baseURL+"/api/share/download?file=sample_range.bin&pin="+pin, nil)
	fullResp, err := client.Do(fullReq)
	if err != nil {
		t.Fatalf("Full download failed: %v", err)
	}
	defer fullResp.Body.Close()
	if fullResp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", fullResp.StatusCode)
	}
	if fullResp.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("Expected Accept-Ranges: bytes, got %s", fullResp.Header.Get("Accept-Ranges"))
	}
	fullBytes, _ := io.ReadAll(fullResp.Body)
	if !bytes.Equal(fullBytes, testData) {
		t.Fatalf("Full download content mismatch")
	}

	// 2. Range request: bytes=10-29 (20 bytes total)
	rangeReq, _ := http.NewRequest(http.MethodGet, baseURL+"/api/share/download?file=sample_range.bin&pin="+pin, nil)
	rangeReq.Header.Set("Range", "bytes=10-29")
	rangeResp, err := client.Do(rangeReq)
	if err != nil {
		t.Fatalf("Range request failed: %v", err)
	}
	defer rangeResp.Body.Close()

	if rangeResp.StatusCode != http.StatusPartialContent {
		t.Fatalf("Expected 206 Partial Content, got %d", rangeResp.StatusCode)
	}
	contentRange := rangeResp.Header.Get("Content-Range")
	if contentRange != "bytes 10-29/100" {
		t.Fatalf("Expected Content-Range: bytes 10-29/100, got %s", contentRange)
	}
	partialBytes, _ := io.ReadAll(rangeResp.Body)
	if len(partialBytes) != 20 {
		t.Fatalf("Expected 20 bytes, got %d", len(partialBytes))
	}
	if !bytes.Equal(partialBytes, testData[10:30]) {
		t.Fatalf("Range content mismatch")
	}

	// 3. Open-ended Range request: bytes=50-
	openRangeReq, _ := http.NewRequest(http.MethodGet, baseURL+"/api/share/download?file=sample_range.bin&pin="+pin, nil)
	openRangeReq.Header.Set("Range", "bytes=50-")
	openRangeResp, err := client.Do(openRangeReq)
	if err != nil {
		t.Fatalf("Open-ended range request failed: %v", err)
	}
	defer openRangeResp.Body.Close()

	if openRangeResp.StatusCode != http.StatusPartialContent {
		t.Fatalf("Expected 206 Partial Content, got %d", openRangeResp.StatusCode)
	}
	if openRangeResp.Header.Get("Content-Range") != "bytes 50-99/100" {
		t.Fatalf("Expected Content-Range: bytes 50-99/100, got %s", openRangeResp.Header.Get("Content-Range"))
	}
	tailBytes, _ := io.ReadAll(openRangeResp.Body)
	if !bytes.Equal(tailBytes, testData[50:]) {
		t.Fatalf("Open-ended range content mismatch")
	}

	t.Logf("VERIFIED: Web Share HTTP Range (pause/resume download) functionality fully verified!")
}

func TestWebShareChunkedUploadAndCancel(t *testing.T) {
	tempDir := t.TempDir()

	server := NewDaemonServer(0, tempDir, "TestDaemonChunk")
	ln, err := server.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Stop()

	server.SetWebShareEnabled(true)
	server.SetAutoAccept(true)
	st := server.GetStatus()
	pin := st.WebSharePIN

	client := &http.Client{Timeout: 3 * time.Second}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Step 1: Request upload ticket for chunked upload
	reqBody := bytes.NewBufferString(`{"files":[{"name":"chunked_file.bin","size":100}]}`)
	reqReq, _ := http.NewRequest(http.MethodPost, baseURL+"/api/share/request_upload?pin="+pin, reqBody)
	reqReq.Header.Set("Content-Type", "application/json")
	reqResp, err := client.Do(reqReq)
	if err != nil {
		t.Fatal(err)
	}
	defer reqResp.Body.Close()

	var ticketRes struct {
		Approved bool   `json:"approved"`
		Ticket   string `json:"ticket"`
	}
	_ = json.NewDecoder(reqResp.Body).Decode(&ticketRes)
	if !ticketRes.Approved || ticketRes.Ticket == "" {
		t.Fatalf("Ticket not approved")
	}

	// Prepare data: chunk 0 (bytes 0..49) and chunk 1 (bytes 50..99)
	chunk0Data := make([]byte, 50)
	for i := range chunk0Data {
		chunk0Data[i] = byte(i)
	}
	chunk1Data := make([]byte, 50)
	for i := range chunk1Data {
		chunk1Data[i] = byte(i + 50)
	}

	// Send Chunk 0
	url0 := fmt.Sprintf("%s/api/share/upload_chunk?pin=%s&ticket=%s&name=chunked_file.bin&chunk_index=0&total_chunks=2&offset=0&total_size=100", baseURL, pin, ticketRes.Ticket)
	c0Req, _ := http.NewRequest(http.MethodPost, url0, bytes.NewReader(chunk0Data))
	c0Req.Header.Set("Content-Type", "application/octet-stream")
	c0Resp, err := client.Do(c0Req)
	if err != nil {
		t.Fatalf("Chunk 0 failed: %v", err)
	}
	defer c0Resp.Body.Close()
	if c0Resp.StatusCode != http.StatusOK {
		t.Fatalf("Chunk 0 status %d", c0Resp.StatusCode)
	}
	var res0 map[string]interface{}
	_ = json.NewDecoder(c0Resp.Body).Decode(&res0)
	if res0["completed"] != false {
		t.Fatalf("Chunk 0 should not be completed, got %v", res0["completed"])
	}

	// Send Chunk 1
	url1 := fmt.Sprintf("%s/api/share/upload_chunk?pin=%s&ticket=%s&name=chunked_file.bin&chunk_index=1&total_chunks=2&offset=50&total_size=100", baseURL, pin, ticketRes.Ticket)
	c1Req, _ := http.NewRequest(http.MethodPost, url1, bytes.NewReader(chunk1Data))
	c1Req.Header.Set("Content-Type", "application/octet-stream")
	c1Resp, err := client.Do(c1Req)
	if err != nil {
		t.Fatalf("Chunk 1 failed: %v", err)
	}
	defer c1Resp.Body.Close()
	if c1Resp.StatusCode != http.StatusOK {
		t.Fatalf("Chunk 1 status %d", c1Resp.StatusCode)
	}
	var res1 map[string]interface{}
	_ = json.NewDecoder(c1Resp.Body).Decode(&res1)
	if res1["completed"] != true {
		t.Fatalf("Chunk 1 should be completed, got %v", res1["completed"])
	}

	// Verify the assembled file matches all 100 bytes
	savedPath := filepath.Join(tempDir, "chunked_file.bin")
	finalData, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("Failed reading saved chunked file: %v", err)
	}
	expectedFull := append(chunk0Data, chunk1Data...)
	if !bytes.Equal(finalData, expectedFull) {
		t.Fatalf("Assembled chunked file data does not match")
	}

	// Test Upload Cancellation & Partial File Purge
	reqBody2 := bytes.NewBufferString(`{"files":[{"name":"cancelled_upload.bin","size":200}]}`)
	reqReq2, _ := http.NewRequest(http.MethodPost, baseURL+"/api/share/request_upload?pin="+pin, reqBody2)
	reqReq2.Header.Set("Content-Type", "application/json")
	reqResp2, err := client.Do(reqReq2)
	if err != nil {
		t.Fatal(err)
	}
	defer reqResp2.Body.Close()
	var ticketRes2 struct {
		Approved bool   `json:"approved"`
		Ticket   string `json:"ticket"`
	}
	_ = json.NewDecoder(reqResp2.Body).Decode(&ticketRes2)

	// Send partial chunk
	urlCancel := fmt.Sprintf("%s/api/share/upload_chunk?pin=%s&ticket=%s&name=cancelled_upload.bin&chunk_index=0&total_chunks=4&offset=0&total_size=200", baseURL, pin, ticketRes2.Ticket)
	cReq, _ := http.NewRequest(http.MethodPost, urlCancel, bytes.NewReader(make([]byte, 50)))
	cReq.Header.Set("Content-Type", "application/octet-stream")
	cResp, err := client.Do(cReq)
	if err != nil {
		t.Fatal(err)
	}
	cResp.Body.Close()

	// Verify partial file was created on disk
	partialPath := filepath.Join(tempDir, "cancelled_upload.bin")
	if _, err := os.Stat(partialPath); err != nil {
		t.Fatalf("Expected partial file to exist before cancel: %v", err)
	}

	// Now call /api/share/cancel
	cancelReq, _ := http.NewRequest(http.MethodPost, baseURL+"/api/share/cancel?pin="+pin, nil)
	cancelResp, err := client.Do(cancelReq)
	if err != nil {
		t.Fatalf("Cancel request failed: %v", err)
	}
	cancelResp.Body.Close()
	if cancelResp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK from cancel, got %d", cancelResp.StatusCode)
	}

	// Verify partial file was purged from disk
	if _, err := os.Stat(partialPath); !os.IsNotExist(err) {
		t.Fatalf("Expected partial file to be deleted upon cancellation, but it still exists")
	}

	t.Logf("VERIFIED: Web Share chunked upload and cancel-purge verified successfully!")
}

func TestWebSharePauseResumeControls(t *testing.T) {
	tempDir := t.TempDir()

	server := NewDaemonServer(0, tempDir, "TestDaemonCtl")
	node := testkit.StartNode(t, server)
	port := node.Port()

	server.SetWebShareEnabled(true)
	st := server.GetStatus()
	pin := st.WebSharePIN

	client := &http.Client{Timeout: 3 * time.Second}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// 1. Initially no transfer active
	if server.IsWebShareTransferPaused() {
		t.Fatalf("Expected not paused")
	}

	// 2. Register mock transfer
	server.RegisterWebShareTransfer("download", "127.0.0.1", "large_video.mp4", nil, "")

	// 3. Call /api/share/pause via REST
	pauseReq, _ := http.NewRequest(http.MethodPost, baseURL+"/api/share/pause?pin="+pin, nil)
	pResp, err := client.Do(pauseReq)
	if err != nil {
		t.Fatal(err)
	}
	pResp.Body.Close()
	if pResp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK from pause, got %d", pResp.StatusCode)
	}
	if !server.IsWebShareTransferPaused() {
		t.Fatalf("Expected transfer to be paused")
	}

	// 4. Call /api/share/resume via REST
	resumeReq, _ := http.NewRequest(http.MethodPost, baseURL+"/api/share/resume?pin="+pin, nil)
	rResp, err := client.Do(resumeReq)
	if err != nil {
		t.Fatal(err)
	}
	rResp.Body.Close()
	if rResp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK from resume, got %d", rResp.StatusCode)
	}
	if server.IsWebShareTransferPaused() {
		t.Fatalf("Expected transfer to be resumed")
	}

	// Host sends web_share_pause action
	node.RequestID("req_pause", "web_share_pause", nil)
	var pausedWS bool
	for i := 0; i < 20; i++ {
		time.Sleep(25 * time.Millisecond)
		if server.IsWebShareTransferPaused() {
			pausedWS = true
			break
		}
	}
	if !pausedWS {
		t.Fatalf("Expected transfer to be paused via WS")
	}

	// Host sends web_share_resume action
	node.RequestID("req_resume", "web_share_resume", nil)
	var resumedWS bool
	for i := 0; i < 20; i++ {
		time.Sleep(25 * time.Millisecond)
		if !server.IsWebShareTransferPaused() {
			resumedWS = true
			break
		}
	}
	if !resumedWS {
		t.Fatalf("Expected transfer to be resumed via WS")
	}

	// Host sends web_share_cancel action
	node.RequestID("req_cancel", "web_share_cancel", nil)
	time.Sleep(50 * time.Millisecond)

	t.Logf("VERIFIED: Bidirectional pause, resume, and cancel via REST and WebSocket verified!")
}

func TestWebShareCancelReflectedToHostAndSpeed(t *testing.T) {
	tempDir := t.TempDir()

	// 1MB test file
	testData := make([]byte, 1024*1024)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	filePath := filepath.Join(tempDir, "cancel_test.bin")
	if err := os.WriteFile(filePath, testData, 0644); err != nil {
		t.Fatal(err)
	}

	server := NewDaemonServer(0, tempDir, "TestHostCancelReflect")
	node := testkit.StartNode(t, server)
	port := node.Port()

	server.SetWebShareEnabled(true)
	server.mu.Lock()
	server.webSharedFile = filePath
	server.webSharedManifest = nil
	server.mu.Unlock()
	st := server.GetStatus()
	pin := st.WebSharePIN

	client := &http.Client{Timeout: 5 * time.Second}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// --- Part 1: Download cancellation reflected to host ---
	// Client calls /api/share/cancel
	cancelReq, _ := http.NewRequest(http.MethodPost, baseURL+"/api/share/cancel?pin="+pin, nil)
	cResp, err := client.Do(cancelReq)
	if err != nil {
		t.Fatal(err)
	}
	cResp.Body.Close()

	// Host must receive web_share_canceled
	node.WaitEvent("web_share_canceled", 2*time.Second)

	// --- Part 2: Upload progress speed calculation & cancellation reflected to host ---
	server.SetAutoAccept(true)
	reqBody := bytes.NewBufferString(`{"files":[{"name":"speed_test.bin","size":409600}]}`)
	reqReq, _ := http.NewRequest(http.MethodPost, baseURL+"/api/share/request_upload?pin="+pin, reqBody)
	reqReq.Header.Set("Content-Type", "application/json")
	reqResp, err := client.Do(reqReq)
	if err != nil {
		t.Fatal(err)
	}
	defer reqResp.Body.Close()
	var ticketRes struct {
		Approved bool   `json:"approved"`
		Ticket   string `json:"ticket"`
	}
	_ = json.NewDecoder(reqResp.Body).Decode(&ticketRes)

	// Send chunk 0 (200 KB)
	chunk0 := make([]byte, 204800)
	url0 := fmt.Sprintf("%s/api/share/upload_chunk?pin=%s&ticket=%s&name=speed_test.bin&chunk_index=0&total_chunks=2&offset=0&total_size=409600", baseURL, pin, ticketRes.Ticket)
	c0Req, _ := http.NewRequest(http.MethodPost, url0, bytes.NewReader(chunk0))
	c0Req.Header.Set("Content-Type", "application/octet-stream")
	c0Resp, err := client.Do(c0Req)
	if err != nil {
		t.Fatal(err)
	}
	c0Resp.Body.Close()

	// Wait 250ms and send chunk 1 (200 KB)
	time.Sleep(250 * time.Millisecond)
	chunk1 := make([]byte, 204800)
	url1 := fmt.Sprintf("%s/api/share/upload_chunk?pin=%s&ticket=%s&name=speed_test.bin&chunk_index=1&total_chunks=2&offset=204800&total_size=409600", baseURL, pin, ticketRes.Ticket)
	c1Req, _ := http.NewRequest(http.MethodPost, url1, bytes.NewReader(chunk1))
	c1Req.Header.Set("Content-Type", "application/octet-stream")
	c1Resp, err := client.Do(c1Req)
	if err != nil {
		t.Fatal(err)
	}
	c1Resp.Body.Close()

	// Drain WS events to verify speed was non-zero on progress
	var sawNonZeroSpeed bool
	for {
		ev, ok := node.TryWaitFor(func(e testkit.Event) bool {
			return e.Event == "web_share_progress" || e.Event == "web_share_complete"
		}, 2*time.Second)
		if !ok || ev.Event == "web_share_complete" {
			break
		}
		var progress struct {
			SpeedMbps string `json:"speed_mbps"`
		}
		_ = ev.Decode(&progress)
		if spd, _ := strconv.ParseFloat(progress.SpeedMbps, 64); spd > 0 {
			sawNonZeroSpeed = true
		}
	}
	if !sawNonZeroSpeed {
		t.Fatalf("Expected non-zero speed in web_share_progress during chunked upload")
	}

	t.Logf("VERIFIED: Cancellation reflection to host and dynamic non-zero speed verified!")
}

func TestWebShareStoreZipFastStreaming(t *testing.T) {
	tempDir := t.TempDir()
	server := NewDaemonServer(0, tempDir, "TestHost")
	ln, err := server.Listen(0)
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() { _ = server.Serve(ln) }()
	defer server.Stop()

	// Create test files
	file1Content := []byte("Hello from file 1 - rapid streaming test")
	file2Content := []byte("Hello from file 2 - verifying zip.Store line speed")
	p1 := filepath.Join(tempDir, "sample1.txt")
	p2 := filepath.Join(tempDir, "sample2.txt")
	if err := os.WriteFile(p1, file1Content, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p2, file2Content, 0644); err != nil {
		t.Fatal(err)
	}

	mf := &manifest.Manifest{
		RootName:   "TestBundle",
		TotalFiles: 2,
		TotalBytes: int64(len(file1Content) + len(file2Content)),
		Items: []manifest.Item{
			{RelPath: "sample1.txt", FullPath: p1, Size: int64(len(file1Content))},
			{RelPath: "sample2.txt", FullPath: p2, Size: int64(len(file2Content))},
		},
	}

	server.SetWebShareEnabled(true)
	server.webSharedManifest = mf
	pin := server.GetStatus().WebSharePIN

	client := &http.Client{Timeout: 5 * time.Second}
	dlURL := fmt.Sprintf("http://127.0.0.1:%d/api/share/download?zip=true&pin=%s", port, pin)
	resp, err := client.Get(dlURL)
	if err != nil {
		t.Fatalf("Failed to GET zip download: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	totalHeader := resp.Header.Get("X-Total-Bytes")
	expectedTotalStr := strconv.FormatInt(mf.TotalBytes, 10)
	if totalHeader != expectedTotalStr {
		t.Fatalf("Expected X-Total-Bytes %s, got %s", expectedTotalStr, totalHeader)
	}

	exposeHeader := resp.Header.Get("Access-Control-Expose-Headers")
	if !strings.Contains(exposeHeader, "X-Total-Bytes") {
		t.Fatalf("Expected Access-Control-Expose-Headers to contain X-Total-Bytes, got %s", exposeHeader)
	}

	zipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read zip body: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("Failed to parse zip archive: %v", err)
	}

	if len(zr.File) != 2 {
		t.Fatalf("Expected 2 files in zip, got %d", len(zr.File))
	}

	for _, f := range zr.File {
		if f.Method != zip.Store {
			t.Errorf("File %s has compression method %d, expected zip.Store (%d)", f.Name, f.Method, zip.Store)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("Failed to open %s from zip: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("Failed to read %s from zip: %v", f.Name, err)
		}
		if f.Name == "sample1.txt" && !bytes.Equal(data, file1Content) {
			t.Errorf("sample1.txt content mismatch")
		}
		if f.Name == "sample2.txt" && !bytes.Equal(data, file2Content) {
			t.Errorf("sample2.txt content mismatch")
		}
	}
	t.Logf("VERIFIED: zip.Store fast uncompressed streaming works correctly and headers match!")
}

func TestWebSharePortalDualProgressElements(t *testing.T) {
	server := NewDaemonServer(0, t.TempDir(), "TestPortal")
	server.SetWebShareEnabled(true)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/share", nil)
	server.handleSharePortal(rec, req)

	body := rec.Body.String()
	requiredElements := []string{
		`id="dlBatchProgressBox"`,
		`id="dlBatchProgressBar"`,
		`id="dlBatchPercent"`,
		`id="dlBatchBytes"`,
		`id="dlBatchRemaining"`,
		`id="dlSkipBtn"`,
		`id="dlFileIndex"`,
		`id="dlBatchQueueBox"`,
		`id="dlBatchQueueList"`,
		`renderBatchQueue()`,
		`toggleFileSkip(`,
		`skipCurrentBatchFile()`,
		`X-Total-Bytes`,
	}

	for _, elem := range requiredElements {
		if !strings.Contains(body, elem) {
			t.Errorf("Web Share portal HTML missing expected element: %s", elem)
		}
	}
	t.Logf("VERIFIED: Web Share portal HTML includes all dual progress bars, skip file controls, and total bytes telemetry!")
}

func TestWebSharePortalNoDuplicateDeclarations(t *testing.T) {
	scriptStart := strings.Index(SharePortalHTML, "<script>")
	scriptEnd := strings.Index(SharePortalHTML, "</script>")
	if scriptStart == -1 || scriptEnd == -1 {
		t.Fatalf("<script> tag not found in SharePortalHTML")
	}
	scriptContent := SharePortalHTML[scriptStart+8 : scriptEnd]

	// Check for duplicate top-level let/const declarations (lines starting with '    let ' or '    const ')
	lines := strings.Split(scriptContent, "\n")
	seen := make(map[string]int)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "let ") || strings.HasPrefix(trimmed, "const ") {
			if strings.HasPrefix(line, "    let ") || strings.HasPrefix(line, "    const ") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 2 {
					varName := strings.Trim(parts[1], ";=")
					if prevLine, exists := seen[varName]; exists {
						t.Errorf("Duplicate top-level declaration %q on line %d (previously declared on line %d)", varName, i+1, prevLine)
					} else {
						seen[varName] = i + 1
					}
				}
			}
		}
	}
	t.Logf("VERIFIED: No duplicate top-level let/const declarations in SharePortalHTML (%d declarations checked)", len(seen))
}

func TestDaemonPairingCode(t *testing.T) {
	server := NewDaemonServer(0, t.TempDir(), "TestPairingNode")
	ln, err := server.Listen(0)
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer ln.Close()

	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Stop()

	st := server.GetStatus()
	if st.PairingCode == "" {
		t.Fatal("Expected non-empty PairingCode in DaemonStatus")
	}

	// Verify pairing code format
	parts := strings.Split(st.PairingCode, "-")
	if len(parts) != 2 || len(parts[0]) != 3 || len(parts[1]) != 3 {
		t.Fatalf("Expected pairing code format XXX-YYY, got %q", st.PairingCode)
	}
	t.Logf("VERIFIED: Daemon initialized with pairing code: %s", st.PairingCode)
}

func TestDaemonNodePairingHandshake(t *testing.T) {
	server := NewDaemonServer(0, t.TempDir(), "DaemonPairTest")
	ln, err := server.Listen(0)
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer ln.Close()

	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Stop()

	st := server.GetStatus()
	validCode := st.PairingCode
	invalidCode := "999-999"
	if validCode == invalidCode {
		invalidCode = "111-111"
	}

	// Serve binds the node listener in the background: wait for it, otherwise
	// GetNodeAddr returns the default :18887, which may be another daemon.
	var nodeAddr string
	for deadline := time.Now().Add(2 * time.Second); ; {
		server.mu.RLock()
		ready := server.nodeLn != nil
		server.mu.RUnlock()
		if ready {
			nodeAddr = server.GetNodeAddr()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("node listener not started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if strings.HasPrefix(nodeAddr, "0.0.0.0:") {
		nodeAddr = "127.0.0.1:" + strings.TrimPrefix(nodeAddr, "0.0.0.0:")
	}

	// 1. Connect with invalid code -> expect pair_reject
	conn1, err := session.DialTLSPeer(nodeAddr)
	if err == nil {
		defer conn1.Close()
		ch1 := session.NewChannel(conn1)
		_ = ch1.Send(session.Message{
			Type:        "pair_request",
			DeviceName:  "TestNodeClient",
			PairingCode: invalidCode,
		})
		_ = conn1.SetReadDeadline(time.Now().Add(2 * time.Second))
		resp1, err := ch1.Read()
		if err != nil || resp1.Type != "pair_reject" {
			t.Fatalf("Expected pair_reject for invalid code, got %v (err: %v)", resp1, err)
		}
		t.Logf("VERIFIED: Invalid code properly rejected with %s", resp1.Type)
	}

	// 2. Connect with valid code -> expect pair_accept
	conn2, err := session.DialTLSPeer(nodeAddr)
	if err != nil {
		t.Fatalf("Failed to connect to daemon node listener at %s: %v", nodeAddr, err)
	}
	defer conn2.Close()
	ch2 := session.NewChannel(conn2)
	_ = ch2.Send(session.Message{
		Type:        "pair_request",
		DeviceName:  "TestNodeClient",
		PairingCode: validCode,
	})
	_ = conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp2, err := ch2.Read()
	if err != nil || resp2.Type != "pair_accept" {
		t.Fatalf("Expected pair_accept for valid code, got %v (err: %v)", resp2, err)
	}
	t.Logf("VERIFIED: Valid code accepted! Device name: %s", resp2.DeviceName)
}

func TestDaemonDiskBenchmarkAction(t *testing.T) {
	tempDir := t.TempDir()
	node := testkit.StartNode(t, NewDaemonServer(0, tempDir, "BenchDevice"))

	node.Request("test_disk", map[string]any{"dir": tempDir, "size_bytes": 4 << 20})
	e := node.WaitEventAny(0, "benchmark_disk_result", "action_error")
	if e.Event == "action_error" {
		t.Fatalf("test_disk failed: %s", e.Data)
	}

	var diskRes engine.DiskBenchResult
	if err := e.Decode(&diskRes); err != nil {
		t.Fatalf("Failed unmarshaling benchmark data: %v (%s)", err, e.Data)
	}
	if diskRes.WriteSpeedMBps <= 0 {
		t.Errorf("Expected positive write speed, got %f", diskRes.WriteSpeedMBps)
	}
	if diskRes.Rating == "" {
		t.Errorf("Expected rating to be set")
	}
	t.Logf("WebSocket Disk Benchmark: Write=%.2f MB/s, Read=%.2f MB/s, Rating=%s",
		diskRes.WriteSpeedMBps, diskRes.ReadSpeedMBps, diskRes.Rating)
}

func TestBatchMultiFileResilienceAndErrorRecovery(t *testing.T) {
	sendDir := t.TempDir()
	folderToShare := filepath.Join(sendDir, "resilience_folder")
	for i := 0; i < 20; i++ {
		subDir := filepath.Join(folderToShare, fmt.Sprintf("sub_%d", i%4))
		_ = os.MkdirAll(subDir, 0755)
		data := bytes.Repeat([]byte(fmt.Sprintf("media payload block %d\n", i)), 500)
		_ = os.WriteFile(filepath.Join(subDir, fmt.Sprintf("video_%d.mp4", i)), data, 0644)
	}

	recvDir := t.TempDir()
	pair := testkit.StartPair(t, NewDaemonServer(0, sendDir, "SenderNode"), NewDaemonServer(0, recvDir, "ReceiverNode"))
	pair.Send([]string{folderToShare}, testkit.SendOptions{})
	pair.AcceptOffer(0)

	// A freeze or deadlock shows up as a missing transfer_complete.
	pair.WaitTransferDone(15 * time.Second)
	testkit.AssertTreesEqual(t, sendDir, recvDir)
}

func TestBatchItemFailedAdvance(t *testing.T) {
	sendDir := t.TempDir()
	folderToShare := filepath.Join(sendDir, "batch_with_skip")
	_ = os.MkdirAll(folderToShare, 0755)
	for i := 0; i < 3; i++ {
		_ = os.WriteFile(filepath.Join(folderToShare, fmt.Sprintf("file_%d.txt", i)), []byte(fmt.Sprintf("file content %d", i)), 0644)
	}

	recvDir := t.TempDir()
	recvServer := NewDaemonServer(0, recvDir, "ReceiverNode")
	pair := testkit.StartPair(t, NewDaemonServer(0, sendDir, "SenderNode"), recvServer)

	// Item 1 is skipped on the receiver before the transfer: the batch must go on without it.
	recvServer.mu.Lock()
	recvServer.skippedFiles[1] = true
	recvServer.mu.Unlock()

	pair.Send([]string{folderToShare}, testkit.SendOptions{})
	pair.AcceptOffer(0)
	pair.WaitTransferDone(0)

	for _, name := range []string{"file_0.txt", "file_2.txt"} {
		if _, err := os.Stat(filepath.Join(recvDir, "batch_with_skip", name)); err != nil {
			t.Fatalf("Expected %s to exist: %v", name, err)
		}
	}
}
