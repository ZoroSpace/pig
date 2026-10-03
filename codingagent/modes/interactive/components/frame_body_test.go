package components

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/tui"
)

// pig-only: FrameBody renders a corner-framed block's content one column
// narrower than the viewport, flush with the corner rules and clear of the
// scrollbar column (docs/feat/BUILTIN-THEME-PALETTES.md §BLOCK-BORDERS).

func TestFrameBodyRendersChildOneColumnNarrower(t *testing.T) {
	child := &widthRecordingComponent{}
	body := NewFrameBody(child)
	lines := body.Render(40)

	if len(lines) != 1 {
		t.Fatalf("lines = %d", len(lines))
	}
	if child.lastWidth != 39 {
		t.Fatalf("child width = %d, want 39", child.lastWidth)
	}
}

func TestFrameBodyForwardsMouseWithFrameSpan(t *testing.T) {
	var seen *tui.TuiMouseEvent
	child := tui.NewMouseRegion(tui.NewText("x", 0, 0), func(event *tui.TuiMouseEvent) *tui.TuiMouseEventResult {
		seen = event
		return &tui.TuiMouseEventResult{Handled: true}
	})
	body := NewFrameBody(child)

	result := body.HandleMouse(&tui.TuiMouseEvent{Type: tui.MouseEventTypeClick, X: 3, Y: 0, Width: 40, Height: 1})
	if result == nil || !result.Handled || seen == nil || seen.Width != 39 {
		t.Fatalf("mouse width not clamped to frame span: result=%v seen=%v", result, seen)
	}

	// A click in the scrollbar gutter clamps into the frame span.
	seen = nil
	result = body.HandleMouse(&tui.TuiMouseEvent{Type: tui.MouseEventTypeClick, X: 39, Y: 0, Width: 40, Height: 1})
	if result == nil || !result.Handled || seen == nil || seen.X != 38 {
		t.Fatalf("gutter click not clamped: result=%v seen=%v", result, seen)
	}
}

// widthRecordingComponent records the render width it was asked for.
type widthRecordingComponent struct {
	lastWidth int
}

func (c *widthRecordingComponent) Render(width int) []string {
	c.lastWidth = width
	return []string{strings.Repeat(" ", width)}
}

func (c *widthRecordingComponent) Invalidate() {}
