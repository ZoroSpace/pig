// interactive_mode_status_test.go (pi:
// test/interactive-mode-status.test.ts subset — showStatus /
// showManagedToolStatus / setToolsExpanded, the cases that don't need a
// stubbed session). The createBaseAutocompleteProvider /
// setupAutocompleteProvider / showLoadedResources describes need a
// constructed AgentSession and land with the T5b-3 session seams (declared
// in the batch handoff).
//
// pi calls the prototype's private methods with stub `this` objects; Go
// methods are bound to the concrete struct, so the tests construct an
// InteractiveMode with only the fields the method touches (same-package
// test).
package interactive

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// ansiRe strips SGR escape sequences (pi:
// .replace(/\u001b\[[0-9;]*m/g, "")).
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// normalizeRenderedOutput strips ANSI and trailing whitespace (pi:
// normalizeRenderedOutput).
func normalizeRenderedOutput(rendered string) string {
	rendered = ansiRe.ReplaceAllString(rendered, "")
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// renderAllChildren renders every child (pi: renderAll helper).
func renderAllChildren(container *tui.Container, width int) string {
	var lines []string
	for _, child := range container.Children {
		lines = append(lines, child.Render(width)...)
	}
	return strings.Join(lines, "\n")
}

func newStatusTestMode() *InteractiveMode {
	m := &InteractiveMode{
		chatContainer: &tui.Container{},
		ui:            &stubRequestRenderTUI{},
		// pi: defaultEditor always exists; the editor-border working status
		// machinery (v0.85.1) touches it on every show/clear.
		defaultEditor: components.NewCustomEditor(&stubRequestRenderTUI{}, theme.GetEditorTheme(), core.CreateKeybindingsManager(""), nil),
	}
	m.editor = m.defaultEditor
	return m
}

// stubRequestRenderTUI satisfies the surface showStatus and friends use.
type stubRequestRenderTUI struct {
	tui.TUI
	requests int
}

func (s *stubRequestRenderTUI) RequestRender(_ bool) { s.requests++ }

// The following satisfy the theme controller's TUI surface without pulling
// in the embedded nil interface.
func (s *stubRequestRenderTUI) OnTerminalColorSchemeChange(func(tui.TerminalColorScheme)) func() {
	return func() {}
}
func (s *stubRequestRenderTUI) Invalidate() {}

// pi: "InteractiveMode.showStatus > coalesces immediately-sequential status
// messages".
func TestShowStatusCoalescesSequentialMessages(t *testing.T) {
	initTestTheme(t)
	m := newStatusTestMode()

	m.showStatus("STATUS_ONE")
	if len(m.chatContainer.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(m.chatContainer.Children))
	}
	if last := renderAllChildren(m.chatContainer, 120); !strings.Contains(last, "STATUS_ONE") {
		t.Fatalf("render = %q, want STATUS_ONE", last)
	}

	m.showStatus("STATUS_TWO")
	// second status updates the previous line instead of appending
	if len(m.chatContainer.Children) != 2 {
		t.Fatalf("children after second status = %d, want 2", len(m.chatContainer.Children))
	}
	last := renderAllChildren(m.chatContainer, 120)
	if !strings.Contains(last, "STATUS_TWO") {
		t.Fatalf("render = %q, want STATUS_TWO", last)
	}
	if strings.Contains(last, "STATUS_ONE") {
		t.Fatalf("render = %q, want STATUS_ONE replaced", last)
	}
}

// pi: "InteractiveMode.showStatus > appends a new status line if something
// else was added in between".
func TestShowStatusAppendsAfterOtherComponent(t *testing.T) {
	initTestTheme(t)
	m := newStatusTestMode()

	m.showStatus("STATUS_ONE")
	if len(m.chatContainer.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(m.chatContainer.Children))
	}

	// Something else gets added to the chat in between status updates
	m.chatContainer.AddChild(stubOtherComponent{})
	if len(m.chatContainer.Children) != 3 {
		t.Fatalf("children = %d, want 3", len(m.chatContainer.Children))
	}

	m.showStatus("STATUS_TWO")
	// adds spacer + text
	if len(m.chatContainer.Children) != 5 {
		t.Fatalf("children = %d, want 5", len(m.chatContainer.Children))
	}
	if last := renderAllChildren(m.chatContainer, 120); !strings.Contains(last, "STATUS_TWO") {
		t.Fatalf("render = %q, want STATUS_TWO", last)
	}
}

// stubOtherComponent is pi's inline `{ render: () => ["OTHER"] }`.
type stubOtherComponent struct{}

func (stubOtherComponent) Render(int) []string { return []string{"OTHER"} }
func (stubOtherComponent) Invalidate()         {}

// pi: "InteractiveMode.showManagedToolStatus > renders tool updates as one
// contiguous group".
func TestShowManagedToolStatusGroups(t *testing.T) {
	initTestTheme(t)
	m := newStatusTestMode()

	m.showManagedToolStatus(utils.ToolStatus{Type: "info", Message: "fd downloading"})
	m.showManagedToolStatus(utils.ToolStatus{Type: "info", Message: "rg downloading"})
	m.showManagedToolStatus(utils.ToolStatus{Type: "warning", Message: "rg failed"})

	if len(m.chatContainer.Children) != 4 {
		t.Fatalf("children = %d, want 4", len(m.chatContainer.Children))
	}
	got := normalizeRenderedOutput(renderAllChildren(m.chatContainer, 220))
	want := "fd downloading\n rg downloading\n Warning: rg failed"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// pi: "InteractiveMode.setToolsExpanded > applies expansion state to the
// active header and chat entries".
func TestSetToolsExpandedAppliesToChildren(t *testing.T) {
	header := &stubExpandable{}
	loadedResourcesChild := &stubExpandable{}
	chatChild := &stubExpandable{}

	m := &InteractiveMode{
		ui: &stubRequestRenderTUI{},
		// pi's stub header records setExpanded; our expandable stub does the
		// same via the expandable interface.
		builtInHeader:            header,
		loadedResourcesContainer: &tui.Container{},
		chatContainer:            &tui.Container{},
	}
	m.loadedResourcesContainer.AddChild(loadedResourcesChild)
	m.chatContainer.AddChild(chatChild)

	m.setToolsExpanded(true)

	if !m.toolOutputExpanded {
		t.Fatalf("toolOutputExpanded = false, want true")
	}
	if !header.expanded {
		t.Fatalf("header.SetExpanded(true) not called")
	}
	if !loadedResourcesChild.expanded {
		t.Fatalf("loadedResourcesChild.SetExpanded(true) not called")
	}
	if !chatChild.expanded {
		t.Fatalf("chatChild.SetExpanded(true) not called")
	}
	if last := renderAllChildren(m.chatContainer, 220); !strings.Contains(last, "Tool output: expanded") {
		t.Fatalf("status = %q, want Tool output: expanded", last)
	}
}

// stubExpandable records the last expanded state (pi's setExpanded vi.fn()).
type stubExpandable struct {
	expanded bool
	called   bool
}

func (s *stubExpandable) SetExpanded(expanded bool) { s.expanded = expanded; s.called = true }
func (stubExpandable) Render(int) []string          { return nil }
func (stubExpandable) Invalidate()                  {}

// statusKindSmoke guards the indicator-kind wiring the status describes rely
// on (pig-side smoke; pi's showStatusIndicator describe needs a UI stub and
// lands with T5b-3).
func TestClearStatusIndicatorRestoresIdle(t *testing.T) {
	initTestTheme(t)
	m := newStatusTestMode()
	m.statusContainer = &tui.Container{}
	m.options.TuiMode = "regular"
	m.idleStatus = &components.IdleStatus{}
	m.ui = &stubGetClearOnShrinkTUI{stubRequestRenderTUI: stubRequestRenderTUI{}, clearOnShrink: true}

	m.showStatusIndicator(components.NewWorkingStatusIndicator(m.ui, "Working…", nil, nil))
	if m.activeStatusIndicator == nil {
		t.Fatal("expected active indicator")
	}
	m.clearStatusIndicator(components.StatusKindWorking)
	if m.activeStatusIndicator != nil {
		t.Fatal("expected indicator cleared")
	}
	if len(m.statusContainer.Children) != 1 {
		t.Fatalf("idle status not restored, children = %d", len(m.statusContainer.Children))
	}
}

// stubGetClearOnShrinkTUI adds GetClearOnShrink to the render stub.
type stubGetClearOnShrinkTUI struct {
	stubRequestRenderTUI
	clearOnShrink bool
}

func (s *stubGetClearOnShrinkTUI) GetClearOnShrink() bool { return s.clearOnShrink }

// pi v0.86.0: "routes every status through the editor opt-in" — every
// StatusIndicator subtype (working/compaction/branchSummary/retry) embeds
// into the editor border when the editor opts in, and lands in the status
// container when it does not.
func TestStatusIndicatorRoutesThroughEditorOptIn(t *testing.T) {
	initTestTheme(t)
	for _, embedWorkingStatus := range []bool{true, false} {
		t.Run(fmt.Sprintf("embedWorkingStatus=%v", embedWorkingStatus), func(t *testing.T) {
			m := newStatusTestMode()
			editor := components.NewCustomEditor(&stubRequestRenderTUI{}, theme.GetEditorTheme(), core.CreateKeybindingsManager(""), &components.CustomEditorOptions{
				EditorOptions:      tui.EditorOptions{},
				EmbedWorkingStatus: embedWorkingStatus,
			})
			m.editor = editor
			m.statusContainer = &tui.Container{}
			m.options.TuiMode = "regular"
			m.idleStatus = &components.IdleStatus{}
			m.ui = &stubGetClearOnShrinkTUI{stubRequestRenderTUI: stubRequestRenderTUI{}, clearOnShrink: true}

			indicators := []statusItemForTest{
				components.NewWorkingStatusIndicator(m.ui, "Working", nil, nil),
				components.NewCompactionStatusIndicator(m.ui, components.CompactionReasonManual),
				components.NewCompactionStatusIndicator(m.ui, components.CompactionReasonThreshold),
				components.NewCompactionStatusIndicator(m.ui, components.CompactionReasonOverflow),
				components.NewBranchSummaryStatusIndicator(m.ui),
				components.NewRetryStatusIndicator(m.ui, 1, 3, 1000),
			}
			for _, indicator := range indicators {
				m.showStatusIndicator(indicator)
				if m.activeStatusIndicator == nil {
					t.Fatal("expected active indicator")
				}
				if m.activeWorkingIndicatorEmbedded != embedWorkingStatus {
					t.Fatalf("embedded = %v, want %v", m.activeWorkingIndicatorEmbedded, embedWorkingStatus)
				}
				if embedWorkingStatus {
					if len(m.statusContainer.Children) != 0 {
						t.Fatalf("embedded indicator leaked into the status container")
					}
				} else if len(m.statusContainer.Children) != 1 {
					t.Fatalf("standalone indicator missing from the status container")
				}
				m.clearStatusIndicator("")
				if m.activeStatusIndicator != nil {
					t.Fatal("expected indicator cleared")
				}
				if m.activeWorkingIndicatorEmbedded {
					t.Fatal("embedded flag should reset on clear")
				}
			}
		})
	}
}

// pi v0.86.0: clearing any embedded kind frees the status row (the working
// kind gate is gone from clearStatusIndicator).
func TestClearStatusIndicatorFreesRowForEveryEmbeddedKind(t *testing.T) {
	initTestTheme(t)
	for _, kind := range []components.StatusIndicatorKind{
		components.StatusKindWorking,
		components.StatusKindCompaction,
		components.StatusKindBranchSummary,
		components.StatusKindRetry,
	} {
		m := newStatusTestMode()
		m.statusContainer = &tui.Container{}
		m.options.TuiMode = "regular"
		m.idleStatus = &components.IdleStatus{}
		m.ui = &stubGetClearOnShrinkTUI{stubRequestRenderTUI: stubRequestRenderTUI{}, clearOnShrink: true}

		m.activeStatusIndicator = stubIndicatorItem{kind: kind}
		m.activeWorkingIndicatorEmbedded = true
		m.clearStatusIndicator("")
		if m.activeStatusIndicator != nil {
			t.Fatalf("%s: indicator not cleared", kind)
		}
		if len(m.statusContainer.Children) != 0 {
			t.Fatalf("%s: idle filler restored for an embedded indicator", kind)
		}
	}
}

// statusItemForTest mirrors the interactive shell's statusIndicatorItem
// surface for the routing test.
type statusItemForTest interface {
	tui.Component
	Dispose()
	IndicatorKind() components.StatusIndicatorKind
	components.StatusBorderRenderer
}

// stubIndicatorItem is a bare statusIndicatorItem for clear-path tests.
type stubIndicatorItem struct {
	kind components.StatusIndicatorKind
}

func (s stubIndicatorItem) Render(int) []string                           { return nil }
func (s stubIndicatorItem) Invalidate()                                   {}
func (s stubIndicatorItem) Dispose()                                      {}
func (s stubIndicatorItem) IndicatorKind() components.StatusIndicatorKind { return s.kind }
func (s stubIndicatorItem) RenderInBorder(int) string                     { return "" }
func (s stubIndicatorItem) RenderSpinnerInBorder(int) string              { return "" }
