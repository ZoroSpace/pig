package components

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// pig-only: the LeftRail joins a block's top and bottom rules on the left
// (docs/feat/BUILTIN-THEME-PALETTES.md §BLOCK-BORDERS).

func TestLeftRailPrefixesChildLines(t *testing.T) {
	initTestTheme(t)

	child := tui.NewText("one\ntwo", 0, 0)
	rail := NewLeftRail(child, func(s string) string { return "\x1b[36m" + s + "\x1b[39m" })
	lines := rail.Render(10)

	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	for i, want := range []string{"one", "two"} {
		got := utils.StripAnsi(lines[i])
		if !strings.HasPrefix(got, "┆") || !strings.HasSuffix(got, "┆") || !strings.Contains(got, want) {
			t.Fatalf("line %d = %q, want rail-wrapped %q", i, got, want)
		}
		if len([]rune(got)) > 10 {
			t.Fatalf("line %d exceeds width: %q", i, got)
		}
	}
}

func TestLeftRailForwardsMouseWithColumnShift(t *testing.T) {
	initTestTheme(t)

	var seen *tui.TuiMouseEvent
	child := tui.NewMouseRegion(tui.NewText("x", 0, 0), func(event *tui.TuiMouseEvent) *tui.TuiMouseEventResult {
		seen = event
		return &tui.TuiMouseEventResult{Handled: true}
	})
	rail := NewLeftRail(child, nil)

	result := rail.HandleMouse(&tui.TuiMouseEvent{Type: tui.MouseEventTypeClick, X: 3, Y: 0, Width: 20, Height: 1})
	if result == nil || !result.Handled || seen == nil || seen.X != 2 {
		t.Fatalf("mouse not shifted to child: result=%v seen=%v", result, seen)
	}

	// A click on the bar itself (column 0) clamps to the first content column.
	seen = nil
	result = rail.HandleMouse(&tui.TuiMouseEvent{Type: tui.MouseEventTypeClick, X: 0, Y: 0, Width: 20, Height: 1})
	if result == nil || !result.Handled || seen == nil || seen.X != 0 {
		t.Fatalf("bar click not clamped: result=%v seen=%v", result, seen)
	}
}
