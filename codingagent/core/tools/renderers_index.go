// renderers_index.go (pi: src/core/tools/renderers/index.ts) — built-in tool
// renderers, without the tools themselves.
//
// S5a note: pi hosts the renderers in core/tools/renderers/ so a process
// that only displays tool output can import them without the execution
// graph. pig keeps them in package tools (renderers_*.go files) because the
// renderers consume package-tools values (render-utils, edit-diff,
// keybinding hints, detail types) while the tool files spread them back
// into their definitions — a Go subpackage would be an import cycle.
package tools

import (
	"github.com/ZoroSpace/pig/tui"
)

// ToolRenderers is what a presentation needs from a tool: how to draw it (pi:
// renderers/index.ts ToolRenderers = Pick<ToolDefinition, "renderCall" |
// "renderResult">, plus tool-execution.ts's ToolRenderers interface which
// adds renderShell; the two fold into one struct here). It neither executes
// tools nor reads their parameter schemas, so a definition and a bare
// renderer pair are equally acceptable.
type ToolRenderers struct {
	// RenderShell controls whether ToolExecutionComponent renders the
	// standard colored shell or the tool renders its own framing (pi:
	// renderShell; "" = default, "self" = tool renders its own framing).
	RenderShell string
	// NewRenderState builds the shared per-tool-row renderer state (pig
	// addition, following the ToolDefinition field of the same name; pi
	// initializes state to a plain {} object that renderers mutate in
	// place, Go needs a concrete value). Only the renderers with typed
	// state (bash, edit) set it; nil falls back to a map[string]any.
	NewRenderState func() any
	// RenderCall renders the tool-call row (pi: renderCall); nil = fallback
	// rendering by the consumer.
	RenderCall func(args any, theme UITheme, context ToolRenderContext) tui.Component
	// RenderResult renders the tool result row (pi: renderResult); nil =
	// fallback rendering by the consumer.
	RenderResult func(result ToolRenderResult, options ToolRenderResultOptions, theme UITheme, context ToolRenderContext) tui.Component
}

// renderersByToolName is the renderers for every built-in tool, keyed by
// tool name (pi: createAllToolRenderers; the record is constant, so pig
// builds it lazily once).
func renderersByToolName() map[ToolName]ToolRenderers {
	return map[ToolName]ToolRenderers{
		"read":       ReadRenderers,
		"bash":       CreateShellRenderers("$"),
		"powershell": CreateShellRenderers("PS>"),
		"edit":       EditRenderers,
		"write":      WriteRenderers,
		"grep":       GrepRenderers,
		"find":       FindRenderers,
		"ls":         LsRenderers,
	}
}

// WithBuiltInRenderers merges built-in renderers into a tool definition that
// does not supply its own (pi: withBuiltInRenderers). ToolExecutionComponent
// used to do this lookup itself, which forced every presentation to import
// the tool implementations. Callers do it now, so a process that renders can
// import renderers alone. A nil definition yields a render-only definition
// shell. Only renderCall/renderResult are merged (pi does not fill
// renderShell either).
func WithBuiltInRenderers(toolName string, definition *ToolDefinition) *ToolDefinition {
	builtIn, ok := renderersByToolName()[ToolName(toolName)]
	if !ok {
		return definition
	}
	if definition == nil {
		return &ToolDefinition{
			Name:           toolName,
			NewRenderState: builtIn.NewRenderState,
			RenderCall:     builtIn.RenderCall,
			RenderResult:   builtIn.RenderResult,
		}
	}
	if definition.RenderCall == nil {
		definition.RenderCall = builtIn.RenderCall
	}
	if definition.RenderResult == nil {
		definition.RenderResult = builtIn.RenderResult
	}
	if definition.NewRenderState == nil {
		definition.NewRenderState = builtIn.NewRenderState
	}
	return definition
}
