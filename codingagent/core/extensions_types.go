// pi: core/extensions/types.ts — the extension system types, restricted to
// the surface AgentSession (core/agent-session.ts) consumes. The remaining
// surface arrives with its consumers: renderer/shortcut/widget component
// types with the TUI batch (Phase 6), and the loader/extension-runtime
// plumbing with the extensions batch (Phase 7).
//
// Placement deviation (same dependency direction as compaction.go): pi's
// core/extensions/ is one TS package with session-manager.ts and imports its
// types; a Go subpackage would import codingagent/core while package core
// imports it back (AgentSession → ExtensionRunner). Files therefore live
// flat in package core.
//
// Structural deviations (declared):
//   - ExtensionContext/ExtensionCommandContext are TS objects whose
//     properties are lazy getters over runner state; Go represents them as
//     structs whose action members are function fields populated by the
//     runner. Stale-instance guards run at runner entry points
//     (assertActive) rather than at property access.
//   - Event union members are separate structs implementing ExtensionEvent
//     (discriminated union → interface + concrete types rule). pi's
//     `signal: AbortSignal` members on session_before_compact /
//     session_before_tree are dropped; cancellation travels as the Emit
//     call's context.
//   - ExtensionUIContext's component-factory overloads arrived with T5b-3
//     (Phase 6 interactive batch); the factories take no theme parameter —
//     pig components read the global current theme via theme.Current()
//     (repo-wide precedent; pi passes the theme as a factory argument).
package core

import (
	"context"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/tui"
)

// ExtensionMode is the run mode reported to extensions (pi: ExtensionMode).
type ExtensionMode string

const (
	ExtensionModeTUI   ExtensionMode = "tui"
	ExtensionModeRPC   ExtensionMode = "rpc"
	ExtensionModeJSON  ExtensionMode = "json"
	ExtensionModePrint ExtensionMode = "print"
)

// InputSource is where an input to prompt() came from (pi: InputSource).
type InputSource string

const (
	InputSourceInteractive InputSource = "interactive"
	InputSourceRPC         InputSource = "rpc"
	InputSourceExtension   InputSource = "extension"
)

// ContextUsage describes the current context window usage (pi:
// ContextUsage). nil Tokens/Percent = pi's null (unknown, e.g. right after
// compaction before the next LLM response).
type ContextUsage struct {
	// Tokens is the estimated context token count; nil = unknown (pi:
	// tokens: number | null).
	Tokens *int64 `json:"tokens"`
	// ContextWindow is the model's context window.
	ContextWindow int64 `json:"contextWindow"`
	// Percent is the usage as percentage of the context window; nil = unknown
	// (pi: percent: number | null).
	Percent *float64 `json:"percent"`
}

// CompactOptions are the options for triggering compaction from extensions
// (pi: CompactOptions).
type CompactOptions struct {
	// CustomInstructions are the optional custom focus for the summary;
	// "" = none (pi: customInstructions?).
	CustomInstructions string
	// OnComplete fires with the compaction result (pi: onComplete).
	OnComplete func(result *CompactionResult)
	// OnError fires when compaction failed (pi: onError).
	OnError func(err error)
}

// WorkingIndicatorOptions configure the interactive working indicator (pi:
// WorkingIndicatorOptions).
type WorkingIndicatorOptions struct {
	// Frames are custom animation frames; ["●"] = static, [] = hidden (pi:
	// frames). Custom frames render verbatim.
	Frames []string
	// IntervalMs is the frame interval in milliseconds for animated
	// indicators (pi: intervalMs).
	IntervalMs int
}

// TerminalInputResult is the handler's return (pi: `{ consume?, data? } |
// undefined`; nil return = undefined).
type TerminalInputResult struct {
	// Consume swallows the input instead of forwarding it (pi: consume?).
	Consume bool
	// Data replaces the forwarded input; "" = leave unchanged (pi: data?).
	Data string
}

// TerminalInputHandler receives raw terminal input and may consume or
// rewrite it (pi: TerminalInputHandler). nil return = undefined (forward
// unchanged).
type TerminalInputHandler func(data string) *TerminalInputResult

// ExtensionUIContext is the UI surface extensions can use (pi:
// ExtensionUIContext).
type ExtensionUIContext interface {
	// Select shows a selector and returns the user's choice (pi: select).
	Select(ctx context.Context, title string, options []string, opts *ExtensionUIDialogOptions) (string, bool)
	// Confirm shows a confirmation dialog (pi: confirm).
	Confirm(ctx context.Context, title string, message string, opts *ExtensionUIDialogOptions) bool
	// Input shows a text input dialog (pi: input).
	Input(ctx context.Context, title string, placeholder string, opts *ExtensionUIDialogOptions) (string, bool)
	// Notify shows a notification (pi: notify). level: "info"|"warning"|"error";
	// "" = pi's undefined (default styling).
	Notify(ctx context.Context, message string, level string)
	// OnTerminalInput listens to raw terminal input (interactive mode only)
	// and returns an unsubscribe function (pi: onTerminalInput).
	OnTerminalInput(ctx context.Context, handler TerminalInputHandler) func()
	// SetStatus sets status text in the footer/status bar; "" clears (pi:
	// setStatus).
	SetStatus(ctx context.Context, key string, text string)
	// SetWorkingMessage sets the working/loading message shown during
	// streaming; "" restores the default (pi: setWorkingMessage).
	SetWorkingMessage(ctx context.Context, message string)
	// SetWorkingVisible shows or hides the built-in interactive working
	// loader row during streaming (pi: setWorkingVisible).
	SetWorkingVisible(ctx context.Context, visible bool)
	// SetWorkingIndicator configures the interactive working indicator (pi:
	// setWorkingIndicator); nil restores the default animated spinner.
	SetWorkingIndicator(ctx context.Context, options *WorkingIndicatorOptions)
	// SetHiddenThinkingLabel sets the label shown for hidden thinking blocks;
	// "" restores the default (pi: setHiddenThinkingLabel).
	SetHiddenThinkingLabel(ctx context.Context, label string)
	// SetWidget sets a widget above or below the editor (pi: setWidget's two
	// overloads, merged into one `content any` parameter — Go has no
	// overloads). Accepted values: nil removes the widget (pi: undefined);
	// []string renders text lines (truncated at MaxWidgetLines);
	// ExtensionWidgetFactory renders a custom component.
	SetWidget(ctx context.Context, key string, content any, options *ExtensionWidgetOptions)
	// SetFooter sets a custom footer component, or restores the built-in
	// footer when factory is nil (pi: setFooter).
	SetFooter(ctx context.Context, factory ExtensionFooterFactory)
	// SetHeader sets a custom header component (shown at startup, above
	// chat), or restores the built-in header when factory is nil (pi:
	// setHeader).
	SetHeader(ctx context.Context, factory ExtensionHeaderFactory)
	// SetTitle sets the terminal window/tab title (pi: setTitle).
	SetTitle(ctx context.Context, title string)
	// Custom shows a custom component with keyboard focus and blocks until
	// the component calls done (pi: custom<T>'s Promise — the value passed to
	// done is returned; the extension's factory closure captures its own
	// typed variable, Go interfaces cannot be generic per method). overlay
	// mode renders on top of existing content.
	Custom(ctx context.Context, factory ExtensionCustomFactory, options *ExtensionCustomOptions) any
	// PasteToEditor pastes text into the editor, triggering paste handling
	// (collapse for large content) (pi: pasteToEditor).
	PasteToEditor(ctx context.Context, text string)
	// SetEditorText sets the text in the core input editor (pi:
	// setEditorText).
	SetEditorText(ctx context.Context, text string)
	// GetEditorText returns the current text from the core input editor (pi:
	// getEditorText — getExpandedText() ?? getText()).
	GetEditorText(ctx context.Context) string
	// Editor shows a multi-line editor for text editing (pi: editor's
	// Promise<string | undefined>; blocks until submitted/cancelled, ok
	// reports whether a value was returned).
	Editor(ctx context.Context, title string, prefill string) (string, bool)
	// AddAutocompleteProvider stacks additional autocomplete behavior on top
	// of the built-in provider (pi: addAutocompleteProvider).
	AddAutocompleteProvider(ctx context.Context, factory AutocompleteProviderFactory)
	// SetEditorComponent sets a custom editor component via factory, or
	// restores the default editor when factory is nil (pi:
	// setEditorComponent).
	SetEditorComponent(ctx context.Context, factory EditorFactory)
	// GetEditorComponent returns the currently configured custom editor
	// factory, or nil when using the default editor (pi: getEditorComponent).
	GetEditorComponent(ctx context.Context) EditorFactory
	// GetAllThemes returns all available themes with their names and file
	// paths (pi: getAllThemes). Path "" = pi's undefined.
	GetAllThemes(ctx context.Context) []ExtensionThemeInfo
	// GetTheme loads a theme by name without switching to it; nil = not
	// found (pi: getTheme: Theme | undefined). The return is any because
	// core cannot import the theme package (modes/interactive/theme); the
	// value is a *theme.Theme. The theme loader half lands with T6.
	GetTheme(ctx context.Context, name string) any
	// SetTheme sets the current theme by name (pi: setTheme's
	// string overload; the Theme-instance overload has no core-representable
	// parameter type and lands with the theme loader half).
	SetTheme(ctx context.Context, themeName string) ThemeSetResult
	// GetToolsExpanded returns the current tool output expansion state (pi:
	// getToolsExpanded).
	GetToolsExpanded(ctx context.Context) bool
	// SetToolsExpanded sets the tool output expansion state (pi:
	// setToolsExpanded).
	SetToolsExpanded(ctx context.Context, expanded bool)
}

// ExtensionWidgetFactory renders a custom widget component (pi: setWidget's
// component-factory overload `(tui, theme) => Component & {dispose?}`). The
// second return is the optional dispose function (nil = none; pi's
// `dispose?()`).
type ExtensionWidgetFactory func(tui tui.TUI) (tui.Component, func())

// ExtensionFooterFactory builds a custom footer component (pi: setFooter's
// factory `(tui, theme, footerData) => Component & {dispose?}`).
type ExtensionFooterFactory func(tui tui.TUI, footerData ReadonlyFooterDataProvider) (tui.Component, func())

// ExtensionHeaderFactory builds a custom header component (pi: setHeader's
// factory `(tui, theme) => Component & {dispose?}`).
type ExtensionHeaderFactory func(tui tui.TUI) (tui.Component, func())

// ExtensionCustomFactory builds a custom component shown with keyboard focus
// (pi: custom's factory `(tui, theme, keybindings, done) => Component &
// {dispose?} | Promise<...>`; pig factories are synchronous and report the
// optional dispose via the second return).
type ExtensionCustomFactory func(tui tui.TUI, keybindings *KeybindingsManager, done func(result any)) (tui.Component, func())

// ExtensionCustomOptions configure the custom component surface (pi:
// custom's options parameter).
type ExtensionCustomOptions struct {
	// Overlay renders the component on top of existing content instead of
	// replacing the editor (pi: overlay?).
	Overlay bool
	// OverlayOptions resolves overlay positioning/sizing when Overlay is
	// set; called each time the overlay is (re)positioned. nil falls back to
	// the component's width when it exposes one (pi: overlayOptions:
	// OverlayOptions | (() => OverlayOptions) — the static form is the
	// constant closure).
	OverlayOptions func() *tui.OverlayOptions
	// OnHandle receives the overlay handle after the overlay is shown, for
	// visibility control (pi: onHandle?).
	OnHandle func(handle tui.OverlayHandle)
}

// ExtensionThemeInfo is one available theme (pi: getAllThemes' element
// {name, path | undefined}; Path "" = undefined).
type ExtensionThemeInfo struct {
	Name string
	Path string
}

// ThemeSetResult reports a theme switch attempt (pi:
// { success: boolean; error?: string }).
type ThemeSetResult struct {
	Success bool
	// Error is set when Success is false; "" = none (pi: error?).
	Error string
}

// ReadonlyFooterDataProvider is the read-only footer data surface given to
// extension footer factories (pi: ReadonlyFooterDataProvider =
// Pick<FooterDataProvider, "getGitBranch" | "getExtensionStatuses" |
// "getAvailableProviderCount" | "onBranchChange">).
type ReadonlyFooterDataProvider interface {
	GetGitBranch() *string
	GetExtensionStatuses() map[string]string
	GetAvailableProviderCount() int
	OnBranchChange(callback func()) func()
}

// AutocompleteProviderFactory wraps/replaces the editor autocomplete
// provider (pi: extensions/types.ts AutocompleteProviderFactory).
type AutocompleteProviderFactory func(provider tui.AutocompleteProvider) tui.AutocompleteProvider

// EditorFactory builds a custom editor component (pi: EditorFactory
// `(tui, theme, keybindings) => EditorComponent`).
type EditorFactory func(tui tui.TUI, editorTheme tui.EditorTheme, keybindings *KeybindingsManager) tui.EditorComponent

// ExtensionUIDialogOptions are the shared dialog options (pi:
// ExtensionUIDialogOptions).
type ExtensionUIDialogOptions struct {
	// Placeholder for input dialogs. (pig addition for the shared struct;
	// pi passes the input placeholder as input()'s second parameter, which
	// the Go Input method also carries.)
	Placeholder string
	// Timeout in milliseconds; the dialog auto-dismisses with a live
	// countdown display (pi: timeout?). 0 = none.
	Timeout int
}

// ExtensionWidgetOptions are the widget placement options (pi:
// ExtensionWidgetOptions).
type ExtensionWidgetOptions struct {
	// Placement: "above" or "below" the editor (pi: placement).
	Placement string
}

// noOpExtensionUIContext is the runner's default UI context (pi:
// noOpUIContext).
type noOpExtensionUIContext struct{}

func (noOpExtensionUIContext) Select(_ context.Context, _ string, _ []string, _ *ExtensionUIDialogOptions) (string, bool) {
	return "", false
}
func (noOpExtensionUIContext) Confirm(_ context.Context, _ string, _ string, _ *ExtensionUIDialogOptions) bool {
	return false
}
func (noOpExtensionUIContext) Input(_ context.Context, _ string, _ string, _ *ExtensionUIDialogOptions) (string, bool) {
	return "", false
}
func (noOpExtensionUIContext) Notify(_ context.Context, _ string, _ string) {}
func (noOpExtensionUIContext) OnTerminalInput(_ context.Context, _ TerminalInputHandler) func() {
	return func() {}
}
func (noOpExtensionUIContext) SetStatus(_ context.Context, _ string, _ string)                   {}
func (noOpExtensionUIContext) SetWorkingMessage(_ context.Context, _ string)                     {}
func (noOpExtensionUIContext) SetWorkingVisible(_ context.Context, _ bool)                       {}
func (noOpExtensionUIContext) SetWorkingIndicator(_ context.Context, _ *WorkingIndicatorOptions) {}
func (noOpExtensionUIContext) SetHiddenThinkingLabel(_ context.Context, _ string)                {}
func (noOpExtensionUIContext) SetWidget(_ context.Context, _ string, _ any, _ *ExtensionWidgetOptions) {
}
func (noOpExtensionUIContext) SetFooter(_ context.Context, _ ExtensionFooterFactory) {}
func (noOpExtensionUIContext) SetHeader(_ context.Context, _ ExtensionHeaderFactory) {}
func (noOpExtensionUIContext) SetTitle(_ context.Context, _ string)                  {}
func (noOpExtensionUIContext) Custom(_ context.Context, _ ExtensionCustomFactory, _ *ExtensionCustomOptions) any {
	return nil
}
func (noOpExtensionUIContext) PasteToEditor(_ context.Context, _ string) {}
func (noOpExtensionUIContext) SetEditorText(_ context.Context, _ string) {}
func (noOpExtensionUIContext) GetEditorText(_ context.Context) string    { return "" }
func (noOpExtensionUIContext) Editor(_ context.Context, _ string, _ string) (string, bool) {
	return "", false
}
func (noOpExtensionUIContext) AddAutocompleteProvider(_ context.Context, _ AutocompleteProviderFactory) {
}
func (noOpExtensionUIContext) SetEditorComponent(_ context.Context, _ EditorFactory) {}
func (noOpExtensionUIContext) GetEditorComponent(_ context.Context) EditorFactory    { return nil }
func (noOpExtensionUIContext) GetAllThemes(_ context.Context) []ExtensionThemeInfo   { return nil }
func (noOpExtensionUIContext) GetTheme(_ context.Context, _ string) any              { return nil }
func (noOpExtensionUIContext) SetTheme(_ context.Context, _ string) ThemeSetResult {
	return ThemeSetResult{}
}
func (noOpExtensionUIContext) GetToolsExpanded(_ context.Context) bool    { return false }
func (noOpExtensionUIContext) SetToolsExpanded(_ context.Context, _ bool) {}

// ExtensionError reports an extension handler failure (pi: ExtensionError).
type ExtensionError struct {
	ExtensionPath string
	Event         string
	Error         string
	// Stack is the JS stack when the panic carried one; "" = none (pi:
	// stack?). Go recover() has no portable stack string, so it stays empty.
	Stack string
}

// ExtensionErrorListener consumes extension errors (pi:
// ExtensionErrorListener).
type ExtensionErrorListener func(error ExtensionError)

// ShutdownHandler requests graceful shutdown (pi: ShutdownHandler).
type ShutdownHandler func()

// ProjectTrustContext is the context for project-trust flows (pi:
// ProjectTrustContext). UI is restricted to the four dialog members
// (Pick<ExtensionUIContext, ...>); pig models the pick as a separate
// interface over the same implementation.
type ProjectTrustContext struct {
	Cwd   string
	Mode  ExtensionMode
	HasUI bool
	UI    ProjectTrustUI
}

// ProjectTrustUI is the dialog subset of ExtensionUIContext (pi:
// Pick<ExtensionUIContext, "select" | "confirm" | "input" | "notify">).
type ProjectTrustUI interface {
	Select(ctx context.Context, title string, options []string, opts *ExtensionUIDialogOptions) (string, bool)
	Confirm(ctx context.Context, title string, message string, opts *ExtensionUIDialogOptions) bool
	Input(ctx context.Context, title string, placeholder string, opts *ExtensionUIDialogOptions) (string, bool)
	Notify(ctx context.Context, message string, level string)
}

// ExtensionContext is the context passed to extension event handlers (pi:
// ExtensionContext; see header for the lazy-getter → struct deviation).
type ExtensionContext struct {
	// UI is the UI surface for user interaction (pi: ui).
	UI ExtensionUIContext
	// Mode is the current run mode; use ExtensionModeTUI to guard
	// terminal-only UI (pi: mode).
	Mode ExtensionMode
	// HasUI reports whether dialog-capable UI is available (true in TUI and
	// RPC modes) (pi: hasUI).
	HasUI bool
	// Cwd is the current working directory (pi: cwd).
	Cwd string
	// SessionManager is the session manager (read-only) (pi:
	// sessionManager).
	SessionManager ReadonlySessionManager
	// ModelRegistry resolves API keys (pi: modelRegistry).
	ModelRegistry *ModelRegistry
	// Model is the current model; nil = none (pi: model).
	Model *types.Model
	// ScopedModels are the models scoped to this session (pi:
	// scopedModels).
	ScopedModels []ScopedModel
	// ThinkingLevel is the current thinking level; "" = unset (pi:
	// thinkingLevel?).
	ThinkingLevel agent.ThinkingLevel

	// IsIdle reports whether the agent is idle (not streaming) (pi: isIdle).
	IsIdle func() bool
	// IsProjectTrusted reports whether project-local trust is active (pi:
	// isProjectTrusted).
	IsProjectTrusted func() bool
	// Signal returns the current run context, or nil when the agent is not
	// streaming (pi: signal: AbortSignal | undefined).
	Signal func() context.Context
	// Abort aborts the current agent operation (pi: abort).
	Abort func()
	// HasPendingMessages reports whether queued messages are waiting (pi:
	// hasPendingMessages).
	HasPendingMessages func() bool
	// Shutdown gracefully shuts pi down (pi: shutdown).
	Shutdown func()
	// GetContextUsage returns the context usage for the active model (pi:
	// getContextUsage).
	GetContextUsage func() *ContextUsage
	// Compact triggers compaction without awaiting completion (pi: compact).
	Compact func(options *CompactOptions)
	// GetSystemPrompt returns the current effective system prompt (pi:
	// getSystemPrompt).
	GetSystemPrompt func() string
	// SendMessage sends a custom message to the session (pi: sendMessage;
	// bound by bindCore, panics when the runtime is not initialized — pi's
	// throwing stub).
	SendMessage func(ctx context.Context, message *CustomMessagePayload, options *SendMessageOptions)
	// SendUserMessage sends a user message to the agent (pi:
	// sendUserMessage; bound by bindCore, panics when the runtime is not
	// initialized — pi's throwing stub).
	SendUserMessage func(ctx context.Context, content string, options *SendUserMessageOptions)
}

// ExtensionCommandContext is the extended context for command handlers (pi:
// ExtensionCommandContext).
type ExtensionCommandContext struct {
	ExtensionContext
	// GetSystemPromptOptions returns the base system-prompt construction
	// options (pi: getSystemPromptOptions).
	GetSystemPromptOptions func() BuildSystemPromptOptions
	// WaitForIdle waits for the agent to finish streaming (pi: waitForIdle).
	WaitForIdle func(ctx context.Context) error
	// NewSession starts a new session, optionally with initialization (pi:
	// newSession).
	NewSession func(ctx context.Context, options *NewSessionCommandOptions) (cancelled bool, err error)
	// Fork forks from a specific entry, creating a new session file (pi:
	// fork).
	Fork func(ctx context.Context, entryID string, options *ForkCommandOptions) (cancelled bool, err error)
	// NavigateTree navigates to a different point in the session tree (pi:
	// navigateTree).
	NavigateTree func(ctx context.Context, targetID string, options *NavigateTreeCommandOptions) (cancelled bool, err error)
	// SwitchSession switches to a different session file (pi:
	// switchSession).
	SwitchSession func(ctx context.Context, sessionPath string, options *SwitchSessionCommandOptions) (cancelled bool, err error)
	// Reload reloads extensions, skills, prompts, themes, and context files
	// (pi: reload).
	Reload func(ctx context.Context) error
}

// ReplacedSessionContext is the fresh command-capable context bound to the
// replacement session after a session switch, passed to withSession
// callbacks (pi: ReplacedSessionContext extends ExtensionCommandContext with
// sendMessage/sendUserMessage overrides; pig re-points the two fields).
type ReplacedSessionContext struct {
	ExtensionCommandContext
	// SendMessage sends a custom message via the replacement session (pi:
	// sendMessage).
	SendMessage func(ctx context.Context, message *CustomMessagePayload, options *SendMessageOptions)
	// SendUserMessage sends a user message via the replacement session (pi:
	// sendUserMessage).
	SendUserMessage func(ctx context.Context, content string, options *SendUserMessageOptions)
}

// ExtensionContextActions are the context action implementations for
// ExtensionContext (pi: ExtensionContextActions). Required by all modes.
type ExtensionContextActions struct {
	GetModel           func() *types.Model
	GetScopedModels    func() []ScopedModel
	IsIdle             func() bool
	IsProjectTrusted   func() bool
	GetSignal          func() context.Context
	Abort              func()
	HasPendingMessages func() bool
	Shutdown           func()
	GetContextUsage    func() *ContextUsage
	Compact            func(options *CompactOptions)
	GetSystemPrompt    func() string
	// GetSystemPromptOptions is optional in pi; nil = unset (pi:
	// getSystemPromptOptions?).
	GetSystemPromptOptions func() BuildSystemPromptOptions
}

// ExtensionCommandContextActions are the actions for
// ExtensionCommandContext; only needed for interactive mode where extension
// commands are invokable (pi: ExtensionCommandContextActions).
type ExtensionCommandContextActions struct {
	WaitForIdle   func(ctx context.Context) error
	NewSession    func(ctx context.Context, options *NewSessionCommandOptions) (cancelled bool, err error)
	Fork          func(ctx context.Context, entryID string, options *ForkCommandOptions) (cancelled bool, err error)
	NavigateTree  func(ctx context.Context, targetID string, options *NavigateTreeCommandOptions) (cancelled bool, err error)
	SwitchSession func(ctx context.Context, sessionPath string, options *SwitchSessionCommandOptions) (cancelled bool, err error)
	Reload        func(ctx context.Context) error
}

// NewSessionCommandOptions are the newSession options (pi: newSession's options
// parameter).
type NewSessionCommandOptions struct {
	// ParentSession points at the parent session (pi: parentSession?).
	ParentSession string
	// Setup runs with the new session manager before withSession (pi:
	// setup?).
	Setup func(ctx context.Context, sessionManager *SessionManager) error
	// WithSession receives the replacement session context (pi:
	// withSession?).
	WithSession func(ctx context.Context, sessionCtx *ReplacedSessionContext) error
}

// ForkCommandOptions are the fork options (pi: fork's options parameter).
type ForkCommandOptions struct {
	// Position: "before" or "at" (pi: position?); "" = pi's undefined.
	Position string
	// WithSession receives the replacement session context (pi:
	// withSession?).
	WithSession func(ctx context.Context, sessionCtx *ReplacedSessionContext) error
}

// NavigateTreeCommandOptions are the navigateTree options (pi: navigateTree's
// options parameter).
type NavigateTreeCommandOptions struct {
	Summarize           bool
	CustomInstructions  string
	ReplaceInstructions bool
	Label               string
	WithSession         func(ctx context.Context, sessionCtx *ReplacedSessionContext) error
}

// SwitchSessionCommandOptions are the switchSession options (pi: switchSession's
// options parameter).
type SwitchSessionCommandOptions struct {
	WithSession func(ctx context.Context, sessionCtx *ReplacedSessionContext) error
}

// ExtensionActions are the action implementations for the pi.* API methods,
// provided to the runner via bindCore (pi: ExtensionActions).
type ExtensionActions struct {
	SendMessage      func(ctx context.Context, message *CustomMessagePayload, options *SendMessageOptions)
	SendUserMessage  func(ctx context.Context, content string, options *SendUserMessageOptions)
	AppendEntry      func(customType string, data any) string
	SetSessionName   func(name string)
	GetSessionName   func() (string, bool)
	SetLabel         func(entryID string, label string)
	GetActiveTools   func() []string
	GetAllTools      func() []ToolInfo
	SetActiveTools   func(toolNames []string)
	RefreshTools     func()
	GetCommands      func() []SlashCommandInfo
	SetModel         func(ctx context.Context, model *types.Model) bool
	GetThinkingLevel func() agent.ThinkingLevel
	SetThinkingLevel func(level agent.ThinkingLevel)
}

// ExtensionProviderActions register or unregister providers (pi:
// bindCore's optional providerActions parameter).
type ExtensionProviderActions struct {
	RegisterProvider       func(name string, config ProviderConfig)
	RegisterNativeProvider func(provider any)
	UnregisterProvider     func(name string)
}

// CustomMessagePayload is the message payload of sendCustomMessage/sendMessage
// (pi: Pick<CustomMessage, "customType" | "content" | "display" | "details">).
type CustomMessagePayload struct {
	CustomType string
	// Content is a string or a []types.Content of text/image parts (pi:
	// `string | (TextContent | ImageContent)[]`).
	Content any
	Display bool
	Details any
}

// SendMessageOptions are the sendMessage delivery options (pi:
// sendMessage's options parameter).
type SendMessageOptions struct {
	// TriggerTurn starts a new LLM turn when not streaming (pi:
	// triggerTurn?).
	TriggerTurn *bool
	// DeliverAs: "steer", "followUp", or "nextTurn" (pi: deliverAs?); "" =
	// pi's undefined.
	DeliverAs string
}

// SendUserMessageOptions are the sendUserMessage delivery options (pi:
// sendUserMessage's options parameter).
type SendUserMessageOptions struct {
	// DeliverAs: "steer" or "followUp" (pi: deliverAs?); "" = pi's
	// undefined.
	DeliverAs string
}

// ToolInfo describes one configured tool (pi: ToolInfo).
type ToolInfo struct {
	Name        string
	Description string
	// Parameters is the JSON Schema of the tool input.
	Parameters any
	// PromptGuidelines are the tool's system-prompt guidelines.
	PromptGuidelines []string
	SourceInfo       SourceInfo
}

// ProviderConfig is the provider registration config (pi: ProviderConfig,
// defined in extensions/types.ts; the full shape arrives with the
// extensions/sidecar batch).
type ProviderConfig struct {
	// Name is the provider id being registered.
	Name string
	// Type is the registration kind; "" = pi's undefined (pi: type?).
	Type string
	// BaseURL is the endpoint base URL (pi: baseUrl).
	BaseURL string
	// APIKey, when set, is resolved per request (pi: apiKey).
	APIKey string
	// Models are the provider's model definitions (pi: models).
	Models []ProviderModelConfig
}

// ProviderModelConfig is one model of a registered provider (pi:
// ProviderModelConfig).
type ProviderModelConfig struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Reasoning     bool   `json:"reasoning"`
	ContextWindow int64  `json:"contextWindow"`
	MaxTokens     int64  `json:"maxTokens"`
	Cost          struct {
		Input      float64 `json:"input"`
		Output     float64 `json:"output"`
		CacheRead  float64 `json:"cacheRead"`
		CacheWrite float64 `json:"cacheWrite"`
	} `json:"cost"`
	// InputLimits is the provider input limits and cache-safe image
	// preprocessing metadata (pi: inputLimits, v0.87.0 #9631). pig's
	// registration surface stays slim (input/thinkingLevelMap remain
	// unported); this field rides through to the model record.
	InputLimits *types.ModelInputLimits `json:"inputLimits,omitempty"`
}

// ============================================================================
// Events
// ============================================================================

// ExtensionEvent is the event union passed to runner.Emit (pi:
// ExtensionEvent; discriminated union → interface + concrete types).
type ExtensionEvent interface {
	// EventType returns the JSON "type" discriminant.
	EventType() string
}

// ResourcesDiscoverEvent asks extensions for extra resource paths (pi:
// ResourcesDiscoverEvent).
type ResourcesDiscoverEvent struct {
	Type string `json:"type"`
	Cwd  string `json:"cwd"`
	// Reason: "startup" or "reload".
	Reason string `json:"reason"`
}

func (e *ResourcesDiscoverEvent) EventType() string { return e.Type }

// ResourcesDiscoverResult carries the paths an extension contributes (pi:
// ResourcesDiscoverResult).
type ResourcesDiscoverResult struct {
	SkillPaths  []string `json:"skillPaths,omitempty"`
	PromptPaths []string `json:"promptPaths,omitempty"`
	ThemePaths  []string `json:"themePaths,omitempty"`
}

// SessionStartEvent fires when a session is started, loaded, or reloaded
// (pi: SessionStartEvent).
type SessionStartEvent struct {
	Type string `json:"type"`
	// Reason: "startup" | "reload" | "new" | "resume" | "fork".
	Reason string `json:"reason"`
	// PreviousSessionFile is present for "new", "resume", and "fork"; "" =
	// absent (pi: previousSessionFile?).
	PreviousSessionFile string `json:"previousSessionFile,omitempty"`
}

func (e *SessionStartEvent) EventType() string { return e.Type }

// SessionInfoChangedEvent fires when the session metadata changes (pi:
// SessionInfoChangedEvent). nil Name = cleared (pi: name: string |
// undefined).
type SessionInfoChangedEvent struct {
	Type string  `json:"type"`
	Name *string `json:"name"`
}

func (e *SessionInfoChangedEvent) EventType() string { return e.Type }

// SessionBeforeCompactEvent fires before compaction starts (pi:
// SessionBeforeCompactEvent). pi's signal member is dropped; cancellation
// travels as the Emit context.
type SessionBeforeCompactEvent struct {
	Type string `json:"type"`
	// Preparation is the compaction preparation.
	Preparation *CompactionPreparation `json:"preparation"`
	// BranchEntries are the entries on the current branch.
	BranchEntries []SessionEntry `json:"branchEntries"`
	// CustomInstructions are the manual /compact instructions; "" = none
	// (pi: customInstructions?).
	CustomInstructions string `json:"customInstructions,omitempty"`
	// Reason: "manual" | "threshold" | "overflow".
	Reason string `json:"reason"`
	// WillRetry is true when the aborted turn is retried after this
	// compaction (overflow recovery).
	WillRetry bool `json:"willRetry"`
}

func (e *SessionBeforeCompactEvent) EventType() string { return e.Type }

// SessionBeforeCompactResult is the handler result (pi:
// SessionBeforeCompactResult).
type SessionBeforeCompactResult struct {
	Cancel bool `json:"cancel,omitempty"`
	// Compaction replaces the default summary generator when set.
	Compaction *CompactionResult `json:"compaction,omitempty"`
}

// SessionCompactEvent fires after context compaction succeeds (pi:
// SessionCompactEvent).
type SessionCompactEvent struct {
	Type string `json:"type"`
	// CompactionEntry is the saved compaction entry.
	CompactionEntry *CompactionEntry `json:"compactionEntry"`
	FromExtension   bool             `json:"fromExtension"`
	// Reason: "manual" | "threshold" | "overflow".
	Reason string `json:"reason"`
	// WillRetry is true when the aborted turn is retried after this
	// compaction (overflow recovery).
	WillRetry bool `json:"willRetry"`
}

func (e *SessionCompactEvent) EventType() string { return e.Type }

// SessionCompactFailedEvent fires after context compaction fails or is
// aborted (pi: SessionCompactFailedEvent).
type SessionCompactFailedEvent struct {
	Type string `json:"type"`
	// Reason: "manual" | "threshold" | "overflow".
	Reason string `json:"reason"`
	// ErrorMessage is the error text for non-abort failures; "" = none (pi:
	// errorMessage?).
	ErrorMessage string `json:"errorMessage,omitempty"`
	// Aborted is true when compaction was cancelled or aborted.
	Aborted bool `json:"aborted"`
	// WillRetry is true when the aborted turn would have been retried after
	// this compaction (overflow recovery).
	WillRetry bool `json:"willRetry"`
	// FromExtension is true when the failing compaction content came from a
	// session_before_compact handler.
	FromExtension bool `json:"fromExtension"`
}

func (e *SessionCompactFailedEvent) EventType() string { return e.Type }

// SessionShutdownEvent fires before an extension runtime is torn down (pi:
// SessionShutdownEvent).
type SessionShutdownEvent struct {
	Type string `json:"type"`
	// Reason: "quit" | "reload" | "new" | "resume" | "fork".
	Reason string `json:"reason"`
	// TargetSessionFile is the destination file for session replacement; ""
	// = none (pi: targetSessionFile?).
	TargetSessionFile string `json:"targetSessionFile,omitempty"`
}

func (e *SessionShutdownEvent) EventType() string { return e.Type }

// TreePreparation is the preparation data for tree navigation (pi:
// TreePreparation).
type TreePreparation struct {
	TargetID string `json:"targetId"`
	// OldLeafID is the position navigating from; nil = session root (pi:
	// oldLeafId: string | null).
	OldLeafID *string `json:"oldLeafId"`
	// CommonAncestorID is the deepest node shared by both paths; nil = none
	// (pi: commonAncestorId: string | null).
	CommonAncestorID *string `json:"commonAncestorId"`
	// EntriesToSummarize are the abandoned entries, chronological order.
	EntriesToSummarize []SessionEntry `json:"entriesToSummarize"`
	UserWantsSummary   bool           `json:"userWantsSummary"`
	// CustomInstructions are the summarization instructions; "" = none (pi:
	// customInstructions?).
	CustomInstructions string `json:"customInstructions,omitempty"`
	// ReplaceInstructions replaces the default prompt with
	// customInstructions (pi: replaceInstructions?).
	ReplaceInstructions bool `json:"replaceInstructions,omitempty"`
	// Label is the label to attach to the branch summary entry; "" = none
	// (pi: label?).
	Label string `json:"label,omitempty"`
}

// SessionBeforeTreeEvent fires before navigating in the session tree (pi:
// SessionBeforeTreeEvent; pi's signal member is dropped).
type SessionBeforeTreeEvent struct {
	Type        string           `json:"type"`
	Preparation *TreePreparation `json:"preparation"`
}

func (e *SessionBeforeTreeEvent) EventType() string { return e.Type }

// SessionBeforeTreeResult is the handler result (pi: SessionBeforeTreeResult).
type SessionBeforeTreeResult struct {
	Cancel bool `json:"cancel,omitempty"`
	// Summary replaces the default summarizer when set (pi: summary?).
	Summary *SessionBeforeTreeSummary `json:"summary,omitempty"`
	// CustomInstructions override the summarization instructions; nil =
	// unchanged (pi: customInstructions?).
	CustomInstructions *string `json:"customInstructions,omitempty"`
	// ReplaceInstructions overrides whether customInstructions replace the
	// default prompt; nil = unchanged (pi: replaceInstructions?).
	ReplaceInstructions *bool `json:"replaceInstructions,omitempty"`
	// Label overrides the branch summary label; nil = unchanged (pi:
	// label?).
	Label *string `json:"label,omitempty"`
}

// SessionBeforeTreeSummary is the custom summary payload (pi:
// SessionBeforeTreeResult's summary object).
type SessionBeforeTreeSummary struct {
	Summary string       `json:"summary"`
	Details any          `json:"details,omitempty"`
	Usage   *types.Usage `json:"usage,omitempty"`
}

// SessionTreeEvent fires after navigating in the session tree (pi:
// SessionTreeEvent).
type SessionTreeEvent struct {
	Type string `json:"type"`
	// NewLeafID is the position navigated to; nil = root (pi: newLeafId:
	// string | null).
	NewLeafID *string `json:"newLeafId"`
	// OldLeafID is the position navigating from; nil = root (pi: oldLeafId:
	// string | null).
	OldLeafID *string `json:"oldLeafId"`
	// SummaryEntry is the generated branch summary entry, if any (pi:
	// summaryEntry?).
	SummaryEntry *BranchSummaryEntry `json:"summaryEntry,omitempty"`
	// FromExtension is set only when a summary was generated (pi:
	// fromExtension?: boolean); nil = absent.
	FromExtension *bool `json:"fromExtension,omitempty"`
}

func (e *SessionTreeEvent) EventType() string { return e.Type }

// AgentStartEvent fires when an agent loop starts (pi: AgentStartEvent).
type AgentStartEvent struct {
	Type string `json:"type"`
}

func (e *AgentStartEvent) EventType() string { return e.Type }

// AgentEndEvent fires when an agent loop ends (pi: AgentEndEvent).
type AgentEndEvent struct {
	Type     string               `json:"type"`
	Messages []agent.AgentMessage `json:"messages"`
}

func (e *AgentEndEvent) EventType() string { return e.Type }

// AgentActivityOutcome classifies the outcome of an agent run (pi:
// AgentActivityOutcome, v0.87.0).
type AgentActivityOutcome string

const (
	AgentActivityCompleted AgentActivityOutcome = "completed"
	AgentActivityAborted   AgentActivityOutcome = "aborted"
	AgentActivityError     AgentActivityOutcome = "error"
)

// CustomEntryDraft proposes a custom entry (pi: CustomEntryDraft, v0.87.0).
type CustomEntryDraft struct {
	Type       string `json:"type"`
	CustomType string `json:"customType"`
	// Data is the entry payload; nil = pi's omitted optional key.
	Data any `json:"data,omitempty"`
}

func (e *CustomEntryDraft) draftType() string { return "custom" }

// CustomMessageEntryDraft proposes a custom message entry (pi:
// CustomMessageEntryDraft, v0.87.0).
type CustomMessageEntryDraft struct {
	Type       string `json:"type"`
	CustomType string `json:"customType"`
	// Content is a string or a (TextContent | ImageContent)[] (pi union).
	Content any  `json:"content"`
	Display bool `json:"display"`
	// Details is the entry payload; nil = pi's omitted optional key.
	Details any `json:"details,omitempty"`
}

func (e *CustomMessageEntryDraft) draftType() string { return "custom_message" }

// ContextEditEntryDraft proposes a context edit (pi: ContextEditEntryDraft,
// v0.87.0).
type ContextEditEntryDraft struct {
	Type     string `json:"type"`
	TargetID string `json:"targetId"`
	// Replacement nil (pi: null) omits the target from model context; a value
	// replaces only its content.
	Replacement *ContextEditReplacement `json:"replacement"`
}

func (e *ContextEditEntryDraft) draftType() string { return "context_edit" }

// CompactionEntryDraft proposes a compaction (pi: CompactionEntryDraft,
// v0.87.0).
type CompactionEntryDraft struct {
	Type    string `json:"type"`
	Summary string `json:"summary"`
	// FirstKeptEntryID nil creates a self-retaining compaction that keeps no
	// preceding entries (pi: firstKeptEntryId: string | null).
	FirstKeptEntryID *string      `json:"firstKeptEntryId"`
	Details          any          `json:"details,omitempty"`
	Usage            *types.Usage `json:"usage,omitempty"`
}

func (e *CompactionEntryDraft) draftType() string { return "compaction" }

// SessionBoundaryDraft is one proposed session entry (pi:
// SessionBoundaryDraft = CustomEntryDraft | CustomMessageEntryDraft |
// ContextEditEntryDraft | CompactionEntryDraft, v0.87.0).
type SessionBoundaryDraft interface{ draftType() string }

// BoundaryContextPreview is the would-be model context for the proposed
// drafts (pi: BoundaryContextPreview, v0.87.0).
type BoundaryContextPreview struct {
	ContextEntries  []ProjectedSessionEntry `json:"contextEntries"`
	ContextMessages []agent.AgentMessage    `json:"contextMessages"`
	LLMMessages     []types.Message         `json:"llmMessages"`
	PendingMessages []agent.AgentMessage    `json:"pendingMessages"`
	CanContinue     bool                    `json:"canContinue"`
}

// BoundaryState is the shared state carried by turn_end and
// agent_before_settle (pi: BoundaryState, v0.87.0).
type BoundaryState struct {
	Entries  []SessionBoundaryDraft `json:"entries"`
	Continue bool                   `json:"continue"`
	Context  BoundaryContextPreview `json:"context"`
}

// BoundaryResult is the handler result for boundary events (pi:
// BoundaryResult, v0.87.0). nil Entries keeps the proposed draft list; nil
// Continue leaves the continuation unspecified.
type BoundaryResult struct {
	Entries  []SessionBoundaryDraft `json:"entries,omitempty"`
	Continue *bool                  `json:"continue,omitempty"`
}

// AgentBeforeSettleEvent fires before final settlement; handlers may append
// entries and request one continuation (pi: AgentBeforeSettleEvent,
// v0.87.0).
type AgentBeforeSettleEvent struct {
	Type string `json:"type"`
	// Outcome classifies the activity being settled.
	Outcome AgentActivityOutcome `json:"outcome"`
	BoundaryState
}

func (e *AgentBeforeSettleEvent) EventType() string { return e.Type }

// AgentSettledEvent fires after an agent run has fully settled (pi:
// AgentSettledEvent).
type AgentSettledEvent struct {
	Type string `json:"type"`
	// Aborted reports whether the run ended because it was aborted, for
	// example with Escape (pi: AgentSettledEvent.aborted, S17 503c60552).
	Aborted bool `json:"aborted"`
}

func (e *AgentSettledEvent) EventType() string { return e.Type }

// UIPromptKind names the blocking user-facing extension UI prompt (pi:
// UIPromptKind, v0.85.1).
type UIPromptKind string

const (
	UIPromptKindSelect  UIPromptKind = "select"
	UIPromptKindConfirm UIPromptKind = "confirm"
	UIPromptKindInput   UIPromptKind = "input"
	UIPromptKindEditor  UIPromptKind = "editor"
	UIPromptKindCustom  UIPromptKind = "custom"
)

// UIPromptStartEvent fires when Pi starts waiting on a blocking user-facing
// extension UI prompt (pi: UIPromptStartEvent, v0.85.1).
type UIPromptStartEvent struct {
	Type   string       `json:"type"`
	Reason string       `json:"reason"`
	Kind   UIPromptKind `json:"kind"`
	// Title is the dialog title; nil = pi's omitted optional key.
	Title *string `json:"title,omitempty"`
}

func (e *UIPromptStartEvent) EventType() string { return e.Type }

// UIPromptEndEvent fires when Pi is no longer waiting on a blocking
// user-facing extension UI prompt (pi: UIPromptEndEvent, v0.85.1).
type UIPromptEndEvent struct {
	Type   string       `json:"type"`
	Reason string       `json:"reason"`
	Kind   UIPromptKind `json:"kind"`
	Title  *string      `json:"title,omitempty"`
}

func (e *UIPromptEndEvent) EventType() string { return e.Type }

// TurnStartEvent fires at the start of each turn (pi: TurnStartEvent).
type TurnStartEvent struct {
	Type      string `json:"type"`
	TurnIndex int    `json:"turnIndex"`
	Timestamp int64  `json:"timestamp"`
}

func (e *TurnStartEvent) EventType() string { return e.Type }

// TurnEndEvent fires at the end of each turn (pi: TurnEndEvent, v0.87.0
// boundary shape). Handlers may append entries via a BoundaryResult and
// request one continuation.
type TurnEndEvent struct {
	Type      string             `json:"type"`
	TurnIndex int                `json:"turnIndex"`
	Message   agent.AgentMessage `json:"message"`
	// ToolResults are the turn's tool result messages (empty when the turn
	// had no tool calls).
	ToolResults []types.ToolResultMessage `json:"toolResults"`
	// MessageEntryID is the persisted session entry of the assistant message.
	MessageEntryID string `json:"messageEntryId"`
	// ToolResultEntryIDs are the persisted session entries of ToolResults,
	// in order (pi flatMaps unresolved IDs out).
	ToolResultEntryIDs []string `json:"toolResultEntryIds"`
	// Outcome classifies the completed turn.
	Outcome AgentActivityOutcome `json:"outcome"`
	BoundaryState
}

func (e *TurnEndEvent) EventType() string { return e.Type }

// MessageStartEvent fires when a message starts (pi: MessageStartEvent).
type MessageStartEvent struct {
	Type    string             `json:"type"`
	Message agent.AgentMessage `json:"message"`
}

func (e *MessageStartEvent) EventType() string { return e.Type }

// MessageUpdateEvent fires during assistant streaming (pi:
// MessageUpdateEvent).
type MessageUpdateEvent struct {
	Type                  string                      `json:"type"`
	Message               agent.AgentMessage          `json:"message"`
	AssistantMessageEvent types.AssistantMessageEvent `json:"assistantMessageEvent"`
}

func (e *MessageUpdateEvent) EventType() string { return e.Type }

// MessageEndEvent fires when a message ends (pi: MessageEndEvent).
type MessageEndEvent struct {
	Type    string             `json:"type"`
	Message agent.AgentMessage `json:"message"`
}

func (e *MessageEndEvent) EventType() string { return e.Type }

// MessageEndEventResult is the message_end handler result (pi:
// MessageEndEventResult). nil Message = no replacement.
type MessageEndEventResult struct {
	// Message replaces the finalized message; must keep the original role.
	Message agent.AgentMessage `json:"message,omitempty"`
}

// ToolExecutionStartEvent fires when a tool starts executing (pi:
// ToolExecutionStartEvent).
type ToolExecutionStartEvent struct {
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Args       any    `json:"args"`
}

func (e *ToolExecutionStartEvent) EventType() string { return e.Type }

// ToolExecutionUpdateEvent fires during tool execution (pi:
// ToolExecutionUpdateEvent).
type ToolExecutionUpdateEvent struct {
	Type          string `json:"type"`
	ToolCallID    string `json:"toolCallId"`
	ToolName      string `json:"toolName"`
	Args          any    `json:"args"`
	PartialResult any    `json:"partialResult"`
}

func (e *ToolExecutionUpdateEvent) EventType() string { return e.Type }

// ToolExecutionEndEvent fires when a tool finishes executing (pi:
// ToolExecutionEndEvent).
type ToolExecutionEndEvent struct {
	Type       string                `json:"type"`
	ToolCallID string                `json:"toolCallId"`
	ToolName   string                `json:"toolName"`
	Result     agent.AgentToolResult `json:"result"`
	IsError    bool                  `json:"isError"`
	// DurationMs is how long Execute took, measured with a monotonic clock;
	// absent when the tool did not run (pi: durationMs, #10549).
	DurationMs *int64 `json:"durationMs,omitempty"`
}

func (e *ToolExecutionEndEvent) EventType() string { return e.Type }

// ModelSelectSource is how a model got selected (pi: ModelSelectSource).
type ModelSelectSource string

const (
	ModelSelectSourceSet     ModelSelectSource = "set"
	ModelSelectSourceCycle   ModelSelectSource = "cycle"
	ModelSelectSourceRestore ModelSelectSource = "restore"
)

// ModelSelectEvent fires when a new model is selected (pi:
// ModelSelectEvent).
type ModelSelectEvent struct {
	Type          string            `json:"type"`
	Model         *types.Model      `json:"model"`
	PreviousModel *types.Model      `json:"previousModel"`
	Source        ModelSelectSource `json:"source"`
}

func (e *ModelSelectEvent) EventType() string { return e.Type }

// ThinkingLevelSelectEvent fires when a new thinking level is selected (pi:
// ThinkingLevelSelectEvent).
type ThinkingLevelSelectEvent struct {
	Type          string              `json:"type"`
	Level         agent.ThinkingLevel `json:"level"`
	PreviousLevel agent.ThinkingLevel `json:"previousLevel"`
}

func (e *ThinkingLevelSelectEvent) EventType() string { return e.Type }

// BeforeAgentStartEvent fires before an agent run starts, from a submitted
// prompt, RPC prompt, sendUserMessage, or an idle custom message with
// triggerTurn (pi: BeforeAgentStartEvent; v1.2.0 splits it into a union on
// messageType — pig keeps the single-struct discriminator pattern).
// MessageType "user" carries Prompt/Images; "custom" carries Message.
type BeforeAgentStartEvent struct {
	Type        string `json:"type"`
	MessageType string `json:"messageType"`
	// Prompt is the raw user prompt text after expansion (messageType "user").
	Prompt string               `json:"prompt,omitempty"`
	Images []types.ImageContent `json:"images,omitempty"`
	// Message is the custom message that starts the run (messageType "custom").
	Message *harness.CustomMessage `json:"message,omitempty"`
	// SystemPrompt is the current system prompt, rendered from
	// systemPromptOptions and earlier handler changes (pi v0.86.0: a
	// getter; pig renders it once per handler dispatch).
	SystemPrompt string `json:"systemPrompt"`
	// SystemPromptOptions is mutable; later handlers observe mutations made
	// by earlier handlers (pi v0.86.0). Pointer so handler field assignments
	// (e.g. replacing Sections) are visible to the runner's result — pi
	// passes the same options object by reference.
	SystemPromptOptions *NormalizedBuildSystemPromptOptions `json:"systemPromptOptions"`
}

func (e *BeforeAgentStartEvent) EventType() string { return e.Type }

// BeforeAgentStartEventResult is one handler's result (pi:
// BeforeAgentStartEventResult).
type BeforeAgentStartEventResult struct {
	// Message is a custom message to inject alongside the prompt (pi:
	// message?).
	Message *CustomMessagePayload `json:"message,omitempty"`
	// SystemPrompt replaces the system prompt for this turn; nil = unchanged
	// (pi: systemPrompt?).
	SystemPrompt *string `json:"systemPrompt,omitempty"`
}

// BeforeAgentStartCombinedResult merges all handler results (pi:
// emitBeforeAgentStart's inline return shape; v0.86.0 always returns the
// normalized options).
type BeforeAgentStartCombinedResult struct {
	// Messages are the custom messages to inject (pi: messages).
	Messages []CustomMessagePayload `json:"messages,omitempty"`
	// SystemPromptOptions is the normalized, handler-mutated prompt options
	// (pi: systemPromptOptions; the ForceSystemPrompt field carries a
	// handler's systemPrompt override).
	SystemPromptOptions NormalizedBuildSystemPromptOptions `json:"systemPromptOptions"`
}

// UserBashEvent fires before the shell executes a `!` command (pi:
// UserBashEvent).
type UserBashEvent struct {
	Type string `json:"type"`
	// Command is the command to execute.
	Command string `json:"command"`
	// ExcludeFromContext is true for the !! prefix (excluded from LLM
	// context).
	ExcludeFromContext bool `json:"excludeFromContext"`
	// Cwd is the current working directory.
	Cwd string `json:"cwd"`
}

func (e *UserBashEvent) EventType() string { return e.Type }

// UserBashEventResult intercepts a `!` command (pi: UserBashEventResult —
// the `{ operations } | { result }` union; exactly one half is set).
type UserBashEventResult struct {
	// Operations customizes execution (pi: { operations }).
	Operations *tools.BashOperations
	// Result fully replaces execution (pi: { result }).
	Result *BashResult
}

// InputEvent fires when the user submits input (pi: InputEvent).
type InputEvent struct {
	Type   string               `json:"type"`
	Text   string               `json:"text"`
	Images []types.ImageContent `json:"images,omitempty"`
	Source InputSource          `json:"source"`
	// StreamingBehavior: "steer" | "followUp"; "" = idle (pi:
	// streamingBehavior?).
	StreamingBehavior string `json:"streamingBehavior,omitempty"`
}

func (e *InputEvent) EventType() string { return e.Type }

// InputEventResult is the input handler result (pi: InputEventResult union →
// struct + Action discriminant).
type InputEventResult struct {
	// Action: "continue" | "transform" | "handled".
	Action string `json:"action"`
	// Text is the transformed text (action "transform").
	Text string `json:"text,omitempty"`
	// Images replaces the attachments (action "transform"); nil = keep (pi:
	// images?).
	Images []types.ImageContent `json:"images,omitempty"`
}

// ToolCallEvent fires before a tool executes (pi: ToolCallEvent).
type ToolCallEvent struct {
	Type       string `json:"type"`
	ToolName   string `json:"toolName"`
	ToolCallID string `json:"toolCallId"`
	Input      any    `json:"input"`
}

func (e *ToolCallEvent) EventType() string { return e.Type }

// ToolCallEventResult is the tool_call handler result (pi:
// ToolCallEventResult).
type ToolCallEventResult struct {
	// Block blocks tool execution (pi: block?).
	Block bool `json:"block,omitempty"`
	// Reason explains the block (pi: reason?).
	Reason string `json:"reason,omitempty"`
	// Terminate hints that the agent should stop after the current tool
	// batch when this call is blocked (pi: terminate?).
	Terminate bool `json:"terminate,omitempty"`
}

// ToolResultEvent fires after a tool executes (pi: ToolResultEvent).
type ToolResultEvent struct {
	Type       string          `json:"type"`
	ToolName   string          `json:"toolName"`
	ToolCallID string          `json:"toolCallId"`
	Input      any             `json:"input"`
	Content    []types.Content `json:"content"`
	Details    any             `json:"details"`
	IsError    bool            `json:"isError"`
	Usage      *types.Usage    `json:"usage,omitempty"`
}

func (e *ToolResultEvent) EventType() string { return e.Type }

// ToolResultEventResult is the tool_result handler result (pi:
// ToolResultEventResult).
type ToolResultEventResult struct {
	// Content replaces the result content; nil = unchanged (pi: content?).
	Content []types.Content `json:"content,omitempty"`
	// Details replaces the result details (pi: details?).
	Details any `json:"details,omitempty"`
	// IsError overrides the error flag; nil = unchanged (pi: isError?).
	IsError *bool `json:"isError,omitempty"`
	// Usage overrides the usage (pi: usage?).
	Usage *types.Usage `json:"usage,omitempty"`
}

// ============================================================================
// Registered items
// ============================================================================

// ExtensionHandler is the handler function type for events (pi: HandlerFn,
// whose (event, ctx) order becomes (ctx, extCtx, event) per the Go context
// convention).
type ExtensionHandler func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error)

// RegisteredTool is one extension-registered tool (pi: RegisteredTool).
type RegisteredTool struct {
	Definition *tools.ToolDefinition
	SourceInfo SourceInfo
}

// RegisteredCommand is one extension-registered command (pi:
// RegisteredCommand).
type RegisteredCommand struct {
	Name        string
	Description string
	// GetArgumentCompletions provides autocomplete items (pi:
	// getArgumentCompletions?); arrives with the TUI batch.
	GetArgumentCompletions func(argumentPrefix string) any
	// Handler runs the command (pi: handler).
	Handler func(ctx context.Context, args string, commandCtx *ExtensionCommandContext) error
	// SourceInfo is where the command came from.
	SourceInfo SourceInfo
}

// ResolvedCommand is a command with its deduplicated invocation name (pi:
// ResolvedCommand).
type ResolvedCommand struct {
	RegisteredCommand
	InvocationName string
}

// ExtensionFlag is one extension-declared flag (pi: ExtensionFlag).
type ExtensionFlag struct {
	Name          string
	Description   string
	Type          string // "boolean" | "string"
	Def           any    // pi: default?: boolean | string
	ExtensionPath string
}

// Extension is one loaded extension with all registered items (pi:
// Extension; the loader arrives in Phase 7 — the runner only ever sees an
// empty slice until then).
type Extension struct {
	Path         string
	ResolvedPath string
	Hidden       bool
	SourceInfo   SourceInfo
	Handlers     map[string][]ExtensionHandler
	Tools        map[string]RegisteredTool
	// MessageRenderers map CustomMessage custom types to components (pi:
	// messageRenderers: Map<string, MessageRenderer>).
	MessageRenderers map[string]MessageRenderer
	EntryRenderers   map[string]EntryRenderer
	// MarkdownTransformer transforms user/assistant Markdown before the
	// transcript renders it (pi: markdownTransformer?; single slot — the
	// last registration wins).
	MarkdownTransformer MarkdownTransformer
	Commands            map[string]RegisteredCommand
	Flags               map[string]ExtensionFlag
	// Shortcuts maps the shortcut key id to the handler (pi: shortcuts:
	// Map<KeyId, ExtensionShortcut>).
	Shortcuts map[tui.KeyId]*ExtensionShortcut
}

// ExtensionShortcut is one extension-registered keyboard shortcut (pi:
// ExtensionShortcut).
type ExtensionShortcut struct {
	// Shortcut is the key id (pi: shortcut: KeyId).
	Shortcut tui.KeyId
	// Description is shown in UI; "" = none (pi: description?).
	Description string
	// Handler runs when the shortcut fires (pi: handler: (ctx) =>
	// Promise<void> | void → sync).
	Handler func(ctx context.Context, ec *ExtensionContext)
	// ExtensionPath is the declaring extension (pi: extensionPath).
	ExtensionPath string
}

// LoadExtensionsResult is the result of loading extensions (pi:
// LoadExtensionsResult).
type LoadExtensionsResult struct {
	Extensions []*Extension
	Errors     []LoadExtensionError
	// Runtime is the shared runtime; actions are throwing stubs until the
	// runner binds (pi: runtime).
	Runtime *ExtensionRuntime
}

// LoadExtensionError is one extension load failure (pi: LoadExtensionsResult
// errors members).
type LoadExtensionError struct {
	Path  string
	Error string
}

// ExtensionContext satisfies the built-in tools' execute-context slice (pi:
// the tools read the same ExtensionContext object; pig bridges the struct
// fields to the tools.ToolExecuteContext methods).
func (c *ExtensionContext) GetModel() *types.Model { return c.Model }

func (c *ExtensionContext) GetSessionID() string { return c.SessionManager.GetSessionID() }

// GetSessionFile is nil when the session is not file-backed.
func (c *ExtensionContext) GetSessionFile() *string {
	file := c.SessionManager.GetSessionFile()
	if file == "" {
		return nil
	}
	return &file
}

func (c *ExtensionContext) GetThinkingLevel() string { return string(c.ThinkingLevel) }

// GetCwd is pi's ctx.cwd (v0.85.1): the built-in tools resolve paths
// against it when provided.
func (c *ExtensionContext) GetCwd() string { return c.Cwd }

// ContextEvent fires with the message context before each agent loop (pi:
// ContextEvent).
type ContextEvent struct {
	Type     string               `json:"type"`
	Messages []agent.AgentMessage `json:"messages"`
}

func (e *ContextEvent) EventType() string { return e.Type }

// ContextEventResult is the handler result of context and
// context_with_system (pi: ContextEventResult).
type ContextEventResult struct {
	Messages []agent.AgentMessage `json:"messages"`
}

// ContextWithSystemEvent fires before each LLM call after every context
// handler has run and Pi has restored the prompt and tool state; the result
// is sent as returned (pi: ContextWithSystemEvent, v0.87.0).
type ContextWithSystemEvent struct {
	Type     string               `json:"type"`
	Messages []agent.AgentMessage `json:"messages"`
}

func (e *ContextWithSystemEvent) EventType() string { return e.Type }

// TurnEndEventResult and AgentBeforeSettleEventResult are the boundary
// handler results (pi: TurnEndEventResult / AgentBeforeSettleEventResult,
// v0.87.0).
type (
	TurnEndEventResult           = BoundaryResult
	AgentBeforeSettleEventResult = BoundaryResult
)

// BeforeProviderRequestEvent fires before a provider request is sent; can
// replace the payload (pi: BeforeProviderRequestEvent).
type BeforeProviderRequestEvent struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

func (e *BeforeProviderRequestEvent) EventType() string { return e.Type }

// BeforeProviderHeadersEvent fires after request headers are assembled,
// before the provider HTTP call. Handlers mutate headers in place (e.g. to
// inject tracing/session headers); the return value is ignored. A nil value
// deletes that header (pi: BeforeProviderHeadersEvent).
type BeforeProviderHeadersEvent struct {
	Type    string                `json:"type"`
	Headers types.ProviderHeaders `json:"headers"`
}

func (e *BeforeProviderHeadersEvent) EventType() string { return e.Type }

// AfterProviderResponseEvent fires after a provider response is received and
// before the response stream is consumed (pi: AfterProviderResponseEvent).
type AfterProviderResponseEvent struct {
	Type    string            `json:"type"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
}

func (e *AfterProviderResponseEvent) EventType() string { return e.Type }

// SessionBeforeSwitchEvent fires before a session switch (new/resume); a
// handler returning cancel aborts the switch (pi: SessionBeforeSwitchEvent).
type SessionBeforeSwitchEvent struct {
	Type string `json:"type"`
	// Reason: "new" | "resume".
	Reason string `json:"reason"`
	// TargetSessionFile is the destination file; "" = none (pi:
	// targetSessionFile?).
	TargetSessionFile string `json:"targetSessionFile,omitempty"`
}

func (e *SessionBeforeSwitchEvent) EventType() string { return e.Type }

// SessionBeforeSwitchResult is the handler result of session_before_switch
// (pi: { cancel?: true }).
type SessionBeforeSwitchResult struct {
	Cancel bool `json:"cancel,omitempty"`
}

// SessionBeforeForkEvent fires before a fork; a handler returning cancel
// aborts the fork (pi: SessionBeforeForkEvent).
type SessionBeforeForkEvent struct {
	Type string `json:"type"`
	// EntryID is the fork target entry (pi: entryId).
	EntryID string `json:"entryId"`
	// Position: "before" | "at".
	Position string `json:"position"`
}

func (e *SessionBeforeForkEvent) EventType() string { return e.Type }

// SessionBeforeForkResult is the handler result of session_before_fork (pi:
// { cancel?: true }).
type SessionBeforeForkResult struct {
	Cancel bool `json:"cancel,omitempty"`
}

// ============================================================================
// Message & Markdown Rendering (pi: extensions/types.ts "Message Rendering"
// section; the Tool Types render surface lives in core/tools/tool_context.go
// — it is defined there because ToolDefinition's render members consume it
// and core cannot be imported by core/tools — and re-exported here under the
// pi names)
// ============================================================================

// UITheme is the theme surface available to UI renderers (pi passes the
// concrete Theme from modes/interactive/theme). Aliased from tools.UITheme;
// *theme.Theme satisfies it.
type UITheme = tools.UITheme

// MessageRenderOptions carries the message renderer options (pi:
// MessageRenderOptions).
type MessageRenderOptions struct {
	Expanded bool
	// OutputPad is the horizontal padding configured by the outputPad
	// setting (pi: outputPad).
	OutputPad int
}

// MessageRenderer renders a CustomMessage to a component; nil = default
// rendering (pi: MessageRenderer<T>). Panics inside the renderer are
// recovered by the consumer (pi: try/catch fallthrough).
type MessageRenderer func(message *harness.CustomMessage, options MessageRenderOptions, theme UITheme) tui.Component

// EntryRenderOptions carries the entry renderer options (pi:
// EntryRenderOptions).
type EntryRenderOptions struct {
	Expanded bool
}

// EntryRenderer renders a CustomEntry to a component; nil = no content (pi:
// EntryRenderer<T>).
type EntryRenderer func(entry *CustomEntry, options EntryRenderOptions, theme UITheme) tui.Component

// MarkdownTransformContext is the transform context handed to markdown
// transformers (pi: MarkdownTransformContext).
type MarkdownTransformContext struct {
	// MessageType: "user" | "assistant" | "assistant-thinking" (pi:
	// messageType union).
	MessageType string
	// IsStreaming reports whether the message is still streaming (pi:
	// isStreaming).
	IsStreaming bool
	// AvailableWidth is the exact width available for content (pi:
	// availableWidth).
	AvailableWidth int
}

// MarkdownTransformer transforms user/assistant Markdown before the
// transcript renders it (pi: MarkdownTransformer). Returning nil keeps the
// current markdown (pi: non-string return ignored).
type MarkdownTransformer func(markdown string, context MarkdownTransformContext) string

// ToolRenderResultOptions carries the tool result render options (pi:
// ToolRenderResultOptions). Aliased from tools.ToolRenderResultOptions.
type ToolRenderResultOptions = tools.ToolRenderResultOptions

// ToolRenderContext is the context passed to tool renderers (pi:
// ToolRenderContext<TState, TArgs>; generics collapse to any). Aliased from
// tools.ToolRenderContext.
type ToolRenderContext = tools.ToolRenderContext
