// left_rail.go — pig-only: the left/right connectors of the border-only
// block frame (docs/feat/BUILTIN-THEME-PALETTES.md §BLOCK-BORDERS). Wraps a
// block's content and prefixes/suffixes every rendered line with a dashed │
// bar so the block's top and bottom rules read as one continuous dashed box.
package components

import (
	"strings"

	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// LeftRail renders its child at width-2 and wraps each line between two
// themed dashed bars, keeping the total within the viewport width.
type LeftRail struct {
	child tui.Component
	color func(string) string
}

// NewLeftRail wraps a child with the vertical bars (color nil = theme
// "border").
func NewLeftRail(child tui.Component, color func(string) string) *LeftRail {
	if color == nil {
		color = func(s string) string { return theme.Fg("border", s) }
	}
	return &LeftRail{child: child, color: color}
}

// Invalidate forwards to the child.
func (r *LeftRail) Invalidate() { r.child.Invalidate() }

// Render wraps the child's lines between the bars. The child renders three
// columns narrower (bars at both edges plus the scrollbar gutter — see
// DynamicBorderCorner); short lines are space-padded so the right bar stays
// aligned.
func (r *LeftRail) Render(width int) []string {
	w := width - 3
	if w < 1 {
		w = 1
	}
	lines := r.child.Render(w)
	bar := r.color("┆")
	out := make([]string, len(lines))
	for i, line := range lines {
		if vis := tui.VisibleWidth(line); vis < w {
			line += strings.Repeat(" ", w-vis)
		} else if vis > w {
			line = tui.SliceWithWidth(line, 0, w, true).Text
		}
		out[i] = bar + line + bar
	}
	return out
}

// HandleMouse forwards to the child shifted one column right (clicks on the
// bars act on the adjacent content column).
func (r *LeftRail) HandleMouse(event *tui.TuiMouseEvent) *tui.TuiMouseEventResult {
	if event == nil {
		return nil
	}
	shifted := *event
	if shifted.Width > 2 {
		shifted.Width -= 2
	}
	if shifted.X > 0 {
		shifted.X--
	}
	if shifted.X >= shifted.Width {
		shifted.X = shifted.Width - 1
	}
	return tui.DispatchMouseEvent(r.child, &shifted)
}
