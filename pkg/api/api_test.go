package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"

	"github.com/gorilla/websocket"
)

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
