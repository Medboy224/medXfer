package api

import (
	"encoding/base64"

	qrcode "github.com/skip2/go-qrcode"
)

// GenerateURLQRDataURI generates a base64 PNG data-uri for mobile browser URL access
func GenerateURLQRDataURI(url string, size int) (string, error) {
	png, err := qrcode.Encode(url, qrcode.Medium, size)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}
