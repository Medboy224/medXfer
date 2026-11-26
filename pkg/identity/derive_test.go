package identity

import (
	"bytes"
	"testing"
)

// A key drawn from the device's key is the same at every launch, another for another label, and
// new with a new identity (ID-06).
func TestDerivedKey(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	other, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	k, err := a.DerivedKey("label-a")
	if err != nil || len(k) != DerivedKeySize {
		t.Fatalf("key %x, %v", k, err)
	}
	if k2, _ := again.DerivedKey("label-a"); !bytes.Equal(k, k2) {
		t.Fatal("another key after a reload")
	}
	if kb, _ := a.DerivedKey("label-b"); bytes.Equal(k, kb) {
		t.Fatal("two labels give one key")
	}
	if ko, _ := other.DerivedKey("label-a"); bytes.Equal(k, ko) {
		t.Fatal("two identities give one key")
	}
}
