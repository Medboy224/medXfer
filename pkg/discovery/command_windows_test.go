//go:build windows

package discovery

import (
	"context"
	"testing"
)

// The commands discovery runs (arp -a) open no console window: in the app, one flashed every
// few seconds.
func TestCommandsOpenNoConsoleWindow(t *testing.T) {
	c := command(context.Background(), "arp", "-a")
	if c.SysProcAttr == nil || !c.SysProcAttr.HideWindow || c.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("SysProcAttr = %+v; want HideWindow and CREATE_NO_WINDOW", c.SysProcAttr)
	}
}
