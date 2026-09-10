// Package tools corresponds to pi's packages/coding-agent/src/core/tools —
// the coding-agent built-in tools and their ToolDefinition layer.
//
// Batch scope (Phase 4, core/tools):
//   - The runtime layer is complete: Operations interfaces, execute logic,
//     and the createXTool AgentTool constructors.
//   - truncate.ts: pi ships a duplicate of the agent package's
//     utils/truncate.ts in core/tools (verified byte-identical logic); pig
//     imports agent/harness/utils instead of porting a second copy.
//   - edit-diff.ts: identical to the harness copy for the shared algorithm;
//     pig imports agent/harness/tools and adds only the coding-agent
//     preview wrappers (computeEditsDiff/computeEditDiff) here.
//
// Not yet ported (arrive with their consumers, placeholders left at their
// source positions):
//   - createXToolDefinition's render members (renderCall/renderResult/
//     renderShell) and render-utils' Theme/pi-tui-dependent helpers: the
//     TUI types arrive with Phase 6. The ToolDefinition struct here carries
//     the non-UI surface only; UI members are placed at their pi source
//     positions when the theme batch lands.
//   - constrainedSampling: pi wires getExperimentalToolSampling() (core/
//     experimental.ts + config experimental flags) into every definition;
//     it arrives with the settings/config batch as nil (undefined) until
//     then.
//   - getReadmePath-based compact read classification (read.ts) is render-
//     layer only and follows the theme batch.
package tools

import (
	"context"
	"encoding/json"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/tui"
)

// ToolExecuteContext is the slice of pi's ExtensionContext (core/extensions/
// types.ts) consumed by the built-in tools' execute() bodies: the current
// model (read's non-vision note, bash's PI_* env), the session identity, and
// the thinking level. The full extensions/types.ts surface arrives with the
// extensions batch; until then callers pass a value implementing this
// interface or nil (pi passes undefined outside the interactive session).
type ToolExecuteContext interface {
	// GetModel is pi's ctx.model.
	GetModel() *types.Model
	// GetSessionID is pi's ctx.sessionManager.getSessionId().
	GetSessionID() string
	// GetSessionFile is pi's ctx.sessionManager.getSessionFile(); nil when
	// the session is not file-backed.
	GetSessionFile() *string
	// GetThinkingLevel is pi's ctx.thinkingLevel; "" when unset.
	GetThinkingLevel() string
	// GetCwd is pi's ctx.cwd (v0.85.1: the tools resolve paths against it
	// when provided, falling back to the definition-captured cwd); "" =
	// undefined.
	GetCwd() string
}

// executeCwd resolves pi's `ctx?.cwd || cwd` (v0.85.1): the tool context's
// cwd wins when provided.
func executeCwd(toolCtx ToolExecuteContext, cwd string) string {
	if toolCtx != nil {
		if ctxCwd := toolCtx.GetCwd(); ctxCwd != "" {
			return ctxCwd
		}
	}
	return cwd
}

// ============================================================================
// Tool render surface (pi: extensions/types.ts "Tool Types" —
// ToolRenderResultOptions, ToolRenderContext, ToolDefinition render members)
// ============================================================================

// UITheme is the theme surface available to UI renderers (pi passes the
// concrete Theme from modes/interactive/theme; *theme.Theme satisfies this
// structurally — its color token types are string aliases for exactly this
// reason). Declared deviation: defined here rather than in package core
// because core aliases it (core → core/tools is an existing edge, the
// reverse would cycle); package core re-exports it as core.UITheme.
type UITheme interface {
	// Fg wraps text in the token's foreground color (pi: theme.fg).
	Fg(color string, text string) string
	// Bg wraps text in the token's background color (pi: theme.bg).
	Bg(color string, text string) string
	// Bold mirrors chalk.bold.
	Bold(text string) string
	// Italic mirrors chalk.italic.
	Italic(text string) string
	// Underline mirrors chalk.underline.
	Underline(text string) string
	// Inverse mirrors chalk.inverse.
	Inverse(text string) string
	// Strikethrough mirrors chalk.strikethrough.
	Strikethrough(text string) string
	// GetFgAnsi returns the raw ANSI foreground prefix (pi: getFgAnsi).
	GetFgAnsi(color string) string
	// GetBgAnsi returns the raw ANSI background prefix (pi: getBgAnsi).
	GetBgAnsi(color string) string
}

// ToolRenderResultOptions carries the tool result render options (pi:
// ToolRenderResultOptions).
type ToolRenderResultOptions struct {
	// Expanded reports whether the result view is expanded (pi: expanded).
	Expanded bool
	// IsPartial reports whether this is a partial/streaming result (pi:
	// isPartial).
	IsPartial bool
}

// ToolRenderContext is the context passed to tool renderers (pi:
// ToolRenderContext<TState, TArgs>; generics collapse to any).
type ToolRenderContext struct {
	// Args are the current tool call arguments (pi: args).
	Args any
	// ToolCallID is the unique id for this tool execution, stable across
	// call/result renders (pi: toolCallId).
	ToolCallID string
	// Invalidate invalidates just this tool execution component for redraw
	// (pi: invalidate).
	Invalidate func()
	// LastComponent is the previously returned component for this render
	// slot, if any (pi: lastComponent).
	LastComponent tui.Component
	// State is shared renderer state for this tool row (pi: state).
	State any
	// Cwd is the working directory for this tool execution (pi: cwd).
	Cwd string
	// ExecutionStarted reports whether the tool execution has started (pi:
	// executionStarted).
	ExecutionStarted bool
	// ArgsComplete reports whether the tool call arguments are complete
	// (pi: argsComplete).
	ArgsComplete bool
	// IsPartial reports whether the tool result is partial/streaming (pi:
	// isPartial).
	IsPartial bool
	// Expanded reports whether the result view is expanded (pi: expanded).
	Expanded bool
	// ShowImages reports whether inline images are currently shown in the
	// TUI (pi: showImages).
	ShowImages bool
	// IsError reports whether the current result is an error (pi: isError).
	IsError bool
}

// ToolRenderResult is the result shape handed to renderResult (pi:
// ToolRenderResult<TDetails> = { content, details? }); pig passes the full
// AgentToolResult the tool produced (Details carries TDetails).
type ToolRenderResult = agent.AgentToolResult

// ToolDefinition is pi's ToolDefinition (core/extensions/types.ts). TS
// renders tools definition-first and wraps via tool-definition-wrapper.ts;
// pig keeps the same split. Exported because AgentSession's definition-first
// registry (core package) stores it (pi: the registry lives next to the
// ToolDefinition type).
type ToolDefinition struct {
	Name        string
	Label       string
	Description string
	// PromptSnippet feeds the Available tools system-prompt section (pi:
	// promptSnippet).
	PromptSnippet string
	// PromptGuidelines feed the system-prompt Guidelines section (pi:
	// promptGuidelines).
	PromptGuidelines []string
	// Parameters is the TypeBox schema (pi: parameters).
	Parameters json.RawMessage
	// ConstrainedSampling is pi's constrainedSampling; nil = undefined (the
	// experimental flag wiring arrives with the config batch).
	ConstrainedSampling any
	// ExecutionMode is the per-tool execution mode override (pi:
	// executionMode).
	ExecutionMode agent.ToolExecutionMode
	// PrepareArguments is the optional compatibility shim applied to raw
	// tool-call arguments before schema validation (pi: prepareArguments).
	PrepareArguments func(args any) any
	// Execute runs the tool (pi: execute). ctx carries cancellation (pi:
	// signal) and toolCtx is the ExtensionContext slice (pi: ctx).
	Execute func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx ToolExecuteContext) (agent.AgentToolResult, error)
	// NewRenderState builds the shared per-tool-row renderer state (pig
	// addition, declared deviation: pi's ToolExecutionComponent initializes
	// state to a plain {} object that renderers mutate in place; Go needs a
	// concrete value. Built-in definitions return their state struct,
	// extension tools keep nil → tool-execution falls back to a
	// map[string]any, the closest analog of pi's {}).
	NewRenderState func() any
	// RenderShell controls whether ToolExecutionComponent renders the
	// standard colored shell or the tool renders its own framing (pi:
	// renderShell; "" = default, "self" = tool renders its own framing).
	RenderShell string
	// RenderCall renders the tool-call row (pi: renderCall); nil = fallback
	// rendering by the consumer.
	RenderCall func(args any, theme UITheme, context ToolRenderContext) tui.Component
	// RenderResult renders the tool result row (pi: renderResult); nil =
	// fallback rendering by the consumer.
	RenderResult func(result ToolRenderResult, options ToolRenderResultOptions, theme UITheme, context ToolRenderContext) tui.Component
}

// Tool is pi's `type Tool = AgentTool<any>` plus the prompt metadata that
// createBashTool/createPowerShellTool assign onto the wrapper (pi:
// Object.assign(tool, { promptSnippet, promptGuidelines })) and the
// ExtensionContext-bearing execute of the wrapped definition (pi's wrapper
// resolves `ctx ?? (ctxFactory?.())`; the ctxFactory variant arrives with
// the extensions batch, so ExecuteWithToolContext lets callers pass the
// context explicitly and Execute passes nil, pi's undefined).
type Tool struct {
	agent.AgentTool
	PromptSnippet    string
	PromptGuidelines []string
	// ExecuteWithToolContext invokes the wrapped definition with the
	// ExtensionContext slice (pi: definition.execute's ctx parameter).
	ExecuteWithToolContext func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx ToolExecuteContext) (agent.AgentToolResult, error)
}

// wrapToolDefinition wraps a definition into an AgentTool for the core
// runtime (pi: tool-definition-wrapper.ts's wrapToolDefinition). The
// optional ctxFactory resolves pi's ExtensionContext for the plain execute
// path (pi: `ctx ?? (ctxFactory?.())`); the extensions batch passes a
// factory, the factory-free callers pass nil.
func WrapToolDefinition(definition *ToolDefinition, ctxFactory func() ToolExecuteContext) Tool {
	return Tool{
		AgentTool: agent.AgentTool{
			Tool: types.Tool{
				Name:                definition.Name,
				Description:         definition.Description,
				Parameters:          definition.Parameters,
				ConstrainedSampling: definition.ConstrainedSampling,
			},
			Label:            definition.Label,
			PrepareArguments: definition.PrepareArguments,
			Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				var toolCtx ToolExecuteContext
				if ctxFactory != nil {
					toolCtx = ctxFactory()
				}
				return definition.Execute(ctx, toolCallID, params, onUpdate, toolCtx)
			},
			ExecutionMode: definition.ExecutionMode,
		},
		PromptSnippet:    definition.PromptSnippet,
		PromptGuidelines: definition.PromptGuidelines,
		ExecuteWithToolContext: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx ToolExecuteContext) (agent.AgentToolResult, error) {
			return definition.Execute(ctx, toolCallID, params, onUpdate, toolCtx)
		},
	}
}
