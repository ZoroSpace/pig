package tui

import "testing"

// pig-only regression (2026-09-21, docs/fix/2026-09-21-vs16-emoji-width-table-misalign.md):
// VS16 emoji presentation sequences of EAW-neutral/ambiguous base code points
// (⏸️ U+23F8+FE0F, ⚠️ U+26A0+FE0F, ❤️ U+2764+FE0F, ☀️ U+2600+FE0F) must count as
// width 1 to match strict-EAW terminals (tmux/VTE ambiguous=narrow). The old
// behavior returned 2 for every RGI emoji, which padded markdown table cells
// one column too wide and misaligned the box borders. Wide-base emoji (❌),
// keycaps, ZWJ sequences and RI flag pairs stay at 2 everywhere.
func TestGraphemeWidthVS16EmojiPresentation(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"pause button U+23F8+FE0F neutral base", "⏸️", 1},
		{"warning sign U+26A0+FE0F neutral base", "⚠️", 1},
		{"heart U+2764+FE0F neutral base", "❤️", 1},
		{"sun U+2600+FE0F neutral base", "☀️", 1},
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
	if got := VisibleWidth("决策，⏸️ 按需延后"); got != 16 {
		t.Errorf("VisibleWidth(composite) = %d, want 16", got)
	}
}
