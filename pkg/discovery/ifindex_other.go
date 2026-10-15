//go:build !linux && !android

package discovery

import "net"

func interfaceByName(name string) (*net.Interface, error) { return net.InterfaceByName(name) }
