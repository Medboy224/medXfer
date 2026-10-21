//go:build windows

package discovery

import (
	"context"
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW: the process gets no console.
const createNoWindow = 0x08000000

// command is exec.CommandContext without a console window. The app has none of its own (a
// Flutter window): each console program it starts would flash one, `arp -a` every few seconds
// while the app is open.
func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return c
}
