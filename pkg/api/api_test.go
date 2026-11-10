package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
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

	// 2. No control surface (API-01): the web dashboard went at gate G7 (DEV-19-R).
	statusResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/status", port))
	if err != nil {
		t.Fatalf("Failed to GET /status: %v", err)
	}
	defer statusResp.Body.Close()
	if statusResp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /status = %d; want 404", statusResp.StatusCode)
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
	status := server.currentBatchItems[2].Status
	server.mu.RUnlock()
	if status != "skipped" {
		t.Fatalf("item 2 is %q; want skipped", status)
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

	// The receiver shows the incoming offer without waiting seconds on the way (a fixed pause,
	// a handshake timeout). 5 s is over 3 times the worst seen on a loaded CI runner (#117): the
	// latency itself is xfer bench's to measure, not this test's.
	pair.Receiver.WaitEvent("incoming_offer", 8*time.Second)
	elapsed := time.Since(startOffer)
	t.Logf("Received incoming_offer in %v", elapsed)
	if elapsed > 5*time.Second {
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

	itemState := func(i int) string {
		server.mu.RLock()
		defer server.mu.RUnlock()
		return server.currentBatchItems[i].Status
	}

	// 1. "pause_file" for the item in progress
	node.Request("pause_file", `{"item_index": 0}`)
	node.WaitEvent("file_paused", 0)
	if status := itemState(0); status != "paused" {
		t.Fatalf("item 0 is %q; want paused", status)
	}

	// 2. "pause_file" for a pending item
	node.Request("pause_file", `{"item_index": 2}`)
	node.WaitEvent("file_paused", 0)
	if status := itemState(2); status != "paused" {
		t.Fatalf("item 2 is %q; want paused", status)
	}

	// 3. "resume_file" for item 0
	node.Request("resume_file", `{"item_index": 0}`)
	node.WaitEvent("file_resumed", 0)
	if status := itemState(0); status != "pending" {
		t.Fatalf("item 0 after resume is %q; want pending", status)
	}
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

	// A folder is sent as a batch, item by item.
	pair.Send([]string{folderToShare}, testkit.SendOptions{})
	pair.AcceptOffer(0)
	pair.WaitTransferDone(0)

	testkit.AssertTreesEqual(t, sendDir, recvDir)
}

func TestQRGeneration(t *testing.T) {
	urlQR, err := GenerateURLQRDataURI("http://192.168.137.1:18888/share", 128)
	if err != nil {
		t.Fatalf("Failed to generate URL QR: %v", err)
	}
	if !strings.HasPrefix(urlQR, "data:image/png;base64,") {
		t.Fatalf("Expected data-uri prefix, got: %s", urlQR[:30])
	}
	t.Logf("VERIFIED: QR codes generated successfully!")
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

	// Item 1 is skipped on the receiver before it accepts: the transfer goes on without it.
	pair.Send([]string{folderToShare}, testkit.SendOptions{})
	pair.Receiver.WaitEvent("incoming_offer", 0)
	pair.Receiver.Request("skip_file", `{"item_index": 1}`)
	pair.Receiver.WaitEvent("file_skipped", 0)
	pair.Receiver.Request("respond_offer", map[string]any{"accept": true})
	pair.WaitTransferDone(0)

	for _, name := range []string{"file_0.txt", "file_2.txt"} {
		if _, err := os.Stat(filepath.Join(recvDir, "batch_with_skip", name)); err != nil {
			t.Fatalf("Expected %s to exist: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(recvDir, "batch_with_skip", "file_1.txt")); err == nil {
		t.Fatal("the skipped file_1.txt was received")
	}
}
