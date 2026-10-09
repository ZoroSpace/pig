// tui/tui_render_test.go — ported from pi: packages/tui/test/tui-render.test.ts
//
// Declared skips (README §4 Phase 8): the four tests constructing the Image
// component (clears reserved rows before appended image placements / full
// redraw fallback when pre-clear would scroll / reserves rows during full
// redraw fallbacks / taller-than-viewport placement) require the Phase 8
// image pipeline. The encodeKitty-based cleanup tests are ported.
//
// Declared deviation (Go goroutine scheduling): pi's "renders keyboard input
// without waiting for a throttled frame" asserts an exact render count that
// relies on JS microtask coalescing within one event-loop turn; in Go the
// immediate-render goroutine may run between synchronous SendInput calls, so
// the test asserts renders happened promptly and the final input state, not
// the exact count.
package tui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testComponent lines/renderCount access is mutex-guarded: in Go the render
// pipeline runs on goroutines, so the test fixture must be race-free (pi
// relies on the event loop).
type testComponent struct {
	mu    sync.Mutex
	lines []string
}

func (c *testComponent) SetLines(lines []string) {
	c.mu.Lock()
	c.lines = lines
	c.mu.Unlock()
}

func (c *testComponent) Lines() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lines
}

func (c *testComponent) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lines
}

func (c *testComponent) Invalidate() {}

type inputTestComponent struct {
	testComponent
	renderCount int
}

func (c *inputTestComponent) Render(width int) []string {
	c.mu.Lock()
	c.renderCount++
	lines := c.lines
	c.mu.Unlock()
	return lines
}

func (c *inputTestComponent) RenderCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.renderCount
}

func (c *inputTestComponent) HandleInput(data string) {
	c.SetLines([]string{data})
}

func waitForCondition(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

func TestTuiRenderScheduling(t *testing.T) {
	t.Run("renders keyboard input without waiting for a throttled frame", func(t *testing.T) {
		terminal := NewVirtualTerminal(40, 10)
		tui := NewTuiMainScreen(terminal, nil, "")
		component := &inputTestComponent{}
		component.SetLines([]string{"initial"})
		tui.AddChild(component)
		tui.SetFocus(component)
		tui.Start()
		tui.RenderNow(false)
		renderCountBeforeInput := component.RenderCount()

		// Queue a normal throttled render first. Keyboard input should preempt it.
		component.SetLines([]string{"pending"})
		tui.RequestRender(false)
		start := time.Now()
		terminal.SendInput("first")
		terminal.SendInput("second")
		terminal.SendInput("typed")

		if !waitForCondition(200*time.Millisecond, func() bool {
			return component.RenderCount() >= renderCountBeforeInput+1
		}) {
			t.Fatalf("input did not trigger a prompt render (count=%d, elapsed=%v)", component.RenderCount(), time.Since(start))
		}

		if !waitForCondition(200*time.Millisecond, func() bool {
			return len(component.Lines()) == 1 && component.Lines()[0] == "typed"
		}) {
			t.Fatalf("lines = %q", component.Lines())
		}
		tui.Stop(nil)
	})
}

func TestTuiDebugLogging(t *testing.T) {
	t.Run("writes redraw logs to the provided directory", func(t *testing.T) {
		logDir := t.TempDir()
		t.Setenv("PI_TUI_DEBUG_REDRAW", "1")
		terminal := NewVirtualTerminal(40, 10)
		tui := NewTuiMainScreen(terminal, nil, logDir)
		component := &testComponent{}
		tui.AddChild(component)
		component.SetLines([]string{"test"})
		tui.Start()
		terminal.WaitForRender()

		data, err := os.ReadFile(filepath.Join(logDir, "pi-tui-debug.log"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "fullRender: first render") {
			t.Fatalf("log = %q", data)
		}
		tui.Stop(nil)
	})
}

func TestTuiKittyImageCleanup(t *testing.T) {
	t.Run("deletes changed image ids before drawing moved placements", func(t *testing.T) {
		terminal := NewVirtualTerminal(40, 10)
		tui := NewTuiMainScreen(terminal, nil, "")
		component := &testComponent{}
		tui.AddChild(component)

		oldImage := EncodeKitty("AAAA", EncodeKittyParams{Columns: 2, Rows: 2, ImageId: 42, MoveCursorOff: true})
		component.SetLines([]string{"top", oldImage})
		tui.Start()
		terminal.WaitForRender()
		terminal.ClearWrites()

		newImage := EncodeKitty("BBBB", EncodeKittyParams{Columns: 2, Rows: 1, ImageId: 42, MoveCursorOff: true})
		component.SetLines([]string{newImage, ""})
		tui.RequestRender(false)
		terminal.WaitForRender()

		writes := terminal.GetWrites()
		deleteIndex := strings.Index(writes, DeleteKittyImage(42))
		drawIndex := strings.Index(writes, newImage)
		if deleteIndex < 0 {
			t.Fatal("changed old image should be deleted")
		}
		if drawIndex < 0 {
			t.Fatal("new image should be drawn")
		}
		if deleteIndex >= drawIndex {
			t.Fatal("old image must be deleted before the new placement is drawn")
		}

		tui.Stop(nil)
	})

	t.Run("redraws image lines when an earlier reserved image row changes", func(t *testing.T) {
		terminal := NewVirtualTerminal(40, 10)
		tui := NewTuiMainScreen(terminal, nil, "")
		component := &testComponent{}
		tui.AddChild(component)

		image := EncodeKitty("AAAA", EncodeKittyParams{Columns: 2, Rows: 2, ImageId: 88, MoveCursorOff: true})
		component.SetLines([]string{"", image})
		tui.Start()
		terminal.WaitForRender()
		terminal.ClearWrites()

		component.SetLines([]string{"covered", image})
		tui.RequestRender(false)
		terminal.WaitForRender()

		writes := terminal.GetWrites()
		deleteIndex := strings.Index(writes, DeleteKittyImage(88))
		drawIndex := strings.Index(writes, image)
		if deleteIndex < 0 {
			t.Fatal("image should be deleted when a reserved row changes")
		}
		if drawIndex < 0 {
			t.Fatal("unchanged image line should be redrawn after deleting the placement")
		}
		if deleteIndex >= drawIndex {
			t.Fatal("old placement must be deleted before the image line is redrawn")
		}
		if strings.Contains(writes, "\x1b[2J") {
			t.Fatal("reserved row changes should not force a full redraw")
		}

		tui.Stop(nil)
	})

	t.Run("deletes previously rendered image ids during full redraws", func(t *testing.T) {
		terminal := NewVirtualTerminal(40, 10)
		tui := NewTuiMainScreen(terminal, nil, "")
		component := &testComponent{}
		tui.AddChild(component)

		component.SetLines([]string{EncodeKitty("AAAA", EncodeKittyParams{Columns: 2, Rows: 2, ImageId: 77, MoveCursorOff: true})})
		tui.Start()
		terminal.WaitForRender()
		terminal.ClearWrites()

		component.SetLines([]string{"plain text"})
		tui.RequestRender(true)
		terminal.WaitForRender()

		writes := terminal.GetWrites()
		deleteIndex := strings.Index(writes, DeleteKittyImage(77))
		clearIndex := strings.Index(writes, "\x1b[2J")
		if deleteIndex < 0 {
			t.Fatal("previous image should be deleted during full redraw")
		}
		if clearIndex < 0 {
			t.Fatal("full redraw should clear the screen")
		}
		if deleteIndex >= clearIndex {
			t.Fatal("old image should be deleted before the screen is cleared")
		}

		tui.Stop(nil)
	})
}

func TestTuiResizeHandling(t *testing.T) {
	t.Run("triggers full re-render when terminal height changes", func(t *testing.T) {
		t.Setenv("TERMUX_VERSION", "")
		terminal := NewVirtualTerminal(40, 10)
		tui := NewTuiMainScreen(terminal, nil, "")
		component := &testComponent{}
		tui.AddChild(component)

		component.SetLines([]string{"Line 0", "Line 1", "Line 2"})
		tui.Start()
		terminal.WaitForRender()

		initialRedraws := tui.FullRedraws()

		// Resize height
		terminal.Resize(40, 15)
		terminal.WaitForRender()

		// Should have triggered a full redraw
		if tui.FullRedraws() <= initialRedraws {
			t.Fatal("height change should trigger full redraw")
		}

		viewport := terminal.GetViewport()
		if !strings.Contains(viewport[0], "Line 0") {
			t.Fatalf("content not preserved after height change: %q", viewport[0])
		}

		tui.Stop(nil)
	})

	t.Run("skips full re-render on height changes in Termux", func(t *testing.T) {
		t.Setenv("TERMUX_VERSION", "1")
		terminal := NewVirtualTerminal(40, 10)
		tui := NewTuiMainScreen(terminal, nil, "")
		component := &testComponent{}
		tui.AddChild(component)

		lines := make([]string, 20)
		for i := range lines {
			lines[i] = "Line " + itoa(i)
		}
		component.SetLines(lines)
		tui.Start()
		terminal.WaitForRender()
		terminal.ClearWrites()

		initialRedraws := tui.FullRedraws()
		for _, height := range []int{15, 8, 14, 11} {
			terminal.Resize(40, height)
			terminal.WaitForRender()
		}

		if tui.FullRedraws() != initialRedraws {
			t.Fatal("height change should not trigger full redraw")
		}
		if strings.Contains(terminal.GetWrites(), "\x1b[2J") {
			t.Fatal("height change should not clear the screen")
		}
		if strings.Contains(terminal.GetWrites(), "\x1b[3J") {
			t.Fatal("height change should not clear scrollback")
		}

		viewport := terminal.GetViewport()
		if !strings.Contains(strings.Join(viewport, "\n"), "Line 19") {
			t.Fatalf("latest content should remain visible: %q", viewport)
		}

		tui.Stop(nil)
	})

	t.Run("triggers full re-render when terminal width changes", func(t *testing.T) {
		terminal := NewVirtualTerminal(40, 10)
		tui := NewTuiMainScreen(terminal, nil, "")
		component := &testComponent{}
		tui.AddChild(component)

		component.SetLines([]string{"Line 0", "Line 1", "Line 2"})
		tui.Start()
		terminal.WaitForRender()

		initialRedraws := tui.FullRedraws()

		// Resize width
		terminal.Resize(60, 10)
		terminal.WaitForRender()

		// Should have triggered a full redraw
		if tui.FullRedraws() <= initialRedraws {
			t.Fatal("width change should trigger full redraw")
		}

		tui.Stop(nil)
	})
}

func TestTuiContentShrinkage(t *testing.T) {
	setup := func(t *testing.T) (*VirtualTerminal, *TuiMainScreen, *testComponent) {
		t.Helper()
		terminal := NewVirtualTerminal(40, 10)
		tui := NewTuiMainScreen(terminal, nil, "")
		tui.SetClearOnShrink(true) // Explicitly enable (may be disabled via env var)
		component := &testComponent{}
		tui.AddChild(component)
		return terminal, tui, component
	}
	t.Run("clears empty rows when content shrinks significantly", func(t *testing.T) {
		terminal, tui, component := setup(t)

		component.SetLines([]string{"Line 0", "Line 1", "Line 2", "Line 3", "Line 4", "Line 5"})
		tui.Start()
		terminal.WaitForRender()

		initialRedraws := tui.FullRedraws()

		// Shrink to fewer lines
		component.SetLines([]string{"Line 0", "Line 1"})
		tui.RequestRender(false)
		terminal.WaitForRender()

		// Should have triggered a full redraw to clear empty rows
		if tui.FullRedraws() <= initialRedraws {
			t.Fatal("content shrinkage should trigger full redraw")
		}

		viewport := terminal.GetViewport()
		if !strings.Contains(viewport[0], "Line 0") {
			t.Fatalf("first line not preserved: %q", viewport[0])
		}
		if !strings.Contains(viewport[1], "Line 1") {
			t.Fatalf("second line not preserved: %q", viewport[1])
		}
		if strings.Trim(viewport[2], " ") != "" {
			t.Fatalf("line 2 should be cleared: %q", viewport[2])
		}
		if strings.Trim(viewport[3], " ") != "" {
			t.Fatalf("line 3 should be cleared: %q", viewport[3])
		}

		tui.Stop(nil)
	})
	t.Run("handles shrink to single line", func(t *testing.T) {
		terminal, tui, component := setup(t)

		component.SetLines([]string{"Line 0", "Line 1", "Line 2", "Line 3"})
		tui.Start()
		terminal.WaitForRender()

		// Shrink to single line
		component.SetLines([]string{"Only line"})
		tui.RequestRender(false)
		terminal.WaitForRender()

		viewport := terminal.GetViewport()
		if !strings.Contains(viewport[0], "Only line") {
			t.Fatalf("single line not rendered: %q", viewport[0])
		}
		if strings.Trim(viewport[1], " ") != "" {
			t.Fatalf("line 1 should be cleared: %q", viewport[1])
		}

		tui.Stop(nil)
	})
	t.Run("handles shrink to empty", func(t *testing.T) {
		terminal, tui, component := setup(t)

		component.SetLines([]string{"Line 0", "Line 1", "Line 2"})
		tui.Start()
		terminal.WaitForRender()

		// Shrink to empty
		component.SetLines(nil)
		tui.RequestRender(false)
		terminal.WaitForRender()

		viewport := terminal.GetViewport()
		if strings.Trim(viewport[0], " ") != "" {
			t.Fatalf("line 0 should be cleared: %q", viewport[0])
		}
		if strings.Trim(viewport[1], " ") != "" {
			t.Fatalf("line 1 should be cleared: %q", viewport[1])
		}

		tui.Stop(nil)
	})
}

func TestTuiDifferentialRendering(t *testing.T) {
	setup := func(t *testing.T, cols, rows int) (*VirtualTerminal, *TuiMainScreen, *testComponent) {
		t.Helper()
		terminal := NewVirtualTerminal(cols, rows)
		tui := NewTuiMainScreen(terminal, nil, "")
		component := &testComponent{}
		tui.AddChild(component)
		return terminal, tui, component
	}
	t.Run("tracks cursor correctly when content shrinks with unchanged remaining lines", func(t *testing.T) {
		terminal, tui, component := setup(t, 40, 10)

		component.SetLines([]string{"Line 0", "Line 1", "Line 2", "Line 3", "Line 4"})
		tui.Start()
		terminal.WaitForRender()

		// Shrink to 3 lines, all identical to before (no content changes in remaining lines)
		component.SetLines([]string{"Line 0", "Line 1", "Line 2"})
		tui.RequestRender(false)
		terminal.WaitForRender()

		// Verify by doing another render with a change on line 1
		component.SetLines([]string{"Line 0", "CHANGED", "Line 2"})
		tui.RequestRender(false)
		terminal.WaitForRender()

		viewport := terminal.GetViewport()
		if !strings.Contains(viewport[1], "CHANGED") {
			t.Fatalf("expected \"CHANGED\" on line 1, got: %q", viewport[1])
		}

		tui.Stop(nil)
	})
	t.Run("renders correctly when only a middle line changes (spinner case)", func(t *testing.T) {
		terminal, tui, component := setup(t, 40, 10)

		component.SetLines([]string{"Header", "Working...", "Footer"})
		tui.Start()
		terminal.WaitForRender()

		// Simulate spinner animation - only middle line changes
		for _, frame := range []string{"|", "/", "-", "\\"} {
			component.SetLines([]string{"Header", "Working " + frame, "Footer"})
			tui.RequestRender(false)
			terminal.WaitForRender()

			viewport := terminal.GetViewport()
			if !strings.Contains(viewport[0], "Header") {
				t.Fatalf("header not preserved: %q", viewport[0])
			}
			if !strings.Contains(viewport[1], "Working "+frame) {
				t.Fatalf("spinner not updated: %q", viewport[1])
			}
			if !strings.Contains(viewport[2], "Footer") {
				t.Fatalf("footer not preserved: %q", viewport[2])
			}
		}

		tui.Stop(nil)
	})
	t.Run("resets styles after each rendered line", func(t *testing.T) {
		terminal, tui, component := setup(t, 20, 6)

		component.SetLines([]string{"\x1b[3mItalic", "Plain"})
		tui.Start()
		terminal.WaitForRender()

		if terminal.GetCellItalic(1, 0) {
			t.Fatal("cell at row 1 col 0 must not be italic")
		}
		tui.Stop(nil)
	})
	t.Run("renders correctly when first line changes but rest stays same", func(t *testing.T) {
		terminal, tui, component := setup(t, 40, 10)

		component.SetLines([]string{"Line 0", "Line 1", "Line 2", "Line 3"})
		tui.Start()
		terminal.WaitForRender()

		component.SetLines([]string{"CHANGED", "Line 1", "Line 2", "Line 3"})
		tui.RequestRender(false)
		terminal.WaitForRender()

		viewport := terminal.GetViewport()
		if !strings.Contains(viewport[0], "CHANGED") {
			t.Fatalf("first line not changed: %q", viewport[0])
		}
		if !strings.Contains(viewport[1], "Line 1") {
			t.Fatalf("line 1 not preserved: %q", viewport[1])
		}
		if !strings.Contains(viewport[2], "Line 2") {
			t.Fatalf("line 2 not preserved: %q", viewport[2])
		}
		if !strings.Contains(viewport[3], "Line 3") {
			t.Fatalf("line 3 not preserved: %q", viewport[3])
		}

		tui.Stop(nil)
	})
	t.Run("renders correctly when last line changes but rest stays same", func(t *testing.T) {
		terminal, tui, component := setup(t, 40, 10)

		component.SetLines([]string{"Line 0", "Line 1", "Line 2", "Line 3"})
		tui.Start()
		terminal.WaitForRender()

		component.SetLines([]string{"Line 0", "Line 1", "Line 2", "CHANGED"})
		tui.RequestRender(false)
		terminal.WaitForRender()

		viewport := terminal.GetViewport()
		if !strings.Contains(viewport[0], "Line 0") {
			t.Fatalf("line 0 not preserved: %q", viewport[0])
		}
		if !strings.Contains(viewport[1], "Line 1") {
			t.Fatalf("line 1 not preserved: %q", viewport[1])
		}
		if !strings.Contains(viewport[2], "Line 2") {
			t.Fatalf("line 2 not preserved: %q", viewport[2])
		}
		if !strings.Contains(viewport[3], "CHANGED") {
			t.Fatalf("last line not changed: %q", viewport[3])
		}

		tui.Stop(nil)
	})
	t.Run("renders correctly when multiple non-adjacent lines change", func(t *testing.T) {
		terminal, tui, component := setup(t, 40, 10)

		component.SetLines([]string{"Line 0", "Line 1", "Line 2", "Line 3", "Line 4"})
		tui.Start()
		terminal.WaitForRender()

		component.SetLines([]string{"Line 0", "CHANGED 1", "Line 2", "CHANGED 3", "Line 4"})
		tui.RequestRender(false)
		terminal.WaitForRender()

		viewport := terminal.GetViewport()
		if !strings.Contains(viewport[0], "Line 0") {
			t.Fatalf("line 0 not preserved: %q", viewport[0])
		}
		if !strings.Contains(viewport[1], "CHANGED 1") {
			t.Fatalf("line 1 not changed: %q", viewport[1])
		}
		if !strings.Contains(viewport[2], "Line 2") {
			t.Fatalf("line 2 not preserved: %q", viewport[2])
		}
		if !strings.Contains(viewport[3], "CHANGED 3") {
			t.Fatalf("line 3 not changed: %q", viewport[3])
		}
		if !strings.Contains(viewport[4], "Line 4") {
			t.Fatalf("line 4 not preserved: %q", viewport[4])
		}

		tui.Stop(nil)
	})
	t.Run("handles transition from content to empty and back to content", func(t *testing.T) {
		terminal, tui, component := setup(t, 40, 10)

		component.SetLines([]string{"Line 0", "Line 1", "Line 2"})
		tui.Start()
		terminal.WaitForRender()

		viewport := terminal.GetViewport()
		if !strings.Contains(viewport[0], "Line 0") {
			t.Fatalf("initial content not rendered: %q", viewport[0])
		}

		// Clear to empty
		component.SetLines(nil)
		tui.RequestRender(false)
		terminal.WaitForRender()

		// Add content back - this should work correctly even after empty state
		component.SetLines([]string{"New Line 0", "New Line 1"})
		tui.RequestRender(false)
		terminal.WaitForRender()

		viewport = terminal.GetViewport()
		if !strings.Contains(viewport[0], "New Line 0") {
			t.Fatalf("new content not rendered: %q", viewport[0])
		}
		if !strings.Contains(viewport[1], "New Line 1") {
			t.Fatalf("new content line 1 not rendered: %q", viewport[1])
		}

		tui.Stop(nil)
	})
	t.Run("full re-renders when deleted lines move the viewport upward", func(t *testing.T) {
		terminal, tui, component := setup(t, 20, 5)

		lines12 := make([]string, 12)
		for i := range lines12 {
			lines12[i] = "Line " + itoa(i)
		}
		lines7 := lines12[:7]
		component.SetLines(lines12)
		tui.Start()
		terminal.WaitForRender()

		initialRedraws := tui.FullRedraws()

		component.SetLines(lines7)
		tui.RequestRender(false)
		terminal.WaitForRender()

		if tui.FullRedraws() <= initialRedraws {
			t.Fatal("shrink should trigger a full redraw")
		}
		want := []string{"Line 2", "Line 3", "Line 4", "Line 5", "Line 6"}
		got := terminal.GetViewport()
		if len(got) != len(want) {
			t.Fatalf("viewport = %q, want %q", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("viewport = %q, want %q", got, want)
			}
		}

		tui.Stop(nil)
	})
	t.Run("appends after a shrink without another full redraw once the viewport is reset", func(t *testing.T) {
		terminal, tui, component := setup(t, 20, 5)

		lines8 := make([]string, 8)
		for i := range lines8 {
			lines8[i] = "Line " + itoa(i)
		}
		component.SetLines(lines8)
		tui.Start()
		terminal.WaitForRender()

		initialRedraws := tui.FullRedraws()

		component.SetLines([]string{"Line 0", "Line 1"})
		tui.RequestRender(false)
		terminal.WaitForRender()

		if tui.FullRedraws() <= initialRedraws {
			t.Fatal("shrink should reset the viewport with a full redraw")
		}
		redrawsAfterShrink := tui.FullRedraws()

		component.SetLines([]string{"Line 0", "Line 1", "Line 2"})
		tui.RequestRender(false)
		terminal.WaitForRender()

		if tui.FullRedraws() != redrawsAfterShrink {
			t.Fatal("append should stay on the differential path")
		}
		want := []string{"Line 0", "Line 1", "Line 2", "", ""}
		got := terminal.GetViewport()
		if len(got) != len(want) {
			t.Fatalf("viewport = %q, want %q", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("viewport = %q, want %q", got, want)
			}
		}

		tui.Stop(nil)
	})
	t.Run("clears stale content when maxLinesRendered was inflated by a transient component", func(t *testing.T) {
		terminal := NewVirtualTerminal(40, 10)
		tui := NewTuiMainScreen(terminal, nil, "")
		chat := &testComponent{}
		editor := &testComponent{}
		tui.AddChild(chat)
		tui.AddChild(editor)

		longChat := make([]string, 15)
		for i := range longChat {
			longChat[i] = "Chat " + itoa(i)
		}
		shortChat := make([]string, 12)
		for i := range shortChat {
			shortChat[i] = "Chat " + itoa(i)
		}
		editorLines := []string{"Editor 0", "Editor 1", "Editor 2"}
		selectorLines := make([]string, 8)
		for i := range selectorLines {
			selectorLines[i] = "Selector " + itoa(i)
		}

		chat.SetLines(longChat)
		editor.SetLines(editorLines)
		tui.Start()
		terminal.WaitForRender()

		editor.SetLines(selectorLines)
		tui.RequestRender(false)
		terminal.WaitForRender()

		editor.SetLines(editorLines)
		tui.RequestRender(false)
		terminal.WaitForRender()

		redrawsBeforeSwitch := tui.FullRedraws()
		chat.SetLines(shortChat)
		tui.RequestRender(false)
		terminal.WaitForRender()

		if tui.FullRedraws() <= redrawsBeforeSwitch {
			t.Fatal("branch switch should trigger a full redraw")
		}

		viewport := terminal.GetViewport()
		for i := 0; i < 10; i++ {
			line := ""
			if i < len(viewport) {
				line = viewport[i]
			}
			if strings.Contains(line, "Chat 12") {
				t.Fatalf("stale \"Chat 12\" at viewport row %d", i)
			}
			if strings.Contains(line, "Chat 13") {
				t.Fatalf("stale \"Chat 13\" at viewport row %d", i)
			}
			if strings.Contains(line, "Chat 14") {
				t.Fatalf("stale \"Chat 14\" at viewport row %d", i)
			}
		}

		want := []string{
			"Chat 5", "Chat 6", "Chat 7", "Chat 8", "Chat 9",
			"Chat 10", "Chat 11", "Editor 0", "Editor 1", "Editor 2",
		}
		got := terminal.GetViewport()
		if len(got) != len(want) {
			t.Fatalf("viewport = %q, want %q", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("viewport = %q, want %q", got, want)
			}
		}

		tui.Stop(nil)
	})
}

// boundedWriteTerminal records each terminal write (pi: BoundedWriteTerminal).
type boundedWriteTerminal struct {
	writes []string
	cols   int
	rows   int
}

func newBoundedWriteTerminal() *boundedWriteTerminal {
	return &boundedWriteTerminal{cols: 80, rows: 24}
}

func (t *boundedWriteTerminal) Start(onInput func(data string), onResize func()) {}
func (t *boundedWriteTerminal) Stop()                                            {}
func (t *boundedWriteTerminal) DrainInput(maxMs int, idleMs int)                 {}
func (t *boundedWriteTerminal) Write(data string)                                { t.writes = append(t.writes, data) }
func (t *boundedWriteTerminal) MoveBy(lines int)                                 {}
func (t *boundedWriteTerminal) HideCursor()                                      {}
func (t *boundedWriteTerminal) ShowCursor()                                      {}
func (t *boundedWriteTerminal) ClearLine()                                       {}
func (t *boundedWriteTerminal) ClearFromCursor()                                 {}
func (t *boundedWriteTerminal) ClearScreen()                                     {}
func (t *boundedWriteTerminal) SetTitle(title string)                            {}
func (t *boundedWriteTerminal) SetProgress(active bool)                          {}

func (t *boundedWriteTerminal) SetProgramStatus(status ProgramStatus) {}
func (t *boundedWriteTerminal) Columns() int                          { return t.cols }
func (t *boundedWriteTerminal) Rows() int                             { return t.rows }
func (t *boundedWriteTerminal) KittyProtocolActive() bool             { return false }
func (t *boundedWriteTerminal) ModifyOtherKeysActive() bool           { return false }

func TestTuiBoundedRenderOutputSplitsLargeFullRenderWithoutChangingItsOutput(t *testing.T) {
	terminal := newBoundedWriteTerminal()
	tui := NewTuiMainScreen(terminal, nil, "")
	component := &testComponent{}
	tui.AddChild(component)
	kittyLine := "\x1b_Ga=T,f=100;" + strings.Repeat("A", 1_200_000) + "\x1b\\"
	component.SetLines([]string{kittyLine, kittyLine})

	tui.RenderNow(false)

	if len(terminal.writes) <= 2 {
		t.Fatalf("writes = %d, want > 2 (large output should be split)", len(terminal.writes))
	}
	for _, write := range terminal.writes {
		if len(write) > maxRenderWriteChars {
			t.Fatalf("write length %d exceeds limit %d", len(write), maxRenderWriteChars)
		}
	}
	joined := strings.Join(terminal.writes, "")
	want := "\x1b[?2026h" + kittyLine + "\r\n" + kittyLine + "\x1b[?2026l"
	if joined != want {
		t.Fatalf("chunked output differs (lengths %d vs %d)", len(joined), len(want))
	}
}

func TestTuiBoundedRenderOutputSplitsLargeDifferentialUpdatesWithoutAFullRedraw(t *testing.T) {
	terminal := newBoundedWriteTerminal()
	tui := NewTuiMainScreen(terminal, nil, "")
	component := &testComponent{}
	tui.AddChild(component)
	component.SetLines([]string{"before"})
	tui.RenderNow(false)
	terminal.writes = nil

	kittyLine := "\x1b_Ga=T,f=100;" + strings.Repeat("A", 1_200_000) + "\x1b\\"
	component.SetLines([]string{"before", kittyLine, kittyLine})
	tui.RenderNow(false)

	if len(terminal.writes) <= 2 {
		t.Fatalf("writes = %d, want > 2", len(terminal.writes))
	}
	for _, write := range terminal.writes {
		if len(write) > maxRenderWriteChars {
			t.Fatalf("write length %d exceeds limit %d", len(write), maxRenderWriteChars)
		}
	}
	output := strings.Join(terminal.writes, "")
	if !strings.HasPrefix(output, "\x1b[?2026h") {
		t.Fatal("output should start with synchronized-output begin")
	}
	if !strings.HasSuffix(output, "\x1b[?2026l") {
		t.Fatal("output should end with synchronized-output end")
	}
	if strings.Contains(output, "\x1b[2J") {
		t.Fatal("the update should stay on the differential render path")
	}
}

func TestTuiCrashDumpWithoutConfiguredLogDirectory(t *testing.T) {
	crashDir := t.TempDir()
	t.Setenv("TMPDIR", crashDir)
	t.Setenv("TEMP", crashDir)
	t.Setenv("TMP", crashDir)

	terminal := NewVirtualTerminal(40, 10)
	tui := NewTuiMainScreen(terminal, nil, "")
	component := &testComponent{}
	tui.AddChild(component)
	component.SetLines([]string{"ok"})
	tui.Start()
	terminal.WaitForRender()

	// Width overflow is detected in the differential render path
	component.SetLines([]string{"ok", strings.Repeat("x", 60)})
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected render to panic on over-wide line")
			}
			err, ok := r.(error)
			if !ok {
				t.Fatalf("panic = %v, want error", r)
			}
			crashLogPath := filepath.Join(crashDir, "pi-tui-crash.log")
			if !strings.Contains(err.Error(), crashLogPath) {
				t.Fatalf("error message should reference %s: %v", crashLogPath, err)
			}
		}()
		tui.RenderNow(false)
	}()
	data, err := os.ReadFile(filepath.Join(crashDir, "pi-tui-crash.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Terminal width: 40") {
		t.Fatalf("crash log = %q", data)
	}
}
