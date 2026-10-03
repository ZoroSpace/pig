// frame_body.go — pig-only: the body of the corner-framed block
// (docs/feat/BUILTIN-THEME-PALETTES.md §BLOCK-BORDERS). Renders a block's
// content one column narrower so it stays flush with the corner rules
// (┌───┐ / └───┘) and leaves the last column free for the chat viewport's
// transient scrollbar.
package components

import (
	"github.com/ZoroSpace/pig/tui"
)

// FrameBody renders its child at width-1, matching the corner rules' span.
type FrameBody struct {
	child tui.Component
}

// NewFrameBody wraps a block's content for the corner-frame shells.
func NewFrameBody(child tui.Component) *FrameBody {
	return &FrameBody{child: child}
}

// Invalidate forwards to the child.
func (r *FrameBody) Invalidate() { r.child.Invalidate() }

// Render draws the child one column narrower than the viewport width.
func (r *FrameBody) Render(width int) []string {
	w := width - 1
	if w < 1 {
		w = 1
	}
	return r.child.Render(w)
}

// HandleMouse forwards to the child with the width clamped to the frame span.
func (r *FrameBody) HandleMouse(event *tui.TuiMouseEvent) *tui.TuiMouseEventResult {
	if event == nil {
		return nil
	}
	shifted := *event
	if shifted.Width > 1 {
		shifted.Width--
	}
	if shifted.X >= shifted.Width {
		shifted.X = shifted.Width - 1
	}
	return tui.DispatchMouseEvent(r.child, &shifted)
}
