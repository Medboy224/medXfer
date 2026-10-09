//go:build windows && hardware

package api

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Ce test démarre un vrai réseau Wi-Fi Direct via PowerShell : il ne peut
// réussir que sous Windows, sur une machine équipée. Lancement explicite :
//
//	go test -tags hardware -run TestHotspotWebSocketCommands ./pkg/api
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
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, testAuth())
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
