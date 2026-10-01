// tui/visible_width_fast_test.go (pi: test/visible-width.test.ts, v0.99.1
// additions) — the styled-text fast path and escape scanning of VisibleWidth.
package tui

import (
	"testing"
)

func TestVisibleWidthStyledASCII(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"\x1b[38;5;4mhello\x1b[39m world", 11},
		{"\x1b]8;;https://example.com\x07link\x1b]8;;\x07", 4},
		{"\x1b]133;A\x1b\\prompt", 6},
		{"\x1b_pi:c\x07cursor", 6},
		{"\x1b[1ma\tb\x1b[22m", 5},
		{"\x1b[31m日本\x1b[39m ok", 7},
		{"\x1b[31m─→\x1b[39m", 2},
		{"\x1b[31", 3},
		{"a\x1b", 1},
	}
	for _, c := range cases {
		if got := VisibleWidth(c.input); got != c.want {
			t.Errorf("VisibleWidth(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}
