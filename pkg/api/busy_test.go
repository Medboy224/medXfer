package api

import "testing"

// PAIR-13: the user who connected to a device that transfers with another one learns it by the
// code busy, as an unreachable device has its own (XFER-08).
func TestBusyRefusalHasItsCode(t *testing.T) {
	e := pairFailed("192.0.2.7:18887", &rejectedError{reason: errBusyElsewhere.Error(), code: codeBusy}, "id")
	data, _ := e.Data.(map[string]string)
	if e.Event != "action_error" || data["code"] != codeBusy {
		t.Fatalf("%s %v; want an action_error with the code %q", e.Event, e.Data, codeBusy)
	}
}
