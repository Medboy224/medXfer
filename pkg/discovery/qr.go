package discovery

import (
	"fmt"

	qrcode "github.com/skip2/go-qrcode"
)

// PrintTerminalQR renders a compact ASCII QR code into the terminal
func PrintTerminalQR(content string) {
	qr, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		fmt.Printf("Could not generate QR code: %v\n", err)
		return
	}
	fmt.Println(qr.ToSmallString(false))
}
