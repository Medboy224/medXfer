//go:build !windows

package discovery

import (
	"context"
	"os/exec"
)

// command is exec.CommandContext; on Windows, without a console window.
func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
