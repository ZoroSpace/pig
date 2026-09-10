package components

import (
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/codingagent/utils"
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

// editorBorderTUI adds the Terminal surface Editor.Render consumes (pi:
// terminal: { rows: 10 }).
type editorBorderTUI struct {
	countRenderTUI
}

func (c *editorBorderTUI) Terminal() tui.Terminal { return tui.NewVirtualTerminal(80, 10) }

// pi: "keeps the top border unchanged unless the editor opts in" (v0.85.1).
func TestStatusIndicatorTopBorderUnchangedWithoutOptIn(t *testing.T) {
	initTestTheme(t)
	editorTUI := &editorBorderTUI{countRenderTUI: countRenderTUI{}}
	editor := NewCustomEditor(editorTUI, theme.GetEditorTheme(), core.CreateKeybindingsManager(""), nil)
	indicator := NewWorkingStatusIndicator(editorTUI, "Working", nil, nil)
	editor.SetWorkingStatusIndicator(indicator)

	topBorder := utils.StripAnsi(editor.Render(20)[0])
	if topBorder != strings.Repeat("─", 20) {
		t.Fatalf("expected default border, got %q", topBorder)
	}
	standaloneLine := indicator.Render(20)[1]
	if !strings.Contains(standaloneLine, theme.Current().GetFgAnsi("accent")) {
		t.Fatal("expected accent spinner color")
	}
	if !strings.Contains(standaloneLine, theme.Current().GetFgAnsi("muted")) {
		t.Fatal("expected muted message color")
	}
	indicator.Dispose()
}

// pi: "embeds the working indicator when the editor opts in" (v0.85.1).
func TestStatusIndicatorEmbedsWorkingIndicatorInBorder(t *testing.T) {
	initTestTheme(t)
	editorTUI := &editorBorderTUI{countRenderTUI: countRenderTUI{}}
	editor := NewCustomEditor(editorTUI, theme.GetEditorTheme(), core.CreateKeybindingsManager(""), &CustomEditorOptions{
		EditorOptions:      tui.EditorOptions{},
		EmbedWorkingStatus: true,
	})
	if !editor.EmbedWorkingStatus {
		t.Fatal("expected EmbedWorkingStatus")
	}
	editor.BorderColor = theme.Current().GetThinkingBorderColor("high")
	indicator := NewWorkingStatusIndicator(editorTUI, "Working", nil, editor.BorderColor)
	editor.SetWorkingStatusIndicator(indicator)

	topBorder := editor.Render(20)[0]
	if got := utils.StripAnsi(topBorder); got != "── ⠋ Working ───────" {
		t.Fatalf("unexpected border %q", got)
	}
	if got := tui.VisibleWidth(topBorder); got != 20 {
		t.Fatalf("border width = %d, want 20", got)
	}
	if parts := strings.Split(topBorder, theme.Current().GetFgAnsi("thinkingHigh")); len(parts) != 5 {
		t.Fatalf("expected 5 thinkingHigh-colored segments, got %d: %q", len(parts), topBorder)
	}
	indicator.Dispose()
}
