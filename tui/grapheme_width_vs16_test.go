package tui

import "testing"

// pig-only regression (2026-09-22, docs/fix/2026-09-22-vs16-emoji-width-wrap-desync.md):
// ALL RGI emoji — including VS16 presentation sequences of EAW-neutral/ambiguous
// base code points (⏸️ U+23F8+FE0F, ⚠️ U+26A0+FE0F, ❤️ U+2764+FE0F, ☀️ U+2600+FE0F) —
// must count as width 2. Measured ground truth via DSR (ESC[6n) cursor-position
// probe on both real environments (tmux 3.7c pane and direct iTerm2): BOTH render
// these sequences 2 cells. The 2026-09-21 attempt to count them 1 ("strict-EAW
// terminals render narrow") was wrong, and under-counting is the catastrophic
// direction: an affected line at the wrap boundary auto-wraps one cell early,
// desyncs the renderer's row accounting, and ghosts every subsequent streaming
// frame (editor Working border drawn mid-text, stale line prefixes stacking).
//
// Asymmetry: over-counting (2 vs real 1) never triggers an early terminal wrap —
// worst case is a one-column padding artifact inside a table. Under-counting
// breaks frame sync. When in doubt, count wider.
func TestGraphemeWidthVS16EmojiPresentation(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"pause button U+23F8+FE0F neutral base", "⏸️", 2},
		{"warning sign U+26A0+FE0F neutral base", "⚠️", 2},
		{"heart U+2764+FE0F neutral base", "❤️", 2},
		{"sun U+2600+FE0F neutral base", "☀️", 2},
		{"pause base without VS16 unchanged", "⏸", 1},
		{"cross mark U+274C wide base", "❌", 2},
		{"grinning face U+1F600 wide base", "😀", 2},
		{"keycap sequence stays wide", "#️⃣", 2},
		{"RI flag pair stays wide", "🇺🇸", 2},
		{"ZWJ family stays wide", "👨‍👩‍👧", 2},
	}
	for _, c := range cases {
		if got := graphemeWidth(c.in); got != c.want {
			t.Errorf("%s: graphemeWidth(%q) = %d, want %d", c.name, c.in, got, c.want)
		}
	}
	// 决(2)策(2)，(2)⏸️(2)space(1)按(2)需(2)延(2)后(2) = 17
	if got := VisibleWidth("决策，⏸️ 按需延后"); got != 17 {
		t.Errorf("VisibleWidth(composite) = %d, want 17", got)
	}
}
