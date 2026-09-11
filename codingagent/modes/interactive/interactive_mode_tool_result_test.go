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
	"os"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	codingagentutils "github.com/ZoroSpace/pig/codingagent/utils"
)

// toolResultProbe is distinct from any rendered call-row text (command,
// labels) so a missing payload cannot false-positive against the frame.
const toolResultProbe = "=== ALL GREEN CHECK DONE ==="

func probeAgentToolResult() agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.Content{types.TextContent{Kind: types.KindText, Text: toolResultProbe}},
		Details: map[string]any{"exitCode": 0},
	}
}

// TestResultToComponentResultCarriesAgentToolResult locks the conversion for
// the live tool_execution_end seam: pi passes the AgentToolResult through
// verbatim, so content and details must survive (value and pointer forms).
func TestResultToComponentResultCarriesAgentToolResult(t *testing.T) {
	got := resultToComponentResult(probeAgentToolResult(), false)
	if len(got.Content) != 1 {
		t.Fatalf("content blocks = %d, want 1", len(got.Content))
	}
	if text, ok := got.Content[0].(types.TextContent); !ok || text.Text != toolResultProbe {
		t.Fatalf("content text = %#v, want %q", got.Content[0], toolResultProbe)
	}
	if got.Details == nil {
		t.Fatal("details dropped")
	}
	if got.IsError {
		t.Fatal("isError should stay false")
	}

	probe := probeAgentToolResult()
	got = resultToComponentResult(&probe, true)
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
			got := resultToComponentResult(tc.in, true)
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

	component.UpdateResult(resultToComponentResult(probeAgentToolResult(), false), true)
	assertRenderContainsProbe(t, component, "partial update")

	component.UpdateResult(resultToComponentResult(probeAgentToolResult(), false), false)
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
