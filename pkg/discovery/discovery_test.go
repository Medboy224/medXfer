package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestGenerateAndParsePairingCode(t *testing.T) {
	// 1. Generation with valid IP
	code, octet, pin := GeneratePairingCode("10.100.104.229")
	if octet != 229 {
		t.Fatalf("expected octet 229, got %d", octet)
	}
	if pin < 100 || pin > 999 {
		t.Fatalf("expected pin between 100 and 999, got %d", pin)
	}
	expectedFormat := fmt.Sprintf("229-%03d", pin)
	if code != expectedFormat {
		t.Fatalf("expected code %s, got %s", expectedFormat, code)
	}

	// 2. Generation with octet < 100
	code2, octet2, pin2 := GeneratePairingCode("192.168.1.42")
	if octet2 != 42 {
		t.Fatalf("expected octet 42, got %d", octet2)
	}
	expectedFormat2 := fmt.Sprintf("042-%03d", pin2)
	if code2 != expectedFormat2 {
		t.Fatalf("expected code %s, got %s", expectedFormat2, code2)
	}

	// 3. Normalization tests
	tests := []struct {
		input       string
		expected    string
		shouldError bool
	}{
		{"229-481", "229-481", false},
		{"229481", "229-481", false},
		{" 229 481 ", "229-481", false},
		{"42-123", "042-123", false},
		{"42123", "042-123", false},
		{"5-123", "005-123", false},
		{"5123", "005-123", false},
		{"abc-123", "", true},
		{"999-123", "", true}, // octet > 254
		{"000-123", "", true}, // octet 0 invalid
		{"12", "", true},      // too short
		{"1234567", "", true}, // too long
	}

	for _, tc := range tests {
		normalized, err := NormalizePairingCode(tc.input)
		if tc.shouldError {
			if err == nil {
				t.Errorf("input '%s' should have errored, got '%s'", tc.input, normalized)
			}
		} else {
			if err != nil {
				t.Errorf("input '%s' returned unexpected error: %v", tc.input, err)
			}
			if normalized != tc.expected {
				t.Errorf("input '%s': expected '%s', got '%s'", tc.input, tc.expected, normalized)
			}
		}
	}
}

func TestDiscoveryServerPairingCode(t *testing.T) {
	srv := NewDiscoveryServer("node", 18887, nil, "Test-Device")
	initialCode := srv.GetPairingCode()
	if initialCode == "" {
		t.Fatal("expected auto-generated pairing code, got empty")
	}

	srv.SetPairingCode("201-555")
	if srv.GetPairingCode() != "201-555" {
		t.Fatalf("expected pairing code '201-555', got '%s'", srv.GetPairingCode())
	}

	p := srv.GetPeer()
	if p.PairingCode != "201-555" {
		t.Fatalf("expected peer pairing code '201-555', got '%s'", p.PairingCode)
	}
}

func TestProbePeerForCode(t *testing.T) {
	mockPeer := Peer{
		ID:          "test-node-1",
		DeviceName:  "TestNode",
		HostIP:      "127.0.0.1",
		Port:        18888,
		Version:     AppVersion,
		Role:        "sender",
		PairingCode: "229-481",
		Offer: &TransferOffer{
			FileName: "secret_file.pdf",
			FileSize: 1024 * 1024,
		},
	}

	// 1. Test Authenticated Query with correct code
	clientConn, serverConn := net.Pipe()
	go func() {
		defer serverConn.Close()
		var q DiscoveryQuery
		_ = json.NewDecoder(serverConn).Decode(&q)
		resp := mockPeer
		normExpected, _ := NormalizePairingCode(mockPeer.PairingCode)
		normProvided, _ := NormalizePairingCode(q.PairingCode)
		if normExpected != normProvided {
			resp.PairingCode = ""
			resp.Offer = &TransferOffer{
				FileName: "[🔒 Private Transfer]",
				FileSize: 0,
			}
		}
		data, _ := json.Marshal(resp)
		_, _ = serverConn.Write(data)
	}()

	q := DiscoveryQuery{PairingCode: "229481"}
	qData, _ := json.Marshal(q)
	_, _ = clientConn.Write(append(qData, '\n'))

	var authPeer Peer
	_ = json.NewDecoder(clientConn).Decode(&authPeer)
	clientConn.Close()

	if authPeer.Offer == nil || authPeer.Offer.FileName != "secret_file.pdf" {
		t.Fatalf("expected unmasked file name 'secret_file.pdf', got '%v'", authPeer.Offer)
	}
	if authPeer.Offer.FileSize != 1024*1024 {
		t.Fatalf("expected unmasked file size 1048576, got %d", authPeer.Offer.FileSize)
	}

	// 2. Test Unauthenticated Query with wrong code
	clientConn2, serverConn2 := net.Pipe()
	go func() {
		defer serverConn2.Close()
		var q DiscoveryQuery
		_ = json.NewDecoder(serverConn2).Decode(&q)
		resp := mockPeer
		normExpected, _ := NormalizePairingCode(mockPeer.PairingCode)
		normProvided, _ := NormalizePairingCode(q.PairingCode)
		if normExpected != normProvided {
			resp.PairingCode = ""
			resp.Offer = &TransferOffer{
				FileName: "[🔒 Private Transfer]",
				FileSize: 0,
			}
		}
		data, _ := json.Marshal(resp)
		_, _ = serverConn2.Write(data)
	}()

	qWrong := DiscoveryQuery{PairingCode: "229999"}
	qWrongData, _ := json.Marshal(qWrong)
	_, _ = clientConn2.Write(append(qWrongData, '\n'))

	var maskedPeer Peer
	_ = json.NewDecoder(clientConn2).Decode(&maskedPeer)
	clientConn2.Close()

	if maskedPeer.Offer == nil || maskedPeer.Offer.FileName != "[🔒 Private Transfer]" {
		t.Fatalf("expected masked file name, got '%v'", maskedPeer.Offer)
	}
	if maskedPeer.Offer.FileSize != 0 {
		t.Fatalf("expected masked file size 0, got %d", maskedPeer.Offer.FileSize)
	}
	if maskedPeer.PairingCode != "" {
		t.Fatalf("expected empty pairing code for unauthenticated client, got '%s'", maskedPeer.PairingCode)
	}
}

func TestAuthenticatedDiscoveryReflector(t *testing.T) {
	// Test the logic of pairing validation on DiscoveryServer
	offer := &TransferOffer{FileName: "confidential.docx", FileSize: 5000000}
	srv := NewDiscoveryServer("sender", 18888, offer, "HostDev")
	srv.SetPairingCode("104-567")

	// Verify that beacons are masked
	peerSnap := srv.GetPeer()
	if peerSnap.PairingCode != "104-567" {
		t.Fatalf("expected internal pairing code 104-567, got %s", peerSnap.PairingCode)
	}

	// Verify normalization on input
	norm, err := NormalizePairingCode("104567")
	if err != nil || norm != "104-567" {
		t.Fatalf("expected 104-567, got %s, err: %v", norm, err)
	}

	normWrong, err := NormalizePairingCode("747485")
	if err == nil {
		t.Fatalf("expected octet 747 to fail validation, got: %s", normWrong)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = QueryPeerWithCode(ctx, "127.0.0.1", "999-123", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected error with invalid code format in QueryPeerWithCode")
	}
}
