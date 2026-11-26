package api

import (
	"errors"
	"net"
	"testing"
)

// #313: with port 18887 and any other port refused, the node cannot listen: StartNode says so,
// rather than a device silent without an error.
func TestStartNodeWithoutAPort(t *testing.T) {
	old := listenNode
	listenNode = func(string) (net.Listener, error) { return nil, errors.New("address in use") }
	t.Cleanup(func() { listenNode = old })
	configMu.RLock()
	previous := customConfigDir
	configMu.RUnlock()
	SetCustomConfigDir(t.TempDir())
	t.Cleanup(func() { SetCustomConfigDir(previous) })
	s := NewDaemonServer(0, t.TempDir(), "Laptop")
	if err := s.StartNode(); err == nil {
		t.Fatal("StartNode succeeded without a port")
	}
}
