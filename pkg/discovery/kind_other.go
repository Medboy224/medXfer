//go:build !linux && !windows

package discovery

// osKind: the system says nothing here; the name decides (nameKind).
func osKind(string) string { return "" }
