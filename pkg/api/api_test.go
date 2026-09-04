package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	// Dispatch "send" from Sender specifying target_ip = 127.0.0.1:<recvNodePort>
	startOffer := time.Now()
	_ = sendWS.WriteJSON(RequestMessage{
		ID:      "send_test",
		Action:  "send",
		Payload: json.RawMessage(fmt.Sprintf(`{"paths":[%q],"target_ip":"127.0.0.1:%d"}`, testFile, recvNodePort)),
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
	pathsJSON, _ := json.Marshal(filePaths)
	_ = sendWS.WriteJSON(RequestMessage{
		ID:     "send_batch_5",
		Action: "send",
		Payload: json.RawMessage(fmt.Sprintf(`{
			"paths": %s,
			"target_ip": "127.0.0.1:%d"
		}`, string(pathsJSON), recvNodePort)),
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

	// Send folder
	_ = sendWS.WriteJSON(RequestMessage{
		ID:      "send_folder",
		Action:  "send",
		Payload: json.RawMessage(fmt.Sprintf(`{"paths":[%q],"target_ip":"127.0.0.1:%d"}`, folderToShare, recvNodePort)),
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

	// 1. Test GET /share (HTML portal)
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

	// 2. Test GET /api/share/list (empty initially)
	respList, err := client.Get(baseURL + "/api/share/list")
	if err != nil {
		t.Fatalf("GET /api/share/list failed: %v", err)
	}
	defer respList.Body.Close()
	if respList.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK from /api/share/list, got %d", respList.StatusCode)
	}

	// 3. Test POST /api/share/upload (mobile upload to PC)
	uploadBody := &bytes.Buffer{}
	writer := multipart.NewWriter(uploadBody)
	part, err := writer.CreateFormFile("files", "mobile_photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	expectedData := []byte("JPEG_MOCK_IMAGE_DATA_12345")
	_, _ = part.Write(expectedData)
	_ = writer.Close()

	uploadReq, err := http.NewRequest(http.MethodPost, baseURL+"/api/share/upload", uploadBody)
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

	t.Logf("VERIFIED: Zero-install web share portal and mobile upload verified!")
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
