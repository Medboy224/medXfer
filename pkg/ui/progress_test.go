package ui

import (
	"strings"
	"testing"
)

// Audit §3.7 (#156): the percentage stays within [0, 100] and the bar never panics, whatever
// the counts say (a resume, a size that changed).
func TestProgressIsBounded(t *testing.T) {
	p := NewProgressBar()
	for _, c := range []struct {
		current, total int64
		want           string
	}{
		{150, 100, "100.0%"},
		{-100, 100, "  0.0%"},
		{50, 100, " 50.0%"},
	} {
		var line string
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%d of %d: panic %v", c.current, c.total, r)
				}
			}()
			line = p.line(c.current, c.total, 1)
		}()
		if !strings.Contains(line, c.want) {
			t.Errorf("%d of %d: %q, want %s", c.current, c.total, line, c.want)
		}
	}
}
