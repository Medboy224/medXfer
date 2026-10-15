//go:build linux || android

package discovery

import (
	"net"

	"golang.org/x/sys/unix"
)

// interfaceByName finds an interface's index by ioctl (SIOCGIFINDEX): Android refuses netlink
// to apps (G2A-09), so net.InterfaceByName fails there, but this call is allowed.
func interfaceByName(name string) (*net.Interface, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	ifr, err := unix.NewIfreq(name)
	if err != nil {
		return nil, err
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFINDEX, ifr); err != nil {
		return nil, err
	}
	// Go sets IP_MULTICAST_IF and joins groups with ip_mreqn on Linux: the index is enough.
	return &net.Interface{Index: int(ifr.Uint32()), Name: name, Flags: net.FlagUp | net.FlagMulticast}, nil
}
