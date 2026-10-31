package discovery

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// osKind reads the kind of an interface from its adapter's type (GetAdaptersAddresses). On
// Windows, net.Interface's name is the adapter's friendly name ("Wi-Fi", "Ethernet"), which
// the user may rename or which the system translates: the type does not change. "" when no
// adapter has that name.
func osKind(name string) string {
	for aa := adapters(); aa != nil; aa = aa.Next {
		if windows.UTF16PtrToString(aa.FriendlyName) == name {
			return adapterKind(aa.IfType)
		}
	}
	return ""
}

// adapterKind maps an adapter's type (IANA ifType) to a path kind.
func adapterKind(ifType uint32) string {
	switch ifType {
	case windows.IF_TYPE_ETHERNET_CSMACD:
		return KindEthernet
	case windows.IF_TYPE_IEEE80211:
		return KindWiFi
	}
	return KindOther
}

// adapters lists the system's adapters, growing the buffer until it fits, as the net package
// does; nil on error.
func adapters() *windows.IpAdapterAddresses {
	size := uint32(15 << 10)
	for i := 0; i < 3; i++ {
		buf := make([]byte, size)
		aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_INCLUDE_PREFIX, 0, aa, &size)
		if err == nil {
			if size == 0 {
				return nil
			}
			return aa
		}
		if !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			return nil
		}
	}
	return nil
}
