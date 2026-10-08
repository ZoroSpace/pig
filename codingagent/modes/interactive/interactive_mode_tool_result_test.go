// interactive_mode_tool_result_test.go — regression lock for the live tool
// result seam (docs/fix/2026-09-11-tool-execution-live-output-empty.md).
//
// pi spreads event.partialResult / event.result (an AgentToolResult) straight
// into component.updateResult (interactive-mode.ts:3351/3360); pig's
// resultToComponentResult only unwrapped ToolResultMessage, so every live
// tool_execution_update/end rendered with empty output — a bash card showed
// only the command row and the "Took" footer, with test errors invisible.
package interactive

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	codingagentutils "github.com/ZoroSpace/pig/codingagent/utils"
)

// toolResultProbe is distinct from any rendered call-row text (command,
// labels) so a missing payload cannot false-positive against the frame.
const toolResultProbe = "=== ALL GREEN CHECK DONE ==="

// probeAgentToolResult mirrors what the builtin tools actually emit:
// asTextContent (codingagent/core/tools/shared.go) builds *pointer* blocks
// (&types.TextContent), while the JSONL/LLM decoders produce values — the
// probe must carry the live-path form or the bug cannot reproduce.
func probeAgentToolResult() agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.Content{&types.TextContent{Kind: types.KindText, Text: toolResultProbe}},
		Details: map[string]any{"exitCode": 0},
	}
}

// TestResultToComponentResultCarriesAgentToolResult locks the conversion for
// the live tool_execution_end seam: pi passes the AgentToolResult through
// verbatim, so content and details must survive (value and pointer forms).
func TestResultToComponentResultCarriesAgentToolResult(t *testing.T) {
	got := resultToComponentResult(probeAgentToolResult(), false, nil)
	if len(got.Content) != 1 {
		t.Fatalf("content blocks = %d, want 1", len(got.Content))
	}
	// Dual-form assertion: the probe mirrors asTextContent (pointer),
	// the decoders produce values — the converter must pass either through.
	var text string
	var textOk bool
	switch c := got.Content[0].(type) {
	case types.TextContent:
		text, textOk = c.Text, true
	case *types.TextContent:
		if c != nil {
			text, textOk = c.Text, true
		}
	}
	if !textOk || text != toolResultProbe {
		t.Fatalf("content text = %#v, want %q", got.Content[0], toolResultProbe)
	}
	if got.Details == nil {
		t.Fatal("details dropped")
	}
	if got.IsError {
		t.Fatal("isError should stay false")
	}

	probe := probeAgentToolResult()
	got = resultToComponentResult(&probe, true, nil)
	if len(got.Content) != 1 || got.Details == nil || !got.IsError {
		t.Fatalf("pointer form lost payload: content=%d details=%v isError=%v",
			len(got.Content), got.Details, got.IsError)
	}
}

// TestResultToComponentResultKeepsToolResultMessage locks the session-restore
// path (interactive_mode.go rebuilds transcript tool rows from
// ToolResultMessage entries — this worked before and must keep working).
func TestResultToComponentResultKeepsToolResultMessage(t *testing.T) {
	value := types.ToolResultMessage{
		Content: []types.Content{types.TextContent{Kind: types.KindText, Text: toolResultProbe}},
		Details: map[string]any{"exitCode": 0},
	}
	for _, tc := range []struct {
		name string
		in   any
	}{
		{"value", value},
		{"pointer", &value},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resultToComponentResult(tc.in, true, nil)
			if len(got.Content) != 1 || got.Details == nil || !got.IsError {
				t.Fatalf("content=%d details=%v isError=%v", len(got.Content), got.Details, got.IsError)
			}
		})
	}
}

// TestLiveToolExecutionResultReachesBashRenderer drives the exact
// handleEvent sequence (update with isPartial=true, then end) through
// resultToComponentResult into a ToolExecutionComponent with the real bash
// renderer; the output text must be visible in both states.
func TestLiveToolExecutionResultReachesBashRenderer(t *testing.T) {
	initTestTheme(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	component := components.NewToolExecutionComponent(
		"bash", "toolu_probe", map[string]any{"command": "go test ./... -race"},
		components.ToolExecutionOptions{}, tools.CreateBashToolDefinition(cwd, nil),
		&stubRequestRenderTUI{}, cwd,
	)

	component.UpdateResult(resultToComponentResult(probeAgentToolResult(), false, nil), true)
	assertRenderContainsProbe(t, component, "partial update")

	component.UpdateResult(resultToComponentResult(probeAgentToolResult(), false, nil), false)
	assertRenderContainsProbe(t, component, "final result")
}

func assertRenderContainsProbe(t *testing.T, component *components.ToolExecutionComponent, phase string) {
	t.Helper()
	var builder strings.Builder
	for _, line := range component.Render(120) {
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	plain := codingagentutils.StripAnsi(builder.String())
	if !strings.Contains(plain, toolResultProbe) {
		t.Fatalf("%s: rendered frame missing output text %q; got:\n%s", phase, toolResultProbe, plain)
	}
}

// mountedToolComponents returns the tool cards currently mounted in the chat
// container (the frames the user actually sees).
func mountedToolComponents(mode *InteractiveMode) []*components.ToolExecutionComponent {
	var out []*components.ToolExecutionComponent
	for _, child := range mode.chatContainer.Children {
		if tool, ok := child.(*components.ToolExecutionComponent); ok {
			out = append(out, tool)
		}
	}
	return out
}

// assertAnyMountedFrameContainsProbe renders every mounted tool card and
// requires the probe in at least one frame.
func assertAnyMountedFrameContainsProbe(t *testing.T, mode *InteractiveMode, phase string) {
	t.Helper()
	mounted := mountedToolComponents(mode)
	if len(mounted) == 0 {
		t.Fatalf("%s: no tool card mounted in chatContainer", phase)
	}
	for _, component := range mounted {
		var builder strings.Builder
		for _, line := range component.Render(120) {
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
		plain := codingagentutils.StripAnsi(builder.String())
		if strings.Contains(plain, toolResultProbe) {
			return
		}
	}
	t.Fatalf("%s: no mounted tool card frame contains %q (cards=%d)", phase, toolResultProbe, len(mounted))
}

// firstSGR returns the leading SGR sequence of s ("" when there is none).
func firstSGR(s string) string {
	if !strings.HasPrefix(s, "\x1b[") {
		return ""
	}
	end := strings.IndexByte(s, 'm')
	if end < 0 {
		return ""
	}
	return s[:end+1]
}

// TestRestoreRendersToolResultsOnRestoredCards locks the session-restore
// seam of renderSessionItems: it matched only *pointer* ToolResultMessage
// while the session-file decoder produces values (core/session_manager.go),
// so every restored transcript kept its tool cards in the pending state —
// no output text, and the frame colored "muted" instead of the state color
// (the same pointer/value family as docs/fix/2026-09-11-tool-execution-live-
// output-empty.md; see docs/fix/2026-10-03-restored-tool-results-dropped.md).
func TestRestoreRendersToolResultsOnRestoredCards(t *testing.T) {
	initTestTheme(t)
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	mode := newSeamTestMode(session, nil)

	const restoreToolCallID = "toolu_restore_probe"
	if _, err := session.SessionManager.AppendMessage(types.UserMessage{
		Role:    types.RoleUser,
		Content: types.UserContent{String: "run the probe"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.SessionManager.AppendMessage(types.AssistantMessage{
		Role: types.RoleAssistant,
		Content: []types.Content{types.ToolCall{
			Kind: types.KindToolCall, ID: restoreToolCallID, Name: "bash",
			Arguments: map[string]any{"command": "echo restore-probe"},
		}},
		API: "openai-completions", Provider: "test", Model: "test-model",
		Usage: types.Usage{Cost: types.Cost{}}, StopReason: types.StopReasonToolUse,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.SessionManager.AppendMessage(types.ToolResultMessage{
		Role: types.RoleToolResult, ToolCallID: restoreToolCallID, ToolName: "bash",
		Content: []types.Content{types.TextContent{Kind: types.KindText, Text: toolResultProbe}},
	}); err != nil {
		t.Fatal(err)
	}

	// Restore path (pi: renderInitialMessages → renderSessionItems).
	mode.renderSessionEntries(session.SessionManager.BuildContextEntries(), nil)

	mounted := mountedToolComponents(mode)
	if len(mounted) != 1 {
		t.Fatalf("restored cards = %d, want 1", len(mounted))
	}
	assertAnyMountedFrameContainsProbe(t, mode, "session restore")

	// The frame carries the state color: a card whose result arrived is no
	// longer "muted"/pending (the user-visible symptom was a dark frame
	// instead of the success color). The rule lines are the frame.
	frame := mounted[0].Render(120)
	wantSuccess := firstSGR(theme.Current().Fg("success", "x"))
	mutedColor := firstSGR(theme.Current().Fg("muted", "x"))
	rules := 0
	for _, line := range frame {
		plain := codingagentutils.StripAnsi(line)
		// Rule lines carry the solid frame corners (┌─┐/└─┘); the dashed rail
		// bars (┆) prefix/suffix content lines — trim the frame glyphs to
		// detect pure rules.
		if plain == "" || strings.Trim(plain, "─┌┐└┘") != "" {
			continue
		}
		rules++
		if got := firstSGR(line); got != wantSuccess {
			t.Fatalf("restored card rule color = %q, want success %q (muted is %q)", got, wantSuccess, mutedColor)
		}
	}
	if rules == 0 {
		t.Fatalf("restored card rendered no rule lines:\n%s", strings.Join(frame, "\n"))
	}
}

// TestRestoreRendersBashExecutionMessages locks the same double-form seam for
// `!command` blocks: addMessageToChat asserted *BashExecutionMessage while
// restored entries decode as values, so resumed transcripts dropped them.
func TestRestoreRendersBashExecutionMessages(t *testing.T) {
	initTestTheme(t)
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	mode := newSeamTestMode(session, nil)

	const bashProbeCommand = "echo restored-bash-probe"
	exitCode := 0
	if _, err := session.SessionManager.AppendMessage(harness.BashExecutionMessage{
		Role: harness.RoleBashExecution, Command: bashProbeCommand, Output: "restored-bash-probe",
		ExitCode: &exitCode, Timestamp: 1,
	}); err != nil {
		t.Fatal(err)
	}

	mode.renderSessionEntries(session.SessionManager.BuildContextEntries(), nil)

	var found *components.BashExecutionComponent
	for _, child := range mode.chatContainer.Children {
		if component, ok := child.(*components.BashExecutionComponent); ok {
			found = component
		}
	}
	if found == nil {
		t.Fatal("restored bash execution block not mounted")
	}
	if got := found.GetCommand(); got != bashProbeCommand {
		t.Fatalf("bash execution command = %q, want %q", got, bashProbeCommand)
	}
}

// TestHandleEventLiveToolOutputReachesMountedComponent is the
// handleEvent-level regression the fix doc called for
// (docs/fix/2026-09-11-tool-execution-live-output-empty.md): the session
// pump and the component were both proven lossless, so the loss had to sit
// in session.Subscribe → handleEvent(tool_execution_*) → m.pendingTools.
// This drives real AgentSessionEvents through handleEvent itself, letting
// the handler create the card the way the live flow does, and asserts the
// probe is visible in the mounted frame after update and after end.
func TestHandleEventLiveToolOutputReachesMountedComponent(t *testing.T) {
	initTestTheme(t)
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	mode := newSeamTestMode(session, nil)
	mode.isInitialized = true
	mode.footer = components.NewFooterComponent(session, nil)
	mode.pendingTools = map[string]*components.ToolExecutionComponent{}

	const toolCallID = "toolu_handleevent_probe"
	args := map[string]any{"command": "go test ./... 2>&1 | tail -5"}

	// Live flow: tool_execution_start creates the card via
	// getRegisteredToolDefinition (no registry in the seam session →
	// WithBuiltInRenderers supplies the real bash renderers).
	mode.handleEvent(context.Background(), &core.AgentSessionEvent{AgentEvent: agent.AgentEvent{
		Type: agent.AgentEventToolExecutionStart, ToolCallID: toolCallID, ToolName: "bash", Args: args,
	}})
	if len(mountedToolComponents(mode)) != 1 {
		t.Fatalf("tool_execution_start: mounted cards = %d, want 1", len(mountedToolComponents(mode)))
	}

	// Live flow: streaming partial output.
	mode.handleEvent(context.Background(), &core.AgentSessionEvent{AgentEvent: agent.AgentEvent{
		Type: agent.AgentEventToolExecutionUpdate, ToolCallID: toolCallID, ToolName: "bash", Args: args,
		PartialResult: probeAgentToolResult(),
	}})
	assertAnyMountedFrameContainsProbe(t, mode, "after tool_execution_update")

	// Live flow: final result.
	mode.handleEvent(context.Background(), &core.AgentSessionEvent{AgentEvent: agent.AgentEvent{
		Type: agent.AgentEventToolExecutionEnd, ToolCallID: toolCallID, ToolName: "bash",
		Result: probeAgentToolResult(),
	}})
	assertAnyMountedFrameContainsProbe(t, mode, "after tool_execution_end")
}
