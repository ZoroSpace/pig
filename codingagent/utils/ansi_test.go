// tui: packages/coding-agent/test — splitIncompleteAnsiSuffix coverage
// (S16-3, pi 27c7b6ff4). The regex equivalence cases that matter for the
// streaming split: sequences cut mid-params, a bare trailing ESC, an OSC
// body containing ESC (but not ESC\), and the >256 window release.
package utils

import (
	"strings"
	"testing"
)

func TestSplitIncompleteAnsiSuffix(t *testing.T) {
	cases := []struct {
		name         string
		value        string
		wantComplete string
		wantPending  string
	}{
		{"no escape sequences", "hello world", "hello world", ""},
		{"complete CSI only", "\x1b[31mred\x1b[0m", "\x1b[31mred\x1b[0m", ""},
		{"CSI cut inside its parameters", "a\x1b[3", "a", "\x1b[3"},
		{"bare trailing ESC starts a CSI", "before\x1b", "before", "\x1b"},
		{"OSC without its terminator", "a\x1b]0;title", "a", "\x1b]0;title"},
		{"OSC body may contain a bare trailing ESC", "\x1b]0;title\x1b", "", "\x1b]0;title\x1b"},
		{"OSC body stops before an ESC backslash terminator", "\x1b]0;title\x1b\\tail", "\x1b]0;title\x1b\\tail", ""},
		{"C1 CSI introducer held back", "x\u009b", "x", "\u009b"},
		{"long unterminated OSC is released as-is", "\x1b]" + strings.Repeat("x", 300), "\x1b]" + strings.Repeat("x", 300), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			complete, pending := SplitIncompleteAnsiSuffix(tc.value)
			if complete != tc.wantComplete || pending != tc.wantPending {
				t.Fatalf("SplitIncompleteAnsiSuffix(%q) = (%q, %q), want (%q, %q)",
					tc.value, complete, pending, tc.wantComplete, tc.wantPending)
			}
		})
	}
}

// The split invariant: concatenating the pieces and stripping the whole
// equals stripping each complete piece — an escape sequence split across
// chunks must never leak into the output.
func TestSplitIncompleteAnsiSuffixRoundTrip(t *testing.T) {
	full := "pre \x1b[31mred\x1b[0m mid \x1b]0;title\x1b\\ post \x1b[?25h"
	for split := 0; split <= len(full); split++ {
		first, pending := SplitIncompleteAnsiSuffix(full[:split])
		second, _ := SplitIncompleteAnsiSuffix(pending + full[split:])
		joined := StripAnsi(first) + StripAnsi(second)
		if want := StripAnsi(full); joined != want {
			t.Fatalf("split at %d: %q + %q -> %q, want %q", split, first, second, joined, want)
		}
	}
}

func TestStripAnsi(t *testing.T) {
	cases := []struct{ value, want string }{
		{"plain", "plain"},
		{"\x1b[31mred\x1b[0m", "red"},
		{"\x1b]0;title\x1b\\body", "body"},
		{"\x1b]0;bell\x07body", "body"},
		{"link\x1b]8;;https://x\x1b\\text\x1b]8;;\x1b\\", "linktext"},
		{"\u009b31mred\u009b0m", "red"},
	}
	for _, tc := range cases {
		if got := StripAnsi(tc.value); got != tc.want {
			t.Fatalf("StripAnsi(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
