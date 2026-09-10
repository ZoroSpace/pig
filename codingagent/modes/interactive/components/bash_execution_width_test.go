// bash_execution_width_test.go — port of test/bash-execution-width.test.ts.
//
// Test that BashExecutionComponent's collapsed output respects the
// render-time width, not a stale captured width. Regression test for
// pi#2569.
//
// Test adaptation: the TUI stub embeds tui.TUI and overrides only the
// surface the component/Loader consume (pi: partial object cast).
package components

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/tui"
)

type widthTestTUI struct {
	tui.TUI
}

func (f *widthTestTUI) RequestRender(force bool) {}

func newWidthTestTUI() *widthTestTUI { return &widthTestTUI{} }

func TestBashExecutionWidthHandling(t *testing.T) {
	initTestTheme(t)

	t.Run("collapsed preview lines respect render-time width, not construction-time width", func(t *testing.T) {
		wideWidth := 200
		narrowWidth := 80

		stub := newWidthTestTUI()
		component := NewBashExecutionComponent("pwd", stub, false)

		// Add output with long lines that will wrap differently at different widths
		longLine := strings.Repeat("x", 150)
		component.AppendOutput(longLine + "\n" + longLine + "\n")

		// Complete the command so it enters collapsed mode
		zero := 0
		component.SetComplete(&zero, false, nil, "")

		// Render at the narrow width (simulating a resize or split pane)
		lines := component.Render(narrowWidth)

		// Every rendered line must fit within the narrow width
		for i, line := range lines {
			if w := tui.VisibleWidth(line); w > narrowWidth {
				t.Fatalf("Line %d visibleWidth=%d > %d: %q", i, w, narrowWidth, line)
			}
		}
		_ = wideWidth
	})

	t.Run("re-computes lines when width changes between renders", func(t *testing.T) {
		stub := newWidthTestTUI()
		component := NewBashExecutionComponent("echo hello", stub, false)

		longLine := strings.Repeat("abcdefghij", 20) // 200 chars
		component.AppendOutput(longLine + "\n")
		zero := 0
		component.SetComplete(&zero, false, nil, "")

		// First render at width 200
		for _, line := range component.Render(200) {
			if w := tui.VisibleWidth(line); w > 200 {
				t.Fatalf("visibleWidth=%d > 200: %q", w, line)
			}
		}

		// Second render at width 60 (split pane scenario)
		for i, line := range component.Render(60) {
			if w := tui.VisibleWidth(line); w > 60 {
				t.Fatalf("Line %d visibleWidth=%d > 60: %q", i, w, line)
			}
		}
	})
}
