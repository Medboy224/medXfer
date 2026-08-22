package discovery

import (
	"fmt"

	qrcode "github.com/skip2/go-qrcode"
)

func PrintTerminalQR(content string) {
	qr, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		fmt.Printf("Could not generate QR code: %v\n", err)
		return
	}
	fmt.Println(qr.ToSmallString(false))
}

func PrintHotspotQR(ssid, password string) {
	wifiPayload := fmt.Sprintf("WIFI:T:WPA;S:%s;P:%s;;", ssid, password)
	PrintTerminalQR(wifiPayload)
}
