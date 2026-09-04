package api

import (
	"encoding/base64"
	"fmt"

	qrcode "github.com/skip2/go-qrcode"
)

// GenerateWiFiQRDataURI generates a base64 PNG data-uri for 1-tap mobile Wi-Fi pairing
// Format: WIFI:T:WPA;S:<SSID>;P:<Password>;;
func GenerateWiFiQRDataURI(ssid, password string, size int) (string, error) {
	payload := fmt.Sprintf("WIFI:T:WPA;S:%s;P:%s;;", ssid, password)
	png, err := qrcode.Encode(payload, qrcode.Medium, size)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

// GenerateURLQRDataURI generates a base64 PNG data-uri for mobile browser URL access
func GenerateURLQRDataURI(url string, size int) (string, error) {
	png, err := qrcode.Encode(url, qrcode.Medium, size)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}
