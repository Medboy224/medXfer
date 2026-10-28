//go:build android && cgo

package netbind

/*
#cgo LDFLAGS: -landroid
#include <errno.h>
#include <android/multinetwork.h>

static int bind_socket(net_handle_t network, int fd) {
	return android_setsocknetwork(network, fd) == 0 ? 0 : errno;
}
*/
import "C"

import "syscall"

func init() {
	// The NDK's android_setsocknetwork (API 23 on): the socket goes through that network.
	bindFD = func(handle uint64, fd uintptr) error {
		if e := C.bind_socket(C.net_handle_t(handle), C.int(fd)); e != 0 {
			return syscall.Errno(e)
		}
		return nil
	}
}
