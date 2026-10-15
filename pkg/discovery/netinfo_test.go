package discovery

import (
	"testing"
)

// DISC-10: the platform's interfaces replace the enumeration; they are checked, ordered like
// enumerated ones (Wi-Fi before the cellular modem), and nil goes back to enumerating.
func TestSetNetworkInfo(t *testing.T) {
	t.Cleanup(func() { _ = SetNetworkInfo(nil) })
	for _, bad := range [][]NetworkInterface{
		{{Name: "", Addrs: []string{"10.0.0.2/24"}}},
		{{Name: "wlan0", Addrs: []string{"10.0.0.2"}}},
		{{Name: "wlan0", Addrs: []string{"fe80::1/64"}}},
		{{Name: "wlan0", Index: -1, Addrs: []string{"10.0.0.2/24"}}},
	} {
		if err := SetNetworkInfo(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if err := SetNetworkInfo([]NetworkInterface{
		{Name: "rmnet0", Index: 12, Addrs: []string{"10.30.122.151/28"}},
		{Name: "wlan0", Index: 50, Addrs: []string{"192.168.1.20/24"}},
	}); err != nil {
		t.Fatal(err)
	}
	targets := GetActiveNetworkTargets()
	if len(targets) != 2 || targets[0].InterfaceName != "wlan0" || targets[0].BroadcastIP.String() != "192.168.1.255" {
		t.Fatalf("targets %+v, want wlan0 first with its broadcast", targets)
	}
	if platformIndex("wlan0") != 50 || platformIndex("eth9") != 0 {
		t.Fatal("platformIndex")
	}
	// #109 with the platform's list: a cellular default route gives way to the Wi-Fi address.
	if got := choosePrimaryIP("10.30.122.151", targets); got != "192.168.1.20" {
		t.Fatalf("primary %s", got)
	}
	if err := SetNetworkInfo(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := platformTargets(); ok {
		t.Fatal("nil did not go back to enumerating")
	}
}
