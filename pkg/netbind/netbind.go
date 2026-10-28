// Package netbind ties the core's sockets to the network the app chose (DISC-10, J6c).
//
// On Android, a network the app joins (the one a QR code names) is not the default one: only
// the sockets tied to it reach it. ConnectivityManager.bindProcessToNetwork ties the sockets the
// C library makes; Go makes its own by direct system calls, which it does not see. The core's
// connections then left by the default network, with no route to the joined one: "network is
// unreachable" (real test of 28 October 2026). The app gives the joined network's handle
// (set_network_info); each socket made through Control is tied to it. Elsewhere, nothing to do.
package netbind

import (
	"net"
	"sync/atomic"
	"syscall"
	"time"
)

var network atomic.Uint64

// bindFD ties a socket to a network; nil where the platform has no such thing.
var bindFD func(handle uint64, fd uintptr) error

// SetNetwork ties the sockets made from now on to the network handle (Android's net_handle_t,
// Network.getNetworkHandle); 0 unties them.
func SetNetwork(handle uint64) { network.Store(handle) }

// Network is the handle the sockets are tied to, 0 for none.
func Network() uint64 { return network.Load() }

// Control is a net.Dialer's, or a net.ListenConfig's, Control: it ties the new socket to the
// network given by SetNetwork, if any and if the platform can.
func Control(_, _ string, c syscall.RawConn) error {
	h := network.Load()
	if h == 0 || bindFD == nil {
		return nil
	}
	var err error
	if cerr := c.Control(func(fd uintptr) { err = bindFD(h, fd) }); cerr != nil {
		return cerr
	}
	return err
}

// Dialer is a net.Dialer whose sockets are tied by Control.
func Dialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, Control: Control}
}
