// pi: core/extensions/runner.ts — the ExtensionRunner, restricted to the
// no-extension path AgentSession consumes. The extension loader (Phase 7)
// will feed real Extension values into the same surface; until then the
// extensions slice is always empty, so every handler loop short-circuits:
// hasHandlers is false, emit is a no-op, and the emitXxx helpers return
// their no-handler defaults (pi's semantics with zero extensions).
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
	"github.com/ZoroSpace/pig/tui"
)

// ExtensionRuntime is the shared runtime state referenced by extension APIs
// (pi: ExtensionRuntime = ExtensionRuntimeState + ExtensionActions; the
// pending registration queues and event-bus tracking arrive with Phase 7).
type ExtensionRuntime struct {
	// FlagValues holds registered extension flag values (pi: flagValues:
	// Map<string, boolean | string>; values are only ever bool/string).
	FlagValues map[string]any
	// assertActive/invalidate state (pi: assertActive/invalidate members).
	staleMessage string
	// Action members, throwing stubs until bindCore (pi: the notInitialized
	// stubs). nil = not initialized.
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
	// PendingProviderRegistrations / pendingNativeProviderRegistrations
	// arrive with Phase 7 (pi: pendingProviderRegistrations).
	// RegisterProvider/RegisterNativeProvider/UnregisterProvider are bound
	// by bindCore (pi: registerProvider/registerNativeProvider/
	// unregisterProvider members; the pre-bind queueing arrives with Phase
	// 7).
	RegisterProvider       func(name string, config ProviderConfig)
	RegisterNativeProvider func(provider any)
	UnregisterProvider     func(name string)
}

// CreateExtensionRuntime creates the shared runtime with throwing action
// stubs (pi: createExtensionRuntime in loader.ts). pi's stubs throw on
// invocation; pig's are nil and consumers guard.
func CreateExtensionRuntime() *ExtensionRuntime {
	return &ExtensionRuntime{FlagValues: map[string]any{}}
}

// AssertActive panics when the runtime is stale after replacement or reload
// (pi: assertActive, which throws).
func (r *ExtensionRuntime) AssertActive() {
	if r.staleMessage != "" {
		panic(r.staleMessage)
	}
}

// Invalidate marks the runtime stale (pi: invalidate).
func (r *ExtensionRuntime) Invalidate(message string) {
	r.staleMessage = message
}

// ExtensionRunner drives extension event dispatch for one session (pi:
// ExtensionRunner).
type ExtensionRunner struct {
	extensions     []*Extension
	runtime        *ExtensionRuntime
	uiContext      ExtensionUIContext
	mode           ExtensionMode
	cwd            string
	sessionManager *SessionManager
	modelRegistry  *ModelRegistry
	errorListeners []ExtensionErrorListener
	// uiPrompt tracking for the blocking UI-prompt events (pi: uiPromptDepth
	// + activeUIPrompt, v0.85.1). Mutex-guarded because prompt wrappers run
	// on extension handler goroutines.
	uiPromptMu     sync.Mutex
	uiPromptDepth  int
	activeUIPrompt *UIPromptStartEvent
	// shortcutDiagnostics accumulates shortcut conflict warnings from
	// GetShortcuts (pi: shortcutDiagnostics).
	shortcutDiagnostics []ResourceDiagnostic
	// Context action state, defaulted like pi's field initializers and
	// completed by bindCore.
	getModel                 func() *types.Model
	getScopedModels          func() []ScopedModel
	isIdleFn                 func() bool
	isProjectTrustedFn       func() bool
	getSignalFn              func() context.Context
	waitForIdleFn            func(ctx context.Context) error
	abortFn                  func()
	hasPendingMessagesFn     func() bool
	getContextUsageFn        func() *ContextUsage
	compactFn                func(options *CompactOptions)
	getSystemPromptFn        func() string
	getSystemPromptOptionsFn func() BuildSystemPromptOptions
	newSessionHandler        func(ctx context.Context, options *NewSessionCommandOptions) (bool, error)
	forkHandler              func(ctx context.Context, entryID string, options *ForkCommandOptions) (bool, error)
	navigateTreeHandler      func(ctx context.Context, targetID string, options *NavigateTreeCommandOptions) (bool, error)
	switchSessionHandler     func(ctx context.Context, sessionPath string, options *SwitchSessionCommandOptions) (bool, error)
	reloadHandler            func(ctx context.Context) error
	shutdownHandler          ShutdownHandler
	// commandDiagnostics arrives with the loader batch (pi:
	// commandDiagnostics).
	staleMessage string
}

// NewExtensionRunner constructs the runner (pi: constructor).
func NewExtensionRunner(
	extensions []*Extension,
	runtime *ExtensionRuntime,
	cwd string,
	sessionManager *SessionManager,
	modelRegistry *ModelRegistry,
) *ExtensionRunner {
	return &ExtensionRunner{
		extensions:           extensions,
		runtime:              runtime,
		uiContext:            noOpExtensionUIContext{},
		mode:                 ExtensionModePrint,
		cwd:                  cwd,
		sessionManager:       sessionManager,
		modelRegistry:        modelRegistry,
		getModel:             func() *types.Model { return nil },
		getScopedModels:      func() []ScopedModel { return nil },
		isIdleFn:             func() bool { return true },
		isProjectTrustedFn:   func() bool { return true },
		getSignalFn:          func() context.Context { return nil },
		waitForIdleFn:        func(_ context.Context) error { return nil },
		abortFn:              func() {},
		hasPendingMessagesFn: func() bool { return false },
		getContextUsageFn:    func() *ContextUsage { return nil },
		compactFn:            func(_ *CompactOptions) {},
		getSystemPromptFn:    func() string { return "" },
		getSystemPromptOptionsFn: func() BuildSystemPromptOptions {
			return BuildSystemPromptOptions{Cwd: cwd}
		},
		newSessionHandler:    func(_ context.Context, _ *NewSessionCommandOptions) (bool, error) { return false, nil },
		forkHandler:          func(_ context.Context, _ string, _ *ForkCommandOptions) (bool, error) { return false, nil },
		navigateTreeHandler:  func(_ context.Context, _ string, _ *NavigateTreeCommandOptions) (bool, error) { return false, nil },
		switchSessionHandler: func(_ context.Context, _ string, _ *SwitchSessionCommandOptions) (bool, error) { return false, nil },
		reloadHandler:        func(_ context.Context) error { return nil },
		shutdownHandler:      func() {},
	}
}

// BindCore copies actions into the shared runtime and binds the context
// actions (pi: bindCore). nil actions fields keep the previous binding, and
// a nil providerActions skips provider rebinding (pi: optional parameter).
func (r *ExtensionRunner) BindCore(
	actions *ExtensionActions,
	contextActions *ExtensionContextActions,
	providerActions *ExtensionProviderActions,
) {
	r.runtime.SendMessage = actions.SendMessage
	r.runtime.SendUserMessage = actions.SendUserMessage
	r.runtime.AppendEntry = actions.AppendEntry
	r.runtime.SetSessionName = actions.SetSessionName
	r.runtime.GetSessionName = actions.GetSessionName
	r.runtime.SetLabel = actions.SetLabel
	r.runtime.GetActiveTools = actions.GetActiveTools
	r.runtime.GetAllTools = actions.GetAllTools
	r.runtime.SetActiveTools = actions.SetActiveTools
	r.runtime.RefreshTools = actions.RefreshTools
	r.runtime.GetCommands = actions.GetCommands
	r.runtime.SetModel = actions.SetModel
	r.runtime.GetThinkingLevel = actions.GetThinkingLevel
	r.runtime.SetThinkingLevel = actions.SetThinkingLevel

	// Context actions (required)
	r.getModel = contextActions.GetModel
	r.getScopedModels = contextActions.GetScopedModels
	r.isIdleFn = contextActions.IsIdle
	r.isProjectTrustedFn = contextActions.IsProjectTrusted
	r.getSignalFn = contextActions.GetSignal
	r.abortFn = contextActions.Abort
	r.hasPendingMessagesFn = contextActions.HasPendingMessages
	r.shutdownHandler = contextActions.Shutdown
	r.getContextUsageFn = contextActions.GetContextUsage
	r.compactFn = contextActions.Compact
	r.getSystemPromptFn = contextActions.GetSystemPrompt
	if contextActions.GetSystemPromptOptions != nil {
		r.getSystemPromptOptionsFn = contextActions.GetSystemPromptOptions
	}

	// Provider actions (optional in pi)
	if providerActions != nil {
		r.runtime.RegisterProvider = providerActions.RegisterProvider
		r.runtime.RegisterNativeProvider = providerActions.RegisterNativeProvider
		r.runtime.UnregisterProvider = providerActions.UnregisterProvider
	}
}

// BindCommandContext binds the command context actions (pi:
// bindCommandContext). nil keeps the previous binding (pi: `if (!actions)
// return`).
func (r *ExtensionRunner) BindCommandContext(actions *ExtensionCommandContextActions) {
	if actions == nil {
		return
	}
	r.waitForIdleFn = actions.WaitForIdle
	r.newSessionHandler = actions.NewSession
	r.forkHandler = actions.Fork
	r.navigateTreeHandler = actions.NavigateTree
	r.switchSessionHandler = actions.SwitchSession
	r.reloadHandler = actions.Reload
}

// SetUIContext binds the UI context (pi: setUIContext). nil uiContext keeps
// the no-op context (pi: `if (uiContext)`).
func (r *ExtensionRunner) SetUIContext(uiContext ExtensionUIContext, mode ExtensionMode) {
	// pi (v0.85.1): wrap the UI context so blocking prompts emit
	// ui_prompt_start/ui_prompt_end.
	if uiContext != nil {
		r.uiContext = r.wrapUIPromptContext(uiContext)
	}
	if mode != "" {
		r.mode = mode
	}
}

// wrapUIPromptContext wraps the five blocking dialog methods with UI-prompt
// event emission (pi: wrapUIPromptContext).
func (r *ExtensionRunner) wrapUIPromptContext(ui ExtensionUIContext) ExtensionUIContext {
	return &uiPromptContextWrapper{ExtensionUIContext: ui, r: r}
}

// withUIPrompt tracks one nested blocking prompt and emits start/end events
// (pi: withUIPrompt; the promise finally collapses into a deferred finish).
func (r *ExtensionRunner) withUIPrompt(ctx context.Context, kind UIPromptKind, title *string, run func()) {
	r.uiPromptMu.Lock()
	outer := r.uiPromptDepth == 0
	r.uiPromptDepth++
	if outer {
		r.activeUIPrompt = &UIPromptStartEvent{Type: "ui_prompt_start", Reason: "ui_prompt", Kind: kind, Title: title}
		event := r.activeUIPrompt
		r.uiPromptMu.Unlock()
		r.emitUIPromptEvent(ctx, event)
	} else {
		r.uiPromptMu.Unlock()
	}

	defer func() {
		r.uiPromptMu.Lock()
		r.uiPromptDepth--
		if r.uiPromptDepth > 0 {
			r.uiPromptMu.Unlock()
			return
		}
		r.uiPromptDepth = 0
		prompt := r.activeUIPrompt
		if prompt == nil {
			prompt = &UIPromptStartEvent{Type: "ui_prompt_start", Reason: "ui_prompt", Kind: kind, Title: title}
		}
		r.activeUIPrompt = nil
		r.uiPromptMu.Unlock()
		r.emitUIPromptEvent(ctx, &UIPromptEndEvent{Type: "ui_prompt_end", Reason: "ui_prompt", Kind: prompt.Kind, Title: prompt.Title})
	}()

	run()
}

// emitUIPromptEvent fires the prompt event off the current call stack (pi:
// queueMicrotask(() => void this.emit(event))).
func (r *ExtensionRunner) emitUIPromptEvent(ctx context.Context, event ExtensionEvent) {
	go func() {
		_, _ = r.Emit(context.WithoutCancel(ctx), event)
	}()
}

// uiPromptContextWrapper delegates to the wrapped UI context, emitting
// ui_prompt events around the blocking dialog methods.
type uiPromptContextWrapper struct {
	ExtensionUIContext
	r *ExtensionRunner
}

func (w *uiPromptContextWrapper) Select(ctx context.Context, title string, options []string, opts *ExtensionUIDialogOptions) (string, bool) {
	var choice string
	var ok bool
	w.r.withUIPrompt(ctx, UIPromptKindSelect, &title, func() {
		choice, ok = w.ExtensionUIContext.Select(ctx, title, options, opts)
	})
	return choice, ok
}

func (w *uiPromptContextWrapper) Confirm(ctx context.Context, title string, message string, opts *ExtensionUIDialogOptions) bool {
	var confirmed bool
	w.r.withUIPrompt(ctx, UIPromptKindConfirm, &title, func() {
		confirmed = w.ExtensionUIContext.Confirm(ctx, title, message, opts)
	})
	return confirmed
}

func (w *uiPromptContextWrapper) Input(ctx context.Context, title string, placeholder string, opts *ExtensionUIDialogOptions) (string, bool) {
	var value string
	var ok bool
	w.r.withUIPrompt(ctx, UIPromptKindInput, &title, func() {
		value, ok = w.ExtensionUIContext.Input(ctx, title, placeholder, opts)
	})
	return value, ok
}

func (w *uiPromptContextWrapper) Editor(ctx context.Context, title string, prefill string) (string, bool) {
	var text string
	var ok bool
	w.r.withUIPrompt(ctx, UIPromptKindEditor, &title, func() {
		text, ok = w.ExtensionUIContext.Editor(ctx, title, prefill)
	})
	return text, ok
}

func (w *uiPromptContextWrapper) Custom(ctx context.Context, factory ExtensionCustomFactory, options *ExtensionCustomOptions) any {
	var result any
	// pi: custom wraps with kind "custom" and no title.
	w.r.withUIPrompt(ctx, UIPromptKindCustom, nil, func() {
		result = w.ExtensionUIContext.Custom(ctx, factory, options)
	})
	return result
}

// HasUI reports whether dialog-capable UI is available (pi: hasUI).
func (r *ExtensionRunner) HasUI() bool {
	_, isNoOp := r.uiContext.(noOpExtensionUIContext)
	return !isNoOp
}

// GetExtensionPaths lists the loaded extension paths (pi:
// getExtensionPaths).
func (r *ExtensionRunner) GetExtensionPaths() []string {
	paths := make([]string, 0, len(r.extensions))
	for _, ext := range r.extensions {
		paths = append(paths, ext.Path)
	}
	return paths
}

// GetAllRegisteredTools collects the registered tools from all extensions,
// first registration per name wins (pi: getAllRegisteredTools).
func (r *ExtensionRunner) GetAllRegisteredTools() []RegisteredTool {
	toolsByName := map[string]RegisteredTool{}
	var order []string
	for _, ext := range r.extensions {
		for _, tool := range ext.Tools {
			if _, seen := toolsByName[tool.Definition.Name]; !seen {
				toolsByName[tool.Definition.Name] = tool
				order = append(order, tool.Definition.Name)
			}
		}
	}
	out := make([]RegisteredTool, 0, len(order))
	for _, name := range order {
		out = append(out, toolsByName[name])
	}
	return out
}

// GetFlagValues returns the registered flag values (pi: getFlagValues).
func (r *ExtensionRunner) GetFlagValues() map[string]any {
	return r.runtime.FlagValues
}

// SetFlagValue sets one flag value (pi: setFlagValue).
func (r *ExtensionRunner) SetFlagValue(name string, value any) {
	r.runtime.FlagValues[name] = value
}

// Invalidate marks the runner stale (pi: invalidate). pi defaults the
// message when called without one.
func (r *ExtensionRunner) Invalidate(message string) {
	if message == "" {
		message = "This extension ctx is stale after session replacement or reload."
	}
	r.runtime.Invalidate(message)
	r.staleMessage = message
}

// assertActive panics when stale (pi: assertActive throwing).
func (r *ExtensionRunner) assertActive() {
	if r.staleMessage != "" {
		panic(r.staleMessage)
	}
}

// OnError registers an error listener and returns the unsubscribe function
// (pi: onError).
func (r *ExtensionRunner) OnError(listener ExtensionErrorListener) func() {
	r.errorListeners = append(r.errorListeners, listener)
	return func() {
		for i, l := range r.errorListeners {
			if listenerEntryEqual(l, listener) {
				r.errorListeners = append(r.errorListeners[:i], r.errorListeners[i+1:]...)
				return
			}
		}
	}
}

func listenerEntryEqual(a, b ExtensionErrorListener) bool {
	return fmt.Sprintf("%p", a) == fmt.Sprintf("%p", b)
}

// EmitError reports an error to all listeners (pi: emitError).
func (r *ExtensionRunner) EmitError(err ExtensionError) {
	for _, listener := range r.errorListeners {
		listener(err)
	}
}

// HasHandlers reports whether any extension registers handlers for the event
// type (pi: hasHandlers).
func (r *ExtensionRunner) HasHandlers(eventType string) bool {
	for _, ext := range r.extensions {
		if handlers := ext.Handlers[eventType]; len(handlers) > 0 {
			return true
		}
	}
	return false
}

// GetCommandDiagnostics returns command-name dedup diagnostics (pi:
// getCommandDiagnostics). The diagnostics arrive with the loader batch
// (P7); empty until then (declared).
func (r *ExtensionRunner) GetCommandDiagnostics() []ResourceDiagnostic {
	return nil
}

// reservedKeybindingsForExtensionConflicts are the canonical keybinding ids
// extension shortcuts may not override (pi:
// RESERVED_KEYBINDINGS_FOR_EXTENSION_CONFLICTS — only editor-global
// shortcuts are reserved; picker-specific bindings are not).
var reservedKeybindingsForExtensionConflicts = []string{
	"app.interrupt",
	"app.clear",
	"app.exit",
	"app.suspend",
	"app.thinking.cycle",
	"app.model.cycleForward",
	"app.model.cycleBackward",
	"app.model.select",
	"app.tools.expand",
	"app.thinking.toggle",
	"app.editor.external",
	"app.message.copy",
	"app.message.followUp",
	"tui.input.submit",
	"tui.select.confirm",
	"tui.select.cancel",
	"tui.input.copy",
	"tui.editor.deleteToLineEnd",
}

// builtinKeybindingEntry is one resolved builtin keybinding keyed by key id
// (pi: BuiltInKeyBindings value type).
type builtinKeybindingEntry struct {
	keybinding       string
	restrictOverride bool
}

// buildBuiltinKeybindings indexes the resolved config by key id (pi:
// buildBuiltinKeybindings).
func buildBuiltinKeybindings(resolvedKeybindings tui.KeybindingsConfig) map[tui.KeyId]*builtinKeybindingEntry {
	builtinKeybindings := map[tui.KeyId]*builtinKeybindingEntry{}
	reserved := map[string]bool{}
	for _, keybinding := range reservedKeybindingsForExtensionConflicts {
		reserved[keybinding] = true
	}
	for keybinding, keys := range resolvedKeybindings {
		if len(keys) == 0 {
			continue
		}
		restrictOverride := reserved[keybinding]
		for _, key := range keys {
			normalizedKey := tui.KeyId(strings.ToLower(key))
			// If multiple actions bind the same key, the reserved action wins
			// so extensions remain blocked by reserved shortcuts regardless of
			// iteration order.
			if existing := builtinKeybindings[normalizedKey]; existing != nil && existing.restrictOverride && !restrictOverride {
				continue
			}
			builtinKeybindings[normalizedKey] = &builtinKeybindingEntry{keybinding: keybinding, restrictOverride: restrictOverride}
		}
	}
	return builtinKeybindings
}

// GetShortcuts resolves the extension shortcuts, computing conflict
// diagnostics against the builtin keybindings (pi: getShortcuts).
func (r *ExtensionRunner) GetShortcuts(resolvedKeybindings tui.KeybindingsConfig) map[tui.KeyId]*ExtensionShortcut {
	r.shortcutDiagnostics = nil
	builtinKeybindings := buildBuiltinKeybindings(resolvedKeybindings)
	extensionShortcuts := map[tui.KeyId]*ExtensionShortcut{}

	addDiagnostic := func(message string, extensionPath string) {
		r.shortcutDiagnostics = append(r.shortcutDiagnostics, ResourceDiagnostic{Type: "warning", Message: message, Path: extensionPath})
		if !r.HasUI() {
			fmt.Fprintln(os.Stderr, message)
		}
	}

	for _, ext := range r.extensions {
		for key, shortcut := range ext.Shortcuts {
			normalizedKey := tui.KeyId(strings.ToLower(key))

			builtInKeybinding := builtinKeybindings[normalizedKey]
			if builtInKeybinding != nil && builtInKeybinding.restrictOverride {
				addDiagnostic(
					fmt.Sprintf("Extension shortcut '%s' from %s conflicts with built-in shortcut. Skipping.", key, shortcut.ExtensionPath),
					shortcut.ExtensionPath,
				)
				continue
			}

			if builtInKeybinding != nil && !builtInKeybinding.restrictOverride {
				addDiagnostic(
					fmt.Sprintf("Extension shortcut conflict: '%s' is built-in shortcut for %s and %s. Using %s.", key, builtInKeybinding.keybinding, shortcut.ExtensionPath, shortcut.ExtensionPath),
					shortcut.ExtensionPath,
				)
			}

			if existing := extensionShortcuts[normalizedKey]; existing != nil {
				addDiagnostic(
					fmt.Sprintf("Extension shortcut conflict: '%s' registered by both %s and %s. Using %s.", key, existing.ExtensionPath, shortcut.ExtensionPath, shortcut.ExtensionPath),
					shortcut.ExtensionPath,
				)
			}
			extensionShortcuts[normalizedKey] = shortcut
		}
	}
	return extensionShortcuts
}

// GetShortcutDiagnostics returns shortcut diagnostics (pi:
// getShortcutDiagnostics); populated by GetShortcuts.
func (r *ExtensionRunner) GetShortcutDiagnostics() []ResourceDiagnostic {
	return r.shortcutDiagnostics
}

// GetRegisteredCommands returns the resolved commands, recomputing the
// diagnostics (pi: getRegisteredCommands).
func (r *ExtensionRunner) GetRegisteredCommands() []ResolvedCommand {
	return r.resolveRegisteredCommands()
}

// resolveRegisteredCommands deduplicates command names into invocation names
// (pi: resolveRegisteredCommands).
func (r *ExtensionRunner) resolveRegisteredCommands() []ResolvedCommand {
	var commands []RegisteredCommand
	counts := map[string]int{}
	for _, ext := range r.extensions {
		for _, command := range ext.Commands {
			commands = append(commands, command)
			counts[command.Name]++
		}
	}
	seen := map[string]int{}
	takenInvocationNames := map[string]bool{}
	out := make([]ResolvedCommand, 0, len(commands))
	for _, command := range commands {
		occurrence := seen[command.Name] + 1
		seen[command.Name] = occurrence
		invocationName := command.Name
		if counts[command.Name] > 1 {
			invocationName = fmt.Sprintf("%s:%d", command.Name, occurrence)
		}
		for takenInvocationNames[invocationName] {
			occurrence++
			invocationName = fmt.Sprintf("%s:%d", command.Name, occurrence)
		}
		takenInvocationNames[invocationName] = true
		resolved := ResolvedCommand{RegisteredCommand: command, InvocationName: invocationName}
		out = append(out, resolved)
	}
	return out
}

// GetCommand finds a command by invocation name (pi: getCommand).
func (r *ExtensionRunner) GetCommand(name string) *ResolvedCommand {
	for _, command := range r.resolveRegisteredCommands() {
		if command.InvocationName == name {
			return &command
		}
	}
	return nil
}

// GetModelRegistry exposes the model registry (pi: getModelRegistry).
func (r *ExtensionRunner) GetModelRegistry() *ModelRegistry {
	return r.modelRegistry
}

// Shutdown requests graceful shutdown (pi: shutdown).
func (r *ExtensionRunner) Shutdown() {
	r.shutdownHandler()
}

// GetActiveTools returns the active tool names (pi: getActiveTools).
func (r *ExtensionRunner) GetActiveTools() []string {
	r.assertActive()
	if r.runtime.GetActiveTools == nil {
		return nil
	}
	return r.runtime.GetActiveTools()
}

// CreateContext builds the context for event handlers and tool execution
// (pi: createContext; the lazy TS getters become eager struct fields, see
// the types header).
func (r *ExtensionRunner) CreateContext() ExtensionContext {
	return ExtensionContext{
		UI:             r.uiContext,
		Mode:           r.mode,
		HasUI:          r.HasUI(),
		Cwd:            r.cwd,
		SessionManager: r.sessionManager,
		ModelRegistry:  r.modelRegistry,
		Model:          r.getModel(),
		ScopedModels:   r.getScopedModels(),
		ThinkingLevel:  r.runtimeThinkingLevel(),
		IsIdle: func() bool {
			return r.isIdleFn()
		},
		IsProjectTrusted: func() bool {
			return r.isProjectTrustedFn()
		},
		Signal: func() context.Context {
			return r.getSignalFn()
		},
		Abort: func() {
			r.abortFn()
		},
		HasPendingMessages: func() bool {
			return r.hasPendingMessagesFn()
		},
		Shutdown: func() {
			r.shutdownHandler()
		},
		GetContextUsage: func() *ContextUsage {
			return r.getContextUsageFn()
		},
		Compact: func(options *CompactOptions) {
			r.compactFn(options)
		},
		GetSystemPrompt: func() string {
			return r.getSystemPromptFn()
		},
		SendMessage: func(ctx context.Context, message *CustomMessagePayload, options *SendMessageOptions) {
			if r.runtime.SendMessage == nil {
				panic("Extension runtime is not initialized")
			}
			r.runtime.SendMessage(ctx, message, options)
		},
		SendUserMessage: func(ctx context.Context, content string, options *SendUserMessageOptions) {
			if r.runtime.SendUserMessage == nil {
				panic("Extension runtime is not initialized")
			}
			r.runtime.SendUserMessage(ctx, content, options)
		},
	}
}

// runtimeThinkingLevel reads the bound getThinkingLevel action; nil (pi's
// notInitialized stub) yields "".
func (r *ExtensionRunner) runtimeThinkingLevel() agent.ThinkingLevel {
	if r.runtime.GetThinkingLevel == nil {
		return ""
	}
	return r.runtime.GetThinkingLevel()
}

// CreateCommandContext builds the command-handler context (pi:
// createCommandContext).
func (r *ExtensionRunner) CreateCommandContext() *ExtensionCommandContext {
	base := r.CreateContext()
	return &ExtensionCommandContext{
		ExtensionContext: base,
		GetSystemPromptOptions: func() BuildSystemPromptOptions {
			r.assertActive()
			return r.getSystemPromptOptionsFn()
		},
		WaitForIdle: func(ctx context.Context) error {
			r.assertActive()
			return r.waitForIdleFn(ctx)
		},
		NewSession: func(ctx context.Context, options *NewSessionCommandOptions) (bool, error) {
			r.assertActive()
			return r.newSessionHandler(ctx, options)
		},
		Fork: func(ctx context.Context, entryID string, options *ForkCommandOptions) (bool, error) {
			r.assertActive()
			return r.forkHandler(ctx, entryID, options)
		},
		NavigateTree: func(ctx context.Context, targetID string, options *NavigateTreeCommandOptions) (bool, error) {
			r.assertActive()
			return r.navigateTreeHandler(ctx, targetID, options)
		},
		SwitchSession: func(ctx context.Context, sessionPath string, options *SwitchSessionCommandOptions) (bool, error) {
			r.assertActive()
			return r.switchSessionHandler(ctx, sessionPath, options)
		},
		Reload: func(ctx context.Context) error {
			r.assertActive()
			return r.reloadHandler(ctx)
		},
	}
}

// Emit dispatches one event to all matching handlers in registration order,
// returning the last non-nil handler result; a cancel result from
// session_before_* events short-circuits (pi: emit — the result variable is
// overwritten by each handler, and cancel results abort the dispatch).
func (r *ExtensionRunner) Emit(ctx context.Context, event ExtensionEvent) (any, error) {
	var lastResult any
	for _, ext := range r.extensions {
		handlers := ext.Handlers[event.EventType()]
		if len(handlers) == 0 {
			continue
		}
		handlerCtxValue := r.CreateContext()
		handlerCtx := &handlerCtxValue
		for _, handler := range handlers {
			result, err := handler(ctx, handlerCtx, event)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         event.EventType(),
					Error:         err.Error(),
				})
				continue
			}
			if before, ok := result.(*SessionBeforeCompactResult); ok && before.Cancel {
				return before, nil
			}
			if before, ok := result.(*SessionBeforeTreeResult); ok && before.Cancel {
				return before, nil
			}
			if result != nil {
				lastResult = result
			}
		}
	}
	return lastResult, nil
}

// isUserBashEventResult validates a handler result (pi:
// isUserBashEventResult — exactly one of { operations } / { result }).
func isUserBashEventResult(value *UserBashEventResult) bool {
	if value == nil {
		return false
	}
	hasOperations := value.Operations != nil
	hasResult := value.Result != nil
	if hasOperations == hasResult {
		return false
	}
	if hasOperations {
		return value.Operations.Exec != nil
	}
	return value.Result != nil
}

// EmitUserBash runs user_bash handlers and returns the interception, if any
// (pi: emitUserBash). nil result = run locally. A throwing handler is
// reported and re-thrown (error return); an invalid result fails closed so
// the `!` command never silently falls back to local execution (#9068).
func (r *ExtensionRunner) EmitUserBash(ctx context.Context, event *UserBashEvent) (*UserBashEventResult, error) {
	handlerCtxValue := r.CreateContext()
	handlerCtx := &handlerCtxValue
	for _, ext := range r.extensions {
		handlers := ext.Handlers[event.EventType()]
		for _, handler := range handlers {
			result, err := handler(ctx, handlerCtx, event)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         event.EventType(),
					Error:         err.Error(),
				})
				return nil, err
			}
			if result == nil {
				continue
			}
			validated, ok := result.(*UserBashEventResult)
			if !ok || !isUserBashEventResult(validated) {
				invalid := fmt.Errorf("Invalid user_bash handler result: return undefined for local execution or exactly one valid { operations } or { result } object")
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         event.EventType(),
					Error:         invalid.Error(),
				})
				return nil, invalid
			}
			return validated, nil
		}
	}
	return nil, nil
}

// EmitMessageEnd runs message_end handlers and returns the replacement
// message, if any handler produced one (pi: emitMessageEnd).
func (r *ExtensionRunner) EmitMessageEnd(ctx context.Context, event *MessageEndEvent) (agent.AgentMessage, error) {
	currentMessage := event.Message
	modified := false
	for _, ext := range r.extensions {
		handlers := ext.Handlers["message_end"]
		if len(handlers) == 0 {
			continue
		}
		handlerCtxValue := r.CreateContext()
		handlerCtx := &handlerCtxValue
		for _, handler := range handlers {
			currentEvent := *event
			currentEvent.Message = currentMessage
			result, err := handler(ctx, handlerCtx, &currentEvent)
			if err != nil {
				r.EmitError(ExtensionError{ExtensionPath: ext.Path, Event: "message_end", Error: err.Error()})
				continue
			}
			messageEndResult, ok := result.(*MessageEndEventResult)
			if !ok || messageEndResult == nil || messageEndResult.Message == nil {
				continue
			}
			if messageEndResult.Message.MessageRole() != currentMessage.MessageRole() {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         "message_end",
					Error:         "message_end handlers must return a message with the same role",
				})
				continue
			}
			currentMessage = messageEndResult.Message
			modified = true
		}
	}
	if modified {
		return currentMessage, nil
	}
	return nil, nil
}

// EmitToolResult runs tool_result handlers and returns the merged result (pi:
// emitToolResult).
func (r *ExtensionRunner) EmitToolResult(ctx context.Context, event *ToolResultEvent) (*ToolResultEventResult, error) {
	currentEvent := *event
	modified := false
	for _, ext := range r.extensions {
		handlers := ext.Handlers["tool_result"]
		if len(handlers) == 0 {
			continue
		}
		handlerCtxValue := r.CreateContext()
		handlerCtx := &handlerCtxValue
		for _, handler := range handlers {
			result, err := handler(ctx, handlerCtx, &currentEvent)
			if err != nil {
				r.EmitError(ExtensionError{ExtensionPath: ext.Path, Event: "tool_result", Error: err.Error()})
				continue
			}
			toolResult, ok := result.(*ToolResultEventResult)
			if !ok || toolResult == nil {
				continue
			}
			if toolResult.Content != nil {
				currentEvent.Content = toolResult.Content
			}
			if toolResult.Details != nil {
				currentEvent.Details = toolResult.Details
			}
			if toolResult.IsError != nil {
				currentEvent.IsError = *toolResult.IsError
			}
			if toolResult.Usage != nil {
				currentEvent.Usage = toolResult.Usage
			}
			modified = true
		}
	}
	if modified {
		return &ToolResultEventResult{
			Content: currentEvent.Content,
			Details: currentEvent.Details,
			IsError: &currentEvent.IsError,
			Usage:   currentEvent.Usage,
		}, nil
	}
	return nil, nil
}

// EmitToolCall runs tool_call handlers and returns the first blocking result
// (pi: emitToolCall).
func (r *ExtensionRunner) EmitToolCall(ctx context.Context, event *ToolCallEvent) (*ToolCallEventResult, error) {
	for _, ext := range r.extensions {
		handlers := ext.Handlers["tool_call"]
		if len(handlers) == 0 {
			continue
		}
		handlerCtxValue := r.CreateContext()
		handlerCtx := &handlerCtxValue
		for _, handler := range handlers {
			result, err := handler(ctx, handlerCtx, event)
			if err != nil {
				r.EmitError(ExtensionError{ExtensionPath: ext.Path, Event: "tool_call", Error: err.Error()})
				continue
			}
			toolCallResult, ok := result.(*ToolCallEventResult)
			if !ok || toolCallResult == nil {
				continue
			}
			if toolCallResult.Block {
				return toolCallResult, nil
			}
		}
	}
	return nil, nil
}

// EmitBeforeAgentStart runs before_agent_start handlers and merges their
// results (pi: emitBeforeAgentStart; v0.86.0 always returns the normalized
// options — a handler's systemPrompt override is recorded as
// forceSystemPrompt, and later handlers observe earlier mutations).
func (r *ExtensionRunner) EmitBeforeAgentStart(
	ctx context.Context,
	prompt string,
	images []types.ImageContent,
	systemPromptOptions BuildSystemPromptOptions,
) (*BeforeAgentStartCombinedResult, error) {
	currentOptions := NormalizeBuildSystemPromptOptions(systemPromptOptions)
	renderCurrentSystemPrompt := func() string {
		return BuildSystemPrompt(currentOptions.buildSystemPromptOptions())
	}
	var messages []CustomMessagePayload
	for _, ext := range r.extensions {
		handlers := ext.Handlers["before_agent_start"]
		if len(handlers) == 0 {
			continue
		}
		handlerCtxValue := r.CreateContext()
		handlerCtx := &handlerCtxValue
		for _, handler := range handlers {
			event := &BeforeAgentStartEvent{
				Type:                "before_agent_start",
				Prompt:              prompt,
				Images:              images,
				SystemPrompt:        renderCurrentSystemPrompt(),
				SystemPromptOptions: &currentOptions,
			}
			result, err := handler(ctx, handlerCtx, event)
			if err != nil {
				r.EmitError(ExtensionError{ExtensionPath: ext.Path, Event: "before_agent_start", Error: err.Error()})
				continue
			}
			beforeResult, ok := result.(*BeforeAgentStartEventResult)
			if !ok || beforeResult == nil {
				continue
			}
			if beforeResult.Message != nil {
				messages = append(messages, *beforeResult.Message)
			}
			if beforeResult.SystemPrompt != nil {
				currentOptions.ForceSystemPrompt = *beforeResult.SystemPrompt
			}
		}
	}
	return &BeforeAgentStartCombinedResult{
		Messages:            messages,
		SystemPromptOptions: currentOptions,
	}, nil
}

// EmitResourcesDiscover runs resources_discover handlers (pi:
// emitResourcesDiscover).
func (r *ExtensionRunner) EmitResourcesDiscover(ctx context.Context, cwd string, reason string) (*ResourcesDiscoverResult, error) {
	result := &ResourcesDiscoverResult{}
	for _, ext := range r.extensions {
		handlers := ext.Handlers["resources_discover"]
		if len(handlers) == 0 {
			continue
		}
		handlerCtxValue := r.CreateContext()
		handlerCtx := &handlerCtxValue
		for _, handler := range handlers {
			event := &ResourcesDiscoverEvent{Type: "resources_discover", Cwd: cwd, Reason: reason}
			handlerResult, err := handler(ctx, handlerCtx, event)
			if err != nil {
				r.EmitError(ExtensionError{ExtensionPath: ext.Path, Event: "resources_discover", Error: err.Error()})
				continue
			}
			discoverResult, ok := handlerResult.(*ResourcesDiscoverResult)
			if !ok || discoverResult == nil {
				continue
			}
			result.SkillPaths = append(result.SkillPaths, discoverResult.SkillPaths...)
			result.PromptPaths = append(result.PromptPaths, discoverResult.PromptPaths...)
			result.ThemePaths = append(result.ThemePaths, discoverResult.ThemePaths...)
		}
	}
	return result, nil
}

// EmitInput runs input handlers as a transform chain; "handled"
// short-circuits (pi: emitInput).
func (r *ExtensionRunner) EmitInput(
	ctx context.Context,
	text string,
	images []types.ImageContent,
	source InputSource,
	streamingBehavior string,
) (*InputEventResult, error) {
	currentText := text
	currentImages := images
	transformed := false
	for _, ext := range r.extensions {
		for _, handler := range ext.Handlers["input"] {
			handlerCtxValue := r.CreateContext()
			handlerCtx := &handlerCtxValue
			event := &InputEvent{
				Type:              "input",
				Text:              currentText,
				Images:            currentImages,
				Source:            source,
				StreamingBehavior: streamingBehavior,
			}
			result, err := handler(ctx, handlerCtx, event)
			if err != nil {
				r.EmitError(ExtensionError{ExtensionPath: ext.Path, Event: "input", Error: err.Error()})
				continue
			}
			inputResult, ok := result.(*InputEventResult)
			if !ok || inputResult == nil {
				continue
			}
			if inputResult.Action == "handled" {
				return inputResult, nil
			}
			if inputResult.Action == "transform" {
				if inputResult.Text != currentText || inputResult.Images != nil {
					transformed = true
				}
				currentText = inputResult.Text
				if inputResult.Images != nil {
					currentImages = inputResult.Images
				}
			}
		}
	}
	// pi compares slice identity (`currentText !== text || currentImages !==
	// images`); Go cannot observe slice identity, so a transform is reported
	// when a handler replaced the text or images (declared deviation).
	if transformed {
		return &InputEventResult{Action: "transform", Text: currentText, Images: currentImages}, nil
	}
	return &InputEventResult{Action: "continue"}, nil
}

// EmitSessionShutdownEvent emits session_shutdown to extensions (pi:
// emitSessionShutdownEvent). Returns whether the event was emitted.
func EmitSessionShutdownEvent(ctx context.Context, extensionRunner *ExtensionRunner, event *SessionShutdownEvent) (bool, error) {
	if extensionRunner.HasHandlers("session_shutdown") {
		_, err := extensionRunner.Emit(ctx, event)
		return true, err
	}
	return false, nil
}

// EmitContext applies extension context transforms (pi: emitContext). pi
// structuredClones the messages first so handlers cannot mutate the caller's
// slice; pig clones via JSON round-trip (reducer precedent).
// EmitCacheWarmingDecision returns the event's own action unless a handler
// overrides it; the last override wins (pi: emitCacheWarmingDecision;
// v0.86.0).
func (r *ExtensionRunner) EmitCacheWarmingDecision(ctx context.Context, event *CacheWarmingDecisionEvent) CacheWarmingAction {
	action := event.Action
	for _, ext := range r.extensions {
		handlers := ext.Handlers["cache_warming_decision"]
		if len(handlers) == 0 {
			continue
		}
		handlerCtxValue := r.CreateContext()
		handlerCtx := &handlerCtxValue
		for _, handler := range handlers {
			result, err := handler(ctx, handlerCtx, event)
			if err != nil {
				r.EmitError(ExtensionError{ExtensionPath: ext.Path, Event: event.Type, Error: err.Error()})
				continue
			}
			if decisionResult, ok := result.(*CacheWarmingDecisionEventResult); ok && decisionResult != nil && decisionResult.Action != "" {
				action = decisionResult.Action
			}
		}
	}
	return action
}

// sameAgentMessages reports element-wise equality (pi: sameMessages
// compares object identity; pig messages are value copies, so DeepEqual is
// the honest equivalent — S6-3a DeepEqual precedent).
func sameAgentMessages(left, right []agent.AgentMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !reflect.DeepEqual(left[i], right[i]) {
			return false
		}
	}
	return true
}

// transcriptOfMessages filters the LLM-message subset of AgentMessage
// (pig-only: pi's getCurrentSystemMessage consumes AgentMessage[] directly;
// pig's aiutils.GetCurrentSystemMessage takes TranscriptMessages).
func transcriptOfMessages(messages []agent.AgentMessage) aiutils.TranscriptMessages {
	var transcript aiutils.TranscriptMessages
	for _, message := range messages {
		if typed, ok := message.(types.Message); ok {
			transcript = append(transcript, typed)
		}
	}
	return transcript
}

// restoreSystemMessages re-attaches the prompt and tool state after a
// `context` handler. Handlers only see the conversation; the system messages
// belong to Pi. An unchanged conversation keeps every system message in
// place, so models with mid-conversation support keep their cached prefix. A
// changed one gets the replayed prompt sections and tool declarations as one
// leading system message, so pruning, windowing, or slicing from a compaction
// summary cannot drop them (pi: restoreSystemMessages, v0.87.0).
func restoreSystemMessages(current, visible, returned []agent.AgentMessage) []agent.AgentMessage {
	if sameAgentMessages(returned, visible) {
		return current
	}
	if head := aiutils.GetCurrentSystemMessage(transcriptOfMessages(current)); head != nil {
		return append([]agent.AgentMessage{*head}, returned...)
	}
	return returned
}

// BoundaryContextBuilder builds the would-be model context for the proposed
// drafts (pi: emitBoundary's buildContext callback). An error marks the
// preview invalid.
type BoundaryContextBuilder func(entries []SessionBoundaryDraft) (BoundaryContextPreview, error)

// BoundaryDispatchResult is the outcome of EmitBoundary (pi:
// BoundaryDispatchResult, v0.87.0).
type BoundaryDispatchResult struct {
	Entries  []SessionBoundaryDraft
	Continue bool
	Context  BoundaryContextPreview
	// Valid reports whether the last preview build succeeded; an invalid
	// dispatch drops the drafts and the continuation (pi: valid).
	Valid bool
}

// EmitBoundary dispatches one boundary event (turn_end / agent_before_settle)
// to all handlers in registration order, threading the shared draft list and
// continuation flag through every handler (pi: emitBoundary, v0.87.0).
//
// Handlers see a fresh event per dispatch carrying the current shared state;
// mutating event.Entries appends to the shared list (pi: the event holds the
// same array), and a BoundaryResult return overrides entries/continue. After
// every handler the preview is rebuilt; a build failure reports
// "Invalid boundary entries" and marks the dispatch invalid — an invalid
// final dispatch drops the drafts and the continuation. An error from the
// INITIAL preview build is returned (pi: it rejects the emitBoundary promise
// and fails the run).
func (r *ExtensionRunner) EmitBoundary(ctx context.Context, baseEvent ExtensionEvent, buildContext BoundaryContextBuilder) (BoundaryDispatchResult, error) {
	handlerCtxValue := r.CreateContext()
	handlerCtx := &handlerCtxValue
	entries := []SessionBoundaryDraft{}
	shouldContinue := false
	initialContext, err := buildContext(entries)
	if err != nil {
		return BoundaryDispatchResult{}, err
	}
	context := initialContext
	valid := true

	for _, ext := range r.extensions {
		handlers := ext.Handlers[baseEvent.EventType()]
		if len(handlers) == 0 {
			continue
		}
		for _, handler := range handlers {
			var event ExtensionEvent
			switch base := baseEvent.(type) {
			case *TurnEndEvent:
				ev := *base
				ev.BoundaryState = BoundaryState{Entries: entries, Continue: shouldContinue, Context: context}
				event = &ev
			case *AgentBeforeSettleEvent:
				ev := *base
				ev.BoundaryState = BoundaryState{Entries: entries, Continue: shouldContinue, Context: context}
				event = &ev
			default:
				return BoundaryDispatchResult{}, fmt.Errorf("emitBoundary requires a turn_end or agent_before_settle event, got %T", baseEvent)
			}
			result, handlerErr := handler(ctx, handlerCtx, event)
			// Capture in-place Entries mutations before applying overrides
			// (pi: the handler mutates the shared array by reference).
			switch ev := event.(type) {
			case *TurnEndEvent:
				entries = ev.Entries
			case *AgentBeforeSettleEvent:
				entries = ev.Entries
			}
			if handlerErr != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         baseEvent.EventType(),
					Error:         handlerErr.Error(),
				})
			} else if boundaryResult, ok := result.(*BoundaryResult); ok && boundaryResult != nil {
				if boundaryResult.Entries != nil {
					entries = boundaryResult.Entries
				}
				if boundaryResult.Continue != nil {
					shouldContinue = *boundaryResult.Continue
				}
			}

			newContext, buildErr := buildContext(entries)
			if buildErr != nil {
				valid = false
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         baseEvent.EventType(),
					Error:         fmt.Sprintf("Invalid boundary entries: %s", buildErr.Error()),
				})
			} else {
				context = newContext
				valid = true
			}
		}
	}

	if valid {
		return BoundaryDispatchResult{Entries: entries, Continue: shouldContinue, Context: context, Valid: true}, nil
	}
	return BoundaryDispatchResult{Entries: []SessionBoundaryDraft{}, Continue: false, Context: context, Valid: false}, nil
}

// EmitContext runs the request-time transforms in two phases (pi:
// emitContext, v0.87.0). `context` handlers see the conversation only and Pi
// restores the prompt and tool state after each; `context_with_system`
// handlers then see the full transcript and their output is used as returned.
func (r *ExtensionRunner) EmitContext(ctx context.Context, messages []agent.AgentMessage) []agent.AgentMessage {
	currentMessages := cloneAgentMessages(messages)

	for _, ext := range r.extensions {
		handlers := ext.Handlers["context"]
		if len(handlers) == 0 {
			continue
		}
		handlerCtxValue := r.CreateContext()
		handlerCtx := &handlerCtxValue
		for _, handler := range handlers {
			visibleMessages := make([]agent.AgentMessage, 0, len(currentMessages))
			for _, message := range currentMessages {
				if message.MessageRole() != types.RoleSystem {
					visibleMessages = append(visibleMessages, message)
				}
			}
			visibleSnapshot := slices.Clone(visibleMessages)
			event := &ContextEvent{Type: "context", Messages: visibleMessages}
			result, err := handler(ctx, handlerCtx, event)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         "context",
					Error:         err.Error(),
				})
				continue
			}
			// Handlers may return a new list or edit event.messages in place
			// (pig detects edits by comparing against the pre-handler
			// snapshot; pi's object identity also leaks in-place property
			// mutations through the shared references, which value semantics
			// cannot — declared adaptation).
			var returned []agent.AgentMessage
			if eventResult, ok := result.(*ContextEventResult); ok && eventResult != nil && eventResult.Messages != nil {
				returned = eventResult.Messages
			} else if !sameAgentMessages(event.Messages, visibleSnapshot) {
				returned = event.Messages
			}
			if returned == nil {
				continue
			}
			currentMessages = restoreSystemMessages(currentMessages, visibleSnapshot, returned)
		}
	}

	for _, ext := range r.extensions {
		handlers := ext.Handlers["context_with_system"]
		if len(handlers) == 0 {
			continue
		}
		handlerCtxValue := r.CreateContext()
		handlerCtx := &handlerCtxValue
		for _, handler := range handlers {
			hadLeadingSystemMessage := len(currentMessages) > 0 && currentMessages[0].MessageRole() == types.RoleSystem
			event := &ContextWithSystemEvent{Type: "context_with_system", Messages: currentMessages}
			result, err := handler(ctx, handlerCtx, event)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         "context_with_system",
					Error:         err.Error(),
				})
				continue
			}
			if eventResult, ok := result.(*ContextEventResult); ok && eventResult != nil && eventResult.Messages != nil {
				currentMessages = eventResult.Messages
			}
			// Providers read the prompt and initial tools from the leading
			// system message. Losing it is never intended; report it but
			// honor the handler's output.
			if hadLeadingSystemMessage && (len(currentMessages) == 0 || currentMessages[0].MessageRole() != types.RoleSystem) {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         "context_with_system",
					Error:         "Handler removed the leading system message; the request has no prompt or initial tool declarations. Keep it at index 0 or replace a dropped prefix with getCurrentSystemMessage().",
				})
			}
		}
	}

	return currentMessages
}

// EmitBeforeProviderRequest applies extension payload transforms (pi:
// emitBeforeProviderRequest). A non-nil handler result replaces the payload.
func (r *ExtensionRunner) EmitBeforeProviderRequest(ctx context.Context, payload any) any {
	currentPayload := payload

	for _, ext := range r.extensions {
		handlers := ext.Handlers["before_provider_request"]
		if len(handlers) == 0 {
			continue
		}

		handlerCtxValue := r.CreateContext()
		handlerCtx := &handlerCtxValue
		for _, handler := range handlers {
			event := &BeforeProviderRequestEvent{Type: "before_provider_request", Payload: currentPayload}
			result, err := handler(ctx, handlerCtx, event)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         "before_provider_request",
					Error:         err.Error(),
				})
				continue
			}
			if result != nil {
				currentPayload = result
			}
		}
	}

	return currentPayload
}

// EmitBeforeProviderHeaders applies extension header mutations (pi:
// emitBeforeProviderHeaders). Handlers mutate headers in place; the return
// value is ignored. A nil entry deletes that header.
func (r *ExtensionRunner) EmitBeforeProviderHeaders(ctx context.Context, headers types.ProviderHeaders) types.ProviderHeaders {
	if headers == nil {
		headers = types.ProviderHeaders{}
	}

	for _, ext := range r.extensions {
		handlers := ext.Handlers["before_provider_headers"]
		if len(handlers) == 0 {
			continue
		}

		handlerCtxValue := r.CreateContext()
		handlerCtx := &handlerCtxValue
		for _, handler := range handlers {
			event := &BeforeProviderHeadersEvent{Type: "before_provider_headers", Headers: headers}
			if _, err := handler(ctx, handlerCtx, event); err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         "before_provider_headers",
					Error:         err.Error(),
				})
				continue
			}
		}
	}

	return headers
}

// cloneAgentMessage deep-clones one message via JSON round-trip (pi:
// structuredClone).
func cloneAgentMessage(message agent.AgentMessage) agent.AgentMessage {
	data, err := json.Marshal(message)
	if err != nil {
		return message
	}
	decoded, err := types.DecodeMessage(data)
	if err != nil {
		return message
	}
	return decoded
}

// cloneAgentMessages deep-clones a message slice (pi: structuredClone).
func cloneAgentMessages(messages []agent.AgentMessage) []agent.AgentMessage {
	if messages == nil {
		return nil
	}
	out := make([]agent.AgentMessage, 0, len(messages))
	for _, message := range messages {
		out = append(out, cloneAgentMessage(message))
	}
	return out
}

// GetEntryRenderer returns the renderer for a custom entry type (pi:
// getEntryRenderer; nil = no content — extension renderers arrive with the
// sidecar host, Phase 7).
func (r *ExtensionRunner) GetEntryRenderer(customType string) EntryRenderer {
	return nil
}

// GetMessageRenderer returns the renderer for a custom message type (pi:
// getMessageRenderer; nil = default rendering).
func (r *ExtensionRunner) GetMessageRenderer(customType string) MessageRenderer {
	return nil
}
