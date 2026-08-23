//go:build !windows

package hotspot

import "fmt"

type genericHotspot struct{}

func newController() Controller {
	return &genericHotspot{}
}

func (g *genericHotspot) Start(cfg Config) (*NetworkInfo, error) {
	return nil, fmt.Errorf("--create-network is currently Windows-only (on Android, please turn on Hotspot in Settings)")
}

func (g *genericHotspot) Stop() error {
	return nil
}
