package components

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	codingagentutils "github.com/ZoroSpace/pig/codingagent/utils"
)

// pig-only regression (2026-10-03,
// docs/feat/BUILTIN-THEME-PALETTES.md §BLOCK-BORDERS): message and tool
// blocks render with state-colored top/bottom rules and no background fill
// (pi fills the box background instead).

// assertNoBackgroundFill fails when the render carries any background SGR
// (48;2 truecolor or 48;5 quantized). All border/content colors in these
// blocks are foreground-only.
func assertNoBackgroundFill(t *testing.T, render string) {
	t.Helper()
	if strings.Contains(render, "48;2;") || strings.Contains(render, "48;5;") {
		t.Fatalf("render must not carry background fills: %q", render)
	}
}

func ruleLine(token string) string {
	// The SGR prefix that theme.Fg(token, …) emits (the reset lands after
	// the full dash run, so the prefix alone identifies the rule color).
	styled := theme.Fg(token, "X")
	idx := strings.Index(styled, "X")
	if idx < 0 {
		panic("theme.Fg produced no text")
	}
	return styled[:idx]
}

func TestToolExecutionBorderOnlyFraming(t *testing.T) {
	initTestTheme(t)

	component := NewToolExecutionComponent("read", "call-1", map[string]any{"path": "x"},
		ToolExecutionOptions{}, nil, nil, "/tmp")

	// Pending (partial): muted rules frame the block.
	component.UpdateResult(ToolExecutionResult{Content: textContent("partial")}, true)
	lines := component.Render(40)
	joined := strings.Join(lines, "\n")
	if len(lines) < 4 {
		t.Fatalf("expected framed render, got %d lines: %q", len(lines), joined)
	}
	if !strings.Contains(lines[1], ruleLine("muted")) {
		t.Fatalf("top rule missing or not muted: %q", lines[1])
	}
	if !strings.Contains(lines[1], "┌") || !strings.Contains(lines[len(lines)-1], "└") {
		t.Fatalf("frame corners missing: %q / %q", lines[1], lines[len(lines)-1])
	}
	if !strings.Contains(lines[len(lines)-1], ruleLine("muted")) {
		t.Fatalf("bottom rule missing or not muted: %q", lines[len(lines)-1])
	}
	assertNoBackgroundFill(t, joined)

	// Settled success: rules switch to the success token.
	component.UpdateResult(ToolExecutionResult{Content: textContent("ok")}, false)
	okRender := strings.Join(component.Render(40), "\n")
	if !strings.Contains(okRender, ruleLine("success")) {
		t.Fatalf("success rule color missing: %q", okRender)
	}
	assertNoBackgroundFill(t, okRender)

	// Settled error: rules switch to the error token.
	component.UpdateResult(ToolExecutionResult{Content: textContent("boom"), IsError: true}, false)
	errRender := strings.Join(component.Render(40), "\n")
	if !strings.Contains(errRender, ruleLine("error")) {
		t.Fatalf("error rule color missing: %q", errRender)
	}
	assertNoBackgroundFill(t, errRender)
}

func TestUserMessageNoBackgroundFill(t *testing.T) {
	initTestTheme(t)

	// The rule color is injectable: interactive_mode passes the editor's
	// thinking-level color so the sent block matches the input box.
	component := NewUserMessageComponent("hello world", nil, 1, nil, func(s string) string {
		return theme.Fg("thinkingMedium", s)
	})
	lines := component.Render(40)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(lines[0], ruleLine("thinkingMedium")) {
		t.Fatalf("top rule missing or not thinking-colored: %q", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], ruleLine("thinkingMedium")) {
		t.Fatalf("bottom rule missing or not thinking-colored: %q", lines[len(lines)-1])
	}
	assertNoBackgroundFill(t, joined)

	// nil ruleColor falls back to the theme "border" token.
	defaultComponent := NewUserMessageComponent("hello world", nil, 1, nil, nil)
	defaultLines := defaultComponent.Render(40)
	if !strings.Contains(defaultLines[0], ruleLine("border")) {
		t.Fatalf("default top rule missing or not border-colored: %q", defaultLines[0])
	}
	assertNoBackgroundFill(t, strings.Join(defaultLines, "\n"))
}

var _ = types.KindText

// pig-only regression (2026-10-10,
// docs/fix/2026-10-10-edit-details-map-form.md): the restored-session path
// decodes toolResult.details into map[string]any, and the pointer-only
// *EditToolDetails assertion dropped the diff (double-form family). The
// framed edit block must render the diff from both shapes.
func TestEditToolResultDetailsMapForm(t *testing.T) {
	initTestTheme(t)

	definition := createBaseToolDefinition("edit")
	definition.NewRenderState = tools.EditRenderers.NewRenderState
	definition.RenderCall = tools.EditRenderers.RenderCall
	definition.RenderResult = tools.EditRenderers.RenderResult
	args := map[string]any{
		"path":  "/tmp/x.py",
		"edits": []any{map[string]any{"oldText": "a", "newText": "b"}},
	}

	for name, details := range map[string]any{
		"pointer (live)":    &tools.EditToolDetails{Diff: "-old line\n+new line"},
		"map (camelCase)":   map[string]any{"diff": "-old line\n+new line"},
		"map (legacy keys)": map[string]any{"Diff": "-old line\n+new line"},
		"value (defensive)": tools.EditToolDetails{Diff: "-old line\n+new line"},
	} {
		t.Run(name, func(t *testing.T) {
			component := NewToolExecutionComponent("edit", "call-details-form", args, ToolExecutionOptions{}, definition, &toolExecutionFakeTUI{}, mustCwd(t))
			component.UpdateResult(ToolExecutionResult{Content: textContent("ok"), Details: details}, false)
			out := codingagentutils.StripAnsi(strings.Join(component.Render(60), "\n"))
			if !strings.Contains(out, "-old line") || !strings.Contains(out, "+new line") {
				t.Fatalf("diff missing for %s details: %q", name, out)
			}
		})
	}
}

// pig-only regression (2026-10-10, same doc §BLOCK-BORDERS): edit joined the
// default shell and renders the state-colored corner frame like write (pi
// self-renders edit; deviation note lives on edit.go's RenderShell field).
// Stash-red check: restoring RenderShell: "self" in edit.go drops the frame.
func TestEditToolFramedShell(t *testing.T) {
	initTestTheme(t)

	definition := createBaseToolDefinition("edit")
	definition.NewRenderState = tools.EditRenderers.NewRenderState
	definition.RenderCall = tools.EditRenderers.RenderCall
	definition.RenderResult = tools.EditRenderers.RenderResult
	args := map[string]any{
		"path":  "/tmp/x.py",
		"edits": []any{map[string]any{"oldText": "a", "newText": "b"}},
	}
	component := NewToolExecutionComponent("edit", "call-edit-frame", args, ToolExecutionOptions{}, definition, &toolExecutionFakeTUI{}, mustCwd(t))
	component.UpdateResult(ToolExecutionResult{Content: textContent("")}, false)

	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if len(lines) < 4 {
		t.Fatalf("expected framed render, got %d lines: %q", len(lines), joined)
	}
	if !strings.Contains(lines[1], "┌") || !strings.Contains(lines[1], ruleLine("success")) {
		t.Fatalf("top rule missing or not success-colored: %q", lines[1])
	}
	if !strings.Contains(lines[len(lines)-1], "└") || !strings.Contains(lines[len(lines)-1], ruleLine("success")) {
		t.Fatalf("bottom rule missing or not success-colored: %q", lines[len(lines)-1])
	}
	assertNoBackgroundFill(t, joined)

	// Header indented exactly once (contentBox padding), not twice.
	header := codingagentutils.StripAnsi(lines[2])
	if !strings.HasPrefix(header, " edit ") {
		t.Fatalf("edit header missing or double-indented: %q", header)
	}
}
