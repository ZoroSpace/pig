// dynamic_border.go (pi: components/dynamic-border.ts) — a border
// component that adjusts to the viewport width.
package components

import (
	"strings"

	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
)

// DynamicBorder is a one-line horizontal rule (pi: class DynamicBorder).
//
// Note (pi comment, preserved): when used from extensions loaded via jiti,
// the global `theme` may be undefined because jiti creates a separate
// module cache. Always pass an explicit color function when using
// DynamicBorder in components exported for extension use.
type DynamicBorder struct {
	color func(string) string
	// corner prefixes the rule with a left box-drawing glyph (pig-only block
	// frame: '┌' tops / '└' bottoms connect the block's left rail).
	corner rune
}

// NewDynamicBorder builds a border with the given colorizer (nil =
// theme.fg("border", ...) — pi's default parameter).
func NewDynamicBorder(color func(string) string) *DynamicBorder {
	if color == nil {
		color = func(s string) string { return theme.Fg("border", s) }
	}
	return &DynamicBorder{color: color}
}

// NewDynamicBorderCorner builds a rule with left corner glyphs (pig-only:
// the block frame's top/bottom rules — ┌───┐ / └───┘).
func NewDynamicBorderCorner(color func(string) string, corner rune) *DynamicBorder {
	b := NewDynamicBorder(color)
	b.corner = corner
	return b
}

// Invalidate has no cached state to clear (pi: invalidate).
func (d *DynamicBorder) Invalidate() {}

// Render draws the border line (pi: render). The pig-only block-frame
// variants (corner set) render solid rules with both corners:
// ┌───┐ / └───┘, joined by the rail's dashed vertical bars. The frame
// leaves the last column free: the chat viewport's transient scrollbar
// paints over the right edge (pi overlays it on full-width content), and
// the frame's right corner must not compete with it.
func (d *DynamicBorder) Render(width int) []string {
	n := width
	if n < 1 {
		n = 1
	}
	if d.corner != 0 {
		runes := n - 3
		if runes < 1 {
			runes = 1
		}
		closing := '┐'
		if d.corner == '└' {
			closing = '┘'
		}
		return []string{d.color(string(d.corner) + strings.Repeat("─", runes) + string(closing))}
	}
	return []string{d.color(strings.Repeat("─", n))}
}
