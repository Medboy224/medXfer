package discovery

import "testing"

// The adapter's type says the kind, whatever its name (renamed, or translated by the system).
func TestAdapterKind(t *testing.T) {
	for ifType, kind := range map[uint32]string{6: KindEthernet, 71: KindWiFi, 24: KindOther, 131: KindOther} {
		if got := adapterKind(ifType); got != kind {
			t.Errorf("adapterKind(%d) = %q, want %q", ifType, got, kind)
		}
	}
	// The machine's own adapters: each has a kind, and an unknown name has none.
	for aa := adapters(); aa != nil; aa = aa.Next {
		if k := adapterKind(aa.IfType); k == "" {
			t.Errorf("adapter of type %d without a kind", aa.IfType)
		}
	}
	if k := osKind("medXfer, no such adapter"); k != "" {
		t.Errorf("osKind of an unknown name = %q", k)
	}
}
