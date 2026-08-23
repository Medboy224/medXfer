package hotspot

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
)

type Band int

const (
	BandAuto Band = iota
	Band2GHz
	Band5GHz
)

func (b Band) String() string {
	switch b {
	case Band5GHz:
		return "5 GHz (Preferred)"
	case Band2GHz:
		return "2.4 GHz"
	default:
		return "Auto"
	}
}

type Config struct {
	SSID     string
	Password string
	Band     Band
}

type NetworkInfo struct {
	SSID        string
	Password    string
	Interface   string
	LocalIP     net.IP
	BroadcastIP net.IP
	Band        Band
}

type Controller interface {
	Start(cfg Config) (*NetworkInfo, error)
	Stop() error
}

func New() Controller {
	return newController()
}

// GenerateCredentials generates an ephemeral SSID and high-entropy password
func GenerateCredentials() (string, string) {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // Crockford Base32 (no ambiguous chars)

	// Random 4-character SSID suffix
	ssidSuffix := make([]byte, 4)
	for i := range ssidSuffix {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		ssidSuffix[i] = charset[num.Int64()]
	}

	// Random 8-character Passphrase
	password := make([]byte, 8)
	for i := range password {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		password[i] = charset[num.Int64()]
	}

	return fmt.Sprintf("medXfer-%s", string(ssidSuffix)), string(password)
}
