// tool_renderer.go (pi: core/export-html/tool-renderer.ts, v0.86.0)
//
// Tool HTML renderer for custom tools in HTML export.
//
// Renders custom tool calls and results to HTML by invoking their TUI
// renderers and converting the ANSI output to HTML.
package exporthtml

import (
	"regexp"
	"strings"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// ToolHtmlRendererDeps parameterizes CreateToolHtmlRenderer (pi:
// ToolHtmlRendererDeps).
type ToolHtmlRendererDeps struct {
	// GetToolDefinition looks up a tool definition by name (pi:
	// getToolDefinition).
	GetToolDefinition func(name string) *coretools.ToolDefinition
	// ThemeFunc resolves the theme for styling (pi: theme). pi passes the
	// live theme proxy, which throws lazily on first access when
	// uninitialized — inside the renderers' try/catch, so export silently
	// falls back to structured rendering. pig resolves it through this
	// lazy accessor inside the same recover-guarded calls (an eager
	// *Theme would move the "Theme not initialized" panic to renderer
	// construction, a behavior change).
	ThemeFunc func() *theme.Theme
	// Cwd is the working directory for the render context.
	Cwd string
	// Width is the terminal width for rendering (pi: width?, default 100).
	Width int
}

// ansiEscapeRegex strips SGR sequences for blank-line detection (pi:
// ANSI_ESCAPE_REGEX).
var ansiEscapeRegex = regexp.MustCompile(`\x1b\[[\d;]*m`)

func isBlankRenderedLine(line string) bool {
	return len(strings.TrimSpace(ansiEscapeRegex.ReplaceAllString(line, ""))) == 0
}

func trimRenderedResultLines(lines []string) []string {
	start := 0
	end := len(lines)
	for start < end && isBlankRenderedLine(lines[start]) {
		start++
	}
	for end > start && isBlankRenderedLine(lines[end-1]) {
		end--
	}
	return lines[start:end]
}

// toolHTMLRenderer is the concrete ToolHtmlRenderer (pi:
// createToolHtmlRenderer's returned object).
type toolHTMLRenderer struct {
	deps                     ToolHtmlRendererDeps
	renderedCallComponents   map[string]tui.Component
	renderedResultComponents map[string]tui.Component
	renderedStates           map[string]any
	renderedArgs             map[string]any
}

// CreateToolHtmlRenderer creates a tool HTML renderer.
//
// The renderer looks up tool definitions and invokes their
// RenderCall/RenderResult methods, converting the resulting TUI component
// output (ANSI) to HTML. pi wraps every renderer call in try/catch and
// returns undefined on error so HTML export falls back to structured
// result rendering; pig recovers panics to the same effect.
func CreateToolHtmlRenderer(deps ToolHtmlRendererDeps) ToolHtmlRenderer {
	if deps.Width == 0 {
		deps.Width = 100
	}
	return &toolHTMLRenderer{
		deps:                     deps,
		renderedCallComponents:   map[string]tui.Component{},
		renderedResultComponents: map[string]tui.Component{},
		renderedStates:           map[string]any{},
		renderedArgs:             map[string]any{},
	}
}

// getState returns the shared per-row renderer state (pi: getState with
// a plain {} initial value). pig initializes built-in tool state via
// NewRenderState (mirroring the tool-execution component); pi's {}
// initial value works there because TS renderers mutate a plain object,
// while pig renderers type-assert their concrete state struct.
func (r *toolHTMLRenderer) getState(toolCallID string, toolName string) any {
	state, ok := r.renderedStates[toolCallID]
	if !ok || state == nil {
		if def := r.deps.GetToolDefinition(toolName); def != nil && def.NewRenderState != nil {
			state = def.NewRenderState()
		} else {
			state = map[string]any{}
		}
		r.renderedStates[toolCallID] = state
	}
	return state
}

func (r *toolHTMLRenderer) createRenderContext(
	toolCallID string,
	toolName string,
	lastComponent tui.Component,
	expanded bool,
	isPartial bool,
	isError bool,
) coretools.ToolRenderContext {
	return coretools.ToolRenderContext{
		Args:             r.renderedArgs[toolCallID],
		ToolCallID:       toolCallID,
		Invalidate:       func() {},
		LastComponent:    lastComponent,
		State:            r.getState(toolCallID, toolName),
		Cwd:              r.deps.Cwd,
		ExecutionStarted: true,
		ArgsComplete:     true,
		IsPartial:        isPartial,
		Expanded:         expanded,
		ShowImages:       false,
		IsError:          isError,
		DurationMs:       nil,
	}
}

func (r *toolHTMLRenderer) RenderCall(toolCallID string, toolName string, args any) (html string) {
	defer func() {
		if recover() != nil {
			// On error, return "" so HTML export can fall back to structured
			// result rendering (pi: catch → undefined).
			html = ""
		}
	}()
	r.renderedArgs[toolCallID] = args
	toolDef := r.deps.GetToolDefinition(toolName)
	if toolDef == nil || toolDef.RenderCall == nil {
		return ""
	}

	component := toolDef.RenderCall(
		args,
		r.deps.ThemeFunc(),
		r.createRenderContext(toolCallID, toolName, r.renderedCallComponents[toolCallID], false, true, false),
	)
	r.renderedCallComponents[toolCallID] = component
	lines := component.Render(r.deps.Width)
	return AnsiLinesToHtml(lines)
}

func (r *toolHTMLRenderer) RenderResult(
	toolCallID string,
	toolName string,
	result []types.Content,
	details any,
	isError bool,
) (collapsed, expanded string, ok bool) {
	defer func() {
		if recover() != nil {
			// On error, fall back to structured result rendering (pi: catch
			// → undefined).
			collapsed, expanded, ok = "", "", false
		}
	}()
	toolDef := r.deps.GetToolDefinition(toolName)
	if toolDef == nil || toolDef.RenderResult == nil {
		return "", "", false
	}
	theme := r.deps.ThemeFunc()

	// Build AgentToolResult from the content array (session storage uses
	// generic object types in pi; pig entries decode to typed content;
	// isError reaches renderers via the ToolRenderContext, pig's
	// AgentToolResult has no isError field).
	agentToolResult := agent.AgentToolResult{
		Content: result,
		Details: details,
	}

	// Render collapsed
	collapsedComponent := toolDef.RenderResult(
		agentToolResult,
		coretools.ToolRenderResultOptions{Expanded: false, IsPartial: false},
		theme,
		r.createRenderContext(toolCallID, toolName, r.renderedResultComponents[toolCallID], false, false, isError),
	)
	r.renderedResultComponents[toolCallID] = collapsedComponent
	collapsedHTML := AnsiLinesToHtml(trimRenderedResultLines(collapsedComponent.Render(r.deps.Width)))

	// Render expanded
	expandedComponent := toolDef.RenderResult(
		agentToolResult,
		coretools.ToolRenderResultOptions{Expanded: true, IsPartial: false},
		theme,
		r.createRenderContext(toolCallID, toolName, r.renderedResultComponents[toolCallID], true, false, isError),
	)
	r.renderedResultComponents[toolCallID] = expandedComponent
	expandedHTML := AnsiLinesToHtml(trimRenderedResultLines(expandedComponent.Render(r.deps.Width)))

	if collapsedHTML != "" && collapsedHTML != expandedHTML {
		collapsed = collapsedHTML
	}
	expanded = expandedHTML
	return collapsed, expanded, true
}
