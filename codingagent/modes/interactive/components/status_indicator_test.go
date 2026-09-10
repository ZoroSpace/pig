package components

import (
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/tui"
)

// Port of test/status-indicator.test.ts.
//
// Test adaptation (declared): pi uses vi.useFakeTimers to advance the
// countdown; pig's countdown runs on a real ticker goroutine, so the
// dispose test sleeps past one tick interval instead of advancing fake
// timers.

func TestStatusIndicators(t *testing.T) {
	t.Run("keeps idle status at the same height as status indicators", func(t *testing.T) {
		idleStatus := &IdleStatus{}

		lines := idleStatus.Render(20)
		if len(lines) != 2 {
			t.Fatalf("lines = %d", len(lines))
		}
		want := strings.Repeat(" ", 20)
		if lines[0] != want || lines[1] != want {
			t.Fatalf("lines = %q", lines)
		}
	})

	t.Run("disposes retry countdown updates", func(t *testing.T) {
		initTestTheme(t)
		fakeTUI := &countRenderTUI{}
		indicator := NewRetryStatusIndicator(fakeTUI, 1, 3, 1000)
		callsBeforeDispose := fakeTUI.renderCalls

		indicator.Dispose()
		// Give the ticker goroutine time to fire one tick if it were still
		// running (pi: vi.advanceTimersByTime(2000)).
		time.Sleep(1200 * time.Millisecond)

		if fakeTUI.renderCalls != callsBeforeDispose {
			t.Fatalf("requestRender after dispose: %d -> %d", callsBeforeDispose, fakeTUI.renderCalls)
		}
	})
}

// countRenderTUI stubs the TUI surface NewLoader consumes.
type countRenderTUI struct {
	tui.TUI
	renderCalls int
}

func (c *countRenderTUI) RequestRender(force bool) {
	c.renderCalls++
}
