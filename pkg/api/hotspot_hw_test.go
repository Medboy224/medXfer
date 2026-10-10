//go:build windows && hardware

package api

import (
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/testkit"
)

// Ce test démarre un vrai réseau Wi-Fi Direct via PowerShell : il ne peut
// réussir que sous Windows, sur une machine équipée. Lancement explicite :
//
//	go test -tags hardware -run TestHotspotWebSocketCommands ./pkg/api
func TestHotspotWebSocketCommands(t *testing.T) {
	node := testkit.StartNode(t, NewDaemonServer(0, t.TempDir(), "TestHotspotHost"))

	// 1. Query initial hotspot status (should be inactive)
	node.Request("hotspot_status", nil)
	var status struct {
		Active bool `json:"active"`
	}
	_ = node.WaitEvent("hotspot_status", 0).Decode(&status)
	if status.Active {
		t.Fatalf("Expected inactive hotspot initially")
	}

	// 2. Start hotspot
	node.Request("hotspot_start", map[string]string{"band": "5ghz", "ssid": "medXfer-UnitTest"})
	e := node.WaitEventAny(12*time.Second, "hotspot_started", "action_error")
	if e.Event == "action_error" {
		t.Fatalf("hotspot_start returned error: %s", e.Data)
	}
	started := e.Fields()
	if started["ssid"] != "DIRECT-medXfer-UnitTest" {
		t.Fatalf("Expected SSID DIRECT-medXfer-UnitTest, got %v", started["ssid"])
	}
	if started["qr_wifi"] == nil || started["qr_portal"] == nil {
		t.Fatalf("Expected QR codes in hotspot_started event")
	}
	t.Logf("VERIFIED: Hotspot started, SSID=%v, Band=%v, IP=%v, Portal=%v",
		started["ssid"], started["band"], started["ip"], started["portal_url"])

	// 3. Stop hotspot
	node.Request("hotspot_stop", nil)
	node.WaitEvent("hotspot_stopped", 0)
}
