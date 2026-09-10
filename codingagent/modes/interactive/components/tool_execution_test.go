// tool_execution_test.go — port of test/tool-execution-component.test.ts.
//
// Test adaptations (declared):
//   - createFakeTui embeds tui.TUI and overrides RequestRender (repo fake
//     TUI precedent); pi casts a partial object.
//   - Raw call args are map[string]any (the JSON decode shape pi streams).
//   - process.cwd() → os.Getwd(); getReadmePath → codingagent.GetReadmePath.
//   - pi's synchronous first-update assertion for bash becomes a bounded
//     wait on the goroutine executing the tool.
package components

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	codingagentutils "github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

func createBaseToolDefinition(name string) *tools.ToolDefinition {
	if name == "" {
		name = "custom_tool"
	}
	return &tools.ToolDefinition{
		Name:        name,
		Label:       name,
		Description: "custom tool",
		Parameters:  json.RawMessage(`true`),
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx tools.ToolExecuteContext) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{Content: textContent("ok"), Details: map[string]any{}}, nil
		},
	}
}

type toolExecutionFakeTUI struct {
	tui.TUI
}

func (f *toolExecutionFakeTUI) RequestRender(force bool) {}

func mustCwd(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return cwd
}

func textContent(text string) []types.Content {
	return []types.Content{types.TextContent{Kind: types.KindText, Text: text}}
}

func intPtr(i int) *int { return &i }

func boolPtr(b bool) *bool { return &b }

func themeFgToolOutput(s string) string { return theme.Current().Fg("toolOutput", s) }

func renderComponentLines(t *testing.T, component tui.Component, width int) string {
	t.Helper()
	return strings.Join(component.Render(width), "\n")
}

func renderedPlain(t *testing.T, component tui.Component, width int) string {
	t.Helper()
	return codingagentutils.StripAnsi(renderComponentLines(t, component, width))
}

func TestToolExecutionStacksCustomRenderers(t *testing.T) {
	initTestTheme(t)
	definition := createBaseToolDefinition("")
	definition.RenderCall = func(args any, themeArg tools.UITheme, context tools.ToolRenderContext) tui.Component {
		return tui.NewText("custom call", 0, 0)
	}
	definition.RenderResult = func(result tools.ToolRenderResult, options tools.ToolRenderResultOptions, themeArg tools.UITheme, context tools.ToolRenderContext) tui.Component {
		return tui.NewText("custom result", 0, 0)
	}

	component := NewToolExecutionComponent("custom_tool", "tool-1", map[string]any{}, ToolExecutionOptions{}, definition, &toolExecutionFakeTUI{}, mustCwd(t))
	rendered := renderedPlain(t, component, 120)
	if !strings.Contains(rendered, "custom call") {
		t.Fatalf("custom call missing: %q", rendered)
	}

	component.UpdateResult(ToolExecutionResult{
		Content: textContent("done"),
		Details: map[string]any{},
	}, false)

	rendered = renderedPlain(t, component, 120)
	if !strings.Contains(rendered, "custom call") || !strings.Contains(rendered, "custom result") {
		t.Fatalf("custom renderers missing: %q", rendered)
	}
}

func TestToolExecutionSelfRenderedEmptyTakesNoSpace(t *testing.T) {
	initTestTheme(t)
	definition := createBaseToolDefinition("")
	definition.RenderShell = "self"
	definition.RenderCall = func(args any, themeArg tools.UITheme, context tools.ToolRenderContext) tui.Component {
		return tui.NewText("", 0, 0)
	}
	definition.RenderResult = func(result tools.ToolRenderResult, options tools.ToolRenderResultOptions, themeArg tools.UITheme, context tools.ToolRenderContext) tui.Component {
		return tui.NewText("", 0, 0)
	}

	component := NewToolExecutionComponent("custom_tool", "tool-empty-self-render", map[string]any{}, ToolExecutionOptions{}, definition, &toolExecutionFakeTUI{}, mustCwd(t))
	if lines := component.Render(120); len(lines) != 0 {
		t.Fatalf("expected no lines, got %v", lines)
	}

	component.UpdateResult(ToolExecutionResult{Content: nil, Details: map[string]any{}}, false)
	if lines := component.Render(120); len(lines) != 0 {
		t.Fatalf("expected no lines after result, got %v", lines)
	}
}

func TestToolExecutionBuiltinOverrideRendering(t *testing.T) {
	initTestTheme(t)
	overrideDefinition := createBaseToolDefinition("edit")

	component := NewToolExecutionComponent("edit", "tool-2",
		map[string]any{"path": "README.md", "oldText": "before", "newText": "after"},
		ToolExecutionOptions{}, tools.WithBuiltInRenderers("edit", overrideDefinition), &toolExecutionFakeTUI{}, mustCwd(t))
	component.UpdateResult(ToolExecutionResult{
		Content: nil,
		Details: &tools.EditToolDetails{Diff: "+1 after", FirstChangedLine: intPtr(1)},
	}, false)
	rendered := renderedPlain(t, component, 120)
	if !strings.Contains(rendered, "edit") || !strings.Contains(rendered, "README.md") {
		t.Fatalf("builtin rendering missing: %q", rendered)
	}
	if strings.Contains(rendered, ":1") {
		t.Fatalf("unexpected :1 line range: %q", rendered)
	}
}

func TestToolExecutionLegacyFilePathCompat(t *testing.T) {
	initTestTheme(t)
	component := NewToolExecutionComponent("read", "tool-3",
		map[string]any{"file_path": "README.md"},
		ToolExecutionOptions{}, nil, &toolExecutionFakeTUI{}, mustCwd(t))
	rendered := renderedPlain(t, component, 120)
	if !strings.Contains(rendered, "read") || !strings.Contains(rendered, "README.md") {
		t.Fatalf("legacy file_path rendering missing: %q", rendered)
	}
}

func TestToolExecutionBashInitialEmptyPartialUpdate(t *testing.T) {
	initTestTheme(t)
	var mu sync.Mutex
	var updates []tools.ToolRenderResult
	operations := &tools.BashOperations{
		Exec: func(ctx context.Context, command string, cwd string, options *tools.BashExecOptions) (*int, error) {
			time.Sleep(10 * time.Millisecond)
			zero := 0
			return &zero, nil
		},
	}
	definition := tools.CreateBashToolDefinition(mustCwd(t), &tools.BashToolOptions{Operations: operations, ExposeSessionEnvironment: boolPtr(false)})
	go func() {
		_, err := definition.Execute(context.Background(), "tool-bash-1", map[string]any{"command": "sleep 10"}, func(partial agent.AgentToolResult) {
			mu.Lock()
			updates = append(updates, partial)
			mu.Unlock()
		}, nil)
		if err != nil {
			t.Errorf("execute: %v", err)
		}
	}()
	// pi asserts the first update synchronously before completion; wait for it.
	deadline := time.Now().Add(2 * time.Second)
	for func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(updates) == 0
	}() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(updates) != 1 {
		t.Fatalf("expected exactly 1 initial update, got %d", len(updates))
	}
	if len(updates[0].Content) != 0 || updates[0].Details != nil {
		t.Fatalf("initial update not empty: %+v", updates[0])
	}
}

func TestToolExecutionBashNoDuplicateTruncationDetails(t *testing.T) {
	initTestTheme(t)
	operations := &tools.BashOperations{
		Exec: func(ctx context.Context, command string, cwd string, options *tools.BashExecOptions) (*int, error) {
			for i := 1; i <= 4000; i++ {
				options.OnData([]byte(fmt.Sprintf("line-%04d\n", i)))
			}
			zero := 0
			return &zero, nil
		},
	}
	definition := tools.CreateBashToolDefinition(mustCwd(t), &tools.BashToolOptions{Operations: operations, ExposeSessionEnvironment: boolPtr(false)})
	result, err := definition.Execute(context.Background(), "tool-bash-1b", map[string]any{"command": "generate output"}, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	component := NewToolExecutionComponent("bash", "tool-bash-1b", map[string]any{"command": "generate output"}, ToolExecutionOptions{}, definition, &toolExecutionFakeTUI{}, mustCwd(t))
	component.SetExpanded(true)
	component.UpdateResult(ToolExecutionResult{Content: result.Content, Details: result.Details}, false)

	rendered := renderedPlain(t, component, 200)
	if got := strings.Count(rendered, "Full output:"); got != 1 {
		t.Fatalf("Full output: count = %d\n%s", got, rendered)
	}
	if !strings.Contains(rendered, "Truncated: showing 2000 of 4000 lines") {
		t.Fatalf("truncation summary missing: %s", rendered)
	}
	if strings.Contains(rendered, "[Showing lines 2001-4000 of 4000. Full output:") {
		t.Fatalf("raw footer leaked: %s", rendered)
	}
}

func TestToolExecutionNoDuplicateHeadersWithBuiltinDefinition(t *testing.T) {
	initTestTheme(t)
	component := NewToolExecutionComponent("read", "tool-4", map[string]any{"path": "README.md"},
		ToolExecutionOptions{}, tools.CreateReadToolDefinition(mustCwd(t), nil), &toolExecutionFakeTUI{}, mustCwd(t))
	component.UpdateResult(ToolExecutionResult{Content: textContent("hello")}, false)
	rendered := renderedPlain(t, component, 120)
	if got := countWordRead(rendered); got != 1 {
		t.Fatalf("\\bread\\b count = %d: %q", got, rendered)
	}
}

func countWordRead(s string) int {
	count := 0
	for _, re := range []*regexp.Regexp{regexp.MustCompile(`\bread\b`)} {
		count += len(re.FindAllString(s, -1))
	}
	return count
}

func TestToolExecutionInheritsBuiltinResultRenderer(t *testing.T) {
	initTestTheme(t)
	overrideDefinition := createBaseToolDefinition("read")
	overrideDefinition.RenderCall = func(args any, themeArg tools.UITheme, context tools.ToolRenderContext) tui.Component {
		return tui.NewText("override call", 0, 0)
	}

	component := NewToolExecutionComponent("read", "tool-4b", map[string]any{"path": "notes.txt"},
		ToolExecutionOptions{}, tools.WithBuiltInRenderers("read", overrideDefinition), &toolExecutionFakeTUI{}, mustCwd(t))
	component.UpdateResult(ToolExecutionResult{Content: textContent("hello")}, false)
	component.SetExpanded(true)
	rendered := renderedPlain(t, component, 120)
	if !strings.Contains(rendered, "override call") || !strings.Contains(rendered, "hello") {
		t.Fatalf("inherited result renderer missing: %q", rendered)
	}
}

func TestToolExecutionInheritsBuiltinCallRenderer(t *testing.T) {
	initTestTheme(t)
	overrideDefinition := createBaseToolDefinition("read")
	overrideDefinition.RenderResult = func(result tools.ToolRenderResult, options tools.ToolRenderResultOptions, themeArg tools.UITheme, context tools.ToolRenderContext) tui.Component {
		return tui.NewText("override result", 0, 0)
	}

	component := NewToolExecutionComponent("read", "tool-4c", map[string]any{"path": "README.md"},
		ToolExecutionOptions{}, tools.WithBuiltInRenderers("read", overrideDefinition), &toolExecutionFakeTUI{}, mustCwd(t))
	component.UpdateResult(ToolExecutionResult{Content: textContent("hello")}, false)
	rendered := renderedPlain(t, component, 120)
	if !strings.Contains(rendered, "read") || !strings.Contains(rendered, "README.md") || !strings.Contains(rendered, "override result") {
		t.Fatalf("inherited call renderer missing: %q", rendered)
	}
}

func TestToolExecutionCustomRenderersOverrideBuiltin(t *testing.T) {
	initTestTheme(t)
	builtInDefinition := tools.CreateReadToolDefinition(mustCwd(t), nil)
	override := createBaseToolDefinition("read")
	override.Parameters = builtInDefinition.Parameters
	override.RenderCall = func(args any, themeArg tools.UITheme, context tools.ToolRenderContext) tui.Component {
		return tui.NewText("override call", 0, 0)
	}
	override.RenderResult = func(result tools.ToolRenderResult, options tools.ToolRenderResultOptions, themeArg tools.UITheme, context tools.ToolRenderContext) tui.Component {
		return tui.NewText("override result", 0, 0)
	}

	component := NewToolExecutionComponent("read", "tool-4d", map[string]any{"path": "README.md"},
		ToolExecutionOptions{}, override, &toolExecutionFakeTUI{}, mustCwd(t))
	component.UpdateResult(ToolExecutionResult{Content: textContent("hello")}, false)
	rendered := renderedPlain(t, component, 120)
	if !strings.Contains(rendered, "override call") || !strings.Contains(rendered, "override result") {
		t.Fatalf("custom renderers missing: %q", rendered)
	}
	if strings.Contains(rendered, "read README.md") {
		t.Fatalf("builtin header leaked: %q", rendered)
	}
}

func TestToolExecutionSharesRendererState(t *testing.T) {
	initTestTheme(t)
	definition := createBaseToolDefinition("")
	// pi: context.state is a plain {} object shared across slots; pig's
	// analog for definitions without NewRenderState is map[string]any.
	definition.RenderCall = func(args any, themeArg tools.UITheme, context tools.ToolRenderContext) tui.Component {
		state, _ := context.State.(map[string]any)
		if state["token"] == nil {
			state["token"] = "shared-token"
		}
		return tui.NewText("custom call "+state["token"].(string), 0, 0)
	}
	definition.RenderResult = func(result tools.ToolRenderResult, options tools.ToolRenderResultOptions, themeArg tools.UITheme, context tools.ToolRenderContext) tui.Component {
		state := context.State.(map[string]any)
		return tui.NewText("custom result "+state["token"].(string), 0, 0)
	}

	component := NewToolExecutionComponent("custom_tool", "tool-5", map[string]any{}, ToolExecutionOptions{}, definition, &toolExecutionFakeTUI{}, mustCwd(t))
	component.UpdateResult(ToolExecutionResult{Content: textContent("done"), Details: map[string]any{}}, false)
	rendered := renderedPlain(t, component, 120)
	if !strings.Contains(rendered, "custom call shared-token") || !strings.Contains(rendered, "custom result shared-token") {
		t.Fatalf("shared state missing: %q", rendered)
	}
}

func TestToolExecutionExposesArgsInResultContext(t *testing.T) {
	initTestTheme(t)
	definition := createBaseToolDefinition("")
	definition.RenderCall = func(args any, themeArg tools.UITheme, context tools.ToolRenderContext) tui.Component {
		return tui.NewText("call", 0, 0)
	}
	definition.RenderResult = func(result tools.ToolRenderResult, options tools.ToolRenderResultOptions, themeArg tools.UITheme, context tools.ToolRenderContext) tui.Component {
		foo := ""
		if m, ok := context.Args.(map[string]any); ok {
			foo, _ = m["foo"].(string)
		}
		return tui.NewText("arg:"+foo, 0, 0)
	}

	component := NewToolExecutionComponent("custom_tool", "tool-5b", map[string]any{"foo": "bar"}, ToolExecutionOptions{}, definition, &toolExecutionFakeTUI{}, mustCwd(t))
	component.UpdateResult(ToolExecutionResult{Content: textContent("done"), Details: map[string]any{}}, false)
	rendered := renderedPlain(t, component, 120)
	if !strings.Contains(rendered, "arg:bar") {
		t.Fatalf("args not exposed: %q", rendered)
	}
}

func TestToolExecutionCollapsesFallbackResults(t *testing.T) {
	initTestTheme(t)
	definition := createBaseToolDefinition("")

	component := NewToolExecutionComponent("custom_tool", "tool-6", map[string]any{"foo": "bar"}, ToolExecutionOptions{}, definition, &toolExecutionFakeTUI{}, mustCwd(t))
	var lines []string
	for i := 1; i <= 15; i++ {
		lines = append(lines, fmt.Sprintf("line-%d", i))
	}
	component.UpdateResult(ToolExecutionResult{Content: textContent(strings.Join(lines, "\n")), Details: map[string]any{}}, false)

	collapsed := renderedPlain(t, component, 120)
	if !strings.Contains(collapsed, "custom_tool") || !strings.Contains(collapsed, "line-10") {
		t.Fatalf("collapsed preview wrong: %q", collapsed)
	}
	if strings.Contains(collapsed, "line-11") {
		t.Fatalf("collapsed preview leaks: %q", collapsed)
	}
	if !strings.Contains(collapsed, "5 more lines") || !strings.Contains(collapsed, "to expand") {
		t.Fatalf("expand hint missing: %q", collapsed)
	}

	component.SetExpanded(true)
	expanded := renderedPlain(t, component, 120)
	if !strings.Contains(expanded, "line-15") || strings.Contains(expanded, "more lines") {
		t.Fatalf("expanded view wrong: %q", expanded)
	}
}

func TestToolExecutionTrimsWritePreviewTrailingBlanks(t *testing.T) {
	initTestTheme(t)
	component := NewToolExecutionComponent("write", "tool-7",
		map[string]any{"path": "README.md", "content": "one\ntwo\n"},
		ToolExecutionOptions{}, tools.CreateWriteToolDefinition(mustCwd(t), nil), &toolExecutionFakeTUI{}, mustCwd(t))
	rendered := renderedPlain(t, component, 120)
	if !strings.Contains(rendered, "one") || !strings.Contains(rendered, "two") {
		t.Fatalf("preview missing: %q", rendered)
	}
	if strings.Contains(rendered, "two\n\n") {
		t.Fatalf("trailing blanks not trimmed: %q", rendered)
	}
}

func TestToolExecutionTrimsReadResultTrailingBlanks(t *testing.T) {
	initTestTheme(t)
	component := NewToolExecutionComponent("read", "tool-8", map[string]any{"path": "notes.txt"},
		ToolExecutionOptions{}, tools.CreateReadToolDefinition(mustCwd(t), nil), &toolExecutionFakeTUI{}, mustCwd(t))
	component.UpdateResult(ToolExecutionResult{Content: textContent("one\ntwo\n")}, false)
	component.SetExpanded(true)
	rendered := renderedPlain(t, component, 120)
	if !strings.Contains(rendered, "one") || !strings.Contains(rendered, "two") {
		t.Fatalf("result missing: %q", rendered)
	}
	if strings.Contains(rendered, "two\n\n") {
		t.Fatalf("trailing blanks not trimmed: %q", rendered)
	}
}

func TestToolExecutionNoHighlightingOnReadErrors(t *testing.T) {
	initTestTheme(t)
	component := NewToolExecutionComponent("read", "tool-read-error-highlighting",
		map[string]any{"path": "config.exs", "offset": 120.0, "limit": 130.0},
		ToolExecutionOptions{}, tools.CreateReadToolDefinition(mustCwd(t), nil), &toolExecutionFakeTUI{}, mustCwd(t))
	errorText := "Offset 120 is beyond end of file (96 lines total)"
	component.UpdateResult(ToolExecutionResult{Content: textContent(errorText), IsError: true}, false)

	rendered := renderComponentLines(t, component, 120)
	if !strings.Contains(codingagentutils.StripAnsi(rendered), errorText) {
		t.Fatalf("error text missing: %q", rendered)
	}
	if !strings.Contains(rendered, themeFgToolOutput(errorText)) {
		t.Fatalf("error not styled as toolOutput: %q", rendered)
	}
}

func TestToolExecutionExpandsCollapsedResultOnClick(t *testing.T) {
	initTestTheme(t)
	component := NewToolExecutionComponent("read", "tool-click-expand", map[string]any{"path": "notes.txt"},
		ToolExecutionOptions{}, tools.CreateReadToolDefinition(mustCwd(t), nil), &toolExecutionFakeTUI{}, mustCwd(t))
	component.UpdateResult(ToolExecutionResult{Content: textContent("hidden content")}, false)

	width := 120
	lines := component.Render(width)
	resultRow := -1
	for i, line := range lines {
		if strings.Contains(codingagentutils.StripAnsi(line), "notes.txt") {
			resultRow = i
			break
		}
	}
	if resultRow < 0 {
		t.Fatalf("result row not found in %v", lines)
	}
	result := component.HandleMouse(&tui.TuiMouseEvent{
		Type: tui.MouseEventTypeClick, Button: tui.MouseButtonLeft,
		X: 2, Y: resultRow, ScreenX: 2, ScreenY: resultRow,
		Width: width, Height: len(lines), ClickCount: ptrInt(1),
	})
	if result == nil || !result.Handled {
		t.Fatalf("click not handled: %v", result)
	}
	if rendered := renderedPlain(t, component, width); !strings.Contains(rendered, "hidden content") {
		t.Fatalf("expected expanded content after click: %q", rendered)
	}
}

func TestToolExecutionCollapsesOrdinaryReadResults(t *testing.T) {
	initTestTheme(t)
	component := NewToolExecutionComponent("read", "tool-ordinary-read-collapsed", map[string]any{"path": "notes.txt"},
		ToolExecutionOptions{}, tools.CreateReadToolDefinition(mustCwd(t), nil), &toolExecutionFakeTUI{}, mustCwd(t))
	component.UpdateResult(ToolExecutionResult{Content: textContent("hidden content")}, false)

	collapsed := renderedPlain(t, component, 120)
	if !strings.Contains(collapsed, "read") || !strings.Contains(collapsed, "notes.txt") {
		t.Fatalf("collapsed header missing: %q", collapsed)
	}
	if strings.Contains(collapsed, "hidden content") {
		t.Fatalf("collapsed leaks content: %q", collapsed)
	}

	component.SetExpanded(true)
	expanded := renderedPlain(t, component, 120)
	if !strings.Contains(expanded, "hidden content") {
		t.Fatalf("expanded missing content: %q", expanded)
	}
}

func TestToolExecutionCompactReadScenarios(t *testing.T) {
	initTestTheme(t)
	cwd := mustCwd(t)
	scenarios := []struct {
		title   string
		path    string
		content string
		compact string
		hidden  string
		absent  string
	}{
		{
			title:   "SKILL.md",
			path:    filepath.Join(cwd, "attio", "SKILL.md"),
			content: "---\nname: attio\ndescription: CRM helper\n---\n\n# Hidden skill instructions",
			compact: "[skill] attio",
			hidden:  "Hidden skill instructions",
			absent:  "read skill attio",
		},
		{
			title:   "AGENTS.md",
			path:    filepath.Join(cwd, ".pig", "AGENTS.md"),
			content: "Hidden resource instructions",
			compact: "read resource .pig/AGENTS.md",
			hidden:  "Hidden resource instructions",
			absent:  "",
		},
		{
			title:   "AGENTS.override.md",
			path:    filepath.Join(cwd, ".pig", "AGENTS.override.md"),
			content: "Hidden override instructions",
			compact: "read resource .pig/AGENTS.override.md",
			hidden:  "Hidden override instructions",
			absent:  "",
		},
		{
			title:   "outside AGENTS.md",
			path:    filepath.Join(filepath.Dir(cwd), "AGENTS.md"),
			content: "Hidden outside resource instructions",
			compact: "read resource " + filepath.ToSlash(filepath.Join(filepath.Dir(cwd), "AGENTS.md")),
			hidden:  "Hidden outside resource instructions",
			absent:  "",
		},
		{
			title:   "Pi documentation",
			path:    codingagent.GetReadmePath(),
			content: "Hidden docs content",
			compact: "read docs README.md",
			hidden:  "Hidden docs content",
			absent:  "",
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.title, func(t *testing.T) {
			component := NewToolExecutionComponent("read", "tool-compact-"+scenario.title,
				map[string]any{"path": scenario.path},
				ToolExecutionOptions{}, tools.CreateReadToolDefinition(cwd, nil), &toolExecutionFakeTUI{}, cwd)
			component.UpdateResult(ToolExecutionResult{Content: textContent(scenario.content)}, false)

			collapsed := renderedPlain(t, component, 120)
			if !strings.Contains(collapsed, scenario.compact) {
				t.Fatalf("compact %q missing: %q", scenario.compact, collapsed)
			}
			if strings.Contains(collapsed, scenario.hidden) {
				t.Fatalf("hidden content leaked: %q", collapsed)
			}
			if scenario.absent != "" && strings.Contains(collapsed, scenario.absent) {
				t.Fatalf("absent content present: %q", collapsed)
			}

			component.SetExpanded(true)
			expanded := renderedPlain(t, component, 120)
			if !strings.Contains(expanded, scenario.hidden) {
				t.Fatalf("expanded missing hidden: %q", expanded)
			}
		})
	}
}

func TestToolExecutionCompactReadLineRangeOrdering(t *testing.T) {
	initTestTheme(t)
	cwd := mustCwd(t)
	scenarios := []struct {
		title   string
		path    string
		compact string
	}{
		{"SKILL.md", filepath.Join(cwd, "attio", "SKILL.md"), "[skill] attio:120-329"},
		{"Pi documentation", codingagent.GetReadmePath(), "read docs README.md:120-329"},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.title, func(t *testing.T) {
			component := NewToolExecutionComponent("read", "tool-compact-range-"+scenario.title,
				map[string]any{"path": scenario.path, "offset": 120.0, "limit": 210.0},
				ToolExecutionOptions{}, tools.CreateReadToolDefinition(cwd, nil), &toolExecutionFakeTUI{}, cwd)

			collapsed := renderedPlain(t, component, 120)
			if !strings.Contains(collapsed, scenario.compact) {
				t.Fatalf("compact %q missing: %q", scenario.compact, collapsed)
			}
			if strings.Index(collapsed, ":120-329") > strings.Index(collapsed, "to expand") {
				t.Fatalf("line range not before expand hint: %q", collapsed)
			}
		})
	}
}

// ptrInt is a local pointer helper for mouse event fields.
func ptrInt(v int) *int { return &v }
