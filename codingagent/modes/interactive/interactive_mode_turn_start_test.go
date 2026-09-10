// interactive_mode_turn_start_test.go (pi: test/interactive-mode-compaction.test.ts —
// "updates the working state when the same agent run resumes after
// compaction", v0.85.1) — progress + working-indicator handling moved from
// agent_start to turn_start.
package interactive

import (
	"testing"

	"github.com/ZoroSpace/pig/agent"
	core "github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// newSeamTestEditor builds the default editor the status machinery touches
// (pig helper; pi stubs it on the fakeThis).
func newSeamTestEditor() *components.CustomEditor {
	return components.NewCustomEditor(&stubRequestRenderTUI{}, theme.GetEditorTheme(), core.CreateKeybindingsManager(""), nil)
}

// turnProgressTerminal records SetProgress calls (pi: terminal.setProgress).
type turnProgressTerminal struct {
	tui.Terminal
	progressCalls []bool
}

func (t *turnProgressTerminal) SetProgress(active bool) {
	t.progressCalls = append(t.progressCalls, active)
}

// turnStartTUI exposes the terminal surface handleEvent's turn_start branch
// consumes.
type turnStartTUI struct {
	stubRequestRenderTUI
	terminal *turnProgressTerminal
}

func (t *turnStartTUI) Terminal() tui.Terminal { return t.terminal }

func TestInteractiveModeUpdatesWorkingStateOnTurnStart(t *testing.T) {
	initTestTheme(t)
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, func(m *core.SettingsManager) {
		m.ApplyOverrides(core.Settings{"terminal": map[string]any{"showTerminalProgress": true}})
	})
	m := newSeamTestMode(session, nil)
	// pi: isInitialized: true — skip the lazy Init with a partial mode.
	m.isInitialized = true
	m.statusContainer = &tui.Container{}
	m.defaultEditor = newSeamTestEditor()
	m.editor = m.defaultEditor
	terminal := &turnProgressTerminal{}
	m.ui = &turnStartTUI{terminal: terminal}

	// First turn_start: workingVisible → show the working indicator.
	m.handleEvent(t.Context(), &core.AgentSessionEvent{AgentEvent: agent.AgentEvent{Type: agent.AgentEventTurnStart}})
	if len(terminal.progressCalls) != 1 || !terminal.progressCalls[0] {
		t.Fatalf("progress calls = %v, want one true", terminal.progressCalls)
	}
	if m.isWorkingIndicator() {
		t.Fatal("expected working indicator shown")
	}

	// Same agent run resumes after compaction: workingVisible=false → clear.
	m.workingVisible = false
	m.handleEvent(t.Context(), &core.AgentSessionEvent{AgentEvent: agent.AgentEvent{Type: agent.AgentEventTurnStart}})
	if len(terminal.progressCalls) != 2 {
		t.Fatalf("progress calls = %d, want 2", len(terminal.progressCalls))
	}
}
