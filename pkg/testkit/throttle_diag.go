//go:build diag

package testkit

import (
	"net"
	"testing"

	"github.com/Medboy224/medXfer/pkg/diag/faultconn"
	"github.com/Medboy224/medXfer/pkg/engine"
)

// Throttle limits every transfer connection opened during the test to bytesPerSec in total,
// so a test can act (pause, skip, cancel) while a small file is still in flight. It applies
// process-wide: tests that use it must not run in parallel with other transfers.
func Throttle(t testing.TB, bytesPerSec int64) {
	lim := faultconn.NewLimiter(bytesPerSec)
	restore := engine.SetConnWrapper(func(_ engine.ConnRole, c net.Conn) net.Conn {
		return faultconn.Wrap(c, faultconn.Plan{Limiter: lim})
	})
	t.Cleanup(restore)
}
