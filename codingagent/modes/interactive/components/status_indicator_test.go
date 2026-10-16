package components

import (
	"strings"
	"sync/atomic"
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
		callsBeforeDispose := fakeTUI.renderCallCount()

		indicator.Dispose()
		// Give the ticker goroutine time to fire one tick if it were still
		// running (pi: vi.advanceTimersByTime(2000)).
		time.Sleep(1200 * time.Millisecond)

		if got := fakeTUI.renderCallCount(); got != callsBeforeDispose {
			t.Fatalf("requestRender after dispose: %d -> %d", callsBeforeDispose, got)
		}
	})
}

// countRenderTUI stubs the TUI surface NewLoader consumes. The render
// counter is atomic: the loader animation goroutine calls RequestRender
// concurrently with the test goroutine (pi's event loop serializes these —
// pig does not).
type countRenderTUI struct {
	tui.TUI
	renderCalls atomic.Int64
}

func (c *countRenderTUI) RequestRender(force bool) {
	c.renderCalls.Add(1)
}

// renderCallCount snapshots the counter.
func (c *countRenderTUI) renderCallCount() int64 { return c.renderCalls.Load() }

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

// pi v0.86.0: "embeds compaction, summary, and retry labels within the border
// width" — all StatusIndicator subtypes embed now (renderInBorder moved to
// the base class); the editor border keeps its exact width at every panel
// width and the retry countdown updates in place.
func TestStatusIndicatorEmbedsAllIndicatorKindsInBorder(t *testing.T) {
	initTestTheme(t)
	editorTUI := &editorBorderTUI{countRenderTUI: countRenderTUI{}}
	editor := NewCustomEditor(editorTUI, theme.GetEditorTheme(), core.CreateKeybindingsManager(""), &CustomEditorOptions{
		EditorOptions:      tui.EditorOptions{},
		EmbedWorkingStatus: true,
	})
	indicators := []tui.Component{
		NewCompactionStatusIndicator(editorTUI, CompactionReasonManual, nil),
		NewCompactionStatusIndicator(editorTUI, CompactionReasonThreshold, nil),
		NewCompactionStatusIndicator(editorTUI, CompactionReasonOverflow, nil),
		NewBranchSummaryStatusIndicator(editorTUI),
		NewRetryStatusIndicator(editorTUI, 1, 3, 3000),
	}
	defer func() {
		for _, indicator := range indicators {
			if disposer, ok := indicator.(interface{ Dispose() }); ok {
				disposer.Dispose()
			}
		}
	}()
	for _, indicator := range indicators {
		editor.SetWorkingStatusIndicator(indicator.(StatusBorderRenderer))
		label := utils.StripAnsi(indicator.Render(120)[1])
		label = strings.TrimSpace(label)
		border := utils.StripAnsi(editor.Render(120)[0])
		if !strings.Contains(border, "── "+label+" ") {
			t.Fatalf("border %q missing label %q", border, label)
		}
		for _, width := range []int{1, 4, 10, 20, 80, 120} {
			if got := visibleWidthOf(utils.StripAnsi(editor.Render(width)[0])); got != width {
				t.Fatalf("editor border width %d: got %d", width, got)
			}
		}
	}
	// Retry countdown: after the delay elapses the retry message re-renders
	// with the next second. (pi advances fake timers; pig waits the 1s
	// countdown tick with a real sleep bounded to the test timeout.)
	retry := indicators[4].(*RetryStatusIndicator)
	editor.SetWorkingStatusIndicator(retry)
	retry.SetMessage("Retrying (1/3) in 2s... (esc to cancel)")
	time.Sleep(1100 * time.Millisecond)
	if got := utils.StripAnsi(editor.Render(120)[0]); !strings.Contains(got, "Retrying (1/3) in 2s") {
		t.Fatalf("retry countdown label missing: %q", got)
	}
	editor.SetWorkingStatusIndicator(nil)
	if got := utils.StripAnsi(editor.Render(120)[0]); got != strings.Repeat("─", 120) {
		t.Fatalf("expected plain border, got %q", got)
	}
}

// visibleWidthOf wraps tui.VisibleWidth for the assertion above.
func visibleWidthOf(text string) int { return tui.VisibleWidth(text) }

// TestCompactionStatusIndicatorUsesColorFnOverride locks #10810: a non-nil
// colorFn overrides both the spinner and the message colors.
func TestCompactionStatusIndicatorUsesColorFnOverride(t *testing.T) {
	initTestTheme(t)
	ui := &editorBorderTUI{countRenderTUI: countRenderTUI{}}
	colorFn := func(text string) string { return "<c>" + text + "</c>" }

	overridden := NewCompactionStatusIndicator(ui, CompactionReasonManual, colorFn)
	defer overridden.Dispose()
	if raw := strings.Join(overridden.Render(120), "\n"); !strings.Contains(raw, "<c>") {
		t.Fatalf("output %q missing colorFn override", raw)
	}

	plain := NewCompactionStatusIndicator(ui, CompactionReasonManual, nil)
	defer plain.Dispose()
	if raw := strings.Join(plain.Render(120), "\n"); strings.Contains(raw, "<c>") {
		t.Fatalf("unexpected override in %q", raw)
	}
}
