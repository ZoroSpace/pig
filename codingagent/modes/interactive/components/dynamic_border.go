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
}

// NewDynamicBorder builds a border with the given colorizer (nil =
// theme.fg("border", ...) — pi's default parameter).
func NewDynamicBorder(color func(string) string) *DynamicBorder {
	if color == nil {
		color = func(s string) string { return theme.Fg("border", s) }
	}
	return &DynamicBorder{color: color}
}

// Invalidate has no cached state to clear (pi: invalidate).
func (d *DynamicBorder) Invalidate() {}

// Render draws the border line (pi: render).
func (d *DynamicBorder) Render(width int) []string {
	n := width
	if n < 1 {
		n = 1
	}
	return []string{d.color(strings.Repeat("─", n))}
}
