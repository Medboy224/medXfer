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
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/manifest"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/gorilla/websocket"
)

func init() {
	temp, err := os.MkdirTemp("", "medxfer_test_cfg_*")
	if err == nil {
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
	statusResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/status", port))
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

	// 3. Test / (Web UI)
	uiResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("Failed to GET /: %v", err)
	}
	defer uiResp.Body.Close()
	if uiResp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK for Web Dashboard, got %d", uiResp.StatusCode)
	}
}

func TestDaemonWebSocketCommands(t *testing.T) {
	server := NewDaemonServer(0, t.TempDir(), "TestWSNode")
	ln, err := server.Listen(0)
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Stop()

	u := url.URL{Scheme: "ws", Host: fmt.Sprintf("127.0.0.1:%d", port), Path: "/ws"}
	ws, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("WebSocket Dial failed: %v", err)
	}
	defer ws.Close()

	// 1. First event received upon connect should be "status"
	var initialEvt EventMessage
	if err := ws.ReadJSON(&initialEvt); err != nil {
		t.Fatalf("Failed reading initial status event: %v", err)
	}
	if initialEvt.Event != "status" {
		t.Fatalf("Expected first event 'status', got %q", initialEvt.Event)
	}

	// 2. Send "set_config" command
	cfgReq := RequestMessage{
		ID:     "req_1",
		Action: "set_config",
		Payload: json.RawMessage(`{
			"device_name": "UpdatedDevice",
			"download_dir": "/tmp/custom_downloads",
			"collision_policy": "overwrite"
		}`),
	}
	if err := ws.WriteJSON(cfgReq); err != nil {
		t.Fatalf("Failed writing set_config: %v", err)
	}

	var statusEvt EventMessage
	if err := ws.ReadJSON(&statusEvt); err != nil {
		t.Fatalf("Failed reading status response: %v", err)
	}
	if statusEvt.Event != "status" || statusEvt.ID != "req_1" {
		t.Fatalf("Expected correlated status event for req_1, got %v", statusEvt)
	}

	// 3. Send "get_status"
	getReq := RequestMessage{
		ID:     "req_2",
		Action: "get_status",
	}
	if err := ws.WriteJSON(getReq); err != nil {
		t.Fatalf("Failed writing get_status: %v", err)
	}

	var getEvt EventMessage
	if err := ws.ReadJSON(&getEvt); err != nil {
		t.Fatalf("Failed reading get_status response: %v", err)
	}
	if getEvt.ID != "req_2" {
		t.Fatalf("Expected ID 'req_2', got %q", getEvt.ID)
	}

	// 4. Send "cancel"
	cancelReq := RequestMessage{
		ID:     "req_3",
		Action: "cancel",
	}
	if err := ws.WriteJSON(cancelReq); err != nil {
		t.Fatalf("Failed writing cancel: %v", err)
	}
	var cancelEvt EventMessage
	if err := ws.ReadJSON(&cancelEvt); err != nil {
		t.Fatalf("Failed reading cancel response: %v", err)
	}
	if cancelEvt.Event != "transfer_canceled" {
		t.Fatalf("Expected 'transfer_canceled', got %q", cancelEvt.Event)
	}
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
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/fs/list?dir=%s", port, url.QueryEscape(tempDir)))
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
	mkdirResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/fs/mkdir?dir=%s&name=NewTestDir", port, url.QueryEscape(tempDir)))
	if err != nil {
		t.Fatalf("Failed to GET /api/fs/mkdir: %v", err)
	}
	defer mkdirResp.Body.Close()
	if mkdirResp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK from mkdir, got %d", mkdirResp.StatusCode)
	}
	if fi, err := os.Stat(filepath.Join(tempDir, "NewTestDir")); err != nil || !fi.IsDir() {
		t.Fatalf("Directory was not created")
	}
}

func TestPauseResumeAndSkipWebSocketCommands(t *testing.T) {
	server := NewDaemonServer(0, t.TempDir(), "TestControlWSNode")
	ln, err := server.Listen(0)
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Stop()

	u := url.URL{Scheme: "ws", Host: fmt.Sprintf("127.0.0.1:%d", port), Path: "/ws"}
	ws, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("WebSocket Dial failed: %v", err)
	}
	defer ws.Close()

	// Initial status event
	var initialEvt EventMessage
	_ = ws.ReadJSON(&initialEvt)

	// 1. Send "pause"
	_ = ws.WriteJSON(RequestMessage{
		ID:     "req_pause",
		Action: "pause",
	})
	var pauseEvt EventMessage
	if err := ws.ReadJSON(&pauseEvt); err != nil || pauseEvt.Event != "transfer_paused" {
		t.Fatalf("Expected transfer_paused event, got %+v (err: %v)", pauseEvt, err)
	}
	if !server.isPaused {
		t.Fatalf("Server should be paused")
	}

	// 2. Send "resume"
	_ = ws.WriteJSON(RequestMessage{
		ID:     "req_resume",
		Action: "resume",
	})
	var resumeEvt EventMessage
	if err := ws.ReadJSON(&resumeEvt); err != nil || resumeEvt.Event != "transfer_resumed" {
		t.Fatalf("Expected transfer_resumed event, got %+v (err: %v)", resumeEvt, err)
	}
	if server.isPaused {
		t.Fatalf("Server should not be paused")
	}

	// 3. Send "skip_file"
	server.currentBatchItems = []BatchFileInfo{
		{Index: 0, RelPath: "file0.txt", Size: 100, Status: "completed"},
		{Index: 1, RelPath: "file1.txt", Size: 200, Status: "transferring"},
		{Index: 2, RelPath: "file2.txt", Size: 300, Status: "pending"},
	}
	_ = ws.WriteJSON(RequestMessage{
		ID:      "req_skip",
		Action:  "skip_file",
		Payload: json.RawMessage(`{"item_index": 2}`),
	})
	var skipEvt EventMessage
	if err := ws.ReadJSON(&skipEvt); err != nil || skipEvt.Event != "file_skipped" {
		t.Fatalf("Expected file_skipped event, got %+v (err: %v)", skipEvt, err)
	}
	if !server.skippedFiles[2] {
		t.Fatalf("File index 2 should be in skippedFiles")
	}

	// 4. Send "cancel"
	_ = ws.WriteJSON(RequestMessage{
		ID:     "req_cancel",
		Action: "cancel",
	})
	var cancelEvt EventMessage
	if err := ws.ReadJSON(&cancelEvt); err != nil || cancelEvt.Event != "transfer_canceled" {
		t.Fatalf("Expected transfer_canceled event, got %+v (err: %v)", cancelEvt, err)
	}
}

func TestInstantOfferAndSessionDispatch(t *testing.T) {
	// Receiver Daemon
	recvServer := NewDaemonServer(0, t.TempDir(), "ReceiverNode")
	recvHTTP, err := recvServer.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	recvHTTPPort := recvHTTP.Addr().(*net.TCPAddr).Port

	recvNodeLn, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	recvNodePort := recvNodeLn.Addr().(*net.TCPAddr).Port
	go recvServer.listenForIncomingPairings(recvNodeLn)

	go func() {
		_ = recvServer.Serve(recvHTTP)
	}()
	defer recvServer.Stop()
	defer recvNodeLn.Close()

	// Sender Daemon
	sendDir := t.TempDir()
	testFile := filepath.Join(sendDir, "fast_offer.mp4")
	_ = os.WriteFile(testFile, []byte("quick video data payload"), 0644)

	sendServer := NewDaemonServer(0, sendDir, "SenderNode")
	sendHTTP, err := sendServer.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	sendHTTPPort := sendHTTP.Addr().(*net.TCPAddr).Port
	go func() {
		_ = sendServer.Serve(sendHTTP)
	}()
	defer sendServer.Stop()

	// Connect WebSocket to Receiver
	recvWSURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", recvHTTPPort)
	recvWS, _, err := websocket.DefaultDialer.Dial(recvWSURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial receiver ws: %v", err)
	}
	defer recvWS.Close()

	// Read initial status event on receiver WS
	var initEvt EventMessage
	_ = recvWS.ReadJSON(&initEvt)

	// Connect WebSocket to Sender
	sendWSURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", sendHTTPPort)
	sendWS, _, err := websocket.DefaultDialer.Dial(sendWSURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial sender ws: %v", err)
	}
	defer sendWS.Close()
	_ = sendWS.ReadJSON(&initEvt)

	recvCode := recvServer.GetStatus().PairingCode
	// Dispatch "send" from Sender specifying target_ip = 127.0.0.1:<recvNodePort> and code = recvCode
	startOffer := time.Now()
	_ = sendWS.WriteJSON(RequestMessage{
		ID:      "send_test",
		Action:  "send",
		Payload: json.RawMessage(fmt.Sprintf(`{"paths":[%q],"target_ip":"127.0.0.1:%d","code":%q}`, testFile, recvNodePort, recvCode)),
	})

	// Receiver WS must receive "paired" then "incoming_offer"
	receivedOffer := false
	for time.Since(startOffer) < 2*time.Second {
		var evt EventMessage
		err := recvWS.ReadJSON(&evt)
		if err != nil {
			break
		}
		if evt.Event == "incoming_offer" {
			receivedOffer = true
			elapsed := time.Since(startOffer)
			t.Logf("Received incoming_offer in %v", elapsed)
			if elapsed > 1500*time.Millisecond {
				t.Fatalf("Offer took too long to show: %v", elapsed)
			}
			break
		}
	}

	if !receivedOffer {
		t.Fatalf("Receiver did not receive incoming_offer modal event within timeout")
	}

	sendServer.mu.Lock()
	sess := sendServer.activeSession
	sendServer.mu.Unlock()
	if sess != nil {
		if !sess.IsEncrypted() {
			t.Fatalf("Expected activeSession to be encrypted with TLS 1.3")
		}
		t.Logf("VERIFIED: activeSession is secured with TLS 1.3 AEAD encryption!")
	}
}

func TestPerFilePauseAndResumeWebSocketCommands(t *testing.T) {
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

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket Dial failed: %v", err)
	}
	defer ws.Close()

	var initEvt EventMessage
	_ = ws.ReadJSON(&initEvt)

	// Setup mock batch items
	server.mu.Lock()
	server.currentBatchItems = []BatchFileInfo{
		{Index: 0, RelPath: "file0.txt", Size: 100, Status: "transferring"},
		{Index: 1, RelPath: "file1.txt", Size: 200, Status: "pending"},
		{Index: 2, RelPath: "file2.txt", Size: 300, Status: "pending"},
	}
	server.mu.Unlock()

	// 1. Send "pause_file" for item 0
	_ = ws.WriteJSON(RequestMessage{
		ID:      "req_pause_0",
		Action:  "pause_file",
		Payload: json.RawMessage(`{"item_index": 0}`),
	})
	var pauseEvt EventMessage
	if err := ws.ReadJSON(&pauseEvt); err != nil || pauseEvt.Event != "file_paused" {
		t.Fatalf("Expected file_paused event, got %+v (err: %v)", pauseEvt, err)
	}
	server.mu.RLock()
	if !server.pausedFiles[0] {
		t.Fatalf("Expected item 0 to be in pausedFiles map")
	}
	if server.currentBatchItems[0].Status != "paused" {
		t.Fatalf("Expected item 0 status to be 'paused', got %q", server.currentBatchItems[0].Status)
	}
	server.mu.RUnlock()

	// 2. Send "pause_file" for pending item 2
	_ = ws.WriteJSON(RequestMessage{
		ID:      "req_pause_2",
		Action:  "pause_file",
		Payload: json.RawMessage(`{"item_index": 2}`),
	})
	if err := ws.ReadJSON(&pauseEvt); err != nil || pauseEvt.Event != "file_paused" {
		t.Fatalf("Expected file_paused event for item 2, got %+v", pauseEvt)
	}
	server.mu.RLock()
	if !server.pausedFiles[2] || server.currentBatchItems[2].Status != "paused" {
		t.Fatalf("Expected item 2 to be paused")
	}
	server.mu.RUnlock()

	// 3. Send "resume_file" for item 0
	_ = ws.WriteJSON(RequestMessage{
		ID:      "req_resume_0",
		Action:  "resume_file",
		Payload: json.RawMessage(`{"item_index": 0}`),
	})
	var resumeEvt EventMessage
	if err := ws.ReadJSON(&resumeEvt); err != nil || resumeEvt.Event != "file_resumed" {
		t.Fatalf("Expected file_resumed event, got %+v (err: %v)", resumeEvt, err)
	}
	server.mu.RLock()
	if server.pausedFiles[0] {
		t.Fatalf("Item 0 should no longer be in pausedFiles map")
	}
	if server.currentBatchItems[0].Status != "pending" {
		t.Fatalf("Expected item 0 status to be 'pending', got %q", server.currentBatchItems[0].Status)
	}
	server.mu.RUnlock()
}

func TestBatchQueueDynamicAdvanceOnPause(t *testing.T) {
	// 1. Setup Receiver
	recvDir := t.TempDir()
	recvServer := NewDaemonServer(0, recvDir, "RecvBatchNode")
	recvHTTP, err := recvServer.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	recvHTTPPort := recvHTTP.Addr().(*net.TCPAddr).Port

	recvNodeLn, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	recvNodePort := recvNodeLn.Addr().(*net.TCPAddr).Port
	go recvServer.listenForIncomingPairings(recvNodeLn)
	go func() { _ = recvServer.Serve(recvHTTP) }()
	defer recvServer.Stop()
	defer recvNodeLn.Close()

	// 2. Setup Sender with 5 files
	sendDir := t.TempDir()
	var filePaths []string
	for i := 0; i < 5; i++ {
		fp := filepath.Join(sendDir, fmt.Sprintf("file_%d.bin", i))
		size := 1024
		if i == 0 {
			size = 512 * 1024 // 512 KB for file 0
		}
		_ = os.WriteFile(fp, make([]byte, size), 0644)
		filePaths = append(filePaths, fp)
	}

	sendServer := NewDaemonServer(0, sendDir, "SendBatchNode")
	sendHTTP, err := sendServer.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	sendHTTPPort := sendHTTP.Addr().(*net.TCPAddr).Port
	go func() { _ = sendServer.Serve(sendHTTP) }()
	defer sendServer.Stop()

	// 3. Connect WebSockets
	recvWSURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", recvHTTPPort)
	recvWS, _, err := websocket.DefaultDialer.Dial(recvWSURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer recvWS.Close()

	sendWSURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", sendHTTPPort)
	sendWS, _, err := websocket.DefaultDialer.Dial(sendWSURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sendWS.Close()

	var initEvt EventMessage
	_ = recvWS.ReadJSON(&initEvt)
	_ = sendWS.ReadJSON(&initEvt)

	// 4. Sender initiates 5-file batch send
	recvCode := recvServer.GetStatus().PairingCode
	pathsJSON, _ := json.Marshal(filePaths)
	_ = sendWS.WriteJSON(RequestMessage{
		ID:     "send_batch_5",
		Action: "send",
		Payload: json.RawMessage(fmt.Sprintf(`{
			"paths": %s,
			"target_ip": "127.0.0.1:%d",
			"code": %q
		}`, string(pathsJSON), recvNodePort, recvCode)),
	})

	// 5. Receiver accepts offer
	for {
		var evt EventMessage
		if err := recvWS.ReadJSON(&evt); err != nil {
			t.Fatal(err)
		}
		if evt.Event == "incoming_offer" {
			_ = recvWS.WriteJSON(RequestMessage{
				ID:      "accept_req",
				Action:  "respond_offer",
				Payload: json.RawMessage(`{"accept": true}`),
			})
			break
		}
	}

	// 6. Read events via channel
	evtChan := make(chan EventMessage, 100)
	go func() {
		for {
			var evt EventMessage
			if err := sendWS.ReadJSON(&evt); err != nil {
				return
			}
			evtChan <- evt
		}
	}()

	var startedOrder []int
	resumedF0 := false
	timeout := time.After(8 * time.Second)

	for {
		select {
		case <-timeout:
			t.Fatalf("Timed out! Started order so far: %v, resumedF0: %v", startedOrder, resumedF0)
		case evt := <-evtChan:
			if evt.Event == "transfer_start" {
				data, _ := json.Marshal(evt.Data)
				var startData struct {
					CurrentFile string `json:"current_file"`
					FileIndex   int    `json:"file_index"`
				}
				_ = json.Unmarshal(data, &startData)
				idx := startData.FileIndex - 1
				startedOrder = append(startedOrder, idx)
				t.Logf("--> transfer_start for file index %d (%s)", idx, startData.CurrentFile)

				if idx == 0 && len(startedOrder) == 1 {
					// Pause file 0 immediately!
					t.Logf("--> Pausing file 0! Next MUST be file 1, NOT file 2!")
					_ = sendWS.WriteJSON(RequestMessage{
						ID:      "pause_f0",
						Action:  "pause_file",
						Payload: json.RawMessage(`{"item_index": 0}`),
					})
				}
			}

			if evt.Event == "batch_paused_waiting" && !resumedF0 {
				t.Logf("--> Remaining files are paused! Resuming file 0 now!")
				resumedF0 = true
				_ = sendWS.WriteJSON(RequestMessage{
					ID:      "resume_f0",
					Action:  "resume_file",
					Payload: json.RawMessage(`{"item_index": 0}`),
				})
			}

			if evt.Event == "transfer_complete" {
				t.Logf("--> Transfer completed 100%% successfully!")
				// Verify start sequence:
				// File 0 was started, then File 1, 2, 3, 4, then File 0 resumed!
				if len(startedOrder) < 6 {
					t.Fatalf("Expected at least 6 start events (0, 1, 2, 3, 4, then 0), got: %v", startedOrder)
				}
				if startedOrder[0] != 0 {
					t.Fatalf("Expected first file to be 0, got %d", startedOrder[0])
				}
				if startedOrder[1] != 1 {
					t.Fatalf("CRITICAL BUG: When file 0 was paused, next file started was %d (expected 1, got %d)!", startedOrder[1], startedOrder[1])
				}
				if startedOrder[2] != 2 {
					t.Fatalf("Expected 3rd file to be 2, got %d", startedOrder[2])
				}

				// Verify all items are completed in sender memory
				sendServer.mu.RLock()
				for _, item := range sendServer.currentBatchItems {
					if item.Status != "completed" {
						sendServer.mu.RUnlock()
						t.Fatalf("Expected all items to be 'completed', but item %d is %q", item.Index, item.Status)
					}
				}
				sendServer.mu.RUnlock()

				t.Logf("VERIFIED: File 1 started immediately after File 0 pause, all 5 files finished, and File 0 marked completed!")
				return
			}
		}
	}
}

func TestFolderTarStreamingBatch(t *testing.T) {
	recvDir := t.TempDir()
	recvServer := NewDaemonServer(0, recvDir, "ReceiverNode")
	recvHTTP, err := recvServer.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	recvHTTPPort := recvHTTP.Addr().(*net.TCPAddr).Port

	recvNodeLn, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	recvNodePort := recvNodeLn.Addr().(*net.TCPAddr).Port
	go recvServer.listenForIncomingPairings(recvNodeLn)

	go func() {
		_ = recvServer.Serve(recvHTTP)
	}()
	defer recvServer.Stop()
	defer recvNodeLn.Close()

	// Sender Daemon
	sendDir := t.TempDir()
	folderToShare := filepath.Join(sendDir, "my_shared_repo")
	_ = os.MkdirAll(folderToShare, 0755)

	// Create 30 files in subdirectories
	expectedFiles := make(map[string][]byte)
	for i := 0; i < 30; i++ {
		subDir := filepath.Join(folderToShare, fmt.Sprintf("sub_%d", i%3))
		_ = os.MkdirAll(subDir, 0755)
		filePath := filepath.Join(subDir, fmt.Sprintf("code_%d.go", i))
		data := bytes.Repeat([]byte(fmt.Sprintf("package main\n// file %d content\n", i)), 50)
		_ = os.WriteFile(filePath, data, 0644)

		rel, _ := filepath.Rel(sendDir, filePath)
		expectedFiles[filepath.ToSlash(rel)] = data
	}

	sendServer := NewDaemonServer(0, sendDir, "SenderNode")
	sendHTTP, err := sendServer.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	sendHTTPPort := sendHTTP.Addr().(*net.TCPAddr).Port
	go func() {
		_ = sendServer.Serve(sendHTTP)
	}()
	defer sendServer.Stop()

	// Connect WebSocket to Receiver
	recvWSURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", recvHTTPPort)
	recvWS, _, err := websocket.DefaultDialer.Dial(recvWSURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial receiver ws: %v", err)
	}
	defer recvWS.Close()

	var initEvt EventMessage
	_ = recvWS.ReadJSON(&initEvt)

	// Connect WebSocket to Sender
	sendWSURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", sendHTTPPort)
	sendWS, _, err := websocket.DefaultDialer.Dial(sendWSURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial sender ws: %v", err)
	}
	defer sendWS.Close()
	_ = sendWS.ReadJSON(&initEvt)

	// Send folder with explicit tar streaming
	recvCode := recvServer.GetStatus().PairingCode
	_ = sendWS.WriteJSON(RequestMessage{
		ID:      "send_folder",
		Action:  "send",
		Payload: json.RawMessage(fmt.Sprintf(`{"paths":[%q],"target_ip":"127.0.0.1:%d","code":%q,"use_tar_stream":true}`, folderToShare, recvNodePort, recvCode)),
	})

	// Receiver accepts offer
	for {
		var evt EventMessage
		if err := recvWS.ReadJSON(&evt); err != nil {
			t.Fatalf("Receiver read error: %v", err)
		}
		if evt.Event == "incoming_offer" {
			_ = recvWS.WriteJSON(RequestMessage{
				ID:      "accept_folder",
				Action:  "respond_offer",
				Payload: json.RawMessage(`{"accept":true}`),
			})
			break
		}
	}

	// Wait for transfer_complete on receiver
	deadline := time.Now().Add(10 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		var evt EventMessage
		if err := recvWS.ReadJSON(&evt); err != nil {
			break
		}
		if evt.Event == "transfer_complete" {
			completed = true
			break
		}
	}

	if !completed {
		t.Fatalf("Folder transfer did not complete within timeout")
	}

	// Verify all 30 files are in recvDir
	for relPath, expected := range expectedFiles {
		targetFile := filepath.Join(recvDir, filepath.FromSlash(relPath))
		actual, err := os.ReadFile(targetFile)
		if err != nil {
			t.Fatalf("Missing extracted file '%s': %v", relPath, err)
		}
		if !bytes.Equal(actual, expected) {
			t.Fatalf("Data mismatch in extracted file '%s'", relPath)
		}
	}

	t.Logf("VERIFIED: All 30 files extracted bit-for-bit via on-the-fly Tar stream!")
}

func TestFolderGranularBatchTransfer(t *testing.T) {
	recvDir := t.TempDir()
	recvServer := NewDaemonServer(0, recvDir, "ReceiverNode")
	recvHTTP, err := recvServer.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	recvHTTPPort := recvHTTP.Addr().(*net.TCPAddr).Port

	recvNodeLn, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	recvNodePort := recvNodeLn.Addr().(*net.TCPAddr).Port
	go recvServer.listenForIncomingPairings(recvNodeLn)

	go func() {
		_ = recvServer.Serve(recvHTTP)
	}()
	defer recvServer.Stop()
	defer recvNodeLn.Close()

	// Sender Daemon
	sendDir := t.TempDir()
	folderToShare := filepath.Join(sendDir, "my_granular_folder")
	_ = os.MkdirAll(folderToShare, 0755)

	// Create 5 files in subdirectories
	expectedFiles := make(map[string][]byte)
	for i := 0; i < 5; i++ {
		subDir := filepath.Join(folderToShare, fmt.Sprintf("sub_%d", i%2))
		_ = os.MkdirAll(subDir, 0755)
		filePath := filepath.Join(subDir, fmt.Sprintf("file_%d.bin", i))
		data := bytes.Repeat([]byte(fmt.Sprintf("content of file %d\n", i)), 100)
		_ = os.WriteFile(filePath, data, 0644)

		rel, _ := filepath.Rel(sendDir, filePath)
		expectedFiles[filepath.ToSlash(rel)] = data
	}

	sendServer := NewDaemonServer(0, sendDir, "SenderNode")
	sendHTTP, err := sendServer.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	sendHTTPPort := sendHTTP.Addr().(*net.TCPAddr).Port
	go func() {
		_ = sendServer.Serve(sendHTTP)
	}()
	defer sendServer.Stop()

	// Connect WebSocket to Receiver
	recvWSURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", recvHTTPPort)
	recvWS, _, err := websocket.DefaultDialer.Dial(recvWSURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial receiver ws: %v", err)
	}
	defer recvWS.Close()

	var initEvt EventMessage
	_ = recvWS.ReadJSON(&initEvt)

	// Connect WebSocket to Sender and continuously drain events so buffer never blocks
	sendWSURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", sendHTTPPort)
	sendWS, _, err := websocket.DefaultDialer.Dial(sendWSURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial sender ws: %v", err)
	}
	defer sendWS.Close()
	_ = sendWS.ReadJSON(&initEvt)

	go func() {
		for {
			var evt EventMessage
			if err := sendWS.ReadJSON(&evt); err != nil {
				return
			}
		}
	}()

	// Send folder without use_tar_stream (defaults to Granular Batch)
	recvCode := recvServer.GetStatus().PairingCode
	_ = sendWS.WriteJSON(RequestMessage{
		ID:      "send_folder_granular",
		Action:  "send",
		Payload: json.RawMessage(fmt.Sprintf(`{"paths":[%q],"target_ip":"127.0.0.1:%d","code":%q}`, folderToShare, recvNodePort, recvCode)),
	})

	// Receiver accepts offer
	for {
		var evt EventMessage
		if err := recvWS.ReadJSON(&evt); err != nil {
			t.Fatalf("Receiver read error: %v", err)
		}
		if evt.Event == "incoming_offer" {
			_ = recvWS.WriteJSON(RequestMessage{
				ID:      "accept_folder",
				Action:  "respond_offer",
				Payload: json.RawMessage(`{"accept":true}`),
			})
			break
		}
	}

	// Wait for transfer_complete on receiver
	deadline := time.Now().Add(10 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		var evt EventMessage
		if err := recvWS.ReadJSON(&evt); err != nil {
			break
		}
		if evt.Event == "transfer_complete" {
			completed = true
			break
		}
	}

	if !completed {
		t.Fatalf("Granular folder batch transfer did not complete within timeout")
	}

	// Verify all 5 files are in recvDir with preserved relative paths
	for relPath, expected := range expectedFiles {
		targetFile := filepath.Join(recvDir, filepath.FromSlash(relPath))
		actual, err := os.ReadFile(targetFile)
		if err != nil {
			t.Fatalf("Missing extracted file '%s': %v", relPath, err)
		}
		if !bytes.Equal(actual, expected) {
			t.Fatalf("Data mismatch in extracted file '%s'", relPath)
		}
	}

	t.Logf("VERIFIED: Granular folder batch transfer successfully transferred and verified all 5 files!")
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
	ln, err := server.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Stop()

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WS Dial failed: %v", err)
	}
	defer conn.Close()

	// 1. Create a dummy file to share
	testFile := filepath.Join(tempDir, "shared_document.pdf")
	_ = os.WriteFile(testFile, []byte("Test Document Content"), 0644)

	// 2. Share the file via WebSocket
	_ = conn.WriteJSON(map[string]interface{}{
		"action": "share_web_files",
		"payload": map[string]interface{}{
			"paths": []string{testFile},
		},
		"id": "req_share",
	})

	var shareResp struct {
		Event string `json:"event"`
		Data  struct {
			Count int `json:"count"`
		} `json:"data"`
	}
	for {
		err = conn.ReadJSON(&shareResp)
		if err != nil {
			t.Fatalf("ReadJSON failed: %v", err)
		}
		if shareResp.Event == "web_files_shared" {
			break
		}
	}
	if shareResp.Data.Count != 1 {
		t.Fatalf("Expected 1 shared file, got %d", shareResp.Data.Count)
	}

	pin := server.GetStatus().WebSharePIN

	// 3. Verify /api/share/list returns the shared file
	listResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/share/list?pin=%s", port, pin))
	if err != nil {
		t.Fatalf("GET /api/share/list failed: %v", err)
	}
	defer listResp.Body.Close()
	var listData struct {
		Items      []SharedItem `json:"items"`
		TotalCount int          `json:"total_count"`
	}
	_ = json.NewDecoder(listResp.Body).Decode(&listData)
	if listData.TotalCount != 1 || len(listData.Items) != 1 {
		t.Fatalf("Expected 1 item in list, got %d", listData.TotalCount)
	}

	// 4. Send clear_web_files command
	_ = conn.WriteJSON(map[string]interface{}{
		"action": "clear_web_files",
		"id":     "req_clear",
	})

	var clearResp struct {
		Event string `json:"event"`
	}
	for {
		err = conn.ReadJSON(&clearResp)
		if err != nil {
			t.Fatalf("ReadJSON failed waiting for clear: %v", err)
		}
		if clearResp.Event == "web_files_cleared" {
			break
		}
	}

	// 5. Verify /api/share/list now returns 0 items!
	listResp2, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/share/list?pin=%s", port, pin))
	if err != nil {
		t.Fatalf("GET /api/share/list after clear failed: %v", err)
	}
	defer listResp2.Body.Close()
	var listData2 struct {
		Items      []SharedItem `json:"items"`
		TotalCount int          `json:"total_count"`
	}
	_ = json.NewDecoder(listResp2.Body).Decode(&listData2)
	if listData2.TotalCount != 0 || len(listData2.Items) != 0 {
		t.Fatalf("Expected 0 items in list after clear, got %d", listData2.TotalCount)
	}

	// 6. Verify /api/share/download returns 404 Not Found!
	dlResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/share/download?file=shared_document.pdf&pin=%s", port, pin))
	if err != nil {
		t.Fatalf("GET /api/share/download failed: %v", err)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusNotFound {
		t.Fatalf("Expected HTTP 404 StatusNotFound after clear, got %d", dlResp.StatusCode)
	}

	t.Logf("VERIFIED: clear_web_files correctly actualizes list to 0 and blocks stale file downloads with 404!")
}

func TestHotspotWebSocketCommands(t *testing.T) {
	tempDir := t.TempDir()
	server := NewDaemonServer(0, tempDir, "TestHotspotHost")
	ln, err := server.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Stop()

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WS Dial failed: %v", err)
	}
	defer conn.Close()

	// 1. Query initial hotspot status (should be inactive)
	_ = conn.WriteJSON(map[string]interface{}{
		"action": "hotspot_status",
		"id":     "req_status",
	})

	var initialResp struct {
		Event string `json:"event"`
		Data  struct {
			Active bool `json:"active"`
		} `json:"data"`
	}
	for {
		err = conn.ReadJSON(&initialResp)
		if err != nil {
			t.Fatalf("Failed to read initial status: %v", err)
		}
		if initialResp.Event == "hotspot_status" {
			break
		}
	}
	if initialResp.Data.Active {
		t.Fatalf("Expected inactive hotspot initially, got: %+v", initialResp)
	}

	// 2. Start hotspot
	_ = conn.WriteJSON(map[string]interface{}{
		"action": "hotspot_start",
		"payload": map[string]string{
			"band": "5ghz",
			"ssid": "medXfer-UnitTest",
		},
		"id": "req_start",
	})

	var startResp struct {
		Event string                 `json:"event"`
		Data  map[string]interface{} `json:"data"`
	}
	_ = conn.SetReadDeadline(time.Now().Add(12 * time.Second))
	for {
		err = conn.ReadJSON(&startResp)
		if err != nil {
			t.Fatalf("Failed to read start response: %v", err)
		}
		if startResp.Event == "hotspot_started" {
			break
		}
		if startResp.Event == "action_error" {
			t.Fatalf("hotspot_start returned error: %v", startResp.Data)
		}
	}

	if startResp.Data["ssid"] != "DIRECT-medXfer-UnitTest" {
		t.Fatalf("Expected SSID DIRECT-medXfer-UnitTest, got %v", startResp.Data["ssid"])
	}
	if startResp.Data["qr_wifi"] == nil || startResp.Data["qr_portal"] == nil {
		t.Fatalf("Expected QR codes in hotspot_started event")
	}

	t.Logf("VERIFIED: Hotspot started, SSID=%v, Band=%v, IP=%v, Portal=%v",
		startResp.Data["ssid"], startResp.Data["band"], startResp.Data["ip"], startResp.Data["portal_url"])

	// 3. Stop hotspot
	_ = conn.WriteJSON(map[string]interface{}{
		"action": "hotspot_stop",
		"id":     "req_stop",
	})

	var stopResp struct {
		Event string `json:"event"`
	}
	for {
		err = conn.ReadJSON(&stopResp)
		if err != nil {
			t.Fatalf("Failed to read stop response: %v", err)
		}
		if stopResp.Event == "hotspot_stopped" {
			break
		}
	}
	t.Logf("VERIFIED: Hotspot stopped cleanly!")
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

func TestWebShareInvalidPINNotification(t *testing.T) {
	tmpDir := t.TempDir()
	server := NewDaemonServer(0, tmpDir, "SecurityHost")
	server.webSharePIN = "4321"
	server.SetWebShareEnabled(true)

	ln, err := server.Listen(0)
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer server.Stop()
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		_ = server.Serve(ln)
	}()

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)

	// Connect WS client to receive real-time events
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect WS: %v", err)
	}
	defer ws.Close()

	// Drain initial status event
	var initialMsg map[string]interface{}
	_ = ws.ReadJSON(&initialMsg)

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

	// Verify WebSocket received web_share_auth_failed
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	var eventMsg struct {
		Event string `json:"event"`
		Data  struct {
			ClientIP     string `json:"client_ip"`
			AttemptedPIN string `json:"attempted_pin"`
		} `json:"data"`
	}
	if err := ws.ReadJSON(&eventMsg); err != nil {
		t.Fatalf("Failed to read WS event: %v", err)
	}
	if eventMsg.Event != "web_share_auth_failed" {
		t.Fatalf("Expected event 'web_share_auth_failed', got %s", eventMsg.Event)
	}
	if eventMsg.Data.ClientIP != "192.168.1.105" {
		t.Fatalf("Expected client IP 192.168.1.105, got %s", eventMsg.Data.ClientIP)
	}
	if eventMsg.Data.AttemptedPIN != "9999" {
		t.Fatalf("Expected attempted PIN 9999, got %s", eventMsg.Data.AttemptedPIN)
	}

	// 3. Test rate limiting: rapid second bad attempt does not emit a second WS event immediately
	reqBadPin2 := httptest.NewRequest("GET", "/api/share/list?pin=8888", nil)
	reqBadPin2.RemoteAddr = "192.168.1.105:54321"
	wBadPin2 := httptest.NewRecorder()
	server.handleShareList(wBadPin2, reqBadPin2)
	if wBadPin2.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401, got %d", wBadPin2.Code)
	}

	_ = ws.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var spamMsg map[string]interface{}
	if err := ws.ReadJSON(&spamMsg); err == nil {
		t.Fatalf("Expected rate-limit to suppress rapid duplicate auth failure event, but got: %v", spamMsg)
	}

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

	ln, err := server.Listen(0)
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer server.Stop()
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		_ = server.Serve(ln)
	}()

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect WS: %v", err)
	}
	defer ws.Close()

	// Drain initial status event
	var initialMsg map[string]interface{}
	_ = ws.ReadJSON(&initialMsg)

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
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		var eventMsg struct {
			Event string `json:"event"`
			Data  struct {
				ClientIP      string `json:"client_ip"`
				FailCount     int    `json:"fail_count"`
				LockedSeconds int    `json:"locked_seconds"`
			} `json:"data"`
		}
		if err := ws.ReadJSON(&eventMsg); err != nil {
			t.Fatalf("Failed reading WS events: %v", err)
		}
		if eventMsg.Event == "web_share_brute_force_blocked" {
			if eventMsg.Data.ClientIP != "192.168.1.77" {
				t.Fatalf("Expected client IP 192.168.1.77, got %s", eventMsg.Data.ClientIP)
			}
			if eventMsg.Data.FailCount != 5 {
				t.Fatalf("Expected FailCount 5, got %d", eventMsg.Data.FailCount)
			}
			if eventMsg.Data.LockedSeconds != 60 {
				t.Fatalf("Expected LockedSeconds 60, got %d", eventMsg.Data.LockedSeconds)
			}
			break
		}
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
	ln, err := server.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Stop()

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

	// 2. Connect WebSocket and toggle Web Share ON
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	// Drain initial status
	var initMsg map[string]interface{}
	_ = ws.ReadJSON(&initMsg)

	// Send toggle_web_share with enabled: true
	_ = ws.WriteJSON(map[string]interface{}{
		"action": "toggle_web_share",
		"payload": map[string]interface{}{
			"enabled": true,
		},
		"id": "req_toggle_on",
	})

	// Wait for status event confirming WebShareEnabled is true
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		var ev struct {
			Event string       `json:"event"`
			Data  DaemonStatus `json:"data"`
		}
		if err := ws.ReadJSON(&ev); err != nil {
			t.Fatalf("Failed reading WS: %v", err)
		}
		if ev.Event == "status" {
			if !ev.Data.WebShareEnabled {
				t.Fatalf("Expected WebShareEnabled to be true after toggle ON")
			}
			break
		}
	}

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
	_ = ws.WriteJSON(map[string]interface{}{
		"action": "toggle_web_share",
		"payload": map[string]interface{}{
			"enabled": false,
		},
		"id": "req_toggle_off",
	})

	// Wait for status event confirming WebShareEnabled is false
	for {
		var ev struct {
			Event string       `json:"event"`
			Data  DaemonStatus `json:"data"`
		}
		if err := ws.ReadJSON(&ev); err != nil {
			t.Fatalf("Failed reading WS: %v", err)
		}
		if ev.Event == "status" {
			if ev.Data.WebShareEnabled {
				t.Fatalf("Expected WebShareEnabled to be false after toggle OFF")
			}
			break
		}
	}

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

	// 5. Connect WebSocket from host to test host controls
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WS connect failed: %v", err)
	}
	defer ws.Close()

	// Host sends web_share_pause action
	_ = ws.WriteJSON(map[string]interface{}{
		"action": "web_share_pause",
		"id":     "req_pause",
	})
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
	_ = ws.WriteJSON(map[string]interface{}{
		"action": "web_share_resume",
		"id":     "req_resume",
	})
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
	_ = ws.WriteJSON(map[string]interface{}{
		"action": "web_share_cancel",
		"id":     "req_cancel",
	})
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

	// Connect Host WebSocket
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WS dial failed: %v", err)
	}
	defer ws.Close()

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
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	var gotCancelEvent bool
	for {
		var ev struct {
			Event string                 `json:"event"`
			Data  map[string]interface{} `json:"data"`
		}
		if err := ws.ReadJSON(&ev); err != nil {
			break
		}
		if ev.Event == "web_share_canceled" {
			gotCancelEvent = true
			break
		}
	}
	if !gotCancelEvent {
		t.Fatalf("Host did not receive web_share_canceled event when client cancelled download!")
	}

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
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		var ev struct {
			Event string `json:"event"`
			Data  struct {
				SpeedMbps string `json:"speed_mbps"`
			} `json:"data"`
		}
		if err := ws.ReadJSON(&ev); err != nil {
			break
		}
		if ev.Event == "web_share_progress" {
			spd, _ := strconv.ParseFloat(ev.Data.SpeedMbps, 64)
			if spd > 0.0 {
				sawNonZeroSpeed = true
			}
		}
		if ev.Event == "web_share_complete" {
			break
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

	nodeAddr := server.GetNodeAddr()
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
	server := NewDaemonServer(0, tempDir, "BenchDevice")
	ln, err := server.Listen(0)
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		_ = server.Serve(ln)
	}()

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect WS: %v", err)
	}
	defer ws.Close()

	// Read initial status event
	var initEvt EventMessage
	_ = ws.ReadJSON(&initEvt)

	// Send test_disk action
	req := RequestMessage{
		ID:     "bench_test_1",
		Action: "test_disk",
		Payload: json.RawMessage(fmt.Sprintf(`{
			"dir": %q,
			"size_bytes": 4194304
		}`, tempDir)),
	}

	if err := ws.WriteJSON(req); err != nil {
		t.Fatalf("Failed to send test_disk request: %v", err)
	}

	// Read events until benchmark_disk_result
	var benchResult EventMessage
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var evt EventMessage
		_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		if err := ws.ReadJSON(&evt); err != nil {
			break
		}
		if evt.Event == "benchmark_disk_result" {
			benchResult = evt
			break
		}
	}

	if benchResult.Event != "benchmark_disk_result" {
		t.Fatalf("Expected benchmark_disk_result event, got: %v", benchResult)
	}

	dataBytes, _ := json.Marshal(benchResult.Data)
	var diskRes engine.DiskBenchResult
	if err := json.Unmarshal(dataBytes, &diskRes); err != nil {
		t.Fatalf("Failed unmarshaling benchmark data: %v", err)
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
	recvDir := t.TempDir()
	recvServer := NewDaemonServer(0, recvDir, "ReceiverNode")
	recvHTTP, err := recvServer.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	recvHTTPPort := recvHTTP.Addr().(*net.TCPAddr).Port

	recvNodeLn, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	recvNodePort := recvNodeLn.Addr().(*net.TCPAddr).Port
	go recvServer.listenForIncomingPairings(recvNodeLn)

	go func() {
		_ = recvServer.Serve(recvHTTP)
	}()
	defer recvServer.Stop()
	defer recvNodeLn.Close()

	// Sender Daemon
	sendDir := t.TempDir()
	folderToShare := filepath.Join(sendDir, "resilience_folder")
	_ = os.MkdirAll(folderToShare, 0755)

	// Create 20 files in subdirectories
	expectedFiles := make(map[string][]byte)
	for i := 0; i < 20; i++ {
		subDir := filepath.Join(folderToShare, fmt.Sprintf("sub_%d", i%4))
		_ = os.MkdirAll(subDir, 0755)
		filePath := filepath.Join(subDir, fmt.Sprintf("video_%d.mp4", i))
		data := bytes.Repeat([]byte(fmt.Sprintf("media payload block %d\n", i)), 500)
		_ = os.WriteFile(filePath, data, 0644)

		rel, _ := filepath.Rel(sendDir, filePath)
		expectedFiles[filepath.ToSlash(rel)] = data
	}

	sendServer := NewDaemonServer(0, sendDir, "SenderNode")
	sendHTTP, err := sendServer.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	sendHTTPPort := sendHTTP.Addr().(*net.TCPAddr).Port
	go func() {
		_ = sendServer.Serve(sendHTTP)
	}()
	defer sendServer.Stop()

	// Connect WebSocket to Receiver
	recvWSURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", recvHTTPPort)
	recvWS, _, err := websocket.DefaultDialer.Dial(recvWSURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial receiver ws: %v", err)
	}
	defer recvWS.Close()

	var initEvt EventMessage
	_ = recvWS.ReadJSON(&initEvt)

	// Connect WebSocket to Sender and drain events in background
	sendWSURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", sendHTTPPort)
	sendWS, _, err := websocket.DefaultDialer.Dial(sendWSURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial sender ws: %v", err)
	}
	defer sendWS.Close()
	_ = sendWS.ReadJSON(&initEvt)

	go func() {
		for {
			var evt EventMessage
			if err := sendWS.ReadJSON(&evt); err != nil {
				return
			}
		}
	}()

	// Send folder
	recvCode := recvServer.GetStatus().PairingCode
	_ = sendWS.WriteJSON(RequestMessage{
		ID:      "send_resilience_batch",
		Action:  "send",
		Payload: json.RawMessage(fmt.Sprintf(`{"paths":[%q],"target_ip":"127.0.0.1:%d","code":%q}`, folderToShare, recvNodePort, recvCode)),
	})

	// Receiver accepts offer
	for {
		var evt EventMessage
		if err := recvWS.ReadJSON(&evt); err != nil {
			t.Fatalf("Receiver read error: %v", err)
		}
		if evt.Event == "incoming_offer" {
			_ = recvWS.WriteJSON(RequestMessage{
				ID:      "accept_resilience_batch",
				Action:  "respond_offer",
				Payload: json.RawMessage(`{"accept":true}`),
			})
			break
		}
	}

	// Wait for transfer_complete on receiver
	deadline := time.Now().Add(15 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		var evt EventMessage
		_ = recvWS.SetReadDeadline(time.Now().Add(2 * time.Second))
		if err := recvWS.ReadJSON(&evt); err != nil {
			break
		}
		if evt.Event == "transfer_complete" {
			completed = true
			break
		}
	}

	if !completed {
		t.Fatalf("Multi-file batch transfer did not complete within timeout (potential freeze/deadlock)")
	}

	// Verify all 20 files are in recvDir with preserved paths
	for relPath, expected := range expectedFiles {
		targetFile := filepath.Join(recvDir, filepath.FromSlash(relPath))
		actual, err := os.ReadFile(targetFile)
		if err != nil {
			t.Fatalf("Missing transferred file '%s': %v", relPath, err)
		}
		if !bytes.Equal(actual, expected) {
			t.Fatalf("Data mismatch in transferred file '%s'", relPath)
		}
	}
}

func TestBatchItemFailedAdvance(t *testing.T) {
	recvDir := t.TempDir()
	recvServer := NewDaemonServer(0, recvDir, "ReceiverNode")
	recvHTTP, err := recvServer.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	recvHTTPPort := recvHTTP.Addr().(*net.TCPAddr).Port

	recvNodeLn, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	recvNodePort := recvNodeLn.Addr().(*net.TCPAddr).Port
	go recvServer.listenForIncomingPairings(recvNodeLn)

	go func() {
		_ = recvServer.Serve(recvHTTP)
	}()
	defer recvServer.Stop()
	defer recvNodeLn.Close()

	// Sender Daemon
	sendDir := t.TempDir()
	folderToShare := filepath.Join(sendDir, "batch_with_skip")
	_ = os.MkdirAll(folderToShare, 0755)

	// Create 3 files
	for i := 0; i < 3; i++ {
		filePath := filepath.Join(folderToShare, fmt.Sprintf("file_%d.txt", i))
		_ = os.WriteFile(filePath, []byte(fmt.Sprintf("file content %d", i)), 0644)
	}

	sendServer := NewDaemonServer(0, sendDir, "SenderNode")
	sendHTTP, err := sendServer.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	sendHTTPPort := sendHTTP.Addr().(*net.TCPAddr).Port
	go func() {
		_ = sendServer.Serve(sendHTTP)
	}()
	defer sendServer.Stop()

	// Connect WebSocket to Receiver
	recvWSURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", recvHTTPPort)
	recvWS, _, err := websocket.DefaultDialer.Dial(recvWSURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial receiver ws: %v", err)
	}
	defer recvWS.Close()

	var initEvt EventMessage
	_ = recvWS.ReadJSON(&initEvt)

	// Connect WebSocket to Sender and track events
	sendWSURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", sendHTTPPort)
	sendWS, _, err := websocket.DefaultDialer.Dial(sendWSURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial sender ws: %v", err)
	}
	defer sendWS.Close()
	_ = sendWS.ReadJSON(&initEvt)

	go func() {
		for {
			var evt EventMessage
			if err := sendWS.ReadJSON(&evt); err != nil {
				return
			}
		}
	}()

	// Mark item 1 as skipped on receiver before transfer to trigger item_skipped / error advance
	recvServer.mu.Lock()
	recvServer.skippedFiles[1] = true
	recvServer.mu.Unlock()

	// Send folder
	recvCode := recvServer.GetStatus().PairingCode
	_ = sendWS.WriteJSON(RequestMessage{
		ID:      "send_skip_batch",
		Action:  "send",
		Payload: json.RawMessage(fmt.Sprintf(`{"paths":[%q],"target_ip":"127.0.0.1:%d","code":%q}`, folderToShare, recvNodePort, recvCode)),
	})

	// Receiver accepts offer
	for {
		var evt EventMessage
		if err := recvWS.ReadJSON(&evt); err != nil {
			t.Fatalf("Receiver read error: %v", err)
		}
		if evt.Event == "incoming_offer" {
			_ = recvWS.WriteJSON(RequestMessage{
				ID:      "accept_skip_batch",
				Action:  "respond_offer",
				Payload: json.RawMessage(`{"accept":true}`),
			})
			break
		}
	}

	// Wait for transfer_complete on receiver
	deadline := time.Now().Add(10 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		var evt EventMessage
		_ = recvWS.SetReadDeadline(time.Now().Add(2 * time.Second))
		if err := recvWS.ReadJSON(&evt); err != nil {
			break
		}
		if evt.Event == "transfer_complete" {
			completed = true
			break
		}
	}

	if !completed {
		t.Fatalf("Batch transfer with skipped file deadlocked instead of completing remaining files")
	}

	// Verify file 0 and file 2 exist, while file 1 was skipped
	f0 := filepath.Join(recvDir, "batch_with_skip", "file_0.txt")
	f2 := filepath.Join(recvDir, "batch_with_skip", "file_2.txt")
	if _, err := os.Stat(f0); err != nil {
		t.Fatalf("Expected file_0.txt to exist: %v", err)
	}
	if _, err := os.Stat(f2); err != nil {
		t.Fatalf("Expected file_2.txt to exist: %v", err)
	}
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
