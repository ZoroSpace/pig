// interactive_mode.go (pi: src/modes/interactive/interactive-mode.ts) —
// interactive-mode shell: startup/main-screen assembly, the transcript
// container, footer, the prompt submit/steer/queue chain, and the agent
// event → UI pipeline. Targets milestone M4 (interactive chat usable).
//
// interactive-mode.ts is the largest pi file (~6.5k lines); per the T5b
// handoff this file carries the shell subset and DECLARES the deferred
// segments (each lands with the T5b-2/T6 batches):
//   - selectors: settings/models/scoped-models/thinking/session/tree/trust/
//     login/oauth/user-message selectors and their submit-command handlers
//     (/settings /scoped-models /model /thinking /fork /clone /tree /trust
//     /login /logout /resume /session); deferred commands surface a
//     declared warning instead of a silent no-op
//   - showLoadedResources + the compact resource path-label helpers
//     (getShortPath/getCompact*/buildScopeGroups/formatScopeGroups/...)
//   - extension UI surface: widgets above/below editor, custom header/
//     footer, extension selector/input/editor/notify/error paths, extension
//     shortcuts, extension statuses, terminal-input listeners, and the
//     ExtensionUIContext factory (createExtensionUIContext) — DELIVERED
//     T5b-3 (see extension UI section)
//   - fullscreen (alt-screen) mode: createInteractiveTui's TuiAltScreen
//     branch, switchTuiMode/stopInteractiveTui/mountInteractiveTui and the
//     fullscreen layout root — DELIVERED T5b-3 (unexercised in real TTY
//     smoke; declared)
//   - changelog display, version/package/tmux startup checks, install
//     telemetry, anthropic-subscription auth warning, model-catalog refresh
//     wiring (refreshModelCatalogs call)
//   - external editor, clipboard text/image paste, right-click paste
//   - suspend (Ctrl+Z) — pi's SIGTSTP dance is Node-event-loop specific
//   - autocomplete: setupAutocompleteProvider/createBaseAutocompleteProvider
//     land with the selectors batch (editor works without a provider)
//   - custom editor component factory (EditorFactory) and the TUI reference
//     proxy (createInteractiveTuiReference) — DELIVERED T5b-3
//   - checkForPackageUpdates (P7 package manager); theme registration from
//     the resource loader: DefaultResourceLoader.GetThemes() returns nil
//     until its theme half lands; builtins and the startup theme come from
//     theme.InitTheme via the theme controller
//
// Declared deviations:
//   - pi is a single event loop; pig runs the editor submit body on a
//     serialized submit-worker goroutine (never blocks the TUI input
//     goroutine) and consumes user input from Run()'s goroutine via a
//     gate that mirrors getUserInput/pendingUserInputs/onInputCallback.
//   - pi's onSubmit is `async`; Go callbacks are synchronous (repo rule),
//     hence the worker.
//   - handleEvent runs on the session's event goroutine(s); containers are
//     only mutated here, matching pi's ownership.
//   - pi's stdout/stderr 'error' events (dead-terminal detection) have no
//     direct Go equivalent; isDeadTerminalError is kept for the TUI write
//     path and emergencyTerminalExit stays reachable from it.
package interactive

import (
	"context"
	crand "crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	harnessutils "github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/core"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
	pigutils "github.com/ZoroSpace/pig/utils"
	"github.com/samber/lo"
)

// InteractiveModeOptions configure the interactive session (pi:
// InteractiveModeOptions; deferred members carry their deferral note).
type InteractiveModeOptions struct {
	// MigratedProviders are providers migrated to auth.json (warning shown).
	MigratedProviders []string
	// StartupDiagnostics were collected before the TUI initialized.
	StartupDiagnostics []core.AgentSessionRuntimeDiagnostic
	// ModelFallbackMessage warns when the session model couldn't be
	// restored.
	ModelFallbackMessage string
	// AutoTrustOnReloadCwd is the cwd to trust after reload if it gained a
	// .pi directory during this implicitly trusted session.
	AutoTrustOnReloadCwd string
	// InitialMessage is sent on startup (can include @file content).
	InitialMessage string
	// InitialImages are attached to the initial message.
	InitialImages []types.ImageContent
	// InitialMessages are sent after the initial message.
	InitialMessages []string
	// Verbose forces verbose startup (overrides quietStartup).
	Verbose bool
	// TuiMode is the TUI layout mode ("regular" in this batch; fullscreen
	// lands with the T6 batch).
	TuiMode core.TuiMode
	// InitialThemeSetting is the initial interactive theme setting for this
	// invocation.
	InitialThemeSetting string
}

// CompactionQueuedMessage is a message queued while compaction runs (pi:
// CompactionQueuedMessage).
type CompactionQueuedMessage struct {
	Text string
	Mode string // "steer" | "followUp"
}

// CompactionCostNotice renders billing usage for a compaction or branch
// summary; derived from persisted summary usage, not stored as an entry
// (pi: CompactionCostNotice).
type CompactionCostNotice struct {
	Kind  string // "compaction" | "branch_summary"
	Usage types.Usage
}

// renderSessionItem is one transcript item (pi: RenderSessionItem =
// AgentMessage | custom SessionEntry | CompactionCostNotice).
type renderSessionItem struct {
	message     agent.AgentMessage
	customEntry *core.CustomEntry
	costNotice  *CompactionCostNotice
}

var deadTerminalErrorCodes = map[string]bool{"EIO": true, "EPIPE": true, "ENOTCONN": true}

// isDeadTerminalError reports whether the error means the terminal is gone
// (pi: isDeadTerminalError).
func isDeadTerminalError(err error) bool {
	if err == nil {
		return false
	}
	code := utils.ErrnoCode(err)
	return code != "" && deadTerminalErrorCodes[code]
}

// expandable is the setExpanded capability (pi: Expandable interface).
type expandable interface {
	SetExpanded(expanded bool)
}

func asExpandable(component tui.Component) expandable {
	if e, ok := component.(expandable); ok {
		return e
	}
	return nil
}

// expandableText switches between collapsed and expanded content (pi:
// ExpandableText extends Text).
type expandableText struct {
	*tui.Text
	getCollapsedText func() string
	getExpandedText  func() string
}

func newExpandableText(getCollapsedText, getExpandedText func() string, expanded bool, paddingX, paddingY int) *expandableText {
	text := tui.NewText(cond(expanded, getExpandedText, getCollapsedText)(), paddingX, paddingY)
	return &expandableText{
		Text:             text,
		getCollapsedText: getCollapsedText,
		getExpandedText:  getExpandedText,
	}
}

func (e *expandableText) SetExpanded(expanded bool) {
	e.SetText(cond(expanded, e.getExpandedText, e.getCollapsedText)())
}

func cond[T any](flag bool, a, b func() T) func() T {
	return func() T {
		if flag {
			return a()
		}
		return b()
	}
}

// statusIndicatorItem is the status-indicator surface the shell consumes
// (pig: components.StatusIndicator/RetryStatusIndicator expose Kind as a
// field; IndicatorKind() is the promoted accessor).
type statusIndicatorItem interface {
	tui.Component
	Dispose()
	IndicatorKind() components.StatusIndicatorKind
}

// InteractiveMode is the TUI application (pi: class InteractiveMode).
type InteractiveMode struct {
	runtimeHost *core.AgentSessionRuntime
	// renderer is the active renderer (pi: TuiMainScreen | TuiAltScreen →
	// tui.TUI; concrete via type assertions).
	renderer tui.TUI
	// ui is the stable component-facing reference (pi:
	// createInteractiveTuiReference proxy).
	ui tui.TUI

	loadedResourcesContainer *tui.Container
	headerContainer          *tui.Container
	chatContainer            *tui.Container
	documentContainer        *tui.Container
	pendingMessagesContainer *tui.Container
	statusContainer          *tui.Container
	// widget containers for extension widgets (pi: widgetContainerAbove/Below).
	widgetContainerAbove *tui.Container
	widgetContainerBelow *tui.Container

	defaultEditor *components.CustomEditor
	// editor is the active editor component (pi: EditorComponent). Custom
	// editors replace it (setCustomEditorComponent); capability methods go
	// through the tui.EditorComponent* interfaces.
	editor tui.EditorComponent

	editorContainer *tui.Container

	builtInHeader tui.Component
	// customHeader is the extension-provided header (pi: customHeader; the
	// dispose function is pi's component.dispose?).
	customHeader        tui.Component
	customHeaderDispose func()

	footer                 *components.FooterComponent
	footerContainer        *tui.Container
	footerDataProviderView *core.FooterDataProvider
	// customFooter is the extension-provided footer (pi: customFooter).
	customFooter tui.Component

	// keybindings is stored so the same manager can be injected into custom
	// editors, selectors, and extension UI.
	keybindings *core.KeybindingsManager
	version     string

	isInitialized bool
	inputGate     userInputGate

	activeStatusIndicator   statusIndicatorItem
	idleStatus              *components.IdleStatus
	workingMessage          *string
	workingVisible          bool
	workingIndicatorOptions *core.WorkingIndicatorOptions
	defaultWorkingMessage   string
	// activeWorkingIndicatorEmbedded reports whether the working indicator is
	// embedded in the editor top border instead of the status row (pi:
	// activeWorkingIndicatorEmbedded, v0.85.1).
	activeWorkingIndicatorEmbedded bool
	defaultHiddenThinkingLabel     string
	hiddenThinkingLabel            string

	lastSigintTime      time.Time
	startupNoticesShown bool

	// Status line tracking (for mutating immediately-sequential status
	// updates)
	lastStatusSpacer         *tui.Spacer
	lastStatusText           *tui.Text
	managedToolStatusStarted bool

	// Streaming message tracking
	streamingComponent *components.AssistantMessageComponent
	streamingMessage   *types.AssistantMessage
	streamingMu        sync.Mutex

	// Tool execution tracking: toolCallId -> component
	pendingTools map[string]*components.ToolExecutionComponent

	// Tool output expansion state
	toolOutputExpanded bool

	// Thinking block visibility state
	hideThinkingBlock bool
	outputPad         int

	// Agent subscription unsubscribe function
	unsubscribe           func()
	signalCleanupHandlers []func()

	// Track if editor is in bash mode (text starts with !)
	isBashMode bool

	// Track current bash execution component
	bashComponent *components.BashExecutionComponent

	// Track pending bash components (shown in pending area, moved to chat on
	// submit)
	pendingBashComponents []*components.BashExecutionComponent

	// Messages queued while compaction is running
	compactionQueuedMessages []CompactionQueuedMessage

	// Shutdown state
	shutdownRequested bool
	isShuttingDown    bool

	options              InteractiveModeOptions
	autoTrustOnReloadCwd string
	themeController      *theme.InteractiveThemeController

	// fdPath is the resolved fd binary path used by the autocomplete
	// provider for file completion (pi: fdPath).
	fdPath string
	// changelogMarkdown holds the new changelog entries for the startup
	// notice (pi: changelogMarkdown).
	changelogMarkdown string
	// autocompleteProvider is the editor's active provider (pi:
	// autocompleteProvider); wrappers are pi's
	// autocompleteProviderWrappers (the extension factory surface lands
	// with T5b-3, see header).
	autocompleteProvider         tui.AutocompleteProvider
	autocompleteProviderWrappers []core.AutocompleteProviderFactory
	// skillCommands maps "skill:<name>" to the skill file path (pi:
	// skillCommands).
	skillCommands map[string]string

	// submitCh feeds the serialized submit worker (pig concurrency note).
	submitCh   chan string
	submitDone chan struct{}

	// abortMu guards escape-handler swap fields read/written from the input
	// goroutine and event goroutine.
	abortMu                     sync.Mutex
	retryEscapeHandler          func()
	autoCompactionEscapeHandler func()

	// lastEscapeTime tracks the double-escape gesture (pi: lastEscapeTime;
	// input-goroutine only, no lock needed).
	lastEscapeTime int64

	// Active in-place selector (pi: activeSelectorToken/activeSelectorDispose;
	// the showSelector callers — model/session/tree/settings selectors — land
	// with T6).
	activeSelectorToken   any
	activeSelectorDispose func()

	// Extension UI state (pi: extensionWidgetsAbove/Below,
	// extensionSelector/extensionInput/extensionEditor, custom editors,
	// terminal input subscriptions).
	extensionWidgetsAbove               map[string]extensionWidget
	extensionWidgetsBelow               map[string]extensionWidget
	extensionSelector                   *components.ExtensionSelectorComponent
	extensionInput                      *components.ExtensionInputComponent
	extensionEditor                     *components.ExtensionEditorComponent
	editorComponentFactory              core.EditorFactory
	extensionTerminalInputSubscriptions []*extensionTerminalInputSubscription
	// autocompleteProviderWrappers is declared in the autocomplete block above.

	// Fullscreen renderer state (pi: transcriptScrollView,
	// fullscreenLayoutRoot, mainScreenRenderState).
	transcriptScrollView     *tui.ScrollView
	fullscreenLayoutRoot     tui.Component
	mainScreenRenderState    tui.TuiMainScreenRenderState
	hasMainScreenRenderState bool
}

// extensionWidget is one extension-registered widget component (pi:
// `Component & {dispose?}`).
type extensionWidget struct {
	component tui.Component
	dispose   func()
}

// extensionTerminalInputSubscription is one raw-input listener (pi:
// { handler, unsubscribe }).
type extensionTerminalInputSubscription struct {
	handler     func(data string) *tui.TuiInputListenerResult
	unsubscribe func()
}

// userInputGate mirrors pi's pendingUserInputs + onInputCallback pair
// (pig concurrency note).
type userInputGate struct {
	mu      sync.Mutex
	pending []string
	waiter  chan string
}

func (g *userInputGate) submit(text string) {
	g.mu.Lock()
	if g.waiter != nil {
		ch := g.waiter
		g.waiter = nil
		g.mu.Unlock()
		ch <- text
		return
	}
	g.pending = append(g.pending, text)
	g.mu.Unlock()
}

func (g *userInputGate) wait() string {
	g.mu.Lock()
	if len(g.pending) > 0 {
		text := g.pending[0]
		g.pending = g.pending[1:]
		g.mu.Unlock()
		return text
	}
	ch := make(chan string, 1)
	g.waiter = ch
	g.mu.Unlock()
	return <-ch
}

// NewInteractiveMode builds the mode (pi: constructor).
func NewInteractiveMode(runtimeHost *core.AgentSessionRuntime, options InteractiveModeOptions) *InteractiveMode {
	m := &InteractiveMode{
		runtimeHost:          runtimeHost,
		version:              codingagent.Version,
		pendingTools:         map[string]*components.ToolExecutionComponent{},
		options:              options,
		autoTrustOnReloadCwd: options.AutoTrustOnReloadCwd,
		submitCh:             make(chan string, 64),
		submitDone:           make(chan struct{}),
		skillCommands:        map[string]string{},
	}
	m.defaultWorkingMessage = "Working"
	// Session replacement hooks (pi: constructor's setBeforeSessionInvalidate
	// / setRebindSession; resetExtensionUI lands with T5b-3 and is a no-op
	// until the extension UI surface exists).
	runtimeHost.SetBeforeSessionInvalidate(func() { m.resetExtensionUI() })
	runtimeHost.SetRebindSession(func(session *core.AgentSession) error {
		if err := m.rebindCurrentSession(true); err != nil {
			return err
		}
		return m.themeController.ApplyFromSettings()
	})
	m.idleStatus = &components.IdleStatus{}
	m.workingVisible = true
	m.hiddenThinkingLabel = m.defaultHiddenThinkingLabel
	m.outputPad = 1

	session := m.session()
	settingsManager := session.SettingsManager

	tuiMode := options.TuiMode
	if tuiMode == "" {
		tuiMode = settingsManager.GetTuiMode()
	}
	m.options.TuiMode = tuiMode

	// pi: setCapabilityOverrides(this.settingsManager.getTerminalCapabilityOverrides())
	// — user-forced terminal capabilities take effect from construction on.
	tui.SetCapabilityOverrides(settingsManager.GetTerminalCapabilityOverrides())

	m.renderer = createInteractiveTui(createInteractiveTuiOptions{
		tuiMode:                tuiMode,
		showHardwareCursor:     settingsManager.GetShowHardwareCursor(),
		logDirectory:           codingagent.GetAgentDir(),
		onRightClickPaste:      m.handleRightClickPaste,
		fullscreenCopyOnSelect: settingsManager.GetFullscreenCopyOnSelect(),
	})
	m.ui = createInteractiveTuiReference(func() tui.TUI { return m.renderer })
	m.ui.SetClearOnShrink(settingsManager.GetClearOnShrink())

	m.headerContainer = &tui.Container{}
	m.loadedResourcesContainer = &tui.Container{}
	m.chatContainer = &tui.Container{}
	m.documentContainer = &tui.Container{}
	m.documentContainer.AddChild(m.headerContainer)
	m.documentContainer.AddChild(m.loadedResourcesContainer)
	m.documentContainer.AddChild(m.chatContainer)
	m.pendingMessagesContainer = &tui.Container{}
	m.statusContainer = &tui.Container{}
	m.widgetContainerAbove = &tui.Container{}
	m.widgetContainerBelow = &tui.Container{}

	m.keybindings = core.CreateKeybindingsManager("")
	tui.SetKeybindings(m.keybindings.KeybindingsManager)
	editorPaddingX := settingsManager.GetEditorPaddingX()
	autocompleteMaxVisible := settingsManager.GetAutocompleteMaxVisible()
	m.defaultEditor = components.NewCustomEditor(m.ui, theme.GetEditorTheme(), m.keybindings, &components.CustomEditorOptions{
		EditorOptions: tui.EditorOptions{
			PaddingX:               int(editorPaddingX),
			AutocompleteMaxVisible: lo.ToPtr(int(autocompleteMaxVisible)),
		},
		EmbedWorkingStatus: true,
	})
	m.editor = m.defaultEditor
	m.editorContainer = &tui.Container{}
	m.editorContainer.AddChild(m.editor)

	m.extensionWidgetsAbove = map[string]extensionWidget{}
	m.extensionWidgetsBelow = map[string]extensionWidget{}

	m.footerDataProviderView = core.NewFooterDataProvider(session.SessionManager.GetCwd())
	m.footer = components.NewFooterComponent(session, m.footerDataProviderView)
	m.footer.SetAutoCompactEnabled(session.AutoCompactionEnabled())
	m.footerContainer = &tui.Container{}
	m.footerContainer.AddChild(m.footer)

	// Load hide thinking block setting
	m.hideThinkingBlock = settingsManager.GetHideThinkingBlock()
	m.outputPad = settingsManager.GetOutputPad()

	m.themeController = theme.NewInteractiveThemeController(m.ui, theme.InteractiveThemeControllerOptions{
		GetSettingsManager:  func() theme.ThemeSettingsManager { return session.SettingsManager },
		ShowError:           func(message string) { m.ShowError(message) },
		OnChanged:           func() { m.updateEditorBorderColor() },
		InitialThemeSetting: options.InitialThemeSetting,
	})

	return m
}

// createInteractiveTuiOptions mirror pi's InteractiveTuiOptions.
// (createInteractiveTui moved to tui_renderer.go, v0.85.1 extraction; see
// that file for the composition root.)

func (m *InteractiveMode) session() *core.AgentSession          { return m.runtimeHost.Session() }
func (m *InteractiveMode) agent() *agent.Agent                  { return m.session().Agent }
func (m *InteractiveMode) sessionManager() *core.SessionManager { return m.session().SessionManager }

// containsString reports whether the list contains v (pi: includes).
func containsString(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
func (m *InteractiveMode) settingsManager() *core.SettingsManager {
	return m.session().SettingsManager
}

// editorAddToHistory adds text to the active editor's history (pi:
// addToHistory optional member via EditorComponentHistory).
func (m *InteractiveMode) editorAddToHistory(text string) {
	if h, ok := m.editor.(tui.EditorComponentHistory); ok {
		h.AddToHistory(text)
	}
}

// editorGetExpandedText returns the expanded text (pi: getExpandedText?.()
// ?? getText() via EditorComponentExpandedText).
func (m *InteractiveMode) editorGetExpandedText() string {
	if h, ok := m.editor.(tui.EditorComponentExpandedText); ok {
		return h.GetExpandedText()
	}
	return m.editor.GetText()
}

// editorInsertTextAtCursor inserts at the cursor (pi: insertTextAtCursor?
// via EditorComponentTextInsertion).
func (m *InteractiveMode) editorInsertTextAtCursor(text string) {
	if h, ok := m.editor.(tui.EditorComponentTextInsertion); ok {
		h.InsertTextAtCursor(text)
	}
}

// Init initializes the UI (pi: init).
func (m *InteractiveMode) Init() error {
	if m.isInitialized {
		return nil
	}

	m.registerSignalHandlers()

	// Load changelog (only show new entries, skip for resumed sessions) (pi:
	// init's changelog load).
	m.changelogMarkdown = m.getChangelogForDisplay()

	// Model scope notice (pi: console.log before the TUI starts).
	if scoped := m.session().ScopedModels(); len(scoped) > 0 && (m.options.Verbose || !m.settingsManager().GetQuietStartup()) {
		modelList := make([]string, 0, len(scoped))
		for _, sm := range scoped {
			entry := sm.Model.ID
			if sm.ThinkingLevel != "" {
				entry += ":" + string(sm.ThinkingLevel)
			}
			modelList = append(modelList, entry)
		}
		cycleHint := ""
		if cycleKeys := m.keybindings.GetKeys(core.AppKeybindingModelCycleForward); len(cycleKeys) > 0 {
			keyTexts := make([]string, 0, len(cycleKeys))
			for _, k := range cycleKeys {
				keyTexts = append(keyTexts, string(k))
			}
			hint := coretools.FormatKeyText(strings.Join(keyTexts, "/"), coretools.KeyTextFormatOptions{Capitalize: true})
			cycleHint = theme.Current().Fg("muted", fmt.Sprintf(" (%s to cycle)", hint))
		}
		fmt.Println(theme.Current().Fg("dim", fmt.Sprintf("Model scope: %s%s", strings.Join(modelList, ", "), cycleHint)))
	}

	// Accept text while startup completes, but only enable interrupt, exit,
	// and submission feedback.
	m.defaultEditor.OnAction(core.AppKeybindingClear, func() { m.handleCtrlC() })
	m.defaultEditor.OnCtrlD = func() { m.handleCtrlD() }
	m.defaultEditor.OnSubmit = func(text string) { m.handleStartupSubmit(text) }

	// Keep one component tree and remount it when changing renderers (pi:
	// init's mountInteractiveTui block).
	m.renderWidgets() // Initialize with default spacer
	if m.options.TuiMode == core.TuiModeFullscreen {
		scrollbar := m.settingsManager().GetFullscreenScrollbar()
		viewport := createChatViewport(chatViewportOptions{
			document:        m.documentContainer,
			pendingMessages: m.pendingMessagesContainer,
			status:          m.statusContainer,
			widgetsAbove:    m.widgetContainerAbove,
			editor:          m.editorContainer,
			widgetsBelow:    m.widgetContainerBelow,
			footer:          m.footerContainer,
			scrollbar:       &scrollbar,
		})
		m.transcriptScrollView = viewport.transcript
		m.fullscreenLayoutRoot = viewport.root
	}
	m.mountInteractiveTui(m.renderer, []tui.Component{
		m.documentContainer,
		m.pendingMessagesContainer,
		m.statusContainer,
		m.widgetContainerAbove,
		m.editorContainer,
		m.widgetContainerBelow,
		m.footerContainer,
	})
	m.ui.SetFocus(m.defaultEditor)

	// Start the UI before initializing extensions so session_start handlers
	// can use interactive dialogs
	m.ui.Start()
	m.isInitialized = true

	if err := m.themeController.ApplyFromSettings(); err != nil {
		return err
	}

	// Add header with keybindings from config (unless silenced)
	if m.options.Verbose || !m.settingsManager().GetQuietStartup() {
		currentTheme := theme.Current()
		logo := currentTheme.Bold(currentTheme.Fg("accent", codingagent.AppName)) +
			currentTheme.Fg("dim", " v"+m.version)

		// Build startup instructions using keybinding hint helpers
		hint := func(keybinding string, description string) string {
			return coretools.KeyHint(keybinding, description)
		}

		expandedInstructions := strings.Join([]string{
			hint(core.AppKeybindingInterrupt, "to interrupt"),
			hint(core.AppKeybindingClear, "to clear"),
			coretools.RawKeyHint(coretools.KeyText(core.AppKeybindingClear)+" twice", "to exit"),
			hint(core.AppKeybindingExit, "to exit (empty)"),
			hint(core.AppKeybindingSuspend, "to suspend"),
			hint("tui.editor.deleteToLineEnd", "to delete to end"),
			hint(core.AppKeybindingThinkingCycle, "to cycle thinking level"),
			coretools.RawKeyHint(coretools.KeyText(core.AppKeybindingModelCycleForward)+"/"+
				coretools.KeyText(core.AppKeybindingModelCycleBackward), "to cycle models"),
			hint(core.AppKeybindingModelSelect, "to select model"),
			hint(core.AppKeybindingToolsExpand, "to expand tools"),
			hint(core.AppKeybindingThinkingToggle, "to expand thinking"),
			hint(core.AppKeybindingEditorExternal, "for external editor"),
			coretools.RawKeyHint("/", "for commands"),
			coretools.RawKeyHint("!", "to run bash"),
			coretools.RawKeyHint("!!", "to run bash (no context)"),
			hint(core.AppKeybindingMessageFollowUp, "to queue follow-up"),
			hint(core.AppKeybindingMessageDequeue, "to edit all queued messages"),
			hint(core.AppKeybindingClipboardPasteImage, "to paste image (with text fallback)"),
			coretools.RawKeyHint("drop files", "to attach"),
		}, "\n")
		compactInstructions := strings.Join([]string{
			hint(core.AppKeybindingInterrupt, "interrupt"),
			coretools.RawKeyHint(coretools.KeyText(core.AppKeybindingClear)+"/"+
				coretools.KeyText(core.AppKeybindingExit), "clear/exit"),
			coretools.RawKeyHint("/", "commands"),
			coretools.RawKeyHint("!", "bash"),
			hint(core.AppKeybindingToolsExpand, "more"),
		}, currentTheme.Fg("muted", " · "))
		compactOnboarding := currentTheme.Fg("dim",
			"Press "+coretools.KeyText(core.AppKeybindingToolsExpand)+" to show full startup help and loaded resources.")
		onboarding := currentTheme.Fg("dim",
			"Pi can explain its own features and look up its docs. Ask it how to use or extend Pi.")
		builtInHeader := newExpandableText(
			func() string {
				return logo + "\n" + compactInstructions + "\n" + compactOnboarding + "\n\n" + onboarding
			},
			func() string { return logo + "\n" + expandedInstructions + "\n\n" + onboarding },
			m.getStartupExpansionState(),
			1,
			0,
		)
		m.builtInHeader = builtInHeader

		// Setup UI layout (pi: headerContainer gets spacer/header/spacer)
		m.headerContainer.AddChild(tui.NewSpacer(1))
		m.headerContainer.AddChild(builtInHeader)
		m.headerContainer.AddChild(tui.NewSpacer(1))
	} else {
		// Minimal header when silenced
		builtInHeader := tui.NewText("", 0, 0)
		m.builtInHeader = builtInHeader
		m.headerContainer.AddChild(builtInHeader)
	}
	m.ui.RequestRender(false)

	// Ensure fd and rg are available after mounting the TUI (downloads if
	// missing, adds to PATH via getBinDir) so slow downloads do not make
	// startup appear frozen. Both are needed: fd for autocomplete, rg for
	// the grep tool and bash commands.
	m.fdPath = utils.EnsureTool(context.Background(), "fd", func(status utils.ToolStatus) { m.showManagedToolStatus(status) })
	utils.EnsureTool(context.Background(), "rg", func(status utils.ToolStatus) { m.showManagedToolStatus(status) })

	// Enable the remaining input handlers only after managed-tool setup
	// completes.
	m.setupKeyHandlers()
	m.startSubmitWorker()
	m.setupSubmitHandler()
	m.ui.RequestRender(false)

	// Render initial messages, then subscribe the UI to session events
	// (pi: rebindCurrentSession inside init; bindCurrentSessionExtensions'
	// extension half lands with Phase 7).
	if err := m.rebindCurrentSession(false); err != nil {
		return err
	}
	m.renderInitialMessages()

	// Set up theme file watcher
	theme.OnThemeChange(func() {
		m.ui.Invalidate()
		m.updateEditorBorderColor()
		m.ui.RequestRender(false)
	})

	// Set up git branch watcher (uses provider instead of footer)
	m.footerDataProviderView.OnBranchChange(func() {
		m.ui.RequestRender(false)
	})

	// Initialize available provider count for footer display
	m.updateAvailableProviderCount()

	// Flush the completed startup state.
	// pi calls loadAllHighlightLanguages() here to lazy-load highlight.js
	// grammars; chroma embeds its lexers, so there is nothing to load
	// (declared deviation).
	m.ui.RenderNow(false)

	return nil
}

// updateTerminalTitle updates the terminal title with session name and cwd
// (pi: updateTerminalTitle).
func (m *InteractiveMode) updateTerminalTitle() {
	cwdBasename := filepath.Base(m.sessionManager().GetCwd())
	sessionName, ok := m.sessionManager().GetSessionName()
	if ok {
		m.ui.Terminal().SetTitle(codingagent.AppTitle + " - " + sessionName + " - " + cwdBasename)
	} else {
		m.ui.Terminal().SetTitle(codingagent.AppTitle + " - " + cwdBasename)
	}
}

// Run runs the interactive mode: initializes the UI, shows warnings,
// processes initial messages, and starts the interactive loop (pi: run).
func (m *InteractiveMode) Run(ctx context.Context) error {
	if err := m.Init(); err != nil {
		return err
	}

	// Refresh model catalogs asynchronously (pi: run's refreshModelCatalogs
	// block; errors are swallowed).
	if os.Getenv("PI_OFFLINE") == "" {
		go func() {
			// pig panic containment (see docs/feat/PANIC-RESILIENCE.md).
			defer pigutils.RecoverPanic("interactive.catalogRefresh")
			refreshCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, _ = RefreshModelCatalogs(refreshCtx, m.session().GetModelRuntime())
			m.updateAvailableProviderCount()
		}()
	}

	// pig disables pi's update-version check (pi: run's checkForNewPiVersion):
	// the check queries pi.dev for PI's latest release and compares it against
	// pig's pinned baseline version — two unrelated products. The resulting
	// "New version available. Run pig update" banner is meaningless for pig
	// (a static Go binary with no npm distribution / self-update channel;
	// self-update arrives with the P8 packaging batch). Re-enable when pig
	// has its own release channel, pointing at pig's endpoint instead.
	// go func() {
	// 	if release := utils.CheckForNewPiVersion(context.Background(), m.version); release != nil {
	// 		m.showNewVersionNotification(release)
	// 	}
	// }()

	// pi's package update check lands with the package manager (P7) and the
	// tmux keyboard setup check with T6 (declared).
	go func() {
		// pig panic containment (see docs/feat/PANIC-RESILIENCE.md).
		defer pigutils.RecoverPanic("interactive.tmuxCheck")
		if warning := m.checkTmuxKeyboardSetup(context.Background()); warning != "" {
			m.ShowWarning(warning)
		}
	}()

	// Show startup warnings (pi also runs version/package/tmux checks,
	// catalog refresh, and the anthropic-subscription warning here —
	// deferred with their batches).
	for _, diagnostic := range m.options.StartupDiagnostics {
		switch diagnostic.Type {
		case "error":
			m.ShowError(diagnostic.Message)
		case "warning":
			m.ShowWarning(diagnostic.Message)
		default:
			m.showStatus(diagnostic.Message)
		}
	}

	if len(m.options.MigratedProviders) > 0 {
		m.ShowWarning("Migrated credentials to auth.json: " + strings.Join(m.options.MigratedProviders, ", "))
	}

	if modelFallback := m.options.ModelFallbackMessage; modelFallback != "" {
		m.ShowWarning(modelFallback)
	}

	// Process initial messages
	if m.options.InitialMessage != "" {
		if err := m.session().Prompt(ctx, m.options.InitialMessage, &core.PromptOptions{Images: m.options.InitialImages}); err != nil {
			m.ShowError(errorMessageOf(err))
		}
	}

	for _, message := range m.options.InitialMessages {
		if err := m.session().Prompt(ctx, message, nil); err != nil {
			m.ShowError(errorMessageOf(err))
		}
	}

	// Main interactive loop
	for {
		userInput := m.getUserInput()
		if err := m.session().Prompt(ctx, userInput, nil); err != nil {
			m.ShowError(errorMessageOf(err))
		}
	}
}

func errorMessageOf(err error) string {
	if err == nil {
		return "Unknown error occurred"
	}
	return err.Error()
}

// getStartupExpansionState reads the startup expansion setting (pi:
// getStartupExpansionState — options.verbose || toolOutputExpanded).
func (m *InteractiveMode) getStartupExpansionState() bool {
	return m.options.Verbose || m.toolOutputExpanded
}

// setupKeyHandlers registers the editor-level app handlers (pi:
// setupKeyHandlers; model/session/tree selector actions and paste hooks are
// deferred with their batches).
func (m *InteractiveMode) setupKeyHandlers() {
	m.defaultEditor.OnEscape = func() {
		if m.session().IsStreaming() {
			m.restoreQueuedMessagesToEditor(true)
		} else if m.session().IsBashRunning() {
			m.session().AbortBash()
		} else if m.isBashMode {
			m.editor.SetText("")
			m.isBashMode = false
			m.updateEditorBorderColor()
		} else if strings.TrimSpace(m.editor.GetText()) == "" {
			// Double-escape with empty editor triggers /tree, /fork, or nothing based on setting
			action := m.settingsManager().GetDoubleEscapeAction()
			if action != "none" {
				now := time.Now().UnixMilli()
				if now-m.lastEscapeTime < 500 {
					if action == "tree" {
						m.showTreeSelector("")
					} else {
						m.showUserMessageSelector()
					}
					m.lastEscapeTime = 0
				} else {
					m.lastEscapeTime = now
				}
			}
		}
	}

	m.defaultEditor.OnAction(core.AppKeybindingClear, func() { m.handleCtrlC() })
	m.defaultEditor.OnCtrlD = func() { m.handleCtrlD() }
	m.defaultEditor.OnAction(core.AppKeybindingThinkingCycle, func() { m.cycleThinkingLevel() })
	m.defaultEditor.OnAction(core.AppKeybindingModelCycleForward, func() { go m.cycleModel("forward") })
	m.defaultEditor.OnAction(core.AppKeybindingModelCycleBackward, func() { go m.cycleModel("backward") })
	m.defaultEditor.OnAction(core.AppKeybindingToolsExpand, func() { m.toggleToolOutputExpansion() })
	m.defaultEditor.OnAction(core.AppKeybindingThinkingToggle, func() { m.toggleThinkingBlockVisibility() })
	m.defaultEditor.OnAction(core.AppKeybindingEditorExternal, func() { go m.handleOpenExternalEditor(context.Background()) })
	m.defaultEditor.OnAction(core.AppKeybindingMessageCopy, func() { go m.handleCopyCommand(true, true) })
	m.defaultEditor.OnAction(core.AppKeybindingMessageFollowUp, func() { m.handleFollowUp() })
	m.defaultEditor.OnAction(core.AppKeybindingMessageDequeue, func() { m.handleDequeue() })
	m.defaultEditor.OnAction(core.AppKeybindingSessionNew, func() { go m.handleClearCommand() })
	// pi: app.session.tree → showTreeSelector / app.model.select →
	// showModelSelector / app.session.resume → showSessionSelector.
	m.defaultEditor.OnAction(core.AppKeybindingSessionFork, func() { m.showUserMessageSelector() })
	m.defaultEditor.OnAction(core.AppKeybindingModelSelect, func() { m.showModelSelector("") })
	m.defaultEditor.OnAction(core.AppKeybindingSessionTree, func() { m.showTreeSelector("") })
	m.defaultEditor.OnAction(core.AppKeybindingSessionResume, func() { m.showSessionSelector() })

	// Handle clipboard paste (triggered on Ctrl+V). Images are attached by
	// path; otherwise, paste plain text from the system clipboard (pi:
	// setupKeyHandlers' onPasteImage wiring; right-click paste is deferred —
	// pig's TuiMainScreen has no right-click hook yet).
	m.defaultEditor.OnPasteImage = func() {
		go m.handleClipboardPaste()
	}

	m.defaultEditor.OnChange = func(text string) {
		wasBashMode := m.isBashMode
		m.isBashMode = strings.HasPrefix(strings.TrimLeft(text, " \t"), "!")
		if wasBashMode != m.isBashMode {
			m.updateEditorBorderColor()
		}
	}
}

// handleStartupSubmit handles a prompt submitted while startup is still in
// progress (pi: handleStartupSubmit).
func (m *InteractiveMode) handleStartupSubmit(text string) {
	m.editor.SetText(text)
	m.showStatus("Startup is still in progress")
}

// startSubmitWorker launches the serialized submit worker (pig concurrency
// note; pi runs the onSubmit body on the event loop).
func (m *InteractiveMode) startSubmitWorker() {
	go func() {
		defer close(m.submitDone)
		ctx := context.Background()
		for text := range m.submitCh {
			// pig panic containment: a panic in submit processing (command
			// expansion, session mutation) is logged and contained; the
			// worker keeps serving the queue.
			// See docs/feat/PANIC-RESILIENCE.md.
			pigutils.Guard("interactive.submitWorker", func() { m.processSubmitText(ctx, text) })
		}
	}()
}

// setupSubmitHandler wires the editor submit callback to the submit worker
// (pi: setupEditorSubmitHandler registration half).
func (m *InteractiveMode) setupSubmitHandler() {
	m.defaultEditor.OnSubmit = func(text string) {
		m.submitCh <- text
	}
}

// processSubmitText is pi's setupEditorSubmitHandler onSubmit body (the
// registration half stays in setupSubmitHandler).
func (m *InteractiveMode) processSubmitText(ctx context.Context, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	// Handle commands (pi routes each branch to a handler; deferred
	// selectors surface a declared warning).
	switch {
	case text == "/quit":
		m.editor.SetText("")
		_ = m.shutdown(false)
		return
	case text == "/new":
		m.editor.SetText("")
		m.handleClearCommand()
		return
	case text == "/compact" || strings.HasPrefix(text, "/compact "):
		customInstructions := ""
		if strings.HasPrefix(text, "/compact ") {
			customInstructions = strings.TrimSpace(text[len("/compact "):])
		}
		m.editor.SetText("")
		m.handleCompactCommand(customInstructions)
		return
	case text == "/name" || strings.HasPrefix(text, "/name "):
		m.handleNameCommand(text)
		m.editor.SetText("")
		return
	case text == "/settings":
		m.editor.SetText("")
		m.showSettingsSelector()
		return
	case text == "/fork":
		m.editor.SetText("")
		m.showUserMessageSelector()
		return
	case text == "/trust":
		m.editor.SetText("")
		m.showTrustSelector()
		return
	case text == "/login" || strings.HasPrefix(text, "/login "):
		providerRef := ""
		if strings.HasPrefix(text, "/login ") {
			providerRef = strings.TrimSpace(text[len("/login "):])
		}
		m.editor.SetText("")
		m.handleLoginCommand(providerRef)
		return
	case text == "/logout":
		m.editor.SetText("")
		m.showOAuthSelector("logout")
		return
	case text == "/tree":
		m.editor.SetText("")
		m.showTreeSelector("")
		return
	case text == "/resume":
		m.editor.SetText("")
		m.showSessionSelector()
		return
	case text == "/scoped-models":
		m.editor.SetText("")
		m.showModelsSelector()
		return
	case text == "/model" || strings.HasPrefix(text, "/model "):
		searchTerm := ""
		if strings.HasPrefix(text, "/model ") {
			searchTerm = strings.TrimSpace(text[len("/model "):])
		}
		m.editor.SetText("")
		go m.handleModelCommand(searchTerm)
		return
	case text == "/thinking" || strings.HasPrefix(text, "/thinking "):
		searchTerm := ""
		if strings.HasPrefix(text, "/thinking ") {
			searchTerm = strings.TrimSpace(text[len("/thinking "):])
		}
		m.editor.SetText("")
		m.handleThinkingCommand(searchTerm)
		return
	case strings.HasPrefix(text, "/"):
		// Deferred command branches (export/import/share/copy/session/
		// changelog/hotkeys/clone/debug and easter eggs): handlers land with
		// their batches.
		commandName := strings.TrimPrefix(text, "/")
		if space := strings.Index(commandName, " "); space >= 0 {
			commandName = commandName[:space]
		}
		m.ShowWarning("/" + commandName + " is not yet available (interactive selectors land in a later batch)")
		m.editor.SetText("")
		return
	}

	// Handle bash command (! for normal, !! for excluded from context)
	if strings.HasPrefix(text, "!") {
		isExcluded := strings.HasPrefix(text, "!!")
		offset := 1
		if isExcluded {
			offset = 2
		}
		command := strings.TrimSpace(text[offset:])
		if command != "" {
			if m.session().IsBashRunning() {
				m.ShowWarning("A bash command is already running. Press Esc to cancel it first.")
				m.editor.SetText(text)
				return
			}
			m.editorAddToHistory(text)
			m.handleBashCommand(ctx, command, isExcluded)
			m.isBashMode = false
			m.updateEditorBorderColor()
			return
		}
	}

	// Queue input during compaction (extension commands execute immediately)
	if m.session().IsCompacting() {
		if m.isExtensionCommand(text) {
			m.editorAddToHistory(text)
			m.editor.SetText("")
			_ = m.session().Prompt(ctx, text, nil)
		} else {
			m.queueCompactionMessage(text, "steer")
		}
		return
	}

	// If streaming, use prompt() with steer behavior. This handles extension
	// commands (execute immediately), prompt template expansion, and
	// queueing.
	if m.session().IsStreaming() {
		m.editorAddToHistory(text)
		m.editor.SetText("")
		_ = m.session().Prompt(ctx, text, &core.PromptOptions{StreamingBehavior: "steer"})
		m.updatePendingMessagesDisplay()
		m.ui.RequestRender(false)
		return
	}

	// Normal message submission. First, move any pending bash components to
	// chat.
	m.flushPendingBashComponents()

	m.inputGate.submit(text)
	m.editorAddToHistory(text)
}

// handleNameCommand sets the session name (pi: handleNameCommand).
func (m *InteractiveMode) handleNameCommand(text string) {
	name := ""
	if strings.HasPrefix(text, "/name ") {
		name = strings.TrimSpace(text[len("/name "):])
	}
	if name == "" {
		m.ShowError("Session name cannot be empty. Usage: /name <name>")
		return
	}
	m.session().SetSessionName(name)
	m.showStatus("Session renamed to " + name)
	m.updateTerminalTitle()
}

// subscribeToAgent subscribes the UI to session events (pi:
// subscribeToAgent).
func (m *InteractiveMode) subscribeToAgent() {
	// pig panic containment: the session's emit() contains listener panics
	// and reports them here so the user sees what happened and where the
	// panic log landed (pi crashes the process instead).
	// See docs/feat/PANIC-RESILIENCE.md.
	//
	// Pig abort-on-panic: a panicking listener usually means the model's
	// current output is what triggers the bug (e.g. rendering a CJK formula
	// mid-stream), so the run is aborted — the programmatic equivalent of
	// the user pressing Esc — instead of streaming on into a broken
	// renderer. The session itself stays alive for the next message.
	// Abort MUST run on its own goroutine: this observer is invoked on the
	// agent's synchronous event-dispatch chain, and the agent does not go
	// idle until that chain returns — a synchronous Abort → WaitForIdle here
	// would deadlock. abortInFlight coalesces panic bursts into one abort;
	// it resets once the abort settles so a panic in a later run triggers a
	// fresh abort.
	session := m.session()
	var abortInFlight atomic.Bool
	session.SetListenerPanicObserver(func(_ *core.AgentSessionEvent, logPath string) {
		if abortInFlight.CompareAndSwap(false, true) {
			go func() {
				defer abortInFlight.Store(false)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = session.Abort(ctx)
			}()
		}
		m.mutateUI(func() {
			m.ShowError("Internal error (recovered) — panic log: " + logPath)
		})
	})
	m.unsubscribe = m.session().Subscribe(func(event *core.AgentSessionEvent) {
		m.handleEvent(context.Background(), event)
	})
}

// handleEvent maps a session event to UI updates (pi: handleEvent).
func (m *InteractiveMode) handleEvent(ctx context.Context, event *core.AgentSessionEvent) {
	if !m.isInitialized {
		_ = m.Init()
	}

	m.footer.Invalidate()

	switch event.Type {
	case agent.AgentEventAgentStart:
		m.pendingTools = map[string]*components.ToolExecutionComponent{}
		// Restore main escape handler if retry handler is still active
		// (retry success event fires later, but we need the main handler
		// now). Progress + working indicator moved to turn_start (v0.85.1)
		// so an agent run that resumes after compaction gets them too.
		m.abortMu.Lock()
		if m.retryEscapeHandler != nil {
			m.defaultEditor.OnEscape = m.retryEscapeHandler
			m.retryEscapeHandler = nil
		}
		m.abortMu.Unlock()
		m.ui.RequestRender(false)

	case agent.AgentEventTurnStart:
		if m.settingsManager().GetShowTerminalProgress() {
			m.ui.Terminal().SetProgress(true)
		}
		if m.workingVisible {
			if !m.isWorkingIndicator() {
				m.showWorkingStatusIndicator()
			}
		} else {
			m.clearStatusIndicator("")
		}
		m.ui.RequestRender(false)

	case core.AgentSessionEventQueueUpdate:
		m.updatePendingMessagesDisplay()
		m.ui.RequestRender(false)

	case core.AgentSessionEventEntryAppended:
		if entry, ok := event.Entry.(*core.CustomEntry); ok {
			m.addCustomEntryToChat(entry)
			m.ui.RequestRender(false)
		}

	case core.AgentSessionEventSessionInfoChanged:
		m.updateTerminalTitle()
		m.footer.Invalidate()
		m.ui.RequestRender(false)

	case core.AgentSessionEventThinkingLevelChanged:
		m.footer.Invalidate()
		m.updateEditorBorderColor()

	case agent.AgentEventMessageStart:
		switch event.Message.MessageRole() {
		case harness.RoleCustomMessage:
			m.addMessageToChat(event.Message, nil)
			m.ui.RequestRender(false)
		case types.RoleUser:
			m.addMessageToChat(event.Message, nil)
			m.updatePendingMessagesDisplay()
			m.ui.RequestRender(false)
		case types.RoleAssistant:
			assistant := asAssistantMessage(event.Message)
			if assistant == nil {
				break
			}
			m.streamingMu.Lock()
			m.streamingComponent = components.NewAssistantMessageComponent(nil, &components.AssistantMessageOptions{
				HideThinkingBlock:    m.hideThinkingBlock,
				MarkdownTheme:        lo.ToPtr(m.getMarkdownThemeWithSettings()),
				HiddenThinkingLabel:  m.hiddenThinkingLabel,
				OutputPad:            m.outputPad,
				MarkdownTransformers: m.getMarkdownTransformers(),
			})
			m.streamingMessage = assistant
			m.chatContainer.AddChild(m.streamingComponent)
			m.streamingComponent.UpdateContent(m.streamingMessage, true)
			m.streamingMu.Unlock()
			m.ui.RequestRender(false)
		}

	case agent.AgentEventMessageUpdate:
		if event.Message.MessageRole() != types.RoleAssistant {
			break
		}
		assistant := asAssistantMessage(event.Message)
		if assistant == nil {
			break
		}
		m.streamingMu.Lock()
		if m.streamingComponent == nil {
			m.streamingMu.Unlock()
			break
		}
		m.streamingMessage = assistant
		m.streamingComponent.UpdateContent(m.streamingMessage, true)

		for _, content := range m.streamingMessage.Content {
			if toolCall, ok := content.(types.ToolCall); ok {
				if _, exists := m.pendingTools[toolCall.ID]; !exists {
					component := components.NewToolExecutionComponent(
						toolCall.Name,
						toolCall.ID,
						toolCall.Arguments,
						components.ToolExecutionOptions{
							ShowImages:      lo.ToPtr(m.settingsManager().GetShowImages()),
							ImageWidthCells: loPtrInt(m.settingsManager().GetImageWidthCells()),
						},
						m.getRegisteredToolDefinition(toolCall.Name),
						m.ui,
						m.sessionManager().GetCwd(),
					)
					component.SetExpanded(m.toolOutputExpanded)
					m.chatContainer.AddChild(component)
					m.pendingTools[toolCall.ID] = component
				} else if component := m.pendingTools[toolCall.ID]; component != nil {
					component.UpdateArgs(toolCall.Arguments)
				}
			}
		}
		m.streamingMu.Unlock()
		m.ui.RequestRender(false)

	case agent.AgentEventMessageEnd:
		if event.Message.MessageRole() == types.RoleUser {
			break
		}
		assistant := asAssistantMessage(event.Message)
		if assistant == nil {
			break
		}
		m.streamingMu.Lock()
		if m.streamingComponent != nil {
			m.streamingMessage = assistant
			errorMessage := ""
			if m.streamingMessage.StopReason == types.StopReasonAborted {
				retryAttempt := m.session().RetryAttempt()
				if retryAttempt > 0 {
					errorMessage = fmt.Sprintf("Aborted after %d retry attempt%s", retryAttempt, pluralSuffix(retryAttempt))
				} else {
					errorMessage = "Operation aborted"
				}
				m.streamingMessage.ErrorMessage = errorMessage
			}
			m.streamingComponent.UpdateContent(m.streamingMessage, false)

			if m.streamingMessage.StopReason == types.StopReasonAborted || m.streamingMessage.StopReason == types.StopReasonError {
				if errorMessage == "" {
					errorMessage = m.streamingMessage.ErrorMessage
					if errorMessage == "" {
						errorMessage = "Error"
					}
				}
				for _, component := range m.pendingTools {
					component.UpdateResult(components.ToolExecutionResult{
						Content: []types.Content{types.TextContent{Kind: types.KindText, Text: errorMessage}},
						IsError: true,
					}, false)
				}
				m.pendingTools = map[string]*components.ToolExecutionComponent{}
			} else {
				// Args are now complete - trigger diff computation for edit
				// tools
				for _, component := range m.pendingTools {
					component.SetArgsComplete()
				}
				m.maybeShowAssistantDiagnostics(m.streamingMessage)
				m.maybeShowCacheMissNotice(m.streamingMessage)
			}
			m.streamingComponent = nil
			m.streamingMessage = nil
			m.footer.Invalidate()
		}
		m.streamingMu.Unlock()
		m.ui.RequestRender(false)

	case core.AgentSessionEventBashExecutionUpdate:
		// The bash execution callback handles TUI output rendering.
		break

	case agent.AgentEventToolExecutionStart:
		component := m.pendingTools[event.ToolCallID]
		if component == nil {
			component = components.NewToolExecutionComponent(
				event.ToolName,
				event.ToolCallID,
				event.Args,
				components.ToolExecutionOptions{
					ShowImages:      lo.ToPtr(m.settingsManager().GetShowImages()),
					ImageWidthCells: loPtrInt(m.settingsManager().GetImageWidthCells()),
				},
				m.getRegisteredToolDefinition(event.ToolName),
				m.ui,
				m.sessionManager().GetCwd(),
			)
			component.SetExpanded(m.toolOutputExpanded)
			m.chatContainer.AddChild(component)
			m.pendingTools[event.ToolCallID] = component
		}
		component.MarkExecutionStarted()
		m.ui.RequestRender(false)

	case agent.AgentEventToolExecutionUpdate:
		if component := m.pendingTools[event.ToolCallID]; component != nil {
			// pi spreads event.partialResult into updateResult (isError:
			// false) — content AND details must surface (interactive-mode.ts:3351).
			component.UpdateResult(resultToComponentResult(event.PartialResult, false), true)
			m.ui.RequestRender(false)
		}

	case agent.AgentEventToolExecutionEnd:
		if component := m.pendingTools[event.ToolCallID]; component != nil {
			component.UpdateResult(resultToComponentResult(event.Result, event.IsError), false)
			delete(m.pendingTools, event.ToolCallID)
			m.ui.RequestRender(false)
		}

	case agent.AgentEventAgentEnd:
		if m.settingsManager().GetShowTerminalProgress() {
			m.ui.Terminal().SetProgress(false)
		}
		m.clearStatusIndicator(components.StatusKindWorking)
		m.streamingMu.Lock()
		if m.streamingComponent != nil {
			m.chatContainer.RemoveChild(m.streamingComponent)
			m.streamingComponent = nil
			m.streamingMessage = nil
		}
		m.streamingMu.Unlock()
		m.pendingTools = map[string]*components.ToolExecutionComponent{}

		m.ui.RequestRender(false)

	case core.AgentSessionEventAgentSettled:
		m.checkShutdownRequested()

	case core.AgentSessionEventCompactionStart:
		if m.settingsManager().GetShowTerminalProgress() {
			m.ui.Terminal().SetProgress(true)
		}
		// Keep editor active; submissions are queued during compaction.
		m.abortMu.Lock()
		m.autoCompactionEscapeHandler = m.defaultEditor.OnEscape
		m.defaultEditor.OnEscape = func() {
			m.session().AbortCompaction()
		}
		m.abortMu.Unlock()
		m.showStatusIndicator(components.NewCompactionStatusIndicator(m.ui, components.CompactionStatusReason(event.Reason)))
		m.ui.RequestRender(false)

	case core.AgentSessionEventCompactionEnd:
		if m.settingsManager().GetShowTerminalProgress() {
			m.ui.Terminal().SetProgress(false)
		}
		m.abortMu.Lock()
		if m.autoCompactionEscapeHandler != nil {
			m.defaultEditor.OnEscape = m.autoCompactionEscapeHandler
			m.autoCompactionEscapeHandler = nil
		}
		m.abortMu.Unlock()
		m.clearStatusIndicator(components.StatusKindCompaction)
		if event.Aborted {
			if event.Reason == "manual" {
				m.ShowError("Compaction cancelled")
			} else {
				m.showStatus("Auto-compaction cancelled")
			}
		} else if event.CompactionResult != nil {
			entries := m.sessionManager().BuildContextEntries()
			if len(entries) == 0 || entries[0].EntryType() != "compaction" {
				panic("Completed compaction is missing from the session context")
			}
			m.chatContainer.Clear()
			// The latest compaction is prepended for model context; append
			// it below at its chronological position.
			m.renderSessionEntries(entries[1:], nil)
			m.addMessageToChat(harness.CreateCompactionSummaryMessage(
				event.CompactionResult.Summary,
				event.CompactionResult.TokensBefore,
				time.Now().UnixMilli(),
			), nil)
			if event.CompactionResult.Usage != nil {
				m.addCompactionCostNotice(&CompactionCostNotice{
					Kind:  "compaction",
					Usage: *event.CompactionResult.Usage,
				})
			}
			m.footer.Invalidate()
		} else if event.ErrorMessage != "" {
			if event.Reason == "manual" {
				m.ShowError(event.ErrorMessage)
			} else {
				m.chatContainer.AddChild(tui.NewSpacer(1))
				m.chatContainer.AddChild(tui.NewText(theme.Current().Fg("error", event.ErrorMessage), 1, 0))
			}
		}
		m.flushCompactionQueue(ctx, event.WillRetry)
		m.ui.RequestRender(false)

	case core.AgentSessionEventAutoRetryStart:
		// Set up escape to abort retry
		m.abortMu.Lock()
		m.retryEscapeHandler = m.defaultEditor.OnEscape
		m.defaultEditor.OnEscape = func() {
			m.session().AbortRetry()
		}
		m.abortMu.Unlock()
		m.showStatusIndicator(components.NewRetryStatusIndicator(m.ui, event.Attempt, event.MaxAttempts, int(event.DelayMs)))
		m.ui.RequestRender(false)

	case core.AgentSessionEventAutoRetryEnd:
		// Restore escape handler
		m.abortMu.Lock()
		if m.retryEscapeHandler != nil {
			m.defaultEditor.OnEscape = m.retryEscapeHandler
			m.retryEscapeHandler = nil
		}
		m.abortMu.Unlock()
		m.clearStatusIndicator(components.StatusKindRetry)
		// Show error only on final failure (success shows normal response)
		if !event.Success {
			finalError := event.FinalError
			if finalError == "" {
				finalError = "Unknown error"
			}
			m.ShowError(fmt.Sprintf("Retry failed after %d attempts: %s", event.Attempt, finalError))
		}
		m.ui.RequestRender(false)

	case core.AgentSessionEventSummarizationRetryScheduled:
		m.ShowError(event.ErrorMessage)
		m.showStatusIndicator(components.NewRetryStatusIndicator(m.ui, event.Attempt, event.MaxAttempts, int(event.DelayMs)))
		m.ui.RequestRender(false)

	case core.AgentSessionEventSummarizationRetryAttemptStart:
		m.clearStatusIndicator(components.StatusKindRetry)
		if event.SummaryRetrySource == "branchSummary" {
			m.showStatusIndicator(components.NewBranchSummaryStatusIndicator(m.ui))
		} else {
			m.showStatusIndicator(components.NewCompactionStatusIndicator(m.ui, components.CompactionStatusReason(event.Reason)))
		}
		m.ui.RequestRender(false)

	case core.AgentSessionEventSummarizationRetryFinished:
		m.clearStatusIndicator(components.StatusKindRetry)
		m.ui.RequestRender(false)
	}
}

// getUserMessageText extracts text content from a user message (pi:
// getUserMessageText).
func (m *InteractiveMode) getUserMessageText(message agent.AgentMessage) string {
	if message.MessageRole() != types.RoleUser {
		return ""
	}
	user, ok := message.(types.UserMessage)
	if !ok {
		userPtr, ok := message.(*types.UserMessage)
		if !ok {
			return ""
		}
		user = *userPtr
	}
	textBlocks := []string{}
	if user.Content.String != "" {
		textBlocks = append(textBlocks, user.Content.String)
	}
	for _, content := range user.Content.Parts {
		if text, ok := content.(types.TextContent); ok {
			textBlocks = append(textBlocks, text.Text)
		}
	}
	return strings.Join(textBlocks, "")
}

// showManagedToolStatus shows a managed-tool status update in the chat (pi:
// showManagedToolStatus).
func (m *InteractiveMode) showManagedToolStatus(status utils.ToolStatus) {
	if !m.managedToolStatusStarted {
		m.chatContainer.AddChild(tui.NewSpacer(1))
		m.managedToolStatusStarted = true
	}
	message := status.Message
	color := "dim"
	if status.Type == "warning" {
		message = "Warning: " + message
		color = "warning"
	}
	m.chatContainer.AddChild(tui.NewText(theme.Current().Fg(color, message), 1, 0))
	m.lastStatusSpacer = nil
	m.lastStatusText = nil
	m.ui.RequestRender(false)
}

// showStatus shows a status message in the chat. If multiple status
// messages are emitted back-to-back (without anything else being added to
// the chat), we update the previous status line instead of appending new
// ones to avoid log spam (pi: showStatus).
func (m *InteractiveMode) showStatus(message string) {
	children := m.chatContainer.Children
	var last, secondLast tui.Component
	if len(children) > 0 {
		last = children[len(children)-1]
	}
	if len(children) > 1 {
		secondLast = children[len(children)-2]
	}

	if last != nil && secondLast != nil && last == tui.Component(m.lastStatusText) && secondLast == tui.Component(m.lastStatusSpacer) {
		m.lastStatusText.SetText(theme.Current().Fg("dim", message))
		m.ui.RequestRender(false)
		return
	}

	spacer := tui.NewSpacer(1)
	text := tui.NewText(theme.Current().Fg("dim", message), 1, 0)
	m.chatContainer.AddChild(spacer)
	m.chatContainer.AddChild(text)
	m.lastStatusSpacer = spacer
	m.lastStatusText = text
	m.ui.RequestRender(false)
}

// handleCopyCommand copies the last assistant message, preferring the active
// fullscreen text selection when copy-on-select is disabled (pi:
// handleCopyCommand; pig additively registers the action — the T5b shell
// missed it, surfaced by the v0.85.1 preferSelection tests).
func (m *InteractiveMode) handleCopyCommand(flashConfirmation bool, preferSelection bool) {
	if altScreen, ok := m.ui.(interface {
		GetCopyOnSelect() bool
		HasActiveSelection() bool
		CopyActiveSelectionToClipboard() bool
	}); ok && preferSelection && !altScreen.GetCopyOnSelect() && altScreen.HasActiveSelection() {
		altScreen.CopyActiveSelectionToClipboard()
		return
	}

	text := m.session().GetLastAssistantText()
	if text == "" {
		m.ShowError("No agent messages to copy yet.")
		return
	}

	if err := interactiveCopyToClipboard(text); err != nil {
		m.ShowError(errorMessageOf(err))
		return
	}
	if flashConfirmation {
		if as, ok := m.ui.(interface{ Flash(string, int) }); ok {
			as.Flash("Copied!", 0)
		} else {
			m.showStatus("Copied last agent message to clipboard")
		}
	} else {
		m.showStatus("Copied last agent message to clipboard")
	}
}

// addCustomEntryToChat renders an appended custom entry (pi:
// addCustomEntryToChat).
func (m *InteractiveMode) addCustomEntryToChat(entry *core.CustomEntry) {
	renderer := m.session().GetExtensionRunner().GetEntryRenderer(entry.CustomType)
	if renderer == nil {
		return
	}
	component := components.NewCustomEntryComponent(entry, renderer)
	component.SetExpanded(m.toolOutputExpanded)
	if !component.HasContent() {
		return
	}

	m.streamingMu.Lock()
	defer m.streamingMu.Unlock()
	if m.streamingComponent != nil {
		children := m.chatContainer.Children
		for i, child := range children {
			if child == tui.Component(m.streamingComponent) {
				// splice at streamingIndex (pi: children.splice)
				updated := make([]tui.Component, 0, len(children)+1)
				updated = append(updated, children[:i]...)
				updated = append(updated, component)
				updated = append(updated, children[i:]...)
				m.chatContainer.Children = updated
				return
			}
		}
	}

	m.chatContainer.AddChild(component)
}

type addMessageOptions struct {
	PopulateHistory bool
}

// asAssistantMessage normalizes an assistant message that may enter the
// interface as a value or a pointer (pig: AgentMessage constructors use
// value receivers; adapters produce pointers).
func asAssistantMessage(message agent.AgentMessage) *types.AssistantMessage {
	switch v := message.(type) {
	case *types.AssistantMessage:
		return v
	case types.AssistantMessage:
		return &v
	}
	return nil
}

// addMessageToChat renders a message into the transcript (pi:
// addMessageToChat).
func (m *InteractiveMode) addMessageToChat(message agent.AgentMessage, options *addMessageOptions) {
	switch message.MessageRole() {
	case harness.RoleBashExecution:
		bash, ok := message.(*harness.BashExecutionMessage)
		if !ok {
			return
		}
		component := components.NewBashExecutionComponent(bash.Command, m.ui, bash.ExcludeFromContext != nil && *bash.ExcludeFromContext)
		if bash.Output != "" {
			component.AppendOutput(bash.Output)
		}
		var truncation *harnessutils.TruncationResult
		if bash.Truncated {
			truncation = &harnessutils.TruncationResult{Truncated: true}
		}
		component.SetComplete(bash.ExitCode, bash.Cancelled, truncation, bash.FullOutputPath)
		m.chatContainer.AddChild(component)

	case harness.RoleCustomMessage:
		custom, ok := message.(*harness.CustomMessage)
		if !ok {
			return
		}
		if !custom.Display {
			return
		}
		renderer := m.session().GetExtensionRunner().GetMessageRenderer(custom.CustomType)
		component := components.NewCustomMessageComponent(custom, renderer, lo.ToPtr(m.getMarkdownThemeWithSettings()), m.outputPad)
		component.SetExpanded(m.toolOutputExpanded)
		m.chatContainer.AddChild(component)

	case harness.RoleCompactionSummary:
		summary, ok := message.(*harness.CompactionSummaryMessage)
		if !ok {
			return
		}
		m.chatContainer.AddChild(tui.NewSpacer(1))
		component := components.NewCompactionSummaryMessageComponent(summary, lo.ToPtr(m.getMarkdownThemeWithSettings()))
		component.SetExpanded(m.toolOutputExpanded)
		m.chatContainer.AddChild(component)

	case harness.RoleBranchSummary:
		summary, ok := message.(*harness.BranchSummaryMessage)
		if !ok {
			return
		}
		m.chatContainer.AddChild(tui.NewSpacer(1))
		component := components.NewBranchSummaryMessageComponent(summary, lo.ToPtr(m.getMarkdownThemeWithSettings()))
		component.SetExpanded(m.toolOutputExpanded)
		m.chatContainer.AddChild(component)

	case types.RoleUser:
		textContent := m.getUserMessageText(message)
		if textContent == "" {
			return
		}
		if len(m.chatContainer.Children) > 0 {
			m.chatContainer.AddChild(tui.NewSpacer(1))
		}
		skillBlock := core.ParseSkillBlock(textContent)
		if skillBlock != nil {
			// Render skill block (collapsible)
			component := components.NewSkillInvocationMessageComponent(skillBlock, lo.ToPtr(m.getMarkdownThemeWithSettings()))
			component.SetExpanded(m.toolOutputExpanded)
			m.chatContainer.AddChild(component)
			// Render user message separately if present
			if skillBlock.UserMessage != nil && *skillBlock.UserMessage != "" {
				m.chatContainer.AddChild(tui.NewSpacer(1))
				userComponent := components.NewUserMessageComponent(*skillBlock.UserMessage, lo.ToPtr(m.getMarkdownThemeWithSettings()), m.outputPad, m.getMarkdownTransformers())
				m.chatContainer.AddChild(userComponent)
			}
		} else {
			userComponent := components.NewUserMessageComponent(textContent, lo.ToPtr(m.getMarkdownThemeWithSettings()), m.outputPad, m.getMarkdownTransformers())
			m.chatContainer.AddChild(userComponent)
		}
		if options != nil && options.PopulateHistory {
			m.editorAddToHistory(textContent)
		}

	case types.RoleAssistant:
		assistant := asAssistantMessage(message)
		if assistant == nil {
			return
		}
		assistantComponent := components.NewAssistantMessageComponent(assistant, &components.AssistantMessageOptions{
			HideThinkingBlock:    m.hideThinkingBlock,
			MarkdownTheme:        lo.ToPtr(m.getMarkdownThemeWithSettings()),
			HiddenThinkingLabel:  m.hiddenThinkingLabel,
			OutputPad:            m.outputPad,
			MarkdownTransformers: m.getMarkdownTransformers(),
		})
		m.chatContainer.AddChild(assistantComponent)

	case types.RoleToolResult:
		// Tool results are rendered inline with tool calls, handled
		// separately
	}
}

// renderSessionItems renders transcript items (pi: renderSessionItems).
func (m *InteractiveMode) renderSessionItems(items []renderSessionItem, options *renderSessionOptions) {
	m.pendingTools = map[string]*components.ToolExecutionComponent{}
	renderedPendingTools := map[string]*components.ToolExecutionComponent{}
	var renderedPendingOrder []string
	// Cache-miss notices are not persisted; re-derive them from the full
	// entry list and re-inject them after the assistant messages that paid
	// for them.
	var cacheMisses map[*types.AssistantMessage]*core.CacheMiss
	if m.settingsManager().GetShowCacheMissNotices() {
		cacheMisses, _ = core.CollectCacheMisses(m.sessionManager().GetEntries(), m.modelPriceSource())
	}

	if options != nil && options.UpdateFooter {
		m.footer.Invalidate()
		m.updateEditorBorderColor()
	}

	for _, item := range items {
		if item.customEntry != nil {
			m.addCustomEntryToChat(item.customEntry)
			continue
		}
		if item.costNotice != nil {
			m.addCompactionCostNotice(item.costNotice)
			continue
		}

		message := item.message
		// Assistant messages need special handling for tool calls
		if message.MessageRole() == types.RoleAssistant {
			assistant := asAssistantMessage(message)
			if assistant == nil {
				continue
			}
			m.addMessageToChat(assistant, nil)
			// Render tool call components
			for _, content := range assistant.Content {
				if toolCall, ok := content.(types.ToolCall); ok {
					component := components.NewToolExecutionComponent(
						toolCall.Name,
						toolCall.ID,
						toolCall.Arguments,
						components.ToolExecutionOptions{
							ShowImages:      lo.ToPtr(m.settingsManager().GetShowImages()),
							ImageWidthCells: loPtrInt(m.settingsManager().GetImageWidthCells()),
						},
						m.getRegisteredToolDefinition(toolCall.Name),
						m.ui,
						m.sessionManager().GetCwd(),
					)
					component.SetExpanded(m.toolOutputExpanded)
					m.chatContainer.AddChild(component)

					if assistant.StopReason == types.StopReasonAborted || assistant.StopReason == types.StopReasonError {
						errorMessage := assistant.ErrorMessage
						if assistant.StopReason == types.StopReasonAborted {
							retryAttempt := m.session().RetryAttempt()
							if retryAttempt > 0 {
								errorMessage = fmt.Sprintf("Aborted after %d retry attempt%s", retryAttempt, pluralSuffix(retryAttempt))
							} else {
								errorMessage = "Operation aborted"
							}
						} else if errorMessage == "" {
							errorMessage = "Error"
						}
						component.UpdateResult(components.ToolExecutionResult{
							Content: []types.Content{types.TextContent{Kind: types.KindText, Text: errorMessage}},
							IsError: true,
						}, false)
					} else {
						renderedPendingTools[toolCall.ID] = component
						renderedPendingOrder = append(renderedPendingOrder, toolCall.ID)
					}
				}
			}
			if assistant.StopReason != types.StopReasonAborted && assistant.StopReason != types.StopReasonError {
				m.maybeShowAssistantDiagnostics(assistant)
				if miss := cacheMisses[assistant]; miss != nil {
					m.addCacheMissNotice(miss)
				}
			}
		} else if message.MessageRole() == types.RoleToolResult {
			// Match tool results to pending tool components
			toolResult, ok := message.(*types.ToolResultMessage)
			if !ok {
				continue
			}
			if component := renderedPendingTools[toolResult.ToolCallID]; component != nil {
				component.UpdateResult(resultToComponentResult(toolResult, false), false)
				delete(renderedPendingTools, toolResult.ToolCallID)
			}
		} else {
			// All other messages use standard rendering
			m.addMessageToChat(message, &addMessageOptions{PopulateHistory: options != nil && options.PopulateHistory})
		}
	}

	for _, toolCallID := range renderedPendingOrder {
		if component, ok := renderedPendingTools[toolCallID]; ok {
			m.pendingTools[toolCallID] = component
		}
	}
	m.ui.RequestRender(false)
}

type renderSessionOptions struct {
	UpdateFooter    bool
	PopulateHistory bool
}

// renderSessionEntries renders compaction-aware session entries to chat.
// Used for initial load and rebuild after compaction (pi:
// renderSessionEntries).
func (m *InteractiveMode) renderSessionEntries(entries []core.SessionEntry, options *renderSessionOptions) {
	var items []renderSessionItem
	for _, entry := range entries {
		if custom, ok := entry.(*core.CustomEntry); ok {
			items = append(items, renderSessionItem{customEntry: custom})
			continue
		}
		messages := core.SessionEntryToContextMessages(entry)
		var usage *types.Usage
		isSummary := false
		switch e := entry.(type) {
		case *core.CompactionEntry:
			usage = e.Usage
			isSummary = true
		case *core.BranchSummaryEntry:
			usage = e.Usage
			isSummary = true
		}
		if isSummary && usage != nil && len(messages) > 0 {
			kind := "branch_summary"
			if entry.EntryType() == "compaction" {
				kind = "compaction"
			}
			for _, message := range messages {
				items = append(items, renderSessionItem{message: message})
			}
			items = append(items, renderSessionItem{costNotice: &CompactionCostNotice{Kind: kind, Usage: *usage}})
			continue
		}
		for _, message := range messages {
			items = append(items, renderSessionItem{message: message})
		}
	}
	m.renderSessionItems(items, options)
}

// addCompactionCostNotice renders billing usage for a compaction or branch
// summary (pi: addCompactionCostNotice).
func (m *InteractiveMode) addCompactionCostNotice(notice *CompactionCostNotice) {
	if !m.settingsManager().GetShowCacheMissNotices() {
		return
	}

	usage := notice.Usage
	tokens := usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
	cost := ""
	if usage.Cost.Total >= 0.01 {
		cost = fmt.Sprintf(" (~$%.2f)", usage.Cost.Total)
	}
	label := "Branch summary"
	if notice.Kind == "compaction" {
		label = "Compaction"
	}
	m.chatContainer.AddChild(tui.NewSpacer(1))
	m.chatContainer.AddChild(tui.NewText(theme.Current().Fg("warning",
		fmt.Sprintf("%s: %s tokens billed%s", label, components.FormatTokens(tokens), cost)), 1, 0))
}

// maybeShowAssistantDiagnostics surfaces assistant-message diagnostics as
// transcript notices (pi: maybeShowAssistantDiagnostics, v0.85.1). Currently
// reports Anthropic thinking blocks dropped by input transformations.
func (m *InteractiveMode) maybeShowAssistantDiagnostics(message *types.AssistantMessage) {
	if !m.settingsManager().GetShowCacheMissNotices() {
		return
	}

	for _, diagnostic := range message.Diagnostics {
		if diagnostic.Type != "anthropic_input_transformations" {
			continue
		}
		transformations, ok := diagnostic.Details["transformations"].([]any)
		if !ok {
			continue
		}

		dropped := make([]string, 0, len(transformations))
		for _, transformation := range transformations {
			details, ok := transformation.(map[string]any)
			if !ok {
				continue
			}
			if details["type"] != "thinking_dropped" {
				continue
			}
			reason := "unknown reason"
			if r, ok := details["reason"].(string); ok {
				reason = r
			}
			location := ""
			if p, ok := details["path"].(string); ok {
				location = " at " + p
			}
			dropped = append(dropped, reason+location)
		}
		if len(dropped) == 0 {
			continue
		}

		noun := "thinking block"
		if len(dropped) != 1 {
			noun = fmt.Sprintf("%d thinking blocks", len(dropped))
		}
		m.chatContainer.AddChild(tui.NewSpacer(1))
		m.chatContainer.AddChild(tui.NewText(
			theme.Fg("warning", fmt.Sprintf("Anthropic dropped %s: %s", noun, strings.Join(dropped, "; "))), 1, 0))
	}
}

// maybeShowCacheMissNotice shows a transcript notice when a completed
// assistant message paid for a significant cache miss. Only states
// observable facts: the miss itself, a model switch, or an idle gap past
// the cache TTL (pi: maybeShowCacheMissNotice).
func (m *InteractiveMode) maybeShowCacheMissNotice(message *types.AssistantMessage) {
	if !m.settingsManager().GetShowCacheMissNotices() {
		return
	}

	// Entries don't contain `message` yet: message_end fires before
	// persistence.
	miss := core.DetectCacheMiss(m.sessionManager().GetEntries(), message, m.modelPriceSource())
	if miss != nil {
		m.addCacheMissNotice(miss)
	}
}

// addCacheMissNotice renders one cache-miss notice (pi: addCacheMissNotice).
func (m *InteractiveMode) addCacheMissNotice(miss *core.CacheMiss) {
	if miss.MissedTokens < 20_000 && miss.MissedCost < 0.1 {
		return
	}

	cost := ""
	if miss.MissedCost >= 0.01 {
		cost = fmt.Sprintf(" (~$%.2f)", miss.MissedCost)
	}
	reBilled := fmt.Sprintf("%s tokens re-billed%s", components.FormatTokens(miss.MissedTokens), cost)
	label := "Cache miss"
	if miss.ModelChanged {
		label = "Cache miss after model switch"
	} else if miss.IdleMs >= core.CacheTtlMs {
		label = fmt.Sprintf("Cache miss after %dm idle", int(float64(miss.IdleMs)/60_000+0.5))
	}
	text := theme.Current().Fg("warning", label+": "+reBilled)
	m.chatContainer.AddChild(tui.NewSpacer(1))
	m.chatContainer.AddChild(tui.NewText(text, 1, 0))
}

// renderInitialMessages renders the session transcript (pi:
// renderInitialMessages).
func (m *InteractiveMode) renderInitialMessages() {
	entries := m.sessionManager().BuildContextEntries()
	m.renderSessionEntries(entries, &renderSessionOptions{
		UpdateFooter:    true,
		PopulateHistory: true,
	})
	m.renderProjectTrustWarningIfNeeded()

	// Show compaction info if session was compacted
	allEntries := m.sessionManager().GetEntries()
	compactionCount := 0
	for _, entry := range allEntries {
		if entry.EntryType() == "compaction" {
			compactionCount++
		}
	}
	if compactionCount > 0 {
		times := fmt.Sprintf("%d times", compactionCount)
		if compactionCount == 1 {
			times = "1 time"
		}
		m.showStatus("Session compacted " + times)
	}
}

// renderProjectTrustWarningIfNeeded warns when project resources are
// ignored (pi: renderProjectTrustWarningIfNeeded).
func (m *InteractiveMode) renderProjectTrustWarningIfNeeded() {
	if m.settingsManager().IsProjectTrusted() || !core.HasTrustRequiringProjectResources(m.sessionManager().GetCwd()) {
		return
	}

	if len(m.chatContainer.Children) > 0 {
		m.chatContainer.AddChild(tui.NewSpacer(1))
	}
	m.chatContainer.AddChild(tui.NewText(theme.Current().Fg("warning",
		fmt.Sprintf("This project is not trusted. Project %s resources and packages are ignored. Use /trust to save a trust decision, then restart pi.", codingagent.ConfigDirName),
	), 1, 0))
}

// getUserInput returns queued input or waits for the next submission (pi:
// getUserInput).
func (m *InteractiveMode) getUserInput() string {
	return m.inputGate.wait()
}

// rebuildChatFromMessages clears and re-renders the transcript (pi:
// rebuildChatFromMessages).
func (m *InteractiveMode) rebuildChatFromMessages() {
	m.chatContainer.Clear()
	m.renderSessionEntries(m.sessionManager().BuildContextEntries(), nil)
}

// =========================================================================
// Key handlers
// =========================================================================

func (m *InteractiveMode) handleCtrlC() {
	now := time.Now()
	if now.Sub(m.lastSigintTime) < 500*time.Millisecond {
		_ = m.shutdown(false)
	} else {
		m.clearEditor()
		m.lastSigintTime = now
	}
}

func (m *InteractiveMode) handleCtrlD() {
	// Only called when editor is empty (enforced by CustomEditor)
	_ = m.shutdown(false)
}

// shutdown gracefully shuts down the agent. Stops the TUI before emitting
// shutdown events so extension UI cleanup cannot repaint the final frame
// while the process is exiting (pi: shutdown; signal path deferred with the
// runtime-host shutdown event batch).
func (m *InteractiveMode) shutdown(fromSignal bool) error {
	if m.isShuttingDown {
		return nil
	}
	m.isShuttingDown = true

	// Interactive quit (Ctrl+D, Ctrl+C, /quit). Drain any in-flight Kitty
	// key release events before stopping. This prevents escape sequences
	// from leaking to the parent shell over slow SSH.
	m.themeController.DisableAutoSync()
	m.ui.Terminal().DrainInput(1000, 50)

	m.stopInteractive()
	_ = m.runtimeHost.Dispose()

	if resumeCommand := formatResumeCommand(m.sessionManager()); resumeCommand != "" {
		fmt.Printf("%s %s\n", ansiDim("To resume this session:"), resumeCommand)
	}

	os.Exit(0)
	return nil
}

func ansiDim(text string) string { return "\x1b[2m" + text + "\x1b[22m" }

// uncaughtCrash restores the terminal before the process dies on any
// uncaught panic (pi: uncaughtCrash; wired via RecoverCrash below).
func (m *InteractiveMode) uncaughtCrash(err error) {
	if m.isShuttingDown {
		os.Exit(1)
	}
	m.isShuttingDown = true
	func() {
		defer func() { _ = recover() }()
		m.unregisterSignalHandlers()
	}()
	func() {
		defer func() { _ = recover() }()
		m.ui.Stop(nil)
	}()
	fmt.Fprintf(os.Stderr, "%s exiting due to uncaughtException:\n", codingagent.AppName)
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// checkShutdownRequested performs shutdown if it was requested (pi:
// checkShutdownRequested).
func (m *InteractiveMode) checkShutdownRequested() {
	if !m.shutdownRequested {
		return
	}
	_ = m.shutdown(false)
}

// registerSignalHandlers installs SIGTERM/SIGHUP handlers (pi:
// registerSignalHandlers).
func (m *InteractiveMode) registerSignalHandlers() {
	m.unregisterSignalHandlers()

	sigTerm := make(chan os.Signal, 1)
	signal.Notify(sigTerm, syscall.SIGTERM)
	m.signalCleanupHandlers = append(m.signalCleanupHandlers, func() { signal.Stop(sigTerm) })
	go func() {
		// pig panic containment (see docs/feat/PANIC-RESILIENCE.md).
		defer pigutils.RecoverPanic("interactive.signal")
		for range sigTerm {
			// SIGHUP/SIGTERM no longer hard-exit: graceful shutdown emits
			// session_shutdown first (extension shutdown events land with
			// Phase 7; terminal restore runs here).
			m.spawnShutdown(true)
		}
	}()

	if !isWindows() {
		sigHup := make(chan os.Signal, 1)
		signal.Notify(sigHup, syscall.SIGHUP)
		m.signalCleanupHandlers = append(m.signalCleanupHandlers, func() { signal.Stop(sigHup) })
		go func() {
			defer pigutils.RecoverPanic("interactive.signal")
			for range sigHup {
				m.spawnShutdown(true)
			}
		}()
	}
}

// spawnShutdown runs m.shutdown on its own goroutine with panic containment
// (pig addition: the serializer for graceful shutdown from signal paths;
// pi performs it inline on the event loop).
// See docs/feat/PANIC-RESILIENCE.md.
func (m *InteractiveMode) spawnShutdown(fatal bool) {
	go func() {
		defer pigutils.RecoverPanic("interactive.shutdown")
		_ = m.shutdown(fatal)
	}()
}

func (m *InteractiveMode) unregisterSignalHandlers() {
	for _, cleanup := range m.signalCleanupHandlers {
		cleanup()
	}
	m.signalCleanupHandlers = nil
}

func isWindows() bool { return os.PathSeparator == '\\' }

// handleFollowUp queues the editor text as a follow-up (pi: handleFollowUp).
func (m *InteractiveMode) handleFollowUp() {
	text := strings.TrimSpace(m.editorGetExpandedText())
	if text == "" {
		return
	}

	ctx := context.Background()

	// Queue input during compaction (extension commands execute immediately)
	if m.session().IsCompacting() {
		if m.isExtensionCommand(text) {
			m.editorAddToHistory(text)
			m.editor.SetText("")
			_ = m.session().Prompt(ctx, text, nil)
		} else {
			m.queueCompactionMessage(text, "followUp")
		}
		return
	}

	// Alt+Enter queues a follow-up message (waits until agent finishes).
	// This handles extension commands (execute immediately), prompt
	// template expansion, and queueing.
	if m.session().IsStreaming() {
		m.editorAddToHistory(text)
		m.editor.SetText("")
		_ = m.session().Prompt(ctx, text, &core.PromptOptions{StreamingBehavior: "followUp"})
		m.updatePendingMessagesDisplay()
		m.ui.RequestRender(false)
		return
	}
	// If not streaming, Alt+Enter acts like regular Enter (trigger onSubmit)
	if m.defaultEditor.OnSubmit != nil {
		m.editor.SetText("")
		m.defaultEditor.OnSubmit(text)
	}
}

// updateEditorBorderColor recolors the editor border by bash mode /
// thinking level (pi: updateEditorBorderColor).
func (m *InteractiveMode) updateEditorBorderColor() {
	if m.isBashMode {
		m.defaultEditor.BorderColor = theme.Current().GetBashModeBorderColor()
	} else {
		level := m.session().ThinkingLevel()
		if level == "" {
			level = "off"
		}
		m.defaultEditor.BorderColor = theme.Current().GetThinkingBorderColor(level)
	}
	// The embedded working indicator colors itself after the border (pi:
	// updateEditorBorderColor invalidates the active working indicator,
	// v0.85.1).
	if m.isWorkingIndicator() {
		m.activeStatusIndicator.Invalidate()
	}
	m.ui.RequestRender(false)
}

// cycleThinkingLevel cycles the thinking level (pi: cycleThinkingLevel).
func (m *InteractiveMode) cycleThinkingLevel() {
	newLevel := m.session().CycleThinkingLevel(nil)
	if newLevel == "" {
		m.showStatus("Current model does not support thinking")
	} else {
		m.footer.Invalidate()
		m.updateEditorBorderColor()
		m.showStatus("Thinking level: " + string(newLevel))
	}
}

// cycleModel cycles forward/backward through models (pi: cycleModel).
func (m *InteractiveMode) cycleModel(direction string) {
	result, err := m.session().CycleModel(context.Background(), direction, nil)
	if err != nil {
		m.ShowError(errorMessageOf(err))
		return
	}
	if result == nil {
		if len(m.session().ScopedModels()) > 0 {
			m.showStatus("Only one model in scope")
		} else {
			m.showStatus("Only one model available")
		}
		return
	}
	m.footer.Invalidate()
	m.updateEditorBorderColor()
	thinkingStr := ""
	if result.Model.Reasoning && result.ThinkingLevel != "off" {
		thinkingStr = fmt.Sprintf(" (thinking: %s)", result.ThinkingLevel)
	}
	name := result.Model.Name
	if name == "" {
		name = result.Model.ID
	}
	m.showStatus("Switched to " + name + thinkingStr)
}

// toggleToolOutputExpansion flips tool output expansion (pi:
// toggleToolOutputExpansion).
func (m *InteractiveMode) toggleToolOutputExpansion() {
	m.setToolsExpanded(!m.toolOutputExpanded)
}

// setToolsExpanded applies tool output expansion across components (pi:
// setToolsExpanded).
func (m *InteractiveMode) setToolsExpanded(expanded bool) {
	if expanded == m.toolOutputExpanded {
		return
	}

	m.toolOutputExpanded = expanded
	if m.builtInHeader != nil {
		if e := asExpandable(m.builtInHeader); e != nil {
			e.SetExpanded(expanded)
		}
	}
	for _, container := range []*tui.Container{m.loadedResourcesContainer, m.chatContainer} {
		for _, child := range container.Children {
			if e := asExpandable(child); e != nil {
				e.SetExpanded(expanded)
			}
		}
	}
	expandedLabel := "expanded"
	if !expanded {
		expandedLabel = "collapsed"
	}
	m.showStatus("Tool output: " + expandedLabel)
}

// updateThinkingBlockVisibility re-renders assistant messages in place
// without rebuilding live tool components (pi: updateThinkingBlockVisibility,
// v0.85.1).
func (m *InteractiveMode) updateThinkingBlockVisibility() {
	for _, child := range m.chatContainer.Children {
		if assistant, ok := child.(*components.AssistantMessageComponent); ok {
			assistant.SetHideThinkingBlock(m.hideThinkingBlock)
		}
	}
	m.ui.RequestRender(false)
}

// toggleThinkingBlockVisibility toggles thinking blocks and rebuilds the
// transcript (pi: toggleThinkingBlockVisibility).
func (m *InteractiveMode) toggleThinkingBlockVisibility() {
	m.hideThinkingBlock = !m.hideThinkingBlock
	m.settingsManager().SetHideThinkingBlock(m.hideThinkingBlock)

	// Update rendered assistant messages in place (v0.85.1) — rebuilding the
	// chat destroyed live streaming/pending tool components (#8611).
	m.updateThinkingBlockVisibility()

	visible := "visible"
	if m.hideThinkingBlock {
		visible = "hidden"
	}
	m.showStatus("Thinking blocks: " + visible)
}

// =========================================================================
// UI helpers
// =========================================================================

// clearEditor clears the editor (pi: clearEditor).
func (m *InteractiveMode) clearEditor() {
	m.editor.SetText("")
	m.ui.RequestRender(false)
}

// ShowError shows an error in the chat (pi: showError).
func (m *InteractiveMode) ShowError(errorMessage string) {
	m.chatContainer.AddChild(tui.NewSpacer(1))
	m.chatContainer.AddChild(tui.NewText(theme.Current().Fg("error", "Error: "+errorMessage), m.outputPad, 0))
	m.ui.RequestRender(false)
}

// ShowWarning shows a warning in the chat (pi: showWarning).
func (m *InteractiveMode) ShowWarning(warningMessage string) {
	m.chatContainer.AddChild(tui.NewSpacer(1))
	m.chatContainer.AddChild(tui.NewText(theme.Current().Fg("warning", "Warning: "+warningMessage), 1, 0))
	m.ui.RequestRender(false)
}

// setEditorWorkingStatusIndicator forwards the indicator to the active
// editor when it opted into border embedding (pi:
// setEditorWorkingStatusIndicator, v0.85.1). Returns false when the active
// editor does not support it.
func (m *InteractiveMode) setEditorWorkingStatusIndicator(indicator *components.WorkingStatusIndicator) bool {
	m.defaultEditor.SetWorkingStatusIndicator(nil)
	editor, ok := m.editor.(*components.CustomEditor)
	if !ok || !editor.EmbedWorkingStatus {
		return false
	}
	editor.SetWorkingStatusIndicator(indicator)
	return true
}

// showStatusIndicator swaps the status container content (pi:
// showStatusIndicator). A working indicator is embedded into the editor's
// top border instead of occupying a standalone row when possible.
func (m *InteractiveMode) showStatusIndicator(indicator statusIndicatorItem) {
	if m.activeStatusIndicator != nil {
		m.activeStatusIndicator.Dispose()
	}
	m.activeStatusIndicator = indicator
	m.activeWorkingIndicatorEmbedded = false
	m.statusContainer.Clear()
	m.setEditorWorkingStatusIndicator(nil)
	if working, ok := indicator.(*components.WorkingStatusIndicator); ok && m.setEditorWorkingStatusIndicator(working) {
		m.activeWorkingIndicatorEmbedded = true
		return
	}
	m.statusContainer.AddChild(indicator)
}

// clearStatusIndicator removes the active indicator when it matches kind
// (pi: clearStatusIndicator).
func (m *InteractiveMode) clearStatusIndicator(kind components.StatusIndicatorKind) {
	if kind != "" && (m.activeStatusIndicator == nil || m.activeStatusIndicator.IndicatorKind() != kind) {
		return
	}
	clearedIndicator := m.activeStatusIndicator
	clearedIndicatorWasEmbedded := clearedIndicator != nil &&
		clearedIndicator.IndicatorKind() == components.StatusKindWorking &&
		m.activeWorkingIndicatorEmbedded
	if clearedIndicator != nil {
		clearedIndicator.Dispose()
	}
	m.activeStatusIndicator = nil
	m.activeWorkingIndicatorEmbedded = false
	m.statusContainer.Clear()
	m.setEditorWorkingStatusIndicator(nil)
	// Reserve the status height only for a standalone indicator row; an
	// embedded working indicator frees its row without the idle filler.
	if clearedIndicator != nil && !clearedIndicatorWasEmbedded && m.options.TuiMode == "regular" && m.ui.GetClearOnShrink() {
		m.statusContainer.AddChild(m.idleStatus)
	}
}

// showWorkingStatusIndicator builds and shows the working indicator, matching
// the editor border color when the editor embeds it (pi:
// showWorkingStatusIndicator, v0.85.1).
func (m *InteractiveMode) showWorkingStatusIndicator() {
	var colorFn func(string) string
	if editor, ok := m.editor.(*components.CustomEditor); ok && editor.EmbedWorkingStatus {
		borderColor := editor.BorderColor
		if borderColor == nil {
			level := m.session().ThinkingLevel()
			if level == "" {
				level = "off"
			}
			borderColor = theme.Current().GetThinkingBorderColor(level)
		}
		colorFn = borderColor
	}
	message := m.defaultWorkingMessage
	if m.workingMessage != nil {
		message = *m.workingMessage
	}
	m.showStatusIndicator(components.NewWorkingStatusIndicator(m.ui, message, m.workingIndicatorOptions, colorFn))
}

// =========================================================================
// Pending messages / queues
// =========================================================================

// getAllQueuedMessages returns the steering/follow-up queue contents
// including the compaction queue (pi: getAllQueuedMessages).
func (m *InteractiveMode) getAllQueuedMessages() (steering []string, followUp []string) {
	steering = append(steering, m.session().GetSteeringMessages()...)
	followUp = append(followUp, m.session().GetFollowUpMessages()...)
	for _, msg := range m.compactionQueuedMessages {
		if msg.Mode == "steer" {
			steering = append(steering, msg.Text)
		} else {
			followUp = append(followUp, msg.Text)
		}
	}
	return steering, followUp
}

// clearAllQueues clears all queued messages and returns their contents
// (pi: clearAllQueues).
func (m *InteractiveMode) clearAllQueues() (steering []string, followUp []string) {
	steering, followUp = m.session().ClearQueue()
	for _, msg := range m.compactionQueuedMessages {
		if msg.Mode == "steer" {
			steering = append(steering, msg.Text)
		} else {
			followUp = append(followUp, msg.Text)
		}
	}
	m.compactionQueuedMessages = nil
	return steering, followUp
}

// updatePendingMessagesDisplay re-renders the pending-messages area (pi:
// updatePendingMessagesDisplay).
func (m *InteractiveMode) updatePendingMessagesDisplay() {
	m.pendingMessagesContainer.Clear()
	steeringMessages, followUpMessages := m.getAllQueuedMessages()
	if len(steeringMessages) > 0 || len(followUpMessages) > 0 {
		m.pendingMessagesContainer.AddChild(tui.NewSpacer(1))
		for _, message := range steeringMessages {
			text := theme.Current().Fg("dim", "Steering: "+message)
			m.pendingMessagesContainer.AddChild(tui.NewTruncatedText(text, 1, 0))
		}
		for _, message := range followUpMessages {
			text := theme.Current().Fg("dim", "Follow-up: "+message)
			m.pendingMessagesContainer.AddChild(tui.NewTruncatedText(text, 1, 0))
		}
		dequeueHint := coretools.KeyText(core.AppKeybindingMessageDequeue)
		hintText := theme.Current().Fg("dim", "↳ "+dequeueHint+" to edit all queued messages")
		m.pendingMessagesContainer.AddChild(tui.NewTruncatedText(hintText, 1, 0))
	}
}

// restoreQueuedMessagesToEditor restores queued messages into the editor
// (pi: restoreQueuedMessagesToEditor).
func (m *InteractiveMode) restoreQueuedMessagesToEditor(abort bool) int {
	steering, followUp := m.clearAllQueues()
	allQueued := append(append([]string{}, steering...), followUp...)
	if len(allQueued) == 0 {
		m.updatePendingMessagesDisplay()
		if abort {
			m.agent().Abort()
		}
		return 0
	}
	queuedText := strings.Join(allQueued, "\n\n")
	currentText := m.editor.GetText()
	parts := []string{}
	if strings.TrimSpace(queuedText) != "" {
		parts = append(parts, queuedText)
	}
	if strings.TrimSpace(currentText) != "" {
		parts = append(parts, currentText)
	}
	combinedText := strings.Join(parts, "\n\n")
	m.editor.SetText(combinedText)
	m.updatePendingMessagesDisplay()
	if abort {
		m.agent().Abort()
	}
	return len(allQueued)
}

// handleDequeue restores queued messages to the editor (pi: handleDequeue).
func (m *InteractiveMode) handleDequeue() {
	restored := m.restoreQueuedMessagesToEditor(false)
	if restored == 0 {
		m.showStatus("No queued messages to restore")
	} else {
		m.showStatus(fmt.Sprintf("Restored %d queued message%s to editor", restored, pluralSuffix(restored)))
	}
}

// queueCompactionMessage queues a message until compaction finishes (pi:
// queueCompactionMessage).
func (m *InteractiveMode) queueCompactionMessage(text string, mode string) {
	m.compactionQueuedMessages = append(m.compactionQueuedMessages, CompactionQueuedMessage{Text: text, Mode: mode})
	m.editorAddToHistory(text)
	m.editor.SetText("")
	m.updatePendingMessagesDisplay()
	m.showStatus("Queued message for after compaction")
}

// isExtensionCommand checks whether text invokes a registered extension
// command (pi: isExtensionCommand).
func (m *InteractiveMode) isExtensionCommand(text string) bool {
	if !strings.HasPrefix(text, "/") {
		return false
	}

	rest := text[1:]
	commandName := rest
	if spaceIndex := strings.Index(rest, " "); spaceIndex != -1 {
		commandName = rest[:spaceIndex]
	}
	return m.session().GetExtensionRunner().GetCommand(commandName) != nil
}

// flushCompactionQueue sends messages queued during compaction (pi:
// flushCompactionQueue).
func (m *InteractiveMode) flushCompactionQueue(ctx context.Context, willRetry bool) {
	if len(m.compactionQueuedMessages) == 0 {
		return
	}

	queuedMessages := append([]CompactionQueuedMessage{}, m.compactionQueuedMessages...)
	m.compactionQueuedMessages = nil
	m.updatePendingMessagesDisplay()

	restoreQueue := func(err error) {
		m.session().ClearQueue()
		m.compactionQueuedMessages = queuedMessages
		m.updatePendingMessagesDisplay()
		m.ShowError(fmt.Sprintf("Failed to send queued message%s: %s", pluralSuffix(len(queuedMessages)), errorMessageOf(err)))
	}

	runQueued := func(messages []CompactionQueuedMessage) error {
		for _, message := range messages {
			if m.isExtensionCommand(message.Text) {
				if err := m.session().Prompt(ctx, message.Text, nil); err != nil {
					return err
				}
			} else if message.Mode == "followUp" {
				if err := m.session().FollowUp(ctx, message.Text, nil); err != nil {
					return err
				}
			} else {
				if err := m.session().Steer(ctx, message.Text, nil); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if willRetry {
		// When retry is pending, queue messages for the retry turn
		if err := runQueued(queuedMessages); err != nil {
			restoreQueue(err)
			return
		}
		m.updatePendingMessagesDisplay()
		return
	}

	// Find first non-extension-command message to use as prompt
	firstPromptIndex := -1
	for i, message := range queuedMessages {
		if !m.isExtensionCommand(message.Text) {
			firstPromptIndex = i
			break
		}
	}
	if firstPromptIndex == -1 {
		// All extension commands - execute them all
		for _, message := range queuedMessages {
			if err := m.session().Prompt(ctx, message.Text, nil); err != nil {
				restoreQueue(err)
				return
			}
		}
		return
	}

	// Execute any extension commands before the first prompt
	if err := runQueued(queuedMessages[:firstPromptIndex]); err != nil {
		restoreQueue(err)
		return
	}

	firstPrompt := queuedMessages[firstPromptIndex]
	rest := queuedMessages[firstPromptIndex+1:]

	// Start a prompt when idle, or queue it into a run still finishing
	// compaction.
	if err := m.session().Prompt(ctx, firstPrompt.Text, &core.PromptOptions{StreamingBehavior: firstPrompt.Mode}); err != nil {
		restoreQueue(err)
		return
	}

	// Queue remaining messages
	if err := runQueued(rest); err != nil {
		restoreQueue(err)
		return
	}
	m.updatePendingMessagesDisplay()
}

// flushPendingBashComponents moves pending bash components from the pending
// area to chat (pi: flushPendingBashComponents).
func (m *InteractiveMode) flushPendingBashComponents() {
	for _, component := range m.pendingBashComponents {
		m.pendingMessagesContainer.RemoveChild(component)
		m.chatContainer.AddChild(component)
	}
	m.pendingBashComponents = nil
}

// =========================================================================
// Bash / commands
// =========================================================================

// handleBashCommand executes a `!` command and streams its output (pi:
// handleBashCommand; the extension user_bash interception branch lands with
// Phase 7 — the plain execution path runs here).
func (m *InteractiveMode) handleBashCommand(ctx context.Context, command string, excludeFromContext bool) {
	isDeferred := m.session().IsStreaming()
	m.bashComponent = components.NewBashExecutionComponent(command, m.ui, excludeFromContext)

	if isDeferred {
		// Show in pending area when agent is streaming
		m.pendingMessagesContainer.AddChild(m.bashComponent)
		m.pendingBashComponents = append(m.pendingBashComponents, m.bashComponent)
	} else {
		// Show in chat immediately when agent is idle
		m.chatContainer.AddChild(m.bashComponent)
	}
	m.ui.RequestRender(false)

	result, err := m.session().ExecuteBash(ctx, command, func(chunk string) {
		if m.bashComponent != nil {
			m.bashComponent.AppendOutput(chunk)
			m.ui.RequestRender(false)
		}
	}, &core.BashExecuteOptions{ExcludeFromContext: excludeFromContext})

	if err != nil {
		if m.bashComponent != nil {
			m.bashComponent.SetComplete(nil, false, nil, "")
		}
		m.ShowError("Bash command failed: " + errorMessageOf(err))
	} else if m.bashComponent != nil {
		var truncation *harnessutils.TruncationResult
		if result.Truncated {
			truncation = &harnessutils.TruncationResult{Truncated: true, Content: result.Output}
		}
		m.bashComponent.SetComplete(result.ExitCode, result.Cancelled, truncation, result.FullOutputPath)
	}

	m.bashComponent = nil
	m.ui.RequestRender(false)
}

// handleClearCommand starts a new session (pi: handleClearCommand).
func (m *InteractiveMode) handleClearCommand() {
	m.clearStatusIndicator("")
	_, err := m.runtimeHost.NewSession(context.Background(), nil)
	if err != nil {
		m.ShowError("Failed to create session: " + errorMessageOf(err))
		return
	}
	m.chatContainer.AddChild(tui.NewSpacer(1))
	m.chatContainer.AddChild(tui.NewText(theme.Current().Fg("accent", "✓ New session started"), 1, 1))
	m.ui.RequestRender(false)
}

// handleCompactCommand triggers compaction; failures arrive as events (pi:
// handleCompactCommand).
func (m *InteractiveMode) handleCompactCommand(customInstructions string) {
	m.clearStatusIndicator("")
	_, _ = m.session().Compact(context.Background(), customInstructions)
}

// =========================================================================
// Shared helpers
// =========================================================================

// getMarkdownThemeWithSettings builds the markdown theme with the user's
// code-block indent (pi: getMarkdownThemeWithSettings).
func (m *InteractiveMode) getMarkdownThemeWithSettings() tui.MarkdownTheme {
	markdownTheme := theme.GetMarkdownTheme()
	markdownTheme.CodeBlockIndent = m.settingsManager().GetCodeBlockIndent()
	return markdownTheme
}

// getMarkdownTransformers returns the markdown transformers (pi:
// getMarkdownTransformers; extension transformers land with Phase 7). The
// mermaid transformer renders mermaid code blocks as Unicode diagrams (pi:
// the mermaidMarkdownTransformer field built at construction with a getMode
// closure over settingsManager and the live global theme).
func (m *InteractiveMode) getMarkdownTransformers() []core.MarkdownTransformer {
	return []core.MarkdownTransformer{
		components.CreateMermaidMarkdownTransformer(components.MermaidTransformerOptions{
			GetMode: func() core.MermaidRenderingMode {
				return m.settingsManager().GetMermaidRenderingMode()
			},
			Theme: func() components.MermaidTheme { return theme.Current() },
		}),
	}
}

// getRegisteredToolDefinition resolves a tool definition for rendering (pi:
// getRegisteredToolDefinition; v0.85.1 merges built-in renderers here so
// extension tools named like a built-in still render, and the component no
// longer imports the tool implementations).
func (m *InteractiveMode) getRegisteredToolDefinition(toolName string) *coretools.ToolDefinition {
	return coretools.WithBuiltInRenderers(toolName, m.session().GetToolDefinition(toolName))
}

// modelPriceSource adapts the model runtime to the pricing lookup (pig:
// ModelPriceSource seam).
func (m *InteractiveMode) modelPriceSource() core.ModelPriceSource {
	return core.ModelPriceSourceFunc(func(provider string, modelID string) *ai.Model {
		if model, ok := m.session().GetModelRuntime().GetModel(provider, modelID); ok {
			return &model
		}
		return nil
	})
}

// updateAvailableProviderCount refreshes the footer provider count (pi:
// updateAvailableProviderCount).
func (m *InteractiveMode) updateAvailableProviderCount() {
	var models []ai.Model
	if scoped := m.session().ScopedModels(); len(scoped) > 0 {
		for _, scopedModel := range scoped {
			models = append(models, scopedModel.Model)
		}
	} else {
		models = m.session().GetModelRuntime().GetAvailableSnapshot()
	}
	uniqueProviders := map[string]bool{}
	for _, model := range models {
		uniqueProviders[string(model.Provider)] = true
	}
	m.footerDataProviderView.SetAvailableProviderCount(len(uniqueProviders))
}

// formatResumeCommand builds the `pi --resume` hint for a persisted session
// (pi: formatResumeCommand).
func formatResumeCommand(sessionManager *core.SessionManager) string {
	if !stdoutIsTTY() {
		return ""
	}
	if !sessionManager.IsPersisted() {
		return ""
	}

	sessionFile := sessionManager.GetSessionFile()
	if sessionFile == "" {
		return ""
	}
	if _, err := os.Stat(sessionFile); err != nil {
		return ""
	}

	args := []string{codingagent.AppName}
	if !sessionManager.UsesDefaultSessionDir() {
		args = append(args, "--session-dir", quoteIfNeeded(sessionManager.GetSessionDir()))
	}
	args = append(args, "--session", sessionManager.GetSessionID())
	return strings.Join(args, " ")
}

// quoteIfNeeded shell-quotes a value when needed (pi: quoteIfNeeded).
func quoteIfNeeded(value string) string {
	if len(value) > 0 && !strings.ContainsFunc(value, func(r rune) bool {
		isSafe := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			strings.ContainsRune("_-./~:@", r)
		return !isSafe
	}) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\\''`) + "'"
}

func stdoutIsTTY() bool {
	stat, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func loPtrInt(v int64) *int { i := int(v); return &i }

// resultToComponentResult converts a tool result to the renderer result
// shape. pi spreads the payload straight into updateResult for both live
// seams (interactive-mode.ts:3351/3360): tool_execution_update/end carry an
// agent.AgentToolResult (content + details), while the session-restore path
// (transcript entries) carries a ToolResultMessage — all forms must surface.
func resultToComponentResult(result any, isError bool) components.ToolExecutionResult {
	out := components.ToolExecutionResult{IsError: isError}
	switch r := result.(type) {
	case agent.AgentToolResult:
		out.Content = r.Content
		out.Details = r.Details
	case *agent.AgentToolResult:
		if r != nil {
			out.Content = r.Content
			out.Details = r.Details
		}
	case *types.ToolResultMessage:
		out.Content = r.Content
		out.Details = r.Details
	case types.ToolResultMessage:
		out.Content = r.Content
		out.Details = r.Details
	}
	return out
}

// mountInteractiveTui mounts the shared component tree on the active
// renderer (pi: mountInteractiveTui).
func (m *InteractiveMode) mountInteractiveTui(renderer tui.TUI, components []tui.Component) {
	for _, component := range components {
		renderer.AddChild(component)
	}
	if vp, ok := renderer.(tui.ViewportTUI); ok {
		_ = vp
		if m.fullscreenLayoutRoot == nil {
			panic("Fullscreen layout is not initialized")
		}
		renderer.(interface{ SetLayoutRoot(tui.Component) }).SetLayoutRoot(m.fullscreenLayoutRoot)
	}
}

// isViewportTUI reports whether the renderer is a viewport TUI (pi:
// TuiLayouts.isViewportTUI).
func isViewportTUI(renderer tui.TUI) bool {
	_, ok := renderer.(tui.ViewportTUI)
	return ok
}

// stopInteractiveTui exits fullscreen via transcript before stopping (pi:
// stopInteractiveTui).
func (m *InteractiveMode) stopInteractiveTui(fullscreenExitOutput core.FullscreenExitOutput) {
	if m.renderer.Mode() == tui.TuiModeFullscreen && fullscreenExitOutput == core.FullscreenExitOutputTranscript {
		for m.renderer.HasOverlay() {
			m.renderer.HideOverlay()
		}
		m.switchTuiMode(core.TuiModeRegular, false, false)
		m.renderer.RenderNow(true)
	}
	m.ui.Stop(&tui.TuiStopOptions{PreserveScreen: m.renderer.Mode() == tui.TuiModeFullscreen})
}

// switchTuiMode swaps the active renderer, preserving the component tree
// (pi: switchTuiMode). ok is always false when overlays are open (pi:
// returns false).
func (m *InteractiveMode) switchTuiMode(mode core.TuiMode, restoreProgress bool, startRenderer bool) bool {
	previousUi := m.renderer
	if mode == previousUi.Mode() {
		return true
	}
	if previousUi.HasOverlay() {
		return false
	}

	components := append([]tui.Component(nil), previousUi.(*tui.TuiBase).Children...)
	focus := previousUi.(interface{ GetFocusedComponent() tui.Component }).GetFocusedComponent()
	terminal := previousUi.Terminal()
	showHardwareCursor := previousUi.GetShowHardwareCursor()
	clearOnShrink := previousUi.GetClearOnShrink()
	if _, ok := previousUi.(*tui.TuiMainScreen); ok {
		m.mainScreenRenderState = previousUi.(*tui.TuiMainScreen).CaptureRenderState()
		m.hasMainScreenRenderState = true
	}

	previousUi.Stop(&tui.TuiStopOptions{PreserveScreen: true})
	previousUi.SetFocus(nil)
	previousUi.Clear()
	if isViewportTUI(previousUi) {
		previousUi.(interface{ SetLayoutRoot(tui.Component) }).SetLayoutRoot(nil)
	}

	nextUi := createInteractiveTui(createInteractiveTuiOptions{
		tuiMode:                mode,
		showHardwareCursor:     showHardwareCursor,
		logDirectory:           codingagent.GetAgentDir(),
		onRightClickPaste:      m.handleRightClickPaste,
		fullscreenCopyOnSelect: m.settingsManager().GetFullscreenCopyOnSelect(),
	})
	nextUi.SetClearOnShrink(clearOnShrink)
	if _, ok := nextUi.(*tui.TuiMainScreen); ok && m.hasMainScreenRenderState {
		nextUi.(*tui.TuiMainScreen).RestoreRenderState(m.mainScreenRenderState)
	}
	m.renderer = nextUi
	m.options.TuiMode = mode
	m.mountInteractiveTui(nextUi, components)
	nextUi.Invalidate()
	nextUi.SetFocus(focus)
	if !startRenderer {
		return true
	}
	nextUi.Start()
	m.themeController.RebindTui()
	m.rebindExtensionTerminalInputListeners()
	if restoreProgress && m.settingsManager().GetShowTerminalProgress() &&
		(m.session().IsStreaming() || m.session().IsCompacting()) {
		terminal.SetProgress(true)
	}
	return true
}

// stopInteractive tears down the TUI and subscriptions (pi: stop).
func (m *InteractiveMode) stopInteractive() {
	disposeActive := m.activeSelectorDispose
	if disposeActive != nil {
		disposeActive()
	}
	if m.settingsManager().GetShowTerminalProgress() {
		m.ui.Terminal().SetProgress(false)
	}
	m.clearStatusIndicator("")
	m.themeController.DisableAutoSync()
	m.clearExtensionTerminalInputListeners()
	m.footer.Dispose()
	if m.footerDataProviderView != nil {
		m.footerDataProviderView.Dispose()
	}
	if m.unsubscribe != nil {
		m.unsubscribe()
		m.unsubscribe = nil
	}
	if m.isInitialized {
		m.stopInteractiveTui(m.settingsManager().GetFullscreenExitOutput())
		m.isInitialized = false
	}
	m.unregisterSignalHandlers()
	// Stop the submit worker (pig concurrency note).
	select {
	case <-m.submitDone:
	default:
		close(m.submitCh)
		<-m.submitDone
	}
}

// =========================================================================
// Startup notices / changelog (pi: showStartupNoticesIfNeeded,
// getChangelogForDisplay, reportInstallTelemetry, checkTmuxKeyboardSetup)
// =========================================================================

// showStartupNoticesIfNeeded shows the "What's New" changelog once (pi:
// showStartupNoticesIfNeeded).
func (m *InteractiveMode) showStartupNoticesIfNeeded() {
	if m.startupNoticesShown {
		return
	}
	m.startupNoticesShown = true

	if m.changelogMarkdown == "" {
		return
	}

	if len(m.chatContainer.Children) > 0 {
		m.chatContainer.AddChild(tui.NewSpacer(1))
	}
	m.chatContainer.AddChild(components.NewDynamicBorder(nil))
	if m.settingsManager().GetCollapseChangelog() {
		latestVersion := m.version
		if match := regexp.MustCompile(`##\s+\[?(\d+\.\d+\.\d+)\]?`).FindStringSubmatch(m.changelogMarkdown); match != nil {
			latestVersion = match[1]
		}
		condensedText := fmt.Sprintf("Updated to v%s. Use %s to view full changelog.", latestVersion,
			theme.Current().Bold("/changelog"))
		m.chatContainer.AddChild(tui.NewText(condensedText, 1, 0))
	} else {
		m.chatContainer.AddChild(tui.NewText(theme.Current().Bold(theme.Current().Fg("accent", "What's New")), 1, 0))
		m.chatContainer.AddChild(tui.NewSpacer(1))
		m.chatContainer.AddChild(tui.NewMarkdown(strings.TrimSpace(m.changelogMarkdown), 1, 0,
			m.getMarkdownThemeWithSettings(), nil, nil))
		m.chatContainer.AddChild(tui.NewSpacer(1))
	}
	m.chatContainer.AddChild(components.NewDynamicBorder(nil))
}

// getChangelogForDisplay returns new changelog entries since the last seen
// version, skipping resumed sessions (pi: getChangelogForDisplay).
func (m *InteractiveMode) getChangelogForDisplay() string {
	// Skip changelog for resumed/continued sessions (already have messages)
	if len(m.session().Messages()) > 0 {
		return ""
	}

	lastVersion, ok := m.settingsManager().GetLastChangelogVersion()
	entries := utils.ParseChangelog(codingagent.GetChangelogPath())

	if !ok {
		// Fresh install - record the version, send telemetry, don't show changelog
		m.settingsManager().SetLastChangelogVersion(codingagent.Version)
		m.reportInstallTelemetry(codingagent.Version)
		return ""
	}

	newEntries := utils.GetNewEntries(entries, lastVersion)
	if len(newEntries) > 0 {
		m.settingsManager().SetLastChangelogVersion(codingagent.Version)
		m.reportInstallTelemetry(codingagent.Version)
		contents := make([]string, 0, len(newEntries))
		for i := range newEntries {
			contents = append(contents, utils.NormalizeChangelogLinks(newEntries[i].Content, newEntries[i]))
		}
		return strings.Join(contents, "\n\n")
	}

	return ""
}

// reportInstallTelemetry posts the install report (pi:
// reportInstallTelemetry); best-effort, errors swallowed.
func (m *InteractiveMode) reportInstallTelemetry(version string) {
	if os.Getenv("PI_OFFLINE") != "" {
		return
	}
	if !core.IsInstallTelemetryEnabled(m.settingsManager(), nil) {
		return
	}
	go func() {
		defer pigutils.RecoverPanic("interactive.installReport")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "GET",
			"https://pi.dev/api/report-install?version="+url.QueryEscape(version), nil)
		if err != nil {
			return
		}
		req.Header.Set("User-Agent", utils.GetPiUserAgent(version))
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
}

// checkTmuxKeyboardSetup warns about tmux keyboard settings (pi:
// checkTmuxKeyboardSetup); "" = no warning.
func (m *InteractiveMode) checkTmuxKeyboardSetup(ctx context.Context) string {
	if os.Getenv("TMUX") == "" {
		return ""
	}

	runTmuxShow := func(option string) string {
		showCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		out, err := exec.CommandContext(showCtx, "tmux", "show", "-gv", option).Output()
		if err != nil {
			// Unquerybable (timeout, sandbox, ...) → pi returns undefined → no warning.
			return ""
		}
		return strings.TrimSpace(string(out))
	}

	extendedKeys := runTmuxShow("extended-keys")
	if extendedKeys == "" {
		return ""
	}

	if extendedKeys != "on" && extendedKeys != "always" {
		return "tmux extended-keys is off. Modified Enter keys may not work. Add `set -g extended-keys on` to ~/.tmux.conf and restart tmux."
	}

	extendedKeysFormat := runTmuxShow("extended-keys-format")
	if extendedKeysFormat == "" {
		// pi distinguishes a failed query (undefined) from an empty value;
		// tmux -gv never prints an empty value for these options, so pig
		// treats both as unqueryable (declared).
		return ""
	}
	if extendedKeysFormat == "xterm" {
		return "tmux extended-keys-format is xterm. Pi works best with csi-u. Add `set -g extended-keys-format csi-u` to ~/.tmux.conf and restart tmux."
	}

	return ""
}

// =========================================================================
// Resource path labels (pi: formatDisplayPath ... formatDiagnostics,
// 1314–1686)
// =========================================================================

// formatDisplayPath replaces the home directory with ~ (pi:
// formatDisplayPath).
func (m *InteractiveMode) formatDisplayPath(p string) string {
	result := p
	home, err := os.UserHomeDir()
	if err == nil && home != "" && strings.HasPrefix(result, home) {
		result = "~" + result[len(home):]
	}
	return result
}

// formatExtensionDisplayPath strips index entry files (pi:
// formatExtensionDisplayPath).
func (m *InteractiveMode) formatExtensionDisplayPath(path string) string {
	result := m.formatDisplayPath(path)
	result = regexp.MustCompile(`/index\.ts$`).ReplaceAllString(result, "")
	result = regexp.MustCompile(`/index\.js$`).ReplaceAllString(result, "")
	return result
}

// formatContextPath renders a context-file path relative to the session cwd
// when inside it (pi: formatContextPath).
func (m *InteractiveMode) formatContextPath(p string) string {
	cwd, _ := filepath.Abs(m.sessionManager().GetCwd())
	var absolutePath string
	if filepath.IsAbs(p) {
		absolutePath, _ = filepath.Abs(p)
	} else {
		absolutePath, _ = filepath.Abs(filepath.Join(cwd, p))
	}
	if relativePath := utils.GetCwdRelativePath(absolutePath, cwd); relativePath != "" {
		return relativePath
	}
	return m.formatDisplayPath(absolutePath)
}

// getShortPath renders a path relative to the resource's package root (pi:
// getShortPath).
func (m *InteractiveMode) getShortPath(fullPath string, sourceInfo *core.SourceInfo) string {
	normalizedFullPath := strings.ReplaceAll(fullPath, "\\", "/")
	baseDir := ""
	if sourceInfo != nil {
		baseDir = sourceInfo.BaseDir
	}
	if baseDir != "" && m.isPackageSource(sourceInfo) {
		normalizedBaseDir := strings.ReplaceAll(baseDir, "\\", "/")
		npmRootMatch := regexp.MustCompile(`^(.*/node_modules)/(@?[^/]+(?:/[^/]+)?)$`).FindStringSubmatch(normalizedBaseDir)
		// If fullPath is under the same node_modules root as baseDir, preserve
		// that relative topology.
		if len(npmRootMatch) > 1 && npmRootMatch[1] != "" && strings.HasPrefix(normalizedFullPath, npmRootMatch[1]+"/") {
			return relPosix(normalizedBaseDir, normalizedFullPath)
		}

		absBase, err1 := filepath.Abs(baseDir)
		absFull, err2 := filepath.Abs(fullPath)
		if err1 == nil && err2 == nil {
			if rel, err := filepath.Rel(absBase, absFull); err == nil && rel != "" && rel != "." &&
				!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
				return strings.ReplaceAll(rel, "\\", "/")
			}
		}
	}

	source := ""
	if sourceInfo != nil {
		source = sourceInfo.Source
	}
	npmMatch := regexp.MustCompile(`node_modules/(@?[^/]+(?:/[^/]+)?)/(.*)`).FindStringSubmatch(normalizedFullPath)
	if npmMatch != nil && strings.HasPrefix(source, "npm:") {
		return npmMatch[2]
	}

	gitMatch := regexp.MustCompile(`git/[^/]+/[^/]+/(.*)`).FindStringSubmatch(normalizedFullPath)
	if gitMatch != nil && strings.HasPrefix(source, "git:") {
		return gitMatch[1]
	}

	return m.formatDisplayPath(fullPath)
}

// getCompactPathLabel returns the last path segment (pi:
// getCompactPathLabel).
func (m *InteractiveMode) getCompactPathLabel(resourcePath string, sourceInfo *core.SourceInfo) string {
	shortPath := m.getShortPath(resourcePath, sourceInfo)
	normalizedPath := strings.ReplaceAll(shortPath, "\\", "/")
	var segments []string
	for _, segment := range strings.Split(normalizedPath, "/") {
		if segment != "" && segment != "~" {
			segments = append(segments, segment)
		}
	}
	if len(segments) > 0 {
		return segments[len(segments)-1]
	}
	return shortPath
}

// getCompactPackageSourceLabel strips the scheme from a package source (pi:
// getCompactPackageSourceLabel).
func (m *InteractiveMode) getCompactPackageSourceLabel(sourceInfo *core.SourceInfo) string {
	source := ""
	if sourceInfo != nil {
		source = sourceInfo.Source
	}
	if strings.HasPrefix(source, "npm:") {
		trimmed := strings.TrimPrefix(source, "npm:")
		if trimmed == "" {
			return source
		}
		return trimmed
	}

	// parseGitUrl (pi: utils/git.ts) lands with the package-manager git.ts
	// port (P7); git sources cannot occur until then (declared).
	return source
}

// getCompactExtensionLabel renders a package extension as "pkg:path" (pi:
// getCompactExtensionLabel).
func (m *InteractiveMode) getCompactExtensionLabel(resourcePath string, sourceInfo *core.SourceInfo) string {
	if !m.isPackageSource(sourceInfo) {
		return m.getCompactPathLabel(resourcePath, sourceInfo)
	}

	sourceLabel := m.getCompactPackageSourceLabel(sourceInfo)
	if sourceLabel == "" {
		return m.getCompactPathLabel(resourcePath, sourceInfo)
	}

	shortPath := strings.ReplaceAll(m.getShortPath(resourcePath, sourceInfo), "\\", "/")
	packagePath := strings.TrimPrefix(shortPath, "extensions/")
	dir, name := path.Split(packagePath)
	dir = strings.TrimSuffix(dir, "/")
	if name == "index" || name == "index.ts" || name == "index.js" {
		// pi: path.posix.parse name (without extension)
		if dir == "" || dir == "." {
			return sourceLabel
		}
		return sourceLabel + ":" + dir
	}
	return sourceLabel + ":" + packagePath
}

// getCompactDisplayPathSegments splits a display path (pi:
// getCompactDisplayPathSegments).
func (m *InteractiveMode) getCompactDisplayPathSegments(resourcePath string) []string {
	normalized := strings.ReplaceAll(m.formatDisplayPath(resourcePath), "\\", "/")
	var segments []string
	for _, segment := range strings.Split(normalized, "/") {
		if segment != "" && segment != "~" {
			segments = append(segments, segment)
		}
	}
	return segments
}

// getCompactNonPackageExtensionLabel finds the shortest unique path suffix
// (pi: getCompactNonPackageExtensionLabel).
func (m *InteractiveMode) getCompactNonPackageExtensionLabel(resourcePath string, index int,
	allPaths []compactPathEntry) string {
	var segments []string
	if index < len(allPaths) {
		segments = allPaths[index].segments
	}
	if len(segments) == 0 {
		return m.getCompactPathLabel(resourcePath, nil)
	}

	for segmentCount := 1; segmentCount <= len(segments); segmentCount++ {
		candidateParts := segments[len(segments)-segmentCount:]
		candidate := strings.Join(candidateParts, "/")
		isUnique := true
		for itemIndex, item := range allPaths {
			if itemIndex == index {
				continue
			}
			tail := item.segments
			if len(tail) > segmentCount {
				tail = tail[len(tail)-segmentCount:]
			}
			if strings.Join(tail, "/") == candidate {
				isUnique = false
				break
			}
		}
		if isUnique {
			return candidate
		}
	}

	return strings.Join(segments, "/")
}

// compactPathEntry is one entry in getCompactExtensionLabels' dedup set.
type compactPathEntry struct {
	path     string
	segments []string
}

// getCompactExtensionLabels computes compact labels for the extension list
// (pi: getCompactExtensionLabels).
func (m *InteractiveMode) getCompactExtensionLabels(extensions []extensionFixture) []string {
	var nonPackageExtensions []compactPathEntry
	for _, extension := range extensions {
		segments := m.getCompactDisplayPathSegments(extension.path)
		if len(segments) > 1 {
			last := segments[len(segments)-1]
			if last == "index.ts" || last == "index.js" {
				segments = segments[:len(segments)-1]
			}
		}
		if m.isPackageSource(extension.sourceInfo) {
			continue
		}
		nonPackageExtensions = append(nonPackageExtensions, compactPathEntry{path: extension.path, segments: segments})
	}

	labels := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		if m.isPackageSource(extension.sourceInfo) {
			labels = append(labels, m.getCompactExtensionLabel(extension.path, extension.sourceInfo))
			continue
		}
		nonPackageIndex := -1
		for i, item := range nonPackageExtensions {
			if item.path == extension.path {
				nonPackageIndex = i
				break
			}
		}
		if nonPackageIndex == -1 {
			labels = append(labels, m.getCompactPathLabel(extension.path, extension.sourceInfo))
			continue
		}
		labels = append(labels, m.getCompactNonPackageExtensionLabel(extension.path, nonPackageIndex, nonPackageExtensions))
	}
	return labels
}

// extensionFixture is one extension path/sourceInfo pair (pi's inline
// { path, sourceInfo } object).
type extensionFixture struct {
	path       string
	sourceInfo *core.SourceInfo
}

// displaySourceInfo is the compact source label (pi:
// getDisplaySourceInfo's return object).
type displaySourceInfo struct {
	label      string
	scopeLabel string
	color      string // "accent" | "muted"
}

// getDisplaySourceInfo maps source/scope onto a display label (pi:
// getDisplaySourceInfo).
func (m *InteractiveMode) getDisplaySourceInfo(sourceInfo *core.SourceInfo) displaySourceInfo {
	source := "local"
	scope := core.SourceScope("project")
	if sourceInfo != nil {
		if sourceInfo.Source != "" {
			source = sourceInfo.Source
		}
		if sourceInfo.Scope != "" {
			scope = sourceInfo.Scope
		}
	}
	if source == "local" {
		switch scope {
		case "user":
			return displaySourceInfo{label: "user", color: "muted"}
		case "project":
			return displaySourceInfo{label: "project", color: "muted"}
		case "temporary":
			return displaySourceInfo{label: "path", scopeLabel: "temp", color: "muted"}
		}
		return displaySourceInfo{label: "path", color: "muted"}
	}

	if source == "cli" {
		scopeLabel := ""
		if scope == "temporary" {
			scopeLabel = "temp"
		}
		return displaySourceInfo{label: "path", scopeLabel: scopeLabel, color: "muted"}
	}

	scopeLabel := ""
	switch scope {
	case "user":
		scopeLabel = "user"
	case "project":
		scopeLabel = "project"
	case "temporary":
		scopeLabel = "temp"
	}
	return displaySourceInfo{label: source, scopeLabel: scopeLabel, color: "accent"}
}

// getScopeGroup buckets a resource into user/project/path (pi:
// getScopeGroup).
func (m *InteractiveMode) getScopeGroup(sourceInfo *core.SourceInfo) string {
	source := "local"
	scope := core.SourceScope("project")
	if sourceInfo != nil {
		if sourceInfo.Source != "" {
			source = sourceInfo.Source
		}
		if sourceInfo.Scope != "" {
			scope = sourceInfo.Scope
		}
	}
	if source == "cli" || scope == "temporary" {
		return "path"
	}
	if scope == "user" {
		return "user"
	}
	if scope == "project" {
		return "project"
	}
	return "path"
}

// isPackageSource reports npm:/git: sources (pi: isPackageSource).
func (m *InteractiveMode) isPackageSource(sourceInfo *core.SourceInfo) bool {
	source := ""
	if sourceInfo != nil {
		source = sourceInfo.Source
	}
	return strings.HasPrefix(source, "npm:") || strings.HasPrefix(source, "git:")
}

// scopeGroup is one display scope bucket (pi: buildScopeGroups' group
// objects).
type scopeGroup struct {
	scope    string
	paths    []extensionFixture
	packages map[string][]extensionFixture
}

// buildScopeGroups groups resources by display scope, package sources keyed
// by source string (pi: buildScopeGroups).
func (m *InteractiveMode) buildScopeGroups(items []extensionFixture) []scopeGroup {
	groups := map[string]*scopeGroup{
		"user":    {scope: "user", packages: map[string][]extensionFixture{}},
		"project": {scope: "project", packages: map[string][]extensionFixture{}},
		"path":    {scope: "path", packages: map[string][]extensionFixture{}},
	}

	for _, item := range items {
		groupKey := m.getScopeGroup(item.sourceInfo)
		group := groups[groupKey]
		source := "local"
		if item.sourceInfo != nil && item.sourceInfo.Source != "" {
			source = item.sourceInfo.Source
		}
		if m.isPackageSource(item.sourceInfo) {
			group.packages[source] = append(group.packages[source], item)
		} else {
			group.paths = append(group.paths, item)
		}
	}

	var out []scopeGroup
	for _, key := range []string{"project", "user", "path"} {
		group := groups[key]
		if len(group.paths) > 0 || len(group.packages) > 0 {
			out = append(out, *group)
		}
	}
	return out
}

// formatScopeGroupOptions carries the per-item formatters (pi:
// formatScopeGroups' options object).
type formatScopeGroupOptions struct {
	formatPath        func(item extensionFixture) string
	formatPackagePath func(item extensionFixture, source string) string
}

// formatScopeGroups renders the grouped listing (pi: formatScopeGroups).
func (m *InteractiveMode) formatScopeGroups(groups []scopeGroup, options formatScopeGroupOptions) string {
	var lines []string

	for _, group := range groups {
		lines = append(lines, fmt.Sprintf("  %s", theme.Current().Fg("accent", group.scope)))

		sortedPaths := append([]extensionFixture{}, group.paths...)
		sort.Slice(sortedPaths, func(i, j int) bool { return sortedPaths[i].path < sortedPaths[j].path })
		for _, item := range sortedPaths {
			lines = append(lines, theme.Current().Fg("dim", fmt.Sprintf("    %s", options.formatPath(item))))
		}

		sources := make([]string, 0, len(group.packages))
		for source := range group.packages {
			sources = append(sources, source)
		}
		sort.Strings(sources)
		for _, source := range sources {
			lines = append(lines, fmt.Sprintf("    %s", theme.Current().Fg("mdLink", source)))
			sortedPackagePaths := append([]extensionFixture{}, group.packages[source]...)
			sort.Slice(sortedPackagePaths, func(i, j int) bool { return sortedPackagePaths[i].path < sortedPackagePaths[j].path })
			for _, item := range sortedPackagePaths {
				lines = append(lines, theme.Current().Fg("dim", fmt.Sprintf("      %s", options.formatPackagePath(item, source))))
			}
		}
	}

	return strings.Join(lines, "\n")
}

// findSourceInfoForPath resolves the closest ancestor SourceInfo (pi:
// findSourceInfoForPath).
func (m *InteractiveMode) findSourceInfoForPath(p string, sourceInfos map[string]core.SourceInfo) *core.SourceInfo {
	if info, ok := sourceInfos[p]; ok {
		return &info
	}

	current := p
	for strings.Contains(current, "/") {
		current = current[:strings.LastIndex(current, "/")]
		if parent, ok := sourceInfos[current]; ok {
			return &parent
		}
	}
	return nil
}

// formatPathWithSource renders "label (scope) path" (pi:
// formatPathWithSource).
func (m *InteractiveMode) formatPathWithSource(p string, sourceInfo *core.SourceInfo) string {
	if sourceInfo != nil {
		shortPath := m.getShortPath(p, sourceInfo)
		display := m.getDisplaySourceInfo(sourceInfo)
		labelText := display.label
		if display.scopeLabel != "" {
			labelText = fmt.Sprintf("%s (%s)", display.label, display.scopeLabel)
		}
		return labelText + " " + shortPath
	}
	return m.formatDisplayPath(p)
}

// formatDiagnostics renders resource diagnostics grouped by collision name
// (pi: formatDiagnostics).
func (m *InteractiveMode) formatDiagnostics(diagnostics []core.ResourceDiagnostic,
	sourceInfos map[string]core.SourceInfo) string {
	var lines []string

	// Group collision diagnostics by name
	collisions := map[string][]core.ResourceDiagnostic{}
	var collisionOrder []string
	var otherDiagnostics []core.ResourceDiagnostic

	for _, d := range diagnostics {
		if d.Type == "collision" && d.Collision != nil {
			if _, ok := collisions[d.Collision.Name]; !ok {
				collisionOrder = append(collisionOrder, d.Collision.Name)
			}
			collisions[d.Collision.Name] = append(collisions[d.Collision.Name], d)
		} else {
			otherDiagnostics = append(otherDiagnostics, d)
		}
	}

	// Format collision diagnostics grouped by name (insertion order)
	for _, name := range collisionOrder {
		collisionList := collisions[name]
		if len(collisionList) == 0 || collisionList[0].Collision == nil {
			continue
		}
		first := collisionList[0].Collision
		lines = append(lines, theme.Current().Fg("warning", fmt.Sprintf("  %q collision:", name)))
		lines = append(lines, theme.Current().Fg("dim",
			fmt.Sprintf("    %s %s", theme.Current().Fg("success", "✓"),
				m.formatPathWithSource(first.WinnerPath, m.findSourceInfoForPath(first.WinnerPath, sourceInfos)))))
		for _, d := range collisionList {
			if d.Collision != nil {
				lines = append(lines, theme.Current().Fg("dim",
					fmt.Sprintf("    %s %s (skipped)", theme.Current().Fg("warning", "✗"),
						m.formatPathWithSource(d.Collision.LoserPath, m.findSourceInfoForPath(d.Collision.LoserPath, sourceInfos)))))
			}
		}
	}

	for _, d := range otherDiagnostics {
		color := "warning"
		if d.Type == "error" {
			color = "error"
		}
		if d.Path != "" {
			formattedPath := m.formatPathWithSource(d.Path, m.findSourceInfoForPath(d.Path, sourceInfos))
			lines = append(lines, theme.Current().Fg(color, fmt.Sprintf("  %s", formattedPath)))
			lines = append(lines, theme.Current().Fg(color, fmt.Sprintf("    %s", d.Message)))
		} else {
			lines = append(lines, theme.Current().Fg(color, fmt.Sprintf("  %s", d.Message)))
		}
	}

	return strings.Join(lines, "\n")
}

// getBuiltInCommandConflictDiagnostics warns when extension commands shadow
// built-ins (pi: getBuiltInCommandConflictDiagnostics).
func (m *InteractiveMode) getBuiltInCommandConflictDiagnostics(extensionRunner *core.ExtensionRunner) []core.ResourceDiagnostic {
	builtinNames := map[string]bool{}
	for _, command := range core.BuiltinSlashCommands {
		builtinNames[command.Name] = true
	}
	var out []core.ResourceDiagnostic
	for _, command := range extensionRunner.GetRegisteredCommands() {
		if !builtinNames[command.Name] {
			continue
		}
		message := fmt.Sprintf("Extension command '/%s' conflicts with built-in interactive command. Available as '/%s'.",
			command.Name, command.InvocationName)
		if command.InvocationName == command.Name {
			message = fmt.Sprintf("Extension command '/%s' conflicts with built-in interactive command. Skipping in autocomplete.", command.Name)
		}
		out = append(out, core.ResourceDiagnostic{
			Type:    "warning",
			Message: message,
			Path:    command.SourceInfo.Path,
		})
	}
	return out
}

// relPosix is filepath.Rel on slash-normalized paths (pig: pi uses
// path.posix.relative after backslash normalization).
func relPosix(baseDir, fullPath string) string {
	base := strings.Split(strings.Trim(baseDir, "/"), "/")
	target := strings.Split(strings.Trim(fullPath, "/"), "/")
	i := 0
	for i < len(base) && i < len(target) && base[i] == target[i] {
		i++
	}
	var parts []string
	for j := i; j < len(base); j++ {
		parts = append(parts, "..")
	}
	parts = append(parts, target[i:]...)
	return strings.Join(parts, "/")
}

// =========================================================================
// Loaded resources (pi: showLoadedResources, 1687–1902)
// =========================================================================

// showLoadedResourcesOptions parameterize showLoadedResources (pi: the
// options object).
type showLoadedResourcesOptions struct {
	Extensions               []extensionFixture
	Force                    bool
	ShowDiagnosticsWhenQuiet bool
}

// showLoadedResources renders the loaded-resource listing and diagnostics
// into loadedResourcesContainer (pi: showLoadedResources).
func (m *InteractiveMode) showLoadedResources(options *showLoadedResourcesOptions) {
	if options == nil {
		options = &showLoadedResourcesOptions{}
	}
	// Resource rendering is idempotent; chat clears no longer clear this
	// separate container.
	m.loadedResourcesContainer.Clear()

	showListing := options.Force || m.options.Verbose || !m.settingsManager().GetQuietStartup()
	showDiagnostics := showListing || options.ShowDiagnosticsWhenQuiet
	if !showListing && !showDiagnostics {
		return
	}

	sectionHeader := func(name string, color string) string {
		if color == "" {
			color = "mdHeading"
		}
		return theme.Current().Fg(color, "["+name+"]")
	}
	formatCompactList := func(items []string, sortItems bool) string {
		var labels []string
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item != "" {
				labels = append(labels, item)
			}
		}
		if sortItems {
			sort.Strings(labels)
		}
		return theme.Current().Fg("dim", fmt.Sprintf("  %s", strings.Join(labels, ", ")))
	}
	addLoadedSection := func(name string, collapsedBody string, expandedBody string, color string) {
		if expandedBody == "" {
			expandedBody = collapsedBody
		}
		section := newExpandableText(
			func() string { return sectionHeader(name, color) + "\n" + collapsedBody },
			func() string { return sectionHeader(name, color) + "\n" + expandedBody },
			m.getStartupExpansionState(), 0, 0,
		)
		m.loadedResourcesContainer.AddChild(section)
		m.loadedResourcesContainer.AddChild(tui.NewSpacer(1))
	}

	session := m.session()
	skills, skillDiagnostics := session.GetResourceLoader().GetSkills()
	skillsResult := core.LoadSkillsResult{Skills: skills, Diagnostics: skillDiagnostics}
	prompts, promptDiagnostics := session.GetResourceLoader().GetPrompts()
	promptsResult := struct {
		Prompts     []core.PromptTemplate
		Diagnostics []core.ResourceDiagnostic
	}{prompts, promptDiagnostics}
	// pi: getThemes returns { themes, diagnostics } with per-theme
	// sourcePath/sourceInfo; pig's ResourceLoader still exposes the
	// placeholder []any form (declared — the theme loader half lands later).
	_, _ = session.GetResourceLoader().GetThemes()
	extensions := options.Extensions
	if extensions == nil {
		for _, extension := range session.GetResourceLoader().GetExtensions().Extensions {
			if extension.Hidden {
				continue
			}
			sourceInfo := extension.SourceInfo
			extensions = append(extensions, extensionFixture{path: extension.Path, sourceInfo: &sourceInfo})
		}
	}
	sourceInfos := map[string]core.SourceInfo{}
	for _, extension := range extensions {
		if extension.sourceInfo != nil {
			sourceInfos[extension.path] = *extension.sourceInfo
		}
	}
	for _, skill := range skillsResult.Skills {
		sourceInfos[skill.FilePath] = skill.SourceInfo
	}
	for _, prompt := range promptsResult.Prompts {
		sourceInfos[prompt.FilePath] = prompt.SourceInfo
	}
	// Theme source paths would register here (declared, see above).

	if showListing {
		var contextFiles []string
		if source, ok := session.GetResourceLoader().GetSystemPromptSource(); ok {
			contextFiles = append(contextFiles, source)
		}
		contextFiles = append(contextFiles, session.GetResourceLoader().GetAppendSystemPromptSources()...)
		for _, agentsFile := range session.GetResourceLoader().GetAgentsFiles() {
			contextFiles = append(contextFiles, agentsFile.Path)
		}
		if len(contextFiles) > 0 {
			m.loadedResourcesContainer.AddChild(tui.NewSpacer(1))
			var contextList []string
			for _, f := range contextFiles {
				contextList = append(contextList, theme.Current().Fg("dim", fmt.Sprintf("  %s", m.formatDisplayPath(f))))
			}
			var contextCompactItems []string
			for _, f := range contextFiles {
				contextCompactItems = append(contextCompactItems, m.formatContextPath(f))
			}
			addLoadedSection("Context", formatCompactList(contextCompactItems, false), strings.Join(contextList, "\n"), "")
		}

		skills := skillsResult.Skills
		if len(skills) > 0 {
			fixtures := make([]extensionFixture, 0, len(skills))
			for _, skill := range skills {
				sourceInfo := skill.SourceInfo
				fixtures = append(fixtures, extensionFixture{path: skill.FilePath, sourceInfo: &sourceInfo})
			}
			groups := m.buildScopeGroups(fixtures)
			skillList := m.formatScopeGroups(groups, formatScopeGroupOptions{
				formatPath:        func(item extensionFixture) string { return m.formatDisplayPath(item.path) },
				formatPackagePath: func(item extensionFixture, _ string) string { return m.getShortPath(item.path, item.sourceInfo) },
			})
			skillNames := make([]string, 0, len(skills))
			for _, skill := range skills {
				skillNames = append(skillNames, skill.Name)
			}
			addLoadedSection("Skills", formatCompactList(skillNames, true), skillList, "")
		}

		templates := session.PromptTemplates()
		if len(templates) > 0 {
			fixtures := make([]extensionFixture, 0, len(templates))
			for _, template := range templates {
				sourceInfo := template.SourceInfo
				fixtures = append(fixtures, extensionFixture{path: template.FilePath, sourceInfo: &sourceInfo})
			}
			groups := m.buildScopeGroups(fixtures)
			templateByPath := map[string]core.PromptTemplate{}
			for _, template := range templates {
				templateByPath[template.FilePath] = template
			}
			templateList := m.formatScopeGroups(groups, formatScopeGroupOptions{
				formatPath: func(item extensionFixture) string {
					if template, ok := templateByPath[item.path]; ok {
						return "/" + template.Name
					}
					return m.formatDisplayPath(item.path)
				},
				formatPackagePath: func(item extensionFixture, _ string) string {
					if template, ok := templateByPath[item.path]; ok {
						return "/" + template.Name
					}
					return m.formatDisplayPath(item.path)
				},
			})
			promptCompactItems := make([]string, 0, len(templates))
			for _, template := range templates {
				promptCompactItems = append(promptCompactItems, "/"+template.Name)
			}
			addLoadedSection("Prompts", formatCompactList(promptCompactItems, true), templateList, "")
		}

		if len(extensions) > 0 {
			groups := m.buildScopeGroups(extensions)
			extList := m.formatScopeGroups(groups, formatScopeGroupOptions{
				formatPath: func(item extensionFixture) string { return m.formatExtensionDisplayPath(item.path) },
				formatPackagePath: func(item extensionFixture, _ string) string {
					return m.formatExtensionDisplayPath(m.getShortPath(item.path, item.sourceInfo))
				},
			})
			extensionCompactList := formatCompactList(m.getCompactExtensionLabels(extensions), true)
			addLoadedSection("Extensions", extensionCompactList, extList, "mdHeading")
		}

		// Loaded themes (excluding built-in) are deferred with the theme
		// loader half (declared, see GetThemes note above).
	}

	if showDiagnostics {
		skillDiagnostics := skillsResult.Diagnostics
		if len(skillDiagnostics) > 0 {
			warningLines := m.formatDiagnostics(skillDiagnostics, sourceInfos)
			m.loadedResourcesContainer.AddChild(tui.NewText(theme.Current().Fg("warning", "[Skill conflicts]")+"\n"+warningLines, 0, 0))
			m.loadedResourcesContainer.AddChild(tui.NewSpacer(1))
		}

		promptDiagnostics := promptsResult.Diagnostics
		if len(promptDiagnostics) > 0 {
			warningLines := m.formatDiagnostics(promptDiagnostics, sourceInfos)
			m.loadedResourcesContainer.AddChild(tui.NewText(theme.Current().Fg("warning", "[Prompt conflicts]")+"\n"+warningLines, 0, 0))
			m.loadedResourcesContainer.AddChild(tui.NewSpacer(1))
		}

		var extensionDiagnostics []core.ResourceDiagnostic
		for _, loadErr := range session.GetResourceLoader().GetExtensions().Errors {
			extensionDiagnostics = append(extensionDiagnostics, core.ResourceDiagnostic{Type: "error", Message: loadErr.Error, Path: loadErr.Path})
		}
		extensionDiagnostics = append(extensionDiagnostics, session.GetExtensionRunner().GetCommandDiagnostics()...)
		extensionDiagnostics = append(extensionDiagnostics, m.getBuiltInCommandConflictDiagnostics(session.GetExtensionRunner())...)
		extensionDiagnostics = append(extensionDiagnostics, session.GetExtensionRunner().GetShortcutDiagnostics()...)

		if len(extensionDiagnostics) > 0 {
			warningLines := m.formatDiagnostics(extensionDiagnostics, sourceInfos)
			m.loadedResourcesContainer.AddChild(tui.NewText(theme.Current().Fg("warning", "[Extension issues]")+"\n"+warningLines, 0, 0))
			m.loadedResourcesContainer.AddChild(tui.NewSpacer(1))
		}

		// Theme diagnostics arrive with the theme loader half (declared).
	}
}

// =========================================================================
// Autocomplete (pi: setupAutocompleteProvider /
// createBaseAutocompleteProvider + source-tag helpers, 627–792)
// =========================================================================

// getAutocompleteSourceTag renders the resource source tag (pi:
// getAutocompleteSourceTag).
func (m *InteractiveMode) getAutocompleteSourceTag(sourceInfo *core.SourceInfo) string {
	if sourceInfo == nil {
		return ""
	}
	var scopePrefix string
	switch sourceInfo.Scope {
	case "user":
		scopePrefix = "u"
	case "project":
		scopePrefix = "p"
	default:
		scopePrefix = "t"
	}
	source := strings.TrimSpace(sourceInfo.Source)

	if source == "auto" || source == "local" || source == "cli" {
		return scopePrefix
	}

	if strings.HasPrefix(source, "npm:") {
		return scopePrefix + ":" + source
	}

	// parseGitUrl (pi: utils/git.ts) lands with the package-manager git.ts
	// port (P7); git sources cannot occur until then (declared).
	return scopePrefix
}

// prefixAutocompleteDescription prefixes the source tag (pi:
// prefixAutocompleteDescription).
func (m *InteractiveMode) prefixAutocompleteDescription(description string, sourceInfo *core.SourceInfo) string {
	sourceTag := m.getAutocompleteSourceTag(sourceInfo)
	if sourceTag == "" {
		return description
	}
	if description != "" {
		return "[" + sourceTag + "] " + description
	}
	return "[" + sourceTag + "]"
}

// createBaseAutocompleteProvider builds the default slash-command provider
// (pi: createBaseAutocompleteProvider; the login branch lands with the T6
// login selector — getLoginProviderOptions is deferred).
func (m *InteractiveMode) createBaseAutocompleteProvider() tui.AutocompleteProvider {
	session := m.session()

	// Define commands for autocomplete
	var slashCommands []tui.SlashCommand
	for _, command := range core.BuiltinSlashCommands {
		cmd := tui.SlashCommand{
			Name:        command.Name,
			Description: command.Description,
		}
		cmd.ArgumentHint = command.ArgumentHint
		slashCommands = append(slashCommands, cmd)
	}

	for i := range slashCommands {
		switch slashCommands[i].Name {
		case "model":
			slashCommands[i].GetArgumentCompletions = func(prefix string) []tui.AutocompleteItem {
				var models []ai.Model
				if scoped := session.ScopedModels(); len(scoped) > 0 {
					for _, s := range scoped {
						models = append(models, s.Model)
					}
				} else {
					models = session.GetModelRuntime().GetAvailableSnapshot()
				}
				if len(models) == 0 {
					return nil
				}

				// Create items with provider/id format
				type modelEntry struct {
					item  modelSearchItem
					label string
				}
				entries := make([]modelEntry, 0, len(models))
				for _, model := range models {
					provider := string(model.Provider)
					entries = append(entries, modelEntry{
						item:  modelSearchItem{ID: model.ID, Provider: provider, Name: model.Name},
						label: fmt.Sprintf("%s/%s", provider, model.ID),
					})
				}
				return createFuzzyAutocompleteItems(entries, prefix, func(e modelEntry) string {
					return GetModelSearchText(e.item)
				}, func(e modelEntry) tui.AutocompleteItem {
					return tui.AutocompleteItem{
						Value:       e.label,
						Label:       e.item.ID,
						Description: e.item.Provider,
					}
				})
			}
		case "thinking":
			slashCommands[i].GetArgumentCompletions = func(prefix string) []tui.AutocompleteItem {
				levels := session.GetAvailableThinkingLevels()
				type levelEntry struct {
					level agent.ThinkingLevel
				}
				entries := make([]levelEntry, 0, len(levels))
				for _, level := range levels {
					entries = append(entries, levelEntry{level: level})
				}
				return createFuzzyAutocompleteItems(entries, prefix, func(e levelEntry) string {
					return string(e.level)
				}, func(e levelEntry) tui.AutocompleteItem {
					return tui.AutocompleteItem{Value: string(e.level), Label: string(e.level)}
				})
			}
		case "login":
			slashCommands[i].GetArgumentCompletions = func(prefix string) []tui.AutocompleteItem {
				providers := getLoginProviderCompletionOptions(m.getLoginProviderOptions(nil))
				type providerEntry struct {
					option loginProviderCompletionOption
				}
				entries := make([]providerEntry, 0, len(providers))
				for _, provider := range providers {
					entries = append(entries, providerEntry{option: provider})
				}
				return createFuzzyAutocompleteItems(entries, prefix, func(e providerEntry) string {
					return getLoginProviderSearchText(e.option)
				}, func(e providerEntry) tui.AutocompleteItem {
					return tui.AutocompleteItem{
						Value:       e.option.id,
						Label:       e.option.id,
						Description: formatLoginProviderCompletionDescription(e.option),
					}
				})
			}
		}
	}

	// Convert prompt templates to SlashCommand format for autocomplete
	var templateCommands []tui.SlashCommand
	for _, cmd := range session.PromptTemplates() {
		templateCmd := tui.SlashCommand{
			Name:        cmd.Name,
			Description: m.prefixAutocompleteDescription(cmd.Description, &cmd.SourceInfo),
		}
		templateCmd.ArgumentHint = cmd.ArgumentHint
		templateCommands = append(templateCommands, templateCmd)
	}

	// Convert extension commands to SlashCommand format
	builtinCommandNames := map[string]bool{}
	for _, c := range slashCommands {
		builtinCommandNames[c.Name] = true
	}
	var extensionCommands []tui.SlashCommand
	for _, cmd := range session.GetExtensionRunner().GetRegisteredCommands() {
		if builtinCommandNames[cmd.Name] {
			continue
		}
		sourceInfo := cmd.SourceInfo
		extensionCommands = append(extensionCommands, tui.SlashCommand{
			Name:        cmd.InvocationName,
			Description: m.prefixAutocompleteDescription(cmd.Description, &sourceInfo),
		})
	}

	// Build skill commands from session.skills (if enabled)
	m.skillCommands = map[string]string{}
	var skillCommandList []tui.SlashCommand
	if m.settingsManager().GetEnableSkillCommands() {
		skills, _ := session.GetResourceLoader().GetSkills()
		for _, skill := range skills {
			commandName := "skill:" + skill.Name
			m.skillCommands[commandName] = skill.FilePath
			sourceInfo := skill.SourceInfo
			skillCommandList = append(skillCommandList, tui.SlashCommand{
				Name:        commandName,
				Description: m.prefixAutocompleteDescription(skill.Description, &sourceInfo),
			})
		}
	}

	var allCommands []tui.SlashCommandOrItem
	for i := range slashCommands {
		allCommands = append(allCommands, slashCommands[i])
	}
	for i := range templateCommands {
		allCommands = append(allCommands, templateCommands[i])
	}
	for i := range extensionCommands {
		allCommands = append(allCommands, extensionCommands[i])
	}
	for i := range skillCommandList {
		allCommands = append(allCommands, skillCommandList[i])
	}
	return tui.NewCombinedAutocompleteProvider(allCommands, session.SessionManager.GetCwd(), m.fdPath)
}

// createFuzzyAutocompleteItems filters items via fuzzyFilter and maps them
// to autocomplete items (pi: interactive-mode.ts createFuzzyAutocompleteItems
// generic helper); nil = pi's null (no matches).
func createFuzzyAutocompleteItems[T any](items []T, prefix string,
	getSearchText func(T) string, toAutocompleteItem func(T) tui.AutocompleteItem) []tui.AutocompleteItem {
	filtered := tui.FuzzyFilter(items, prefix, getSearchText)
	if len(filtered) == 0 {
		return nil
	}
	out := make([]tui.AutocompleteItem, 0, len(filtered))
	for _, item := range filtered {
		out = append(out, toAutocompleteItem(item))
	}
	return out
}

// modelSearchItem is pi's ModelSearchItem (modes/interactive/model-search.ts);
// the canonical type + functions live in components and are re-exported in
// model_search.go.

// setupAutocompleteProvider builds the base provider and applies the wrapper
// chain (pi: setupAutocompleteProvider).
func (m *InteractiveMode) setupAutocompleteProvider() {
	provider := m.createBaseAutocompleteProvider()
	var triggerCharacters []string
	for _, wrapProvider := range m.autocompleteProviderWrappers {
		provider = wrapProvider(provider)
		if tc, ok := provider.(tui.TriggerCharactersProvider); ok {
			triggerCharacters = append(triggerCharacters, tc.TriggerCharacters()...)
		}
	}
	if len(triggerCharacters) > 0 {
		triggerCharacters = dedupeStrings(triggerCharacters)
		if tc, ok := provider.(tui.TriggerCharactersSetter); ok {
			tc.SetTriggerCharacters(triggerCharacters)
		}
	}

	m.autocompleteProvider = provider
	m.defaultEditor.SetAutocompleteProvider(provider)
}

// dedupeStrings preserves first occurrence order (pi: [...new Set(xs)]).
func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// =========================================================================
// Extension UI surface (pi: setExtensionStatus / showStatusIndicator / ...
// / showExtensionError, interactive-mode.ts 2127–2848 + 2075–2126)
// =========================================================================

// setWorkingMessage updates the working message (pi: setWorkingMessage's
// createExtensionUIContext closure body — hoisted to a method for reuse).
func (m *InteractiveMode) setWorkingMessage(message string) {
	if message == "" {
		m.workingMessage = nil
	} else {
		m.workingMessage = &message
	}
	if ind, ok := m.activeStatusIndicator.(interface{ SetMessage(string) }); ok && m.isWorkingIndicator() {
		if message == "" {
			ind.SetMessage(m.defaultWorkingMessage)
		} else {
			ind.SetMessage(message)
		}
	}
}

func (m *InteractiveMode) isWorkingIndicator() bool {
	return m.activeStatusIndicator != nil && m.activeStatusIndicator.IndicatorKind() == components.StatusKindWorking
}

// setWorkingVisible shows or hides the working loader row (pi:
// setWorkingVisible).
func (m *InteractiveMode) setWorkingVisible(visible bool) {
	m.workingVisible = visible
	if !visible {
		m.clearStatusIndicator("working")
		m.ui.RequestRender(false)
		return
	}
	if m.session().IsStreaming() && !m.isWorkingIndicator() {
		m.showWorkingStatusIndicator()
	}
	m.ui.RequestRender(false)
}

// setWorkingIndicator configures the working indicator (pi:
// setWorkingIndicator).
func (m *InteractiveMode) setWorkingIndicator(options *core.WorkingIndicatorOptions) {
	m.workingIndicatorOptions = options
	if ind, ok := m.activeStatusIndicator.(interface {
		SetIndicator(*core.WorkingIndicatorOptions)
	}); ok && m.isWorkingIndicator() {
		ind.SetIndicator(options)
	}
	m.ui.RequestRender(false)
}

// setHiddenThinkingLabel updates the hidden thinking label (pi:
// setHiddenThinkingLabel).
func (m *InteractiveMode) setHiddenThinkingLabel(label string) {
	if label == "" {
		label = m.defaultHiddenThinkingLabel
	}
	m.hiddenThinkingLabel = label
	for _, child := range m.chatContainer.Children {
		if am, ok := child.(*components.AssistantMessageComponent); ok {
			am.SetHiddenThinkingLabel(label)
		}
	}
	if m.streamingComponent != nil {
		m.streamingComponent.SetHiddenThinkingLabel(label)
	}
	m.ui.RequestRender(false)
}

// setExtensionStatus sets extension status text in the footer (pi:
// setExtensionStatus).
func (m *InteractiveMode) setExtensionStatus(key string, text string) {
	var value *string
	if text != "" {
		value = lo.ToPtr(text)
	}
	m.footerDataProviderView.SetExtensionStatus(key, value)
	m.ui.RequestRender(false)
}

// setExtensionWidget registers a widget component under key (pi:
// setExtensionWidget).
func (m *InteractiveMode) setExtensionWidget(key string, content any, options *core.ExtensionWidgetOptions) {
	placement := "aboveEditor"
	if options != nil && options.Placement != "" {
		placement = options.Placement
	}
	removeExisting := func(widgets map[string]extensionWidget) {
		if existing, ok := widgets[key]; ok {
			if existing.dispose != nil {
				existing.dispose()
			}
			delete(widgets, key)
		}
	}

	removeExisting(m.extensionWidgetsAbove)
	removeExisting(m.extensionWidgetsBelow)

	if content == nil {
		m.renderWidgets()
		return
	}

	var widget extensionWidget
	switch value := content.(type) {
	case []string:
		// Wrap string array in a Container with Text components
		container := &tui.Container{}
		truncated := false
		for i, line := range value {
			if i >= maxWidgetLines {
				truncated = true
				break
			}
			container.AddChild(tui.NewText(line, 1, 0))
		}
		if truncated {
			container.AddChild(tui.NewText(theme.Current().Fg("muted", "... (widget truncated)"), 1, 0))
		}
		widget = extensionWidget{component: container}
	case core.ExtensionWidgetFactory:
		component, dispose := value(m.ui)
		widget = extensionWidget{component: component, dispose: dispose}
	default:
		return
	}

	if placement == "belowEditor" {
		m.extensionWidgetsBelow[key] = widget
	} else {
		m.extensionWidgetsAbove[key] = widget
	}
	m.renderWidgets()
}

// clearExtensionWidgets disposes and removes all extension widgets (pi:
// clearExtensionWidgets).
func (m *InteractiveMode) clearExtensionWidgets() {
	for _, widget := range m.extensionWidgetsAbove {
		if widget.dispose != nil {
			widget.dispose()
		}
	}
	for _, widget := range m.extensionWidgetsBelow {
		if widget.dispose != nil {
			widget.dispose()
		}
	}
	m.extensionWidgetsAbove = map[string]extensionWidget{}
	m.extensionWidgetsBelow = map[string]extensionWidget{}
	m.renderWidgets()
}

// maxWidgetLines is the maximum total widget lines to prevent viewport
// overflow (pi: InteractiveMode.MAX_WIDGET_LINES).
const maxWidgetLines = 10

// renderWidgets renders all extension widgets to the widget containers (pi:
// renderWidgets).
func (m *InteractiveMode) renderWidgets() {
	if m.widgetContainerAbove == nil || m.widgetContainerBelow == nil {
		return
	}
	m.renderWidgetContainer(m.widgetContainerAbove, m.extensionWidgetsAbove, true, true)
	m.renderWidgetContainer(m.widgetContainerBelow, m.extensionWidgetsBelow, false, false)
	m.ui.RequestRender(false)
}

func (m *InteractiveMode) renderWidgetContainer(container *tui.Container, widgets map[string]extensionWidget,
	spacerWhenEmpty bool, leadingSpacer bool) {
	m.mutateUI(func() {
		container.Clear()

		if len(widgets) == 0 {
			if spacerWhenEmpty {
				container.AddChild(tui.NewSpacer(1))
			}
			return
		}

		if leadingSpacer {
			container.AddChild(tui.NewSpacer(1))
		}
		for _, widget := range widgets {
			container.AddChild(widget.component)
		}
	})
}

// disposer is the optional Component disposal capability (pi: `dispose?()`).
type disposer interface {
	Dispose()
}

// setExtensionFooter sets a custom footer component, or restores the
// built-in footer (pi: setExtensionFooter).
func (m *InteractiveMode) setExtensionFooter(factory core.ExtensionFooterFactory) {
	// Dispose existing custom footer
	if m.customFooter != nil {
		if d, ok := m.customFooter.(disposer); ok {
			d.Dispose()
		}
	}

	// The factory may create components that mutate other trees, so it runs
	// outside the UI lock; only the container swap is serialized.
	var component tui.Component
	if factory != nil {
		component, _ = factory(m.ui, m.footerDataProviderView)
	}
	m.mutateUI(func() {
		m.footerContainer.Clear()
		if factory != nil {
			// Create and add custom footer, passing the data provider
			m.customFooter = component
			m.footerContainer.AddChild(component)
		} else {
			// Restore built-in footer
			m.customFooter = nil
			m.footerContainer.AddChild(m.footer)
		}
	})

	m.ui.RequestRender(false)
}

// setExtensionHeader sets a custom header component, or restores the
// built-in header (pi: setExtensionHeader).
func (m *InteractiveMode) setExtensionHeader(factory core.ExtensionHeaderFactory) {
	// Header may not be initialized yet if called during early initialization
	if m.builtInHeader == nil {
		return
	}

	// Dispose existing custom header
	if m.customHeader != nil {
		if d, ok := m.customHeader.(disposer); ok {
			d.Dispose()
		}
		if m.customHeaderDispose != nil {
			m.customHeaderDispose()
			m.customHeaderDispose = nil
		}
	}

	// Find the index of the current header in the header container
	currentHeader := m.customHeader
	if currentHeader == nil {
		currentHeader = m.builtInHeader
	}
	index := -1
	for i, child := range m.headerContainer.Children {
		if child == currentHeader {
			index = i
			break
		}
	}

	// The factory may create components that mutate other trees, so it runs
	// outside the UI lock; only the container swap is serialized.
	var component tui.Component
	var dispose func()
	if factory != nil {
		component, dispose = factory(m.ui)
	}
	m.mutateUI(func() {
		if factory != nil {
			// Create and add custom header
			m.customHeader = component
			m.customHeaderDispose = dispose
			if e := asExpandable(component); e != nil {
				e.SetExpanded(m.toolOutputExpanded)
			}
			if index != -1 {
				m.headerContainer.Children[index] = component
			} else {
				// If not found (e.g. builtInHeader was never added), add at the top
				m.headerContainer.Children = append([]tui.Component{component}, m.headerContainer.Children...)
			}
		} else {
			// Restore built-in header
			m.customHeader = nil
			if e := asExpandable(m.builtInHeader); e != nil {
				e.SetExpanded(m.toolOutputExpanded)
			}
			if index != -1 {
				m.headerContainer.Children[index] = m.builtInHeader
			}
		}
	})

	m.ui.RequestRender(false)
}

// addExtensionTerminalInputListener registers a raw terminal input listener
// tracked for rebinding (pi: addExtensionTerminalInputListener).
func (m *InteractiveMode) addExtensionTerminalInputListener(
	handler func(data string) *tui.TuiInputListenerResult,
) func() {
	subscription := &extensionTerminalInputSubscription{handler: handler, unsubscribe: m.ui.AddInputListener(handler)}
	m.extensionTerminalInputSubscriptions = append(m.extensionTerminalInputSubscriptions, subscription)
	return func() {
		subscription.unsubscribe()
		for i, s := range m.extensionTerminalInputSubscriptions {
			if s == subscription {
				m.extensionTerminalInputSubscriptions = append(m.extensionTerminalInputSubscriptions[:i], m.extensionTerminalInputSubscriptions[i+1:]...)
				break
			}
		}
	}
}

// rebindExtensionTerminalInputListeners re-registers the tracked listeners
// on the active renderer (pi: rebindExtensionTerminalInputListeners).
func (m *InteractiveMode) rebindExtensionTerminalInputListeners() {
	for _, subscription := range m.extensionTerminalInputSubscriptions {
		subscription.unsubscribe()
		subscription.unsubscribe = m.ui.AddInputListener(subscription.handler)
	}
}

// clearExtensionTerminalInputListeners unregisters all tracked listeners
// (pi: clearExtensionTerminalInputListeners).
func (m *InteractiveMode) clearExtensionTerminalInputListeners() {
	for _, subscription := range m.extensionTerminalInputSubscriptions {
		subscription.unsubscribe()
	}
	m.extensionTerminalInputSubscriptions = nil
}

// createProjectTrustContext builds the project-trust UI context (pi:
// createProjectTrustContext).
func (m *InteractiveMode) createProjectTrustContext(cwd string) *core.ProjectTrustContext {
	ui := m.createExtensionUIContext()
	return &core.ProjectTrustContext{
		Cwd:   cwd,
		Mode:  core.ExtensionModeTUI,
		HasUI: true,
		UI:    ui,
	}
}

// createExtensionUIContext builds the ExtensionUIContext for extensions (pi:
// createExtensionUIContext).
func (m *InteractiveMode) createExtensionUIContext() core.ExtensionUIContext {
	return &interactiveExtensionUIContext{m: m}
}

// mutateUI runs fn while holding the active renderer's lock (pig
// concurrency note: pi mutates the component tree on the single event
// loop; pig serializes the cross-goroutine mutations against the render/
// input pipeline via TuiBase.RunExclusive).
func (m *InteractiveMode) mutateUI(fn func()) {
	if r, ok := m.ui.(interface{ RunExclusive(func()) }); ok {
		r.RunExclusive(fn)
		return
	}
	fn()
}

// interactiveExtensionUIContext is the ExtensionUIContext implementation
// bound to an InteractiveMode (pi's object literal in
// createExtensionUIContext; pig names the type for testability).
type interactiveExtensionUIContext struct{ m *InteractiveMode }

func (u *interactiveExtensionUIContext) Select(ctx context.Context, title string, options []string, opts *core.ExtensionUIDialogOptions) (string, bool) {
	return u.m.showExtensionSelector(ctx, title, options, opts)
}

func (u *interactiveExtensionUIContext) Confirm(ctx context.Context, title string, message string, opts *core.ExtensionUIDialogOptions) bool {
	return u.m.showExtensionConfirm(ctx, title, message, opts)
}

func (u *interactiveExtensionUIContext) Input(ctx context.Context, title string, placeholder string, opts *core.ExtensionUIDialogOptions) (string, bool) {
	return u.m.showExtensionInput(ctx, title, placeholder, opts)
}

func (u *interactiveExtensionUIContext) Notify(ctx context.Context, message string, level string) {
	u.m.showExtensionNotify(message, level)
}

func (u *interactiveExtensionUIContext) OnTerminalInput(ctx context.Context, handler core.TerminalInputHandler) func() {
	return u.m.addExtensionTerminalInputListener(func(data string) *tui.TuiInputListenerResult {
		result := handler(data)
		if result == nil {
			return nil
		}
		out := &tui.TuiInputListenerResult{Consume: result.Consume}
		if result.Data != "" {
			out.Data = &result.Data
		}
		return out
	})
}

func (u *interactiveExtensionUIContext) SetStatus(ctx context.Context, key string, text string) {
	u.m.setExtensionStatus(key, text)
}

func (u *interactiveExtensionUIContext) SetWorkingMessage(ctx context.Context, message string) {
	u.m.setWorkingMessage(message)
}

func (u *interactiveExtensionUIContext) SetWorkingVisible(ctx context.Context, visible bool) {
	u.m.setWorkingVisible(visible)
}

func (u *interactiveExtensionUIContext) SetWorkingIndicator(ctx context.Context, options *core.WorkingIndicatorOptions) {
	u.m.setWorkingIndicator(options)
}

func (u *interactiveExtensionUIContext) SetHiddenThinkingLabel(ctx context.Context, label string) {
	u.m.setHiddenThinkingLabel(label)
}

func (u *interactiveExtensionUIContext) SetWidget(ctx context.Context, key string, content any, options *core.ExtensionWidgetOptions) {
	u.m.setExtensionWidget(key, content, options)
}

func (u *interactiveExtensionUIContext) SetFooter(ctx context.Context, factory core.ExtensionFooterFactory) {
	u.m.setExtensionFooter(factory)
}

func (u *interactiveExtensionUIContext) SetHeader(ctx context.Context, factory core.ExtensionHeaderFactory) {
	u.m.setExtensionHeader(factory)
}

func (u *interactiveExtensionUIContext) SetTitle(ctx context.Context, title string) {
	u.m.ui.Terminal().SetTitle(title)
}

func (u *interactiveExtensionUIContext) Custom(ctx context.Context, factory core.ExtensionCustomFactory, options *core.ExtensionCustomOptions) any {
	return u.m.showExtensionCustom(ctx, factory, options)
}

func (u *interactiveExtensionUIContext) PasteToEditor(ctx context.Context, text string) {
	u.m.editor.HandleInput(fmt.Sprintf("\x1b[200~%s\x1b[201~", text))
}

func (u *interactiveExtensionUIContext) SetEditorText(ctx context.Context, text string) {
	u.m.editor.SetText(text)
}

func (u *interactiveExtensionUIContext) GetEditorText(ctx context.Context) string {
	return u.m.editorGetExpandedText()
}

func (u *interactiveExtensionUIContext) Editor(ctx context.Context, title string, prefill string) (string, bool) {
	return u.m.showExtensionEditor(ctx, title, prefill)
}

func (u *interactiveExtensionUIContext) AddAutocompleteProvider(ctx context.Context, factory core.AutocompleteProviderFactory) {
	u.m.autocompleteProviderWrappers = append(u.m.autocompleteProviderWrappers, factory)
	u.m.setupAutocompleteProvider()
}

func (u *interactiveExtensionUIContext) SetEditorComponent(ctx context.Context, factory core.EditorFactory) {
	u.m.setCustomEditorComponent(factory)
}

func (u *interactiveExtensionUIContext) GetEditorComponent(ctx context.Context) core.EditorFactory {
	return u.m.editorComponentFactory
}

func (u *interactiveExtensionUIContext) GetAllThemes(ctx context.Context) []core.ExtensionThemeInfo {
	var out []core.ExtensionThemeInfo
	for _, info := range theme.GetAvailableThemesWithPaths() {
		out = append(out, core.ExtensionThemeInfo{Name: info.Name, Path: info.Path})
	}
	return out
}

func (u *interactiveExtensionUIContext) GetTheme(ctx context.Context, name string) any {
	return theme.GetThemeByName(name)
}

func (u *interactiveExtensionUIContext) SetTheme(ctx context.Context, themeName string) core.ThemeSetResult {
	result := u.m.themeController.SetThemeName(themeName, true)
	if result.Success {
		if func() bool { name, _ := u.m.settingsManager().GetTheme(); return name != themeName }() {
			u.m.settingsManager().SetTheme(themeName)
		}
	}
	return core.ThemeSetResult{Success: result.Success, Error: result.Error}
}

func (u *interactiveExtensionUIContext) GetToolsExpanded(ctx context.Context) bool {
	return u.m.toolOutputExpanded
}

func (u *interactiveExtensionUIContext) SetToolsExpanded(ctx context.Context, expanded bool) {
	u.m.setToolsExpanded(expanded)
}

// showExtensionSelector shows a selector for extensions (pi:
// showExtensionSelector). Blocks until the user selects or cancels; ok
// reports whether a value was returned (pi's Promise resolution
// value/undefined). pi's opts.signal is the caller's ctx per repo rules;
// the dialog auto-dismisses on ctx cancellation.
func (m *InteractiveMode) showExtensionSelector(ctx context.Context, title string, options []string,
	opts *core.ExtensionUIDialogOptions) (string, bool) {
	if ctx != nil && ctx.Err() != nil {
		return "", false
	}
	resultCh := make(chan string, 1)
	done := make(chan struct{})
	var closeOnce sync.Once
	finish := func(value string) {
		closeOnce.Do(func() { close(done) })
		m.hideExtensionSelector()
		resultCh <- value
	}

	if ctx != nil {
		go func() {
			// pig panic containment (see docs/feat/PANIC-RESILIENCE.md).
			defer pigutils.RecoverPanic("interactive.inputGate")
			select {
			case <-ctx.Done():
				finish("")
			case <-done:
			}
		}()
	}

	m.extensionSelector = components.NewExtensionSelectorComponent(
		title,
		options,
		func(option string) { finish(option) },
		func() { finish("") },
		&components.ExtensionSelectorOptions{
			Tui:                   m.ui,
			Timeout:               dialogTimeout(opts),
			OnToggleToolsExpanded: func() { m.toggleToolOutputExpansion() },
		},
	)

	m.disposeActiveSelector()
	m.mutateUI(func() {
		m.editorContainer.Clear()
		m.editorContainer.AddChild(m.extensionSelector)
		m.ui.SetFocus(m.extensionSelector)
	})
	m.ui.RequestRender(false)

	result := <-resultCh
	return result, result != ""
}

// hideExtensionSelector hides the extension selector (pi:
// hideExtensionSelector).
func (m *InteractiveMode) hideExtensionSelector() {
	m.extensionSelector = nil
	m.mutateUI(func() {
		m.editorContainer.Clear()
		m.editorContainer.AddChild(m.editor)
		m.ui.SetFocus(m.editor)
	})
	m.ui.RequestRender(false)
}

// showExtensionConfirm shows a confirmation dialog for extensions (pi:
// showExtensionConfirm).
func (m *InteractiveMode) showExtensionConfirm(ctx context.Context, title string, message string,
	opts *core.ExtensionUIDialogOptions) bool {
	result, ok := m.showExtensionSelector(ctx, fmt.Sprintf("%s\n%s", title, message), []string{"Yes", "No"}, opts)
	return ok && result == "Yes"
}

// promptForMissingSessionCwd asks the user for a fallback cwd (pi:
// promptForMissingSessionCwd). ok reports whether a fallback was confirmed.
func (m *InteractiveMode) promptForMissingSessionCwd(err error) (string, bool) {
	missing, ok := err.(*core.MissingSessionCwdError)
	if !ok {
		return "", false
	}
	confirmed := m.showExtensionConfirm(context.Background(), "Session cwd not found",
		core.FormatMissingSessionCwdPrompt(missing.Issue()), nil)
	if !confirmed {
		return "", false
	}
	return missing.Issue().FallbackCwd, true
}

// showExtensionInput shows a text input for extensions (pi:
// showExtensionInput).
func (m *InteractiveMode) showExtensionInput(ctx context.Context, title string, placeholder string,
	opts *core.ExtensionUIDialogOptions) (string, bool) {
	if ctx != nil && ctx.Err() != nil {
		return "", false
	}
	resultCh := make(chan string, 1)

	var placeholderPtr *string
	if placeholder != "" {
		placeholderPtr = lo.ToPtr(placeholder)
	}
	m.extensionInput = components.NewExtensionInputComponent(
		title,
		placeholderPtr,
		func(value string) {
			m.hideExtensionInput()
			resultCh <- value
		},
		func() {
			m.hideExtensionInput()
			resultCh <- ""
		},
		&components.ExtensionInputOptions{
			Tui:     m.ui,
			Timeout: dialogTimeout(opts),
		},
	)

	m.disposeActiveSelector()
	m.mutateUI(func() {
		m.editorContainer.Clear()
		m.editorContainer.AddChild(m.extensionInput)
		m.ui.SetFocus(m.extensionInput)
	})
	m.ui.RequestRender(false)

	result := <-resultCh
	return result, result != ""
}

// hideExtensionInput hides the extension input (pi: hideExtensionInput).
func (m *InteractiveMode) hideExtensionInput() {
	m.extensionInput = nil
	m.mutateUI(func() {
		m.editorContainer.Clear()
		m.editorContainer.AddChild(m.editor)
		m.ui.SetFocus(m.editor)
	})
	m.ui.RequestRender(false)
}

// showExtensionEditor shows a multi-line editor for extensions (pi:
// showExtensionEditor).
func (m *InteractiveMode) showExtensionEditor(ctx context.Context, title string, prefill string) (string, bool) {
	resultCh := make(chan string, 1)

	m.extensionEditor = components.NewExtensionEditorComponent(
		m.ui,
		m.keybindings,
		title,
		prefill,
		func(value string) {
			m.hideExtensionEditor()
			resultCh <- value
		},
		func() {
			m.hideExtensionEditor()
			resultCh <- ""
		},
		nil,
		m.settingsManager().GetExternalEditorCommand(),
	)

	m.disposeActiveSelector()
	m.mutateUI(func() {
		m.editorContainer.Clear()
		m.editorContainer.AddChild(m.extensionEditor)
		m.ui.SetFocus(m.extensionEditor)
	})
	m.ui.RequestRender(false)

	result := <-resultCh
	return result, result != ""
}

// hideExtensionEditor hides the extension editor (pi: hideExtensionEditor).
func (m *InteractiveMode) hideExtensionEditor() {
	m.extensionEditor = nil
	m.mutateUI(func() {
		m.editorContainer.Clear()
		m.editorContainer.AddChild(m.editor)
		m.ui.SetFocus(m.editor)
	})
	m.ui.RequestRender(false)
}

// setCustomEditorComponent sets a custom editor component from an extension,
// or restores the default editor (pi: setCustomEditorComponent). Deviation:
// OnSubmit/OnChange/borderColor/actionHandlers wiring only applies to
// *components.CustomEditor editors (pi duck-types the same members, which
// all live on CustomEditor); other editors get capability-based wiring only.
func (m *InteractiveMode) setCustomEditorComponent(factory core.EditorFactory) {
	m.editorComponentFactory = factory

	// Save text from current editor before switching
	currentText := m.editor.GetText()

	m.disposeActiveSelector()
	m.mutateUI(func() { m.editorContainer.Clear() })

	if factory != nil {
		// Create the custom editor with tui, theme, and keybindings
		newEditor := factory(m.ui, theme.GetEditorTheme(), m.keybindings)

		// Wire up callbacks and appearance from the default editor (pi:
		// duck-typed CustomEditor members)
		if ce, ok := newEditor.(*components.CustomEditor); ok {
			ce.OnSubmit = m.defaultEditor.OnSubmit
			ce.OnChange = m.defaultEditor.OnChange
			if m.defaultEditor.BorderColor != nil {
				ce.BorderColor = m.defaultEditor.BorderColor
			}
			ce.SetPaddingX(m.defaultEditor.GetPaddingX())
			ce.SetAutocompleteMaxVisible(m.defaultEditor.GetAutocompleteMaxVisible())
			// Copy action handlers and hook fallbacks (pi: actionHandlers +
			// onEscape/onCtrlD/onPasteImage/onExtensionShortcut)
			ce.CopyAppHandlersFrom(m.defaultEditor)
			if ce.OnEscape == nil {
				ce.OnEscape = m.defaultEditor.OnEscape
			}
			if ce.OnCtrlD == nil {
				ce.OnCtrlD = m.defaultEditor.OnCtrlD
			}
			if ce.OnPasteImage == nil {
				ce.OnPasteImage = m.defaultEditor.OnPasteImage
			}
			if ce.OnExtensionShortcut == nil {
				ce.OnExtensionShortcut = m.defaultEditor.OnExtensionShortcut
			}
		}

		// Set autocomplete if supported
		if a, ok := newEditor.(tui.EditorComponentAutocomplete); ok && m.autocompleteProvider != nil {
			a.SetAutocompleteProvider(m.autocompleteProvider)
		}

		m.editor = newEditor
	} else {
		// Restore default editor with text from custom editor
		m.defaultEditor.SetText(currentText)
		m.editor = m.defaultEditor
	}

	m.mutateUI(func() {
		m.editorContainer.AddChild(m.editor)
		// Re-embed the working indicator when a custom editor without border
		// embedding was swapped out (pi: setCustomEditorComponent tail, v0.85.1).
		if working, ok := m.activeStatusIndicator.(*components.WorkingStatusIndicator); ok {
			m.statusContainer.Clear()
			m.activeWorkingIndicatorEmbedded = m.setEditorWorkingStatusIndicator(working)
			if !m.activeWorkingIndicatorEmbedded {
				m.statusContainer.AddChild(m.activeStatusIndicator)
			}
		}
		m.ui.SetFocus(m.editor)
	})
	m.ui.RequestRender(false)
}

// showExtensionNotify shows a notification for extensions (pi:
// showExtensionNotify).
func (m *InteractiveMode) showExtensionNotify(message string, level string) {
	switch level {
	case "error":
		m.ShowError(message)
	case "warning":
		m.ShowWarning(message)
	default:
		m.showStatus(message)
	}
}

// showExtensionCustom shows a custom component with keyboard focus (pi:
// showExtensionCustom). Blocks until the factory's done callback fires and
// returns the value passed to it (pi: Promise<T>).
func (m *InteractiveMode) showExtensionCustom(ctx context.Context, factory core.ExtensionCustomFactory,
	options *core.ExtensionCustomOptions) any {
	savedText := m.editor.GetText()
	isOverlay := options != nil && options.Overlay

	resultCh := make(chan any, 1)
	closed := false

	restoreEditor := func() {
		m.mutateUI(func() {
			m.editorContainer.Clear()
			m.editorContainer.AddChild(m.editor)
			m.editor.SetText(savedText)
			m.ui.SetFocus(m.editor)
		})
		m.ui.RequestRender(false)
	}

	close := func(result any) {
		if closed {
			return
		}
		closed = true
		if isOverlay {
			m.ui.HideOverlay()
		} else {
			restoreEditor()
		}
		// Note: both branches above already call requestRender
		resultCh <- result
	}

	component, dispose := factory(m.ui, m.keybindings, close)
	if isOverlay {
		// Resolve overlay options — dynamic function or component width
		var overlayOptions *tui.OverlayOptions
		if options != nil && options.OverlayOptions != nil {
			overlayOptions = options.OverlayOptions()
		} else if w, ok := component.(interface{ GetWidth() int }); ok {
			if width := w.GetWidth(); width > 0 {
				overlayOptions = &tui.OverlayOptions{Width: width}
			}
		}
		handle := m.ui.ShowOverlay(component, overlayOptions)
		// Expose handle to caller for visibility control
		if options != nil && options.OnHandle != nil {
			options.OnHandle(handle)
		}
	} else {
		m.disposeActiveSelector()
		m.mutateUI(func() {
			m.editorContainer.Clear()
			m.editorContainer.AddChild(component)
			m.ui.SetFocus(component)
		})
		m.ui.RequestRender(false)
	}

	result := <-resultCh
	if dispose != nil {
		func() {
			defer func() { _ = recover() }() // ignore dispose errors
			dispose()
		}()
	}
	return result
}

// showExtensionError shows an extension error in the UI (pi:
// showExtensionError).
func (m *InteractiveMode) showExtensionError(extensionPath string, errMsg string, stack string) {
	errorMsg := fmt.Sprintf("Extension %q error: %s", extensionPath, errMsg)
	m.mutateUI(func() {
		m.chatContainer.AddChild(tui.NewText(theme.Current().Fg("error", errorMsg), 1, 0))
	})
	if stack != "" {
		// Show stack trace in dim color, indented
		lines := strings.Split(stack, "\n")
		if len(lines) > 1 {
			stackLines := make([]string, 0, len(lines)-1)
			for _, line := range lines[1:] { // Skip first line (duplicates error message)
				stackLines = append(stackLines, theme.Current().Fg("dim", "  "+strings.TrimSpace(line)))
			}
			if len(stackLines) > 0 {
				m.mutateUI(func() {
					m.chatContainer.AddChild(tui.NewText(strings.Join(stackLines, "\n"), 1, 0))
				})
			}
		}
	}
	m.ui.RequestRender(false)
}

// dialogTimeout maps the dialog options to the component timeout (pi:
// opts?.timeout).
func dialogTimeout(opts *core.ExtensionUIDialogOptions) int {
	if opts == nil {
		return 0
	}
	return opts.Timeout
}

// selectorComponent carries the showSelector create result (pi's inline
// object literal { component, focus, dispose? }).
type selectorComponent struct {
	component tui.Component
	focus     tui.Component
	dispose   func()
}

// showSelector mounts an in-place selector in the editor container (pi:
// showSelector).
func (m *InteractiveMode) showSelector(create func(done func()) selectorComponent) {
	token := &struct{}{}
	var dispose func()
	done := func() {
		if dispose != nil {
			dispose()
		}
		if m.activeSelectorToken != token {
			return
		}
		m.activeSelectorToken = nil
		m.activeSelectorDispose = nil
		m.mutateUI(func() {
			m.editorContainer.Clear()
			m.editorContainer.AddChild(m.editor)
			m.ui.SetFocus(m.editor)
		})
	}
	created := create(done)
	dispose = created.dispose
	m.disposeActiveSelector()
	m.activeSelectorToken = token
	m.activeSelectorDispose = dispose
	m.mutateUI(func() {
		m.editorContainer.Clear()
		m.editorContainer.AddChild(created.component)
		m.ui.SetFocus(created.focus)
	})
	m.ui.RequestRender(false)
}

// showSettingsSelector mounts the /settings panel (pi: showSettingsSelector).
func (m *InteractiveMode) showSettingsSelector() {
	m.showSelector(func(done func()) selectorComponent {
		var selector *components.SettingsSelectorComponent
		defaultProvider, hasDefaultProvider := m.settingsManager().GetDefaultProvider()
		defaultModelID, hasDefaultModel := m.settingsManager().GetDefaultModel()
		defaultModel := "not set"
		if hasDefaultProvider && hasDefaultModel {
			defaultModel = defaultProvider + "/" + defaultModelID
		}
		httpIdleTimeoutMs, _ := m.settingsManager().GetHTTPIdleTimeoutMs()
		thinkingLevel, hasThinkingLevel := m.settingsManager().GetDefaultThinkingLevel()
		if !hasThinkingLevel {
			thinkingLevel = core.DefaultThinkingLevel
		}
		currentThemeSetting, hasThemeSelection := m.themeController.GetThemeSelection()
		if !hasThemeSelection {
			currentThemeSetting = "dark"
		}
		selector = components.NewSettingsSelectorComponent(
			&components.SettingsConfig{
				AutoCompact:            m.session().AutoCompactionEnabled(),
				DefaultModel:           defaultModel,
				CurrentModel:           m.session().Model(),
				AvailableDefaultModels: m.session().GetModelRuntime().GetAvailableSnapshot(),
				ShowImages:             m.settingsManager().GetShowImages(),
				ImageWidthCells:        m.settingsManager().GetImageWidthCells(),
				AutoResizeImages:       m.settingsManager().GetImageAutoResize(),
				BlockImages:            m.settingsManager().GetBlockImages(),
				EnableSkillCommands:    m.settingsManager().GetEnableSkillCommands(),
				SteeringMode:           string(m.session().SteeringMode()),
				FollowUpMode:           string(m.session().FollowUpMode()),
				Transport:              m.settingsManager().GetTransport(),
				HTTPIdleTimeoutMs:      httpIdleTimeoutMs,
				ThinkingLevel:          thinkingLevel,
				// pi: [...THINKING_LEVEL_OPTIONS] — read by no component code
				// today (pi shape fidelity).
				AvailableThinkingLevels: []agent.ThinkingLevel{},
				ModelThinkingLevels:     m.settingsManager().GetAllModelThinkingLevels(),
				CurrentTheme:            currentThemeSetting,
				TerminalTheme:           m.themeController.GetTerminalTheme(),
				AvailableThemes:         theme.GetAvailableThemes(),
				HideThinkingBlock:       m.hideThinkingBlock,
				MermaidRenderingMode:    m.settingsManager().GetMermaidRenderingMode(),
				ShowCacheMissNotices:    m.settingsManager().GetShowCacheMissNotices(),
				CollapseChangelog:       m.settingsManager().GetCollapseChangelog(),
				EnableInstallTelemetry:  m.settingsManager().GetEnableInstallTelemetry(),
				DoubleEscapeAction:      m.settingsManager().GetDoubleEscapeAction(),
				TreeFilterMode:          m.settingsManager().GetTreeFilterMode(),
				ShowHardwareCursor:      m.settingsManager().GetShowHardwareCursor(),
				DefaultProjectTrust:     m.settingsManager().GetDefaultProjectTrust(),
				EditorPaddingX:          m.settingsManager().GetEditorPaddingX(),
				OutputPad:               m.outputPad,
				AutocompleteMaxVisible:  m.settingsManager().GetAutocompleteMaxVisible(),
				QuietStartup:            m.settingsManager().GetQuietStartup(),
				ClearOnShrink:           m.settingsManager().GetClearOnShrink(),
				ShowTerminalProgress:    m.settingsManager().GetShowTerminalProgress(),
				TuiMode:                 m.ui.Mode(),
				FullscreenExitOutput:    m.settingsManager().GetFullscreenExitOutput(),
				FullscreenScrollbar:     m.settingsManager().GetFullscreenScrollbar(),
				FullscreenCopyOnSelect:  m.settingsManager().GetFullscreenCopyOnSelect(),
				Warnings:                m.settingsManager().GetWarnings(),
			},
			&components.SettingsCallbacks{
				OnAutoCompactChange: func(enabled bool) {
					m.session().SetAutoCompactionEnabled(enabled)
					m.footer.SetAutoCompactEnabled(enabled)
				},
				OnShowImagesChange: func(enabled bool) {
					m.settingsManager().SetShowImages(enabled)
					for _, child := range m.chatContainer.Children {
						if tool, ok := child.(*components.ToolExecutionComponent); ok {
							tool.SetShowImages(enabled)
						}
					}
				},
				OnImageWidthCellsChange: func(width int64) {
					m.settingsManager().SetImageWidthCells(width)
					for _, child := range m.chatContainer.Children {
						if tool, ok := child.(*components.ToolExecutionComponent); ok {
							tool.SetImageWidthCells(int(width))
						}
					}
				},
				OnAutoResizeImagesChange: func(enabled bool) {
					m.settingsManager().SetImageAutoResize(enabled)
				},
				OnBlockImagesChange: func(blocked bool) {
					m.settingsManager().SetBlockImages(blocked)
				},
				OnEnableSkillCommandsChange: func(enabled bool) {
					m.settingsManager().SetEnableSkillCommands(enabled)
					m.setupAutocompleteProvider()
				},
				OnSteeringModeChange: func(mode string) {
					m.session().SetSteeringMode(mode)
				},
				OnFollowUpModeChange: func(mode string) {
					m.session().SetFollowUpMode(mode)
				},
				OnTransportChange: func(transport ai.Transport) {
					m.settingsManager().SetTransport(transport)
					m.agent().Transport = transport
				},
				OnHTTPIdleTimeoutMsChange: func(timeoutMs int64) {
					_ = m.settingsManager().SetHTTPIdleTimeoutMs(float64(timeoutMs))
					// pi: configureHttpDispatcher(timeoutMs) — deferred (Node
					// undici-specific; pig's HTTP layer has no global dispatcher).
					m.showStatus("HTTP idle timeout: " + core.FormatHTTPIdleTimeoutMs(timeoutMs))
				},
				OnModelThinkingLevelChange: func(provider string, modelID string, level agent.ThinkingLevel) {
					m.settingsManager().SetModelThinkingLevel(provider, modelID, level)
					// If the override is for the current model, apply it to the
					// session too
					current := m.session().Model()
					if current != nil && string(current.Provider) == provider && current.ID == modelID {
						m.session().SetThinkingLevel(level, nil)
						m.footer.Invalidate()
						m.updateEditorBorderColor()
					}
				},
				OnModelThinkingLevelRemove: func(provider string, modelID string) {
					m.settingsManager().RemoveModelThinkingLevel(provider, modelID)
					// If the override was for the current model, revert to global
					// default
					current := m.session().Model()
					if current != nil && string(current.Provider) == provider && current.ID == modelID {
						globalDefault, ok := m.settingsManager().GetDefaultThinkingLevel()
						if !ok {
							globalDefault = core.DefaultThinkingLevel
						}
						m.session().SetThinkingLevel(globalDefault, nil)
						m.footer.Invalidate()
						m.updateEditorBorderColor()
					}
				},
				OnThemeChange: func(themeSetting string) {
					m.settingsManager().SetTheme(themeSetting)
					_ = m.themeController.SetThemeSetting(themeSetting)
				},
				OnThemePreview: func(themeName string) {
					m.themeController.Preview(themeName)
				},
				OnHideThinkingBlockChange: func(hidden bool) {
					m.hideThinkingBlock = hidden
					m.settingsManager().SetHideThinkingBlock(hidden)
					m.updateThinkingBlockVisibility()
				},
				OnMermaidRenderingModeChange: func(mode core.MermaidRenderingMode) {
					m.settingsManager().SetMermaidRenderingMode(mode)
					m.chatContainer.Invalidate()
					m.ui.RequestRender(false)
				},
				OnShowCacheMissNoticesChange: func(shown bool) {
					m.settingsManager().SetShowCacheMissNotices(shown)
					m.rebuildChatFromMessages()
				},
				OnCollapseChangelogChange: func(collapsed bool) {
					m.settingsManager().SetCollapseChangelog(collapsed)
				},
				OnEnableInstallTelemetryChange: func(enabled bool) {
					m.settingsManager().SetEnableInstallTelemetry(enabled)
				},
				OnQuietStartupChange: func(enabled bool) {
					m.settingsManager().SetQuietStartup(enabled)
				},
				OnDefaultProjectTrustChange: func(defaultProjectTrust core.DefaultProjectTrust) {
					m.settingsManager().SetDefaultProjectTrust(defaultProjectTrust)
				},
				OnDoubleEscapeActionChange: func(action string) {
					m.settingsManager().SetDoubleEscapeAction(action)
				},
				OnTreeFilterModeChange: func(mode string) {
					m.settingsManager().SetTreeFilterMode(mode)
				},
				OnShowHardwareCursorChange: func(enabled bool) {
					m.settingsManager().SetShowHardwareCursor(enabled)
					m.ui.SetShowHardwareCursor(enabled)
				},
				OnEditorPaddingXChange: func(padding int64) {
					m.settingsManager().SetEditorPaddingX(padding)
					m.defaultEditor.SetPaddingX(int(padding))
					if m.editor != m.defaultEditor {
						if padder, ok := m.editor.(interface{ SetPaddingX(int) }); ok {
							padder.SetPaddingX(int(padding))
						}
					}
				},
				OnOutputPadChange: func(padding int) {
					m.settingsManager().SetOutputPad(padding)
					m.outputPad = padding
					if m.streamingComponent != nil || m.session().IsStreaming() {
						for _, child := range m.chatContainer.Children {
							switch c := child.(type) {
							case *components.AssistantMessageComponent:
								c.SetOutputPad(padding)
							case *components.CustomMessageComponent:
								c.SetOutputPad(padding)
							case *components.UserMessageComponent:
								c.SetOutputPad(padding)
							}
						}
						if m.streamingComponent != nil {
							m.streamingComponent.SetOutputPad(padding)
						}
						m.ui.RequestRender(false)
						return
					}
					m.rebuildChatFromMessages()
				},
				OnAutocompleteMaxVisibleChange: func(maxVisible int64) {
					m.settingsManager().SetAutocompleteMaxVisible(maxVisible)
					m.defaultEditor.SetAutocompleteMaxVisible(int(maxVisible))
					if m.editor != m.defaultEditor {
						if setter, ok := m.editor.(interface{ SetAutocompleteMaxVisible(int) }); ok {
							setter.SetAutocompleteMaxVisible(int(maxVisible))
						}
					}
				},
				OnClearOnShrinkChange: func(enabled bool) {
					m.settingsManager().SetClearOnShrink(enabled)
					m.ui.SetClearOnShrink(enabled)
					if !enabled && m.activeStatusIndicator == nil {
						m.statusContainer.Clear()
					}
				},
				OnShowTerminalProgressChange: func(enabled bool) {
					m.settingsManager().SetShowTerminalProgress(enabled)
				},
				OnTuiModeChange: func(mode core.TuiMode) {
					// pi: this.switchTuiMode(mode) — defaults restoreProgress/startRenderer true.
					if !m.switchTuiMode(mode, true, true) {
						selector.GetSettingsList().UpdateValue("tui-mode", m.ui.Mode())
						m.showStatus("Close active overlays before changing TUI mode")
						return
					}
					m.settingsManager().SetTuiMode(mode)
					if m.activeStatusIndicator == nil {
						m.statusContainer.Clear()
					}
					m.showStatus("TUI mode: " + string(mode))
				},
				OnFullscreenExitOutputChange: func(output core.FullscreenExitOutput) {
					m.settingsManager().SetFullscreenExitOutput(output)
				},
				OnFullscreenScrollbarChange: func(mode core.ScrollViewScrollbar) {
					m.settingsManager().SetFullscreenScrollbar(mode)
					m.applyFullscreenScrollbarSetting()
				},
				OnFullscreenCopyOnSelectChange: func(enabled bool) {
					m.settingsManager().SetFullscreenCopyOnSelect(enabled)
					if altScreen, ok := m.renderer.(*tui.TuiAltScreen); ok {
						altScreen.SetCopyOnSelect(enabled)
					}
				},
				OnWarningsChange: func(warnings core.WarningSettings) {
					m.settingsManager().SetWarnings(warnings)
				},
				OnCancel: func() {
					done()
					m.ui.RequestRender(false)
				},
			},
		)
		return selectorComponent{component: selector, focus: selector.GetSettingsList()}
	})
}

// showTrustSelector mounts the /trust prompt (pi: showTrustSelector).
func (m *InteractiveMode) showTrustSelector() {
	cwd := m.sessionManager().GetCwd()
	trustStore := core.NewProjectTrustStore(codingagent.GetAgentDir())
	// pi: trustStore.getEntry(cwd) — null when nothing saved; pig's GetEntry
	// returns (nil, nil) in that case and swallows read errors as nil (the
	// selector renders "none").
	savedDecision, _ := trustStore.GetEntry(cwd)
	m.showSelector(func(done func()) selectorComponent {
		selector := components.NewTrustSelectorComponent(components.TrustSelectorOptions{
			Cwd:            cwd,
			SavedDecision:  savedDecision,
			ProjectTrusted: m.settingsManager().IsProjectTrusted(),
			OnSelect: func(selection components.TrustSelection) {
				_ = trustStore.SetMany(selection.Updates)
				done()
				decision := "untrusted"
				if selection.Trusted {
					decision = "trusted"
				}
				m.showStatus(fmt.Sprintf("Saved trust decision: %s. Restart %s for this to take effect.", decision, codingagent.AppName))
			},
			OnCancel: func() {
				done()
				m.ui.RequestRender(false)
			},
		})
		return selectorComponent{component: selector, focus: selector}
	})
}

// showUserMessageSelector mounts the /fork message picker (pi:
// showUserMessageSelector).
func (m *InteractiveMode) showUserMessageSelector() {
	userMessages := m.session().GetUserMessagesForForking()

	if len(userMessages) == 0 {
		m.showStatus("No messages to fork from")
		return
	}

	initialSelectedID := userMessages[len(userMessages)-1].EntryID

	items := make([]components.UserMessageItem, 0, len(userMessages))
	for _, message := range userMessages {
		items = append(items, components.UserMessageItem{ID: message.EntryID, Text: message.Text})
	}

	m.showSelector(func(done func()) selectorComponent {
		selector := components.NewUserMessageSelectorComponent(
			items,
			func(entryID string) {
				done()
				// pi's onSelect is async — pig runs the fork on its own
				// goroutine (session replacement is a blocking operation).
				go func() {
					// pig panic containment (see docs/feat/PANIC-RESILIENCE.md).
					defer pigutils.RecoverPanic("interactive.fork")
					result, err := m.runtimeHost.Fork(context.Background(), entryID, nil)
					if err != nil {
						_ = m.handleFatalRuntimeError("Failed to fork", err)
						return
					}
					if result.Cancelled {
						m.ui.RequestRender(false)
						return
					}
					m.editor.SetText(result.SelectedText)
					m.showStatus("Forked to new session")
				}()
			},
			func() {
				done()
				m.ui.RequestRender(false)
			},
			initialSelectedID,
		)
		return selectorComponent{component: selector, focus: selector.GetMessageList()}
	})
}

// =======================================================================
// Thinking / model / scoped-models / tree / session selectors (pi:
// handleThinkingCommand / showThinkingSelector / handleModelCommand /
// findExactModelMatch / showModelSelector / showModelsSelector /
// showTreeSelector / showSessionSelector)
// =======================================================================

// handleThinkingCommand applies a /thinking argument or opens the selector
// (pi: handleThinkingCommand).
func (m *InteractiveMode) handleThinkingCommand(searchTerm string) {
	availableLevels := m.session().GetAvailableThinkingLevels()
	if searchTerm == "" {
		m.showThinkingSelector()
		return
	}

	normalized := strings.ToLower(strings.TrimSpace(searchTerm))
	for _, candidate := range availableLevels {
		if strings.ToLower(string(candidate)) == normalized {
			m.selectThinkingLevel(candidate, false)
			return
		}
	}
	levelStrings := make([]string, 0, len(availableLevels))
	for _, level := range availableLevels {
		levelStrings = append(levelStrings, string(level))
	}
	m.ShowError(fmt.Sprintf("Unknown thinking level %q. Available levels: %s.", searchTerm, strings.Join(levelStrings, ", ")))
}

// selectThinkingLevel applies a thinking level (pi: selectThinkingLevel).
func (m *InteractiveMode) selectThinkingLevel(level agent.ThinkingLevel, persist bool) {
	options := &core.ModelMutationOptions{Persist: persist}
	m.session().SetThinkingLevel(level, options)
	m.footer.Invalidate()
	m.updateEditorBorderColor()
	if persist {
		m.showStatus(fmt.Sprintf("Default thinking level: %s", level))
	} else {
		m.showStatus(fmt.Sprintf("Thinking level: %s", level))
	}
}

// showThinkingSelector mounts the /thinking selector (pi:
// showThinkingSelector).
func (m *InteractiveMode) showThinkingSelector() {
	m.showSelector(func(done func()) selectorComponent {
		selectLevel := func(level agent.ThinkingLevel, persist bool) {
			m.selectThinkingLevel(level, persist)
			done()
		}
		currentLevel := m.session().ThinkingLevel()
		if currentLevel == "" {
			currentLevel = core.DefaultThinkingLevel
		}
		defaultLevel, hasDefault := m.settingsManager().GetDefaultThinkingLevel()
		if !hasDefault {
			defaultLevel = core.DefaultThinkingLevel
		}
		selector := components.NewThinkingSelectorComponent(
			currentLevel,
			m.session().GetAvailableThinkingLevels(),
			func(level agent.ThinkingLevel) { selectLevel(level, false) },
			func() {
				done()
				m.ui.RequestRender(false)
			},
			func(level agent.ThinkingLevel) { selectLevel(level, true) },
			defaultLevel,
		)
		return selectorComponent{component: selector, focus: selector}
	})
}

// handleModelCommand applies a /model argument or opens the selector (pi:
// handleModelCommand).
func (m *InteractiveMode) handleModelCommand(searchTerm string) {
	if searchTerm == "" {
		m.showModelSelector("")
		return
	}

	model, ok := m.findExactModelMatch(searchTerm)
	if ok {
		if err := m.session().SetModel(context.Background(), &model, nil); err != nil {
			m.ShowError(errorMessageOf(err))
			return
		}
		m.footer.Invalidate()
		m.updateEditorBorderColor()
		m.showStatus(fmt.Sprintf("Model: %s", model.ID))
		// pi: maybeWarnAboutAnthropicSubscriptionAuth + checkDaxnutsEasterEgg
		// stay deferred (see completeProviderAuthentication).
		return
	}

	m.showModelSelector(searchTerm)
}

// findExactModelMatch resolves an exact model reference against the cached
// (or post-refresh) snapshot (pi: findExactModelMatch).
func (m *InteractiveMode) findExactModelMatch(searchTerm string) (ai.Model, bool) {
	var cachedModels []ai.Model
	if scoped := m.session().ScopedModels(); len(scoped) > 0 {
		for _, scopedModel := range scoped {
			cachedModels = append(cachedModels, scopedModel.Model)
		}
	} else {
		cachedModels = m.session().GetModelRuntime().GetAvailableSnapshot()
	}
	cachedMatch, ok := core.FindExactModelReferenceMatch(searchTerm, cachedModels)
	if ok || len(m.session().ScopedModels()) > 0 {
		return cachedMatch, ok
	}

	m.showStatus("Refreshing model catalogs…")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := RefreshModelCatalogs(ctx, m.session().GetModelRuntime())
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			m.ShowWarning("Model refresh timed out; searching cached models.")
		} else {
			m.ShowWarning("Could not refresh model catalogs: " + errorMessageOf(err))
		}
	} else if result.Aborted {
		m.ShowWarning("Model refresh timed out; searching cached models.")
	} else if len(result.Errors) > 0 {
		providers := make([]string, 0, len(result.Errors))
		for provider := range result.Errors {
			providers = append(providers, provider)
		}
		sort.Strings(providers)
		m.ShowWarning(fmt.Sprintf("Could not refresh %s; searching cached models.", strings.Join(providers, ", ")))
	}
	return core.FindExactModelReferenceMatch(searchTerm, m.session().GetModelRuntime().GetAvailableSnapshot())
}

// showModelSelector mounts the /model selector (pi: showModelSelector).
func (m *InteractiveMode) showModelSelector(initialSearchInput string) {
	m.showSelector(func(done func()) selectorComponent {
		selectModel := func(model ai.Model, persist bool) {
			trySetModel := func() error {
				return m.session().SetModel(context.Background(), &model, &core.ModelMutationOptions{Persist: persist})
			}
			// pi: await setModel — pig runs the mutation on its own goroutine
			// (session mutation is potentially blocking).
			go func() {
				// pig panic containment (see docs/feat/PANIC-RESILIENCE.md).
				defer pigutils.RecoverPanic("interactive.setModel")
				if err := trySetModel(); err != nil {
					done()
					m.ShowError(errorMessageOf(err))
					return
				}
				// pi: updateAvailableProviderCount() after setModel (v0.85.1)
				// keeps the footer provider count in sync.
				m.updateAvailableProviderCount()
				m.footer.Invalidate()
				m.updateEditorBorderColor()
				done()
				if persist {
					m.showStatus(fmt.Sprintf("Default model: %s/%s", model.Provider, model.ID))
				} else {
					m.showStatus(fmt.Sprintf("Model: %s", model.ID))
				}
			}()
		}
		var defaultRef *components.ModelSelectorDefaultReference
		defaultProvider, hasDefaultProvider := m.settingsManager().GetDefaultProvider()
		defaultModelID, hasDefaultModel := m.settingsManager().GetDefaultModel()
		if hasDefaultProvider && hasDefaultModel {
			defaultRef = &components.ModelSelectorDefaultReference{Provider: defaultProvider, ID: defaultModelID}
		}
		selector := components.NewModelSelectorComponent(
			m.ui,
			m.session().Model(),
			m.session().GetModelRuntime(),
			m.session().ScopedModels(),
			func(model ai.Model) { selectModel(model, false) },
			func() {
				done()
				m.ui.RequestRender(false)
			},
			initialSearchInput,
			func(model ai.Model) { selectModel(model, true) },
			defaultRef,
			func(ctx context.Context) (ai.ModelsRefreshResult, error) {
				return RefreshModelCatalogs(ctx, m.session().GetModelRuntime())
			},
			m.mutateUI,
		)
		return selectorComponent{component: selector, focus: selector, dispose: selector.Dispose}
	})
}

// showModelsSelector mounts the /scoped-models panel (pi:
// showModelsSelector).
func (m *InteractiveMode) showModelsSelector() {
	session := m.session()
	availableModels := append([]ai.Model{}, session.GetModelRuntime().GetAvailableSnapshot()...)
	availableModelIDs := map[string]bool{}
	for _, model := range availableModels {
		availableModelIDs[fmt.Sprintf("%s/%s", model.Provider, model.ID)] = true
	}
	configuredPatterns, _ := m.settingsManager().GetEnabledModels()
	sessionScopedModels := session.ScopedModels()

	configuredEnabledIDs := func(models []ai.Model) []string {
		if len(configuredPatterns) == 0 {
			return nil
		}
		resolved := core.ResolveModelScopeFromModels(configuredPatterns, models)
		ids := make([]string, 0, len(resolved.ScopedModels))
		for _, scopedModel := range resolved.ScopedModels {
			ids = append(ids, fmt.Sprintf("%s/%s", scopedModel.Model.Provider, scopedModel.Model.ID))
		}
		for _, diagnostic := range resolved.Diagnostics {
			if diagnostic.Code == "no-match" && !containsString(ids, diagnostic.Pattern) {
				ids = append(ids, diagnostic.Pattern)
			}
		}
		return ids
	}

	var currentEnabledIDs []string
	if len(sessionScopedModels) > 0 {
		currentEnabledIDs = make([]string, 0, len(sessionScopedModels))
		for _, scopedModel := range sessionScopedModels {
			currentEnabledIDs = append(currentEnabledIDs, fmt.Sprintf("%s/%s", scopedModel.Model.Provider, scopedModel.Model.ID))
		}
	} else {
		currentEnabledIDs = configuredEnabledIDs(availableModels)
	}
	selectionChanged := false

	updateSessionModels := func(enabledIDs []string) {
		if enabledIDs == nil {
			currentEnabledIDs = nil
		} else {
			currentEnabledIDs = append([]string{}, enabledIDs...)
		}
		hasEnabledAvailableModel := false
		for _, id := range enabledIDs {
			if availableModelIDs[id] {
				hasEnabledAvailableModel = true
				break
			}
		}
		allAvailableModelsEnabled := enabledIDs != nil
		for id := range availableModelIDs {
			if !containsString(enabledIDs, id) {
				allAvailableModelsEnabled = false
				break
			}
		}
		if enabledIDs != nil && hasEnabledAvailableModel && !allAvailableModelsEnabled {
			newScopedModels := core.ResolveModelScopeFromModels(enabledIDs, availableModels).ScopedModels
			m.session().SetScopedModels(newScopedModels)
		} else {
			m.session().SetScopedModels(nil)
		}
		m.updateAvailableProviderCount()
		m.ui.RequestRender(false)
	}

	m.showSelector(func(done func()) selectorComponent {
		disposed := false
		selector := components.NewScopedModelsSelectorComponent(
			components.ScopedModelsConfig{
				AllModels:       availableModels,
				EnabledModelIDs: currentEnabledIDs,
				RefreshStatus:   "Refreshing model catalogs…",
			},
			components.ScopedModelsCallbacks{
				OnChange: func(enabledIDs []string) {
					selectionChanged = true
					updateSessionModels(enabledIDs)
				},
				OnPersist: func(enabledIDs []string) {
					allEnabled := enabledIDs != nil && len(enabledIDs) == len(availableModels)
					if allEnabled {
						for _, id := range enabledIDs {
							if !availableModelIDs[id] {
								allEnabled = false
								break
							}
						}
					}
					if enabledIDs == nil || allEnabled {
						m.settingsManager().SetEnabledModels(nil)
					} else {
						m.settingsManager().SetEnabledModels(enabledIDs)
					}
					m.showStatus("Model selection saved to settings")
				},
				OnCancel: func() {
					done()
					m.ui.RequestRender(false)
				},
			},
		)

		// pi: void refreshModelCatalogs(...).then(...) — pig runs the refresh
		// on its own goroutine; component mutations go through mutateUI.
		go func() {
			// pig panic containment (see docs/feat/PANIC-RESILIENCE.md).
			defer pigutils.RecoverPanic("interactive.modelRefresh")
			refreshCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			result, err := RefreshModelCatalogs(refreshCtx, session.GetModelRuntime())
			m.mutateUI(func() {
				if disposed {
					return
				}
				if err != nil {
					if refreshCtx.Err() == context.DeadlineExceeded {
						selector.SetRefreshStatus("Model refresh timed out; showing cached models.", "warning")
					} else {
						selector.SetRefreshStatus("Could not refresh model catalogs: "+errorMessageOf(err), "warning")
					}
					m.ui.RequestRender(false)
					return
				}
				availableModels = append([]ai.Model{}, session.GetModelRuntime().GetAvailableSnapshot()...)
				availableModelIDs = map[string]bool{}
				for _, model := range availableModels {
					availableModelIDs[fmt.Sprintf("%s/%s", model.Provider, model.ID)] = true
				}
				if !selectionChanged && len(sessionScopedModels) == 0 {
					currentEnabledIDs = configuredEnabledIDs(availableModels)
					selector.UpdateModels(availableModels, currentEnabledIDs, true)
				} else {
					selector.UpdateModels(availableModels, nil, false)
				}
				if currentEnabledIDs != nil {
					updateSessionModels(currentEnabledIDs)
				}
				if result.Aborted {
					selector.SetRefreshStatus("Model refresh timed out; showing cached models.", "warning")
				} else if len(result.Errors) > 0 {
					providers := make([]string, 0, len(result.Errors))
					for provider := range result.Errors {
						providers = append(providers, provider)
					}
					sort.Strings(providers)
					selector.SetRefreshStatus(fmt.Sprintf("Could not refresh %s; showing cached models.", strings.Join(providers, ", ")), "warning")
				} else {
					selector.SetRefreshStatus("Model catalogs refreshed.", "success")
				}
				m.ui.RequestRender(false)
			})
		}()
		return selectorComponent{
			component: selector,
			focus:     selector,
			dispose: func() {
				disposed = true
			},
		}
	})
}

// showTreeSelector mounts the /tree selector and handles navigation (pi:
// showTreeSelector; initialSelectedID "" = none).
func (m *InteractiveMode) showTreeSelector(initialSelectedID string) {
	tree := m.sessionManager().GetTree()
	realLeafID := m.sessionManager().GetLeafID()
	currentLeafID := ""
	if realLeafID != nil {
		currentLeafID = *realLeafID
	}
	initialFilterMode := components.FilterMode(m.settingsManager().GetTreeFilterMode())

	if len(tree) == 0 {
		m.showStatus("No entries in session")
		return
	}

	m.showSelector(func(done func()) selectorComponent {
		selector := components.NewTreeSelectorComponent(
			tree,
			currentLeafID,
			m.ui.Terminal().Rows(),
			func(entryID string) {
				// pi's onSelect is async — pig runs the navigation on its own
				// goroutine (it may abort streaming and summarize).
				go m.handleTreeNavigation(entryID, done)
			},
			func() {
				done()
				m.ui.RequestRender(false)
			},
			func(entryID string, label *string) {
				if _, err := m.sessionManager().AppendLabelChange(entryID, label); err != nil {
					m.ShowError("Failed to save label: " + errorMessageOf(err))
					return
				}
				m.ui.RequestRender(false)
			},
			initialSelectedID,
			initialFilterMode,
		)
		selector.OnCopy = func(text string) {
			if text == "" {
				m.ShowError("Selected entry has no text to copy")
				return
			}
			if err := utils.CopyToClipboard(text); err != nil {
				m.ShowError(errorMessageOf(err))
				return
			}
			m.showStatus("Copied selected message to clipboard")
		}
		return selectorComponent{component: selector, focus: selector}
	})
}

// handleTreeNavigation performs the post-selection navigation flow of the
// tree selector (pi: the onSelect body of showTreeSelector).
func (m *InteractiveMode) handleTreeNavigation(entryID string, done func()) {
	// Selecting the current leaf is a no-op (already there)
	if leafID := m.sessionManager().GetLeafID(); leafID != nil && entryID == *leafID {
		done()
		m.showStatus("Already at this point")
		return
	}

	// Ask about summarization
	done() // Close selector first

	wantsSummary := false
	customInstructions := ""

	// Check if we should skip the prompt (user preference to always default to no summary)
	if !m.settingsManager().GetBranchSummarySkipPrompt() {
		ctx := context.Background()
		for {
			summaryChoice, ok := m.showExtensionSelector(ctx, "Summarize branch?", []string{
				"No summary",
				"Summarize",
				"Summarize with custom prompt",
			}, nil)

			if !ok {
				// User pressed escape - re-show tree selector with same selection
				m.showTreeSelector(entryID)
				return
			}

			wantsSummary = summaryChoice != "No summary"

			if summaryChoice == "Summarize with custom prompt" {
				instructions, ok := m.showExtensionEditor(ctx, "Custom summarization instructions", "")
				if !ok {
					// User cancelled - loop back to summary selector
					continue
				}
				customInstructions = instructions
			}

			// User made a complete choice
			break
		}
	}

	// The user committed to navigating: stop the active response first.
	if m.session().IsStreaming() {
		m.restoreQueuedMessagesToEditor(false)
		if err := m.session().Abort(context.Background()); err != nil {
			m.ShowError("Failed to abort: " + errorMessageOf(err))
		}
	}

	// Set up escape handler and status indicator if summarizing
	showingSummaryIndicator := false

	if wantsSummary {
		m.abortMu.Lock()
		m.retryEscapeHandler = m.defaultEditor.OnEscape
		m.defaultEditor.OnEscape = func() {
			m.session().AbortBranchSummary()
		}
		m.abortMu.Unlock()
		m.mutateUI(func() {
			m.chatContainer.AddChild(tui.NewSpacer(1))
		})
		m.showStatusIndicator(components.NewBranchSummaryStatusIndicator(m.ui))
		showingSummaryIndicator = true
		m.ui.RequestRender(false)
	}

	result, err := m.session().NavigateTree(context.Background(), entryID, &core.NavigateTreeOptions{
		Summarize:          wantsSummary,
		CustomInstructions: customInstructions,
	})
	defer func() {
		if showingSummaryIndicator {
			m.clearStatusIndicator(components.StatusKindBranchSummary)
		}
		m.abortMu.Lock()
		if m.retryEscapeHandler != nil {
			m.defaultEditor.OnEscape = m.retryEscapeHandler
			m.retryEscapeHandler = nil
		}
		m.abortMu.Unlock()
	}()
	if err != nil {
		m.ShowError(errorMessageOf(err))
		return
	}

	if result.Aborted {
		// Summarization aborted - re-show tree selector with same selection
		m.showStatus("Branch summarization cancelled")
		m.showTreeSelector(entryID)
		return
	}
	if result.Cancelled {
		m.showStatus("Navigation cancelled")
		return
	}

	// Update UI
	m.mutateUI(func() {
		m.chatContainer.Clear()
	})
	m.renderInitialMessages()
	if result.EditorText != nil && strings.TrimSpace(m.editor.GetText()) == "" {
		m.editor.SetText(*result.EditorText)
	}
	m.showStatus("Navigated to selected point")
	go m.flushCompactionQueue(context.Background(), false)
}

// showSessionSelector mounts the /resume selector (pi: showSessionSelector).
func (m *InteractiveMode) showSessionSelector() {
	m.showSelector(func(done func()) selectorComponent {
		selector := components.NewSessionSelectorComponent(
			func(onProgress func(loaded, total int)) []core.SessionInfo {
				return core.ListSessions(m.sessionManager().GetCwd(), m.sessionManager().GetSessionDir(), onProgress)
			},
			func(onProgress func(loaded, total int)) []core.SessionInfo {
				if m.sessionManager().UsesDefaultSessionDir() {
					return core.ListAllSessions("", onProgress)
				}
				return core.ListAllSessions(m.sessionManager().GetSessionDir(), onProgress)
			},
			func(sessionPath string) {
				done()
				go func() {
					// pig panic containment (see docs/feat/PANIC-RESILIENCE.md).
					defer pigutils.RecoverPanic("interactive.resumeSession")
					if _, err := m.handleResumeSession(context.Background(), sessionPath, nil); err != nil {
						_ = err
					}
				}()
			},
			func() {
				done()
				m.ui.RequestRender(false)
			},
			func() {
				defer pigutils.RecoverPanic("interactive.shutdown")
				_ = m.shutdown(false)
			},
			func() { m.ui.RequestRender(false) },
			&components.SessionSelectorOptions{
				RenameSession: func(sessionFilePath string, nextName string) error {
					mgr, err := core.OpenSession(sessionFilePath, "", nil)
					if err != nil {
						return err
					}
					_, err = mgr.AppendSessionInfo(nextName)
					return err
				},
				ShowRenameHint: lo.ToPtr(true),
				Keybindings:    m.keybindings,
			},
			m.sessionManager().GetSessionFile(),
			m.mutateUI,
		)
		return selectorComponent{component: selector, focus: selector}
	})
}

// =======================================================================
// Login flow (pi: getLoginProviderOptions and the /login//logout family)
// =======================================================================

// loginDialogInteraction adapts the login dialog to auth.AuthInteraction
// (pi's inline { prompt, notify } callbacks in loginProvider).
type loginDialogInteraction struct {
	m      *InteractiveMode
	dialog *components.LoginDialogComponent
}

func (i *loginDialogInteraction) Prompt(prompt auth.AuthPrompt) (string, error) {
	return i.m.showAuthPrompt(i.dialog, prompt)
}

func (i *loginDialogInteraction) Notify(event auth.AuthEvent) {
	i.m.notifyAuthDialog(i.dialog, event)
}

// isUnknownModel reports whether the session model is the unknown placeholder
// (pi: isUnknownModel).
func isUnknownModel(model *ai.Model) bool {
	return model != nil && string(model.Provider) == "unknown" && model.ID == "unknown" && string(model.API) == "unknown"
}

// hasDefaultModelProvider reports whether the provider has a known default
// model (pi: hasDefaultModelProvider).
func hasDefaultModelProvider(providerID string) bool {
	_, ok := core.DefaultModelPerProvider[providerID]
	return ok
}

// llamaCppPostLoginGuidance renders the llama.cpp post-login hint (pi:
// llamaCppPostLoginGuidance).
func llamaCppPostLoginGuidance(actionLabel string, loadedModelCount int) string {
	if loadedModelCount == 0 {
		return actionLabel + ". No llama.cpp models are loaded. Use /llama to load a model, then /model to select it."
	}
	return actionLabel + ". Use /model to select a loaded llama.cpp model, or /llama to manage models."
}

// loginProviderCompletionOption is one /login autocomplete entry (pi:
// LoginProviderCompletionOption).
type loginProviderCompletionOption struct {
	id        string
	name      string
	authTypes []auth.AuthType
}

// loginAuthTypeOrder ranks auth types in completion labels (pi:
// AUTH_TYPE_ORDER).
var loginAuthTypeOrder = map[auth.AuthType]int{
	auth.AuthTypeOAuth:  0,
	auth.AuthTypeAPIKey: 1,
}

// getLoginProviderCompletionOptions collapses provider options into one entry
// per provider id (pi: getLoginProviderCompletionOptions).
func getLoginProviderCompletionOptions(providerOptions []components.AuthSelectorProvider) []loginProviderCompletionOption {
	byID := map[string]*loginProviderCompletionOption{}
	var order []string
	for _, provider := range providerOptions {
		if existing, ok := byID[provider.ID]; ok {
			found := false
			for _, authType := range existing.authTypes {
				if authType == provider.AuthType {
					found = true
					break
				}
			}
			if !found {
				existing.authTypes = append(existing.authTypes, provider.AuthType)
				sort.SliceStable(existing.authTypes, func(i, j int) bool {
					return loginAuthTypeOrder[existing.authTypes[i]] < loginAuthTypeOrder[existing.authTypes[j]]
				})
			}
			continue
		}
		byID[provider.ID] = &loginProviderCompletionOption{id: provider.ID, name: provider.Name, authTypes: []auth.AuthType{provider.AuthType}}
		order = append(order, provider.ID)
	}
	options := make([]loginProviderCompletionOption, 0, len(order))
	for _, id := range order {
		options = append(options, *byID[id])
	}
	sort.SliceStable(options, func(i, j int) bool { return options[i].name < options[j].name })
	return options
}

// getLoginProviderSearchText is the fuzzy search text for /login completions
// (pi: getLoginProviderSearchText).
func getLoginProviderSearchText(provider loginProviderCompletionOption) string {
	var authTypes []string
	for _, authType := range provider.authTypes {
		authTypes = append(authTypes, fmt.Sprintf("%s %s", authType, components.FormatAuthSelectorProviderType(authType)))
	}
	return provider.id + " " + provider.name + " " + strings.Join(authTypes, " ")
}

// formatLoginProviderCompletionDescription renders the completion description
// (pi: formatLoginProviderCompletionDescription).
func formatLoginProviderCompletionDescription(provider loginProviderCompletionOption) string {
	var authTypes []string
	for _, authType := range provider.authTypes {
		authTypes = append(authTypes, components.FormatAuthSelectorProviderType(authType))
	}
	joined := strings.Join(authTypes, "/")
	if provider.name == provider.id {
		return joined
	}
	return provider.name + " · " + joined
}

// getLoginProviderOptions projects the provider registry into login selector
// options (pi: getLoginProviderOptions). authType nil = both.
func (m *InteractiveMode) getLoginProviderOptions(authType *auth.AuthType) []components.AuthSelectorProvider {
	var options []components.AuthSelectorProvider
	runtime := m.session().GetModelRuntime()
	for _, provider := range runtime.GetProviders() {
		authStatus := runtime.GetProviderAuthStatus(provider.ID())
		var status *auth.AuthCheck
		if authStatus.Configured {
			statusType := auth.AuthTypeAPIKey
			if runtime.IsUsingOAuth(provider.ID()) {
				statusType = auth.AuthTypeOAuth
			}
			source := authStatus.Label
			if source == "" {
				source = authStatus.Source
			}
			status = &auth.AuthCheck{Type: statusType, Source: source}
		}
		providerAuth := provider.Auth()
		if (authType == nil || *authType == auth.AuthTypeOAuth) && providerAuth.OAuth != nil {
			options = append(options, components.AuthSelectorProvider{
				ID:       provider.ID(),
				Name:     provider.Name(),
				AuthType: auth.AuthTypeOAuth,
				Method:   providerAuth.OAuth,
				Status:   status,
			})
		}
		if (authType == nil || *authType == auth.AuthTypeAPIKey) && providerAuth.APIKey != nil {
			options = append(options, components.AuthSelectorProvider{
				ID:       provider.ID(),
				Name:     provider.Name(),
				AuthType: auth.AuthTypeAPIKey,
				Method:   providerAuth.APIKey,
				Status:   status,
			})
		}
	}
	sort.SliceStable(options, func(i, j int) bool { return options[i].Name < options[j].Name })
	return options
}

// getLogoutProviderOptions lists stored credentials as logout candidates (pi:
// getLogoutProviderOptions).
func (m *InteractiveMode) getLogoutProviderOptions(ctx context.Context) ([]components.AuthSelectorProvider, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	credentials, err := m.session().GetModelRuntime().ListCredentials(timeoutCtx)
	if err != nil {
		return nil, err
	}
	var options []components.AuthSelectorProvider
	for _, credential := range credentials {
		name := credential.ProviderID
		if provider := m.session().GetModelRuntime().GetProvider(credential.ProviderID); provider != nil {
			name = provider.Name()
		}
		authType := auth.AuthType(credential.Type)
		options = append(options, components.AuthSelectorProvider{
			ID:       credential.ProviderID,
			Name:     name,
			AuthType: authType,
			Status:   &auth.AuthCheck{Type: authType, Source: "stored credential"},
		})
	}
	sort.SliceStable(options, func(i, j int) bool { return options[i].Name < options[j].Name })
	return options, nil
}

// findLoginProviderOptions resolves a /login provider reference (pi:
// findLoginProviderOptions).
func (m *InteractiveMode) findLoginProviderOptions(providerRef string) []components.AuthSelectorProvider {
	normalizedProviderRef := strings.ToLower(strings.TrimSpace(providerRef))
	if normalizedProviderRef == "" {
		return []components.AuthSelectorProvider{}
	}

	var options []components.AuthSelectorProvider
	for _, provider := range m.getLoginProviderOptions(nil) {
		if strings.ToLower(provider.ID) == normalizedProviderRef || strings.ToLower(provider.Name) == normalizedProviderRef {
			options = append(options, provider)
		}
	}
	return options
}

// handleLoginCommand dispatches /login (pi: handleLoginCommand).
func (m *InteractiveMode) handleLoginCommand(providerRef string) {
	if providerRef == "" {
		m.showLoginAuthTypeSelector(nil)
		return
	}

	providerOptions := m.findLoginProviderOptions(providerRef)
	if len(providerOptions) == 1 {
		m.startProviderLogin(providerOptions[0])
		return
	}

	if len(providerOptions) > 1 {
		providerIDs := map[string]bool{}
		for _, provider := range providerOptions {
			providerIDs[provider.ID] = true
		}
		if len(providerIDs) == 1 {
			m.showLoginAuthTypeSelector(providerOptions)
			return
		}
	}

	m.showLoginProviderSelector(nil, providerRef)
}

// startProviderLogin begins the login flow for one provider option (pi:
// startProviderLogin; pi's `void` callers map to pig goroutines at the call
// sites — this body blocks on dialog input).
func (m *InteractiveMode) startProviderLogin(providerOption components.AuthSelectorProvider) {
	if providerOption.AuthType == auth.AuthTypeOAuth {
		m.showLoginDialog(providerOption.ID, providerOption.Name)
	} else if hasAPIKeyLogin(providerOption.Method) {
		m.showApiKeyLoginDialog(providerOption.ID, providerOption.Name)
	} else {
		m.showAmbientAuthDialog(providerOption)
	}
}

// hasAPIKeyLogin reports whether the method carries the optional login
// capability (pi: providerOption.method?.login).
func hasAPIKeyLogin(method any) bool {
	_, ok := method.(auth.ApiKeyAuthLogin)
	return ok
}

// showLoginAuthTypeSelector offers subscription vs API key login (pi:
// showLoginAuthTypeSelector). providerOptions nil = full registry scan.
func (m *InteractiveMode) showLoginAuthTypeSelector(providerOptions []components.AuthSelectorProvider) {
	subscriptionLabel := "Sign in with an account"
	for _, provider := range providerOptions {
		if provider.AuthType != auth.AuthTypeOAuth {
			continue
		}
		if oauth, ok := provider.Method.(auth.OAuthAuth); ok && oauth != nil && oauth.LoginLabel() != "" {
			subscriptionLabel = oauth.LoginLabel()
		}
		break
	}
	apiKeyLabel := "Sign in with an API key"
	availableAuthTypes := map[auth.AuthType]bool{
		auth.AuthTypeOAuth:  true,
		auth.AuthTypeAPIKey: true,
	}
	if providerOptions != nil {
		availableAuthTypes = map[auth.AuthType]bool{}
		for _, provider := range providerOptions {
			availableAuthTypes[provider.AuthType] = true
		}
	}
	var options []string
	if availableAuthTypes[auth.AuthTypeOAuth] {
		options = append(options, subscriptionLabel)
	}
	if availableAuthTypes[auth.AuthTypeAPIKey] {
		options = append(options, apiKeyLabel)
	}

	if len(options) == 0 {
		m.showStatus("No login methods available.")
		return
	}

	if providerOptions != nil && len(options) == 1 {
		providerOption := providerOptions[0]
		go m.startProviderLogin(providerOption)
		return
	}

	title := "Select authentication method:"
	if len(providerOptions) > 0 {
		title = "Select authentication method for " + providerOptions[0].Name + ":"
	}
	m.showSelector(func(done func()) selectorComponent {
		selector := components.NewExtensionSelectorComponent(
			title,
			options,
			func(option string) {
				done()
				authType := auth.AuthTypeAPIKey
				if option == subscriptionLabel {
					authType = auth.AuthTypeOAuth
				}
				if providerOptions != nil {
					for _, provider := range providerOptions {
						if provider.AuthType == authType {
							go m.startProviderLogin(provider)
						}
					}
					return
				}
				m.showLoginProviderSelector(&authType, "")
			},
			func() {
				done()
				m.ui.RequestRender(false)
			},
			nil,
		)
		return selectorComponent{component: selector, focus: selector}
	})
}

// showLoginProviderSelector offers provider choices for one auth type (pi:
// showLoginProviderSelector). authType nil = both.
func (m *InteractiveMode) showLoginProviderSelector(authType *auth.AuthType, initialSearchInput string) {
	providerOptions := m.getLoginProviderOptions(authType)
	if len(providerOptions) == 0 {
		var message string
		switch {
		case authType != nil && *authType == auth.AuthTypeOAuth:
			message = "No subscription providers available."
		case authType != nil && *authType == auth.AuthTypeAPIKey:
			message = "No API key providers available."
		default:
			message = "No login providers available."
		}
		m.showStatus(message)
		return
	}

	m.showSelector(func(done func()) selectorComponent {
		selector := components.NewOAuthSelectorComponent(
			"login",
			providerOptions,
			func(providerID string, selectedAuthType auth.AuthType) {
				done()

				var providerOption *components.AuthSelectorProvider
				for i := range providerOptions {
					if providerOptions[i].ID == providerID && providerOptions[i].AuthType == selectedAuthType {
						providerOption = &providerOptions[i]
						break
					}
				}
				if providerOption == nil {
					return
				}

				go m.startProviderLogin(*providerOption)
			},
			func() {
				done()
				if authType != nil {
					m.showLoginAuthTypeSelector(nil)
				} else {
					m.ui.RequestRender(false)
				}
			},
			initialSearchInput,
		)
		return selectorComponent{component: selector, focus: selector}
	})
}

// showOAuthSelector dispatches /login (auth-type selector) or /logout (pi:
// showOAuthSelector).
func (m *InteractiveMode) showOAuthSelector(mode string) {
	if mode == "login" {
		m.showLoginAuthTypeSelector(nil)
		return
	}

	providerOptions, err := m.getLogoutProviderOptions(context.Background())
	if err != nil {
		m.ShowError(fmt.Sprintf("Could not read stored credentials: %s", err.Error()))
		return
	}
	if len(providerOptions) == 0 {
		m.showStatus("No stored credentials to remove. /logout only removes credentials saved by /login; environment variables and models.json config are unchanged.")
		return
	}

	m.showSelector(func(done func()) selectorComponent {
		selector := components.NewOAuthSelectorComponent(
			mode,
			providerOptions,
			func(providerID string, _ auth.AuthType) {
				done()

				var providerOption *components.AuthSelectorProvider
				for i := range providerOptions {
					if providerOptions[i].ID == providerID {
						providerOption = &providerOptions[i]
						break
					}
				}
				if providerOption == nil {
					return
				}

				go func() {
					// pig panic containment (see docs/feat/PANIC-RESILIENCE.md).
					defer pigutils.RecoverPanic("interactive.logout")
					logoutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					err := m.session().GetModelRuntime().Logout(logoutCtx, providerOption.ID)
					if err != nil {
						message := err.Error()
						var syncErr *core.CredentialSynchronizationError
						if errors.As(err, &syncErr) {
							m.ShowError(fmt.Sprintf("Credentials removed for %s, but local model state could not be synchronized: %s", providerOption.Name, message))
						} else {
							m.ShowError(fmt.Sprintf("Logout failed: %s", message))
						}
						return
					}
					m.updateAvailableProviderCount()
					var message string
					if providerOption.AuthType == auth.AuthTypeOAuth {
						message = fmt.Sprintf("Logged out of %s", providerOption.Name)
					} else {
						message = fmt.Sprintf("Removed stored API key for %s. Environment variables and models.json config are unchanged.", providerOption.Name)
					}
					m.showStatus(message)
				}()
			},
			func() {
				done()
				m.ui.RequestRender(false)
			},
			"",
		)
		return selectorComponent{component: selector, focus: selector}
	})
}

// completeProviderAuthentication applies post-login state updates and model
// selection (pi: completeProviderAuthentication). The anthropic-subscription
// warning and daxnuts easter egg calls are deferred.
func (m *InteractiveMode) completeProviderAuthentication(ctx context.Context, providerID string, providerName string, authType auth.AuthType, previousModel *ai.Model) {
	actionLabel := "Saved API key for " + providerName
	if authType == auth.AuthTypeOAuth {
		actionLabel = "Logged in to " + providerName
	}

	var selectedModel *ai.Model
	selectionError := ""
	if isUnknownModel(previousModel) {
		availableModels := m.session().GetModelRuntime().GetAvailableSnapshot()
		var providerModels []ai.Model
		for _, model := range availableModels {
			if string(model.Provider) == providerID {
				providerModels = append(providerModels, model)
			}
		}
		// Matches LLAMA_PROVIDER_ID from extensions/llama/provider.ts; kept
		// inline to avoid coupling interactive mode to the built-in extension.
		if providerID == "llama.cpp" {
			selectionError = llamaCppPostLoginGuidance(actionLabel, len(providerModels))
		} else if !hasDefaultModelProvider(providerID) {
			selectionError = fmt.Sprintf("%s, but no default model is configured for provider %q. Use /model to select a model.", actionLabel, providerID)
		} else if len(providerModels) == 0 {
			selectionError = fmt.Sprintf("%s, but no models are available for that provider. Use /model to select a model.", actionLabel)
		} else {
			defaultModelID := core.DefaultModelPerProvider[providerID]
			for i := range providerModels {
				if providerModels[i].ID == defaultModelID {
					selectedModel = &providerModels[i]
					break
				}
			}
			if selectedModel == nil {
				selectionError = fmt.Sprintf("%s, but its default model %q is not available. Use /model to select a model.", actionLabel, defaultModelID)
			} else if err := m.session().SetModel(ctx, selectedModel, nil); err != nil {
				selectedModel = nil
				selectionError = fmt.Sprintf("%s, but selecting its default model failed: %s. Use /model to select a model.", actionLabel, err.Error())
			}
		}
	}

	m.updateAvailableProviderCount()
	m.footer.Invalidate()
	m.updateEditorBorderColor()
	if selectedModel != nil {
		m.showStatus(fmt.Sprintf("%s. Selected %s. Credentials saved to %s", actionLabel, selectedModel.ID, codingagent.GetAuthPath()))
	} else {
		m.showStatus(fmt.Sprintf("%s. Credentials saved to %s", actionLabel, codingagent.GetAuthPath()))
		if selectionError != "" {
			m.ShowError(selectionError)
		}
	}

	go func() {
		// pig panic containment (see docs/feat/PANIC-RESILIENCE.md).
		defer pigutils.RecoverPanic("interactive.providerRefresh")
		refreshCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result := m.session().GetModelRuntime().Refresh(refreshCtx, &ai.ModelsRefreshOptions{Providers: []string{providerID}})
		if result.Aborted {
			m.ShowWarning(fmt.Sprintf("%s, but its model catalog refresh timed out; using cached models.", actionLabel))
		} else if len(result.Errors) > 0 {
			failed := make([]string, 0, len(result.Errors))
			for provider := range result.Errors {
				failed = append(failed, provider)
			}
			sort.Strings(failed)
			m.ShowWarning(fmt.Sprintf("%s, but its model catalog could not be refreshed; using cached models.", actionLabel))
			_ = failed
		}
		m.updateAvailableProviderCount()
		m.footer.Invalidate()
		m.ui.RequestRender(false)
	}()
}

// showAmbientAuthDialog renders the provider-owned auth info dialog (pi:
// showAmbientAuthDialog).
func (m *InteractiveMode) showAmbientAuthDialog(providerOption components.AuthSelectorProvider) {
	restoreEditor := func() {
		m.mutateUI(func() {
			m.editorContainer.Clear()
			m.editorContainer.AddChild(m.editor)
			m.ui.SetFocus(m.editor)
		})
	}

	methodName := "Authentication"
	if name := components.AuthSelectorMethodName(providerOption.Method); name != "" {
		methodName = name
	}
	dialog := components.NewLoginDialogComponent(
		m.ui,
		providerOption.ID,
		func(bool, string) { restoreEditor() },
		providerOption.Name,
		providerOption.Name+" setup",
	)
	dialog.Mutator = m.mutateUI
	dialog.ShowInfo(
		fmt.Sprintf("%s is configured outside %s.", methodName, codingagent.AppName),
		nil,
		true,
	)

	m.mutateUI(func() {
		m.editorContainer.Clear()
		m.editorContainer.AddChild(dialog)
		m.ui.SetFocus(dialog)
	})
}

// showApiKeyLoginDialog runs the API-key login flow (pi:
// showApiKeyLoginDialog).
func (m *InteractiveMode) showApiKeyLoginDialog(providerID string, providerName string) {
	previousModel := m.session().Model()

	dialog := components.NewLoginDialogComponent(
		m.ui,
		providerID,
		func(bool, string) {
			// Completion handled below
		},
		providerName,
		"",
	)
	dialog.Mutator = m.mutateUI

	if providerID == "amazon-bedrock" {
		dialog.ShowDetails([]string{
			theme.Current().Fg("text", "You can also use an AWS profile, IAM keys, or role-based credentials."),
			theme.Current().Fg("muted", "See:"),
			theme.Current().Fg("accent", "  "+path.Join(codingagent.GetDocsPath(), "providers.md")),
		})
	}

	m.mutateUI(func() {
		m.editorContainer.Clear()
		m.editorContainer.AddChild(dialog)
		m.ui.SetFocus(dialog)
	})

	restoreEditor := func() {
		m.mutateUI(func() {
			m.editorContainer.Clear()
			m.editorContainer.AddChild(m.editor)
			m.ui.SetFocus(m.editor)
		})
	}

	err := m.loginProvider(dialog, providerID, auth.AuthTypeAPIKey)
	if err != nil {
		restoreEditor()
		errorMsg := err.Error()
		var syncErr *core.CredentialSynchronizationError
		if errors.As(err, &syncErr) {
			m.ShowError(fmt.Sprintf("Saved API key for %s, but local model state could not be synchronized: %s", providerName, errorMsg))
		} else if errorMsg != "Login cancelled" {
			m.ShowError(fmt.Sprintf("Failed to save API key for %s: %s", providerName, errorMsg))
		}
		return
	}
	restoreEditor()
	m.completeProviderAuthentication(context.Background(), providerID, providerName, auth.AuthTypeAPIKey, previousModel)
}

// showAuthSelect renders a select prompt inside an extension selector (pi:
// showAuthSelect).
func (m *InteractiveMode) showAuthSelect(dialog *components.LoginDialogComponent, prompt auth.AuthPrompt) (string, error) {
	type authSelectResult struct {
		id  string
		err error
	}
	resultCh := make(chan authSelectResult, 1)
	restoreDialog := func() {
		m.mutateUI(func() {
			m.editorContainer.Clear()
			m.editorContainer.AddChild(dialog)
			m.ui.SetFocus(dialog)
		})
	}
	labels := make([]string, 0, len(prompt.Options))
	for _, option := range prompt.Options {
		labels = append(labels, option.Label)
	}
	selector := components.NewExtensionSelectorComponent(
		prompt.Message,
		labels,
		func(optionLabel string) {
			restoreDialog()
			for _, option := range prompt.Options {
				if option.Label == optionLabel {
					resultCh <- authSelectResult{id: option.ID}
					return
				}
			}
			resultCh <- authSelectResult{err: errorString("Login cancelled")}
		},
		func() {
			restoreDialog()
			resultCh <- authSelectResult{err: errorString("Login cancelled")}
		},
		nil,
	)
	m.mutateUI(func() {
		m.editorContainer.Clear()
		m.editorContainer.AddChild(selector)
		m.ui.SetFocus(selector)
	})
	result := <-resultCh
	if result.err != nil {
		return "", result.err
	}
	return result.id, nil
}

// errorString is a minimal error wrapper for the fixed cancel message (pi:
// new Error("Login cancelled")).
type errorString string

func (e errorString) Error() string { return string(e) }

// showAuthPrompt renders one auth prompt (pi: showAuthPrompt). pi's
// per-prompt AbortSignal race is folded into the dialog context (pig
// adaptation declared in ai/auth AuthPrompt).
func (m *InteractiveMode) showAuthPrompt(dialog *components.LoginDialogComponent, prompt auth.AuthPrompt) (string, error) {
	switch prompt.Type {
	case "select":
		return m.showAuthSelect(dialog, prompt)
	case "manual_code":
		return dialog.ShowManualInput(prompt.Message)
	default:
		return dialog.ShowPrompt(prompt.Message, prompt.Placeholder)
	}
}

// notifyAuthDialog forwards auth events to the dialog (pi: notifyAuthDialog).
func (m *InteractiveMode) notifyAuthDialog(dialog *components.LoginDialogComponent, event auth.AuthEvent) {
	switch event.Type {
	case "auth_url":
		dialog.ShowAuth(event.URL, event.Instructions)
	case "device_code":
		dialog.ShowDeviceCode(ai.OAuthDeviceCodeInfo{
			UserCode:        event.UserCode,
			VerificationURI: event.VerificationURI,
		})
		dialog.ShowWaiting("Waiting for authentication...")
	case "info":
		dialog.ShowInfo(event.Message, event.Links, false)
	default:
		dialog.ShowProgress(event.Message)
	}
}

// loginProvider runs the provider login flow against the dialog (pi:
// loginProvider).
func (m *InteractiveMode) loginProvider(dialog *components.LoginDialogComponent, providerID string, method auth.AuthType) error {
	interaction := &loginDialogInteraction{m: m, dialog: dialog}
	_, err := m.session().GetModelRuntime().Login(dialog.Context(), providerID, method, interaction)
	return err
}

// showLoginDialog runs the OAuth login flow (pi: showLoginDialog).
func (m *InteractiveMode) showLoginDialog(providerID string, providerName string) {
	previousModel := m.session().Model()
	dialog := components.NewLoginDialogComponent(m.ui, providerID, func(bool, string) {}, providerName, "")
	dialog.Mutator = m.mutateUI
	m.mutateUI(func() {
		m.editorContainer.Clear()
		m.editorContainer.AddChild(dialog)
		m.ui.SetFocus(dialog)
	})

	restoreEditor := func() {
		m.mutateUI(func() {
			m.editorContainer.Clear()
			m.editorContainer.AddChild(m.editor)
			m.ui.SetFocus(m.editor)
		})
	}

	err := m.loginProvider(dialog, providerID, auth.AuthTypeOAuth)
	if err != nil {
		restoreEditor()
		errorMsg := err.Error()
		var syncErr *core.CredentialSynchronizationError
		if errors.As(err, &syncErr) {
			m.ShowError(fmt.Sprintf("Logged in to %s, but local model state could not be synchronized: %s", providerName, errorMsg))
		} else if errorMsg != "Login cancelled" {
			m.ShowError(fmt.Sprintf("Failed to login to %s: %s", providerName, errorMsg))
		}
		return
	}
	restoreEditor()
	m.completeProviderAuthentication(context.Background(), providerID, providerName, auth.AuthTypeOAuth, previousModel)
}

// disposeActiveSelector disposes the active in-place selector (pi:
// disposeActiveSelector; the showSelector callers — model/session/tree/
// settings selectors — land with T6).
func (m *InteractiveMode) disposeActiveSelector() {
	dispose := m.activeSelectorDispose
	m.activeSelectorToken = nil
	m.activeSelectorDispose = nil
	if dispose != nil {
		dispose()
	}
}

// =========================================================================
// Session rebinding (pi: rebindCurrentSession /
// bindCurrentSessionExtensions / renderCurrentSessionState)
// =========================================================================

// resetExtensionUI restores the extension UI surface to defaults (pi:
// resetExtensionUI).
func (m *InteractiveMode) resetExtensionUI() {
	if m.extensionSelector != nil {
		m.hideExtensionSelector()
	}
	if m.extensionInput != nil {
		m.hideExtensionInput()
	}
	if m.extensionEditor != nil {
		m.hideExtensionEditor()
	}
	m.ui.HideOverlay()
	m.clearExtensionTerminalInputListeners()
	m.setExtensionFooter(nil)
	m.setExtensionHeader(nil)
	m.clearExtensionWidgets()
	m.footerDataProviderView.ClearExtensionStatuses()
	m.footer.Invalidate()
	m.autocompleteProviderWrappers = nil
	m.setCustomEditorComponent(nil)
	m.setupAutocompleteProvider()
	m.defaultEditor.OnExtensionShortcut = nil
	m.updateTerminalTitle()
	m.workingMessage = nil
	m.workingVisible = true
	m.setWorkingIndicator(nil)
	if m.isWorkingIndicator() {
		if ind, ok := m.activeStatusIndicator.(interface{ SetMessage(string) }); ok {
			ind.SetMessage(fmt.Sprintf("%s (%s to interrupt)", m.defaultWorkingMessage,
				coretools.KeyText(core.AppKeybindingInterrupt)))
		}
	}
	m.setHiddenThinkingLabel("")
}

// bindCurrentSessionExtensions binds extension handlers to the session (pi:
// bindCurrentSessionExtensions).
func (m *InteractiveMode) bindCurrentSessionExtensions() error {
	uiContext := m.createExtensionUIContext()
	if err := m.session().BindExtensions(context.Background(), &core.ExtensionBindings{
		UIContext: uiContext,
		Mode:      core.ExtensionModeTUI,
		AbortHandler: func() {
			m.restoreQueuedMessagesToEditor(true)
		},
		CommandContextActions: &core.ExtensionCommandContextActions{
			WaitForIdle: func(ctx context.Context) error {
				return m.session().WaitForIdle(ctx)
			},
			NewSession: func(ctx context.Context, options *core.NewSessionCommandOptions) (bool, error) {
				m.clearStatusIndicator("")
				var runtimeOptions core.RuntimeNewSessionOptions
				if options != nil {
					runtimeOptions.ParentSession = options.ParentSession
					if options.Setup != nil {
						runtimeOptions.Setup = func(sessionManager *core.SessionManager) error {
							return options.Setup(ctx, sessionManager)
						}
					}
					if options.WithSession != nil {
						runtimeOptions.WithSession = func(sessionCtx *core.ReplacedSessionContext) error {
							return options.WithSession(ctx, sessionCtx)
						}
					}
				}
				cancelled, err := m.runtimeHost.NewSession(ctx, &runtimeOptions)
				if err != nil {
					return false, m.handleFatalRuntimeError("Failed to create session", err)
				}
				return cancelled, nil
			},
			Fork: func(ctx context.Context, entryID string, options *core.ForkCommandOptions) (bool, error) {
				var forkOptions core.ForkOptions
				if options != nil && options.WithSession != nil {
					forkOptions.WithSession = func(replacedCtx *core.ReplacedSessionContext) error {
						return options.WithSession(ctx, replacedCtx)
					}
				}
				result, err := m.runtimeHost.Fork(ctx, entryID, &forkOptions)
				if err != nil {
					return false, m.handleFatalRuntimeError("Failed to fork", err)
				}
				if !result.Cancelled {
					m.editor.SetText(result.SelectedText)
					m.showStatus("Forked to new session")
				}
				return result.Cancelled, nil
			},
			NavigateTree: func(ctx context.Context, targetID string, options *core.NavigateTreeCommandOptions) (bool, error) {
				result, err := m.session().NavigateTree(ctx, targetID, &core.NavigateTreeOptions{
					Summarize:           options.Summarize,
					CustomInstructions:  options.CustomInstructions,
					ReplaceInstructions: options.ReplaceInstructions,
					Label:               options.Label,
				})
				if err != nil {
					return false, err
				}
				if result.Cancelled {
					return true, nil
				}
				m.chatContainer.Clear()
				m.renderInitialMessages()
				if result.EditorText != nil && strings.TrimSpace(m.editor.GetText()) == "" {
					m.editor.SetText(*result.EditorText)
				}
				m.showStatus("Navigated to selected point")
				go m.flushCompactionQueue(context.Background(), false)
				return false, nil
			},
			SwitchSession: func(ctx context.Context, sessionPath string, options *core.SwitchSessionCommandOptions) (bool, error) {
				return m.handleResumeSession(ctx, sessionPath, options)
			},
			Reload: func(ctx context.Context) error {
				return m.handleReloadCommand(ctx)
			},
		},
		ShutdownHandler: func() {
			m.shutdownRequested = true
			if m.session().IsIdle() {
				m.spawnShutdown(false)
			}
		},
		OnError: func(err core.ExtensionError) {
			m.showExtensionError(err.ExtensionPath, err.Error, err.Stack)
		},
	}); err != nil {
		return err
	}

	m.setupAutocompleteProvider()
	m.setupExtensionShortcuts(m.session().GetExtensionRunner())
	m.showLoadedResources(&showLoadedResourcesOptions{Force: false, ShowDiagnosticsWhenQuiet: true})
	m.showStartupNoticesIfNeeded()
	return nil
}

// setupExtensionShortcuts wires extension-registered keyboard shortcuts onto
// the default editor (pi: setupExtensionShortcuts).
func (m *InteractiveMode) setupExtensionShortcuts(extensionRunner *core.ExtensionRunner) {
	shortcuts := extensionRunner.GetShortcuts(m.keybindings.GetEffectiveConfig())
	if len(shortcuts) == 0 {
		return
	}

	// Create a context for shortcut handlers
	createContext := func() *core.ExtensionContext {
		session := m.session()
		return &core.ExtensionContext{
			UI:               m.createExtensionUIContext(),
			Mode:             core.ExtensionModeTUI,
			HasUI:            true,
			Cwd:              m.sessionManager().GetCwd(),
			SessionManager:   m.sessionManager(),
			ModelRegistry:    extensionRunner.GetModelRegistry(),
			Model:            session.Model(),
			ScopedModels:     session.ScopedModels(),
			ThinkingLevel:    session.ThinkingLevel(),
			IsIdle:           func() bool { return session.IsIdle() },
			IsProjectTrusted: func() bool { return m.settingsManager().IsProjectTrusted() },
			Signal:           session.Agent.Signal,
			Abort: func() {
				m.restoreQueuedMessagesToEditor(true)
			},
			HasPendingMessages: func() bool { return session.PendingMessageCount() > 0 },
			Shutdown: func() {
				m.shutdownRequested = true
			},
			GetContextUsage: func() *core.ContextUsage { return session.GetContextUsage() },
			Compact: func(options *core.CompactOptions) {
				go func() {
					// pig panic containment (see docs/feat/PANIC-RESILIENCE.md).
					defer pigutils.RecoverPanic("interactive.compact")
					var customInstructions string
					if options != nil {
						customInstructions = options.CustomInstructions
					}
					result, err := session.Compact(context.Background(), customInstructions)
					if err != nil {
						if options != nil && options.OnError != nil {
							options.OnError(err)
						}
						return
					}
					if options != nil && options.OnComplete != nil {
						options.OnComplete(result)
					}
				}()
			},
			GetSystemPrompt: func() string { return session.SystemPrompt() },
		}
	}

	// Set up the extension shortcut handler on the default editor
	m.defaultEditor.OnExtensionShortcut = func(data string) bool {
		for key, shortcut := range shortcuts {
			// Extension shortcuts use the same format as keybinding ids
			if tui.MatchesKey(data, key) {
				// Run handler async, don't block input
				go func(shortcut *core.ExtensionShortcut) {
					defer func() {
						if r := recover(); r != nil {
							m.ShowError(fmt.Sprintf("Shortcut handler error: %v", r))
						}
					}()
					shortcut.Handler(context.Background(), createContext())
				}(shortcut)
				return true
			}
		}
		return false
	}
}

// applyFullscreenScrollbarSetting propagates the scrollbar setting (pi:
// applyFullscreenScrollbarSetting).
func (m *InteractiveMode) applyFullscreenScrollbarSetting() {
	if m.transcriptScrollView != nil {
		m.transcriptScrollView.SetScrollbar(m.settingsManager().GetFullscreenScrollbar())
	}
}

// applyRuntimeSettings applies the settings that take effect at runtime (pi:
// applyRuntimeSettings).
func (m *InteractiveMode) applyRuntimeSettings() {
	// pi configures the http dispatcher here (configureHttpDispatcher); pig's
	// net/http transport needs no dispatcher swap (declared in
	// core/http_dispatcher.go), so only the idle timeout parse runs.
	// v0.85.1: terminal capability overrides apply at runtime too (settings
	// changes), and fullscreen copy-on-select syncs to the alt-screen
	// renderer.
	tui.SetCapabilityOverrides(m.settingsManager().GetTerminalCapabilityOverrides())
	m.applyFullscreenScrollbarSetting()
	if altScreen, ok := m.renderer.(*tui.TuiAltScreen); ok {
		altScreen.SetCopyOnSelect(m.settingsManager().GetFullscreenCopyOnSelect())
	}
	m.footer.SetSession(m.session())
	m.footer.SetAutoCompactEnabled(m.session().AutoCompactionEnabled())
	m.footerDataProviderView.SetCwd(m.sessionManager().GetCwd())
	m.hideThinkingBlock = m.settingsManager().GetHideThinkingBlock()
	m.outputPad = m.settingsManager().GetOutputPad()
	m.ui.SetShowHardwareCursor(m.settingsManager().GetShowHardwareCursor())
	clearOnShrink := m.settingsManager().GetClearOnShrink()
	m.ui.SetClearOnShrink(clearOnShrink)
	if !clearOnShrink && m.activeStatusIndicator == nil {
		m.statusContainer.Clear()
	}
	editorPaddingX := m.settingsManager().GetEditorPaddingX()
	autocompleteMaxVisible := m.settingsManager().GetAutocompleteMaxVisible()
	m.defaultEditor.SetPaddingX(int(editorPaddingX))
	m.defaultEditor.SetAutocompleteMaxVisible(int(autocompleteMaxVisible))
	if m.editor != tui.EditorComponent(m.defaultEditor) {
		if e, ok := m.editor.(tui.EditorComponentAppearance); ok {
			e.SetPaddingX(int(editorPaddingX))
			e.SetAutocompleteMaxVisible(int(autocompleteMaxVisible))
		}
	}
}

// handleReloadCommand reloads keybindings, extensions, skills, prompts,
// themes, and context files (pi: handleReloadCommand).
func (m *InteractiveMode) handleReloadCommand(ctx context.Context) error {
	if m.session().IsStreaming() {
		m.ShowWarning("Wait for the current response to finish before reloading.")
		return nil
	}
	if m.session().IsCompacting() {
		m.ShowWarning("Wait for compaction to finish before reloading.")
		return nil
	}

	m.resetExtensionUI()

	currentTheme := theme.Current()
	reloadBox := &tui.Container{}
	borderColor := func(s string) string { return currentTheme.Fg("border", s) }
	reloadBox.AddChild(components.NewDynamicBorder(borderColor))
	reloadBox.AddChild(tui.NewSpacer(1))
	reloadBox.AddChild(tui.NewText(
		currentTheme.Fg("muted", "Reloading keybindings, extensions, skills, prompts, themes, and context files..."), 1, 0))
	reloadBox.AddChild(tui.NewSpacer(1))
	reloadBox.AddChild(components.NewDynamicBorder(borderColor))

	previousEditor := m.editor
	m.editorContainer.Clear()
	m.editorContainer.AddChild(reloadBox)
	m.ui.SetFocus(reloadBox)
	m.ui.RequestRender(true)
	runtime.Gosched() // pi: process.nextTick

	dismissReloadBox := func(editor tui.EditorComponent) {
		m.editorContainer.Clear()
		m.editorContainer.AddChild(editor)
		m.ui.SetFocus(editor)
		m.ui.RequestRender(false)
	}

	chatRestoredBeforeSessionStart := false
	reloadBoxDismissed := false
	restoreChatBeforeSessionStart := func() {
		if chatRestoredBeforeSessionStart {
			return
		}
		m.hideThinkingBlock = m.settingsManager().GetHideThinkingBlock()
		m.outputPad = m.settingsManager().GetOutputPad()
		m.rebuildChatFromMessages()
		chatRestoredBeforeSessionStart = true
	}

	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%v", r)
			}
		}()
		return m.session().Reload(ctx, &core.ReloadOptions{BeforeSessionStart: func(_ context.Context) error {
			restoreChatBeforeSessionStart()
			return nil
		}})
	}()
	if err == nil {
		restoreChatBeforeSessionStart()
		m.keybindings.Reload()
		activeHeader := m.customHeader
		if activeHeader == nil {
			activeHeader = m.builtInHeader
		}
		if e := asExpandable(activeHeader); e != nil {
			e.SetExpanded(m.toolOutputExpanded)
		}
		// v0.85.1 swaps the order: applyRuntimeSettings (which installs the
		// terminal capability overrides) runs before the theme is applied so
		// theme-dependent rendering sees the final capabilities.
		m.applyRuntimeSettings()
		if err := m.themeController.ApplyFromSettings(); err != nil {
			return err
		}
		m.setupAutocompleteProvider()
		runner := m.session().GetExtensionRunner()
		m.setupExtensionShortcuts(runner)
		m.showLoadedResources(&showLoadedResourcesOptions{Force: false, ShowDiagnosticsWhenQuiet: true})
		savedImplicitProjectTrust := m.maybeSaveImplicitProjectTrustAfterReload()
		modelsJsonError := m.session().GetModelRuntime().GetError()
		if modelsJsonError != "" {
			m.ShowError(fmt.Sprintf("models.json error: %s", modelsJsonError))
		}
		if savedImplicitProjectTrust {
			m.showStatus("Reloaded keybindings, extensions, skills, prompts, themes, and context files; saved project trust")
		} else {
			m.showStatus("Reloaded keybindings, extensions, skills, prompts, themes, and context files")
		}
		dismissReloadBox(m.editor)
		reloadBoxDismissed = true
	}
	if err != nil {
		if !reloadBoxDismissed {
			dismissReloadBox(previousEditor)
		}
		m.ShowError(fmt.Sprintf("Reload failed: %s", errorMessageOf(err)))
	}
	return nil
}

// maybeSaveImplicitProjectTrustAfterReload persists implicitly-granted trust
// after a reload (pi: maybeSaveImplicitProjectTrustAfterReload).
func (m *InteractiveMode) maybeSaveImplicitProjectTrustAfterReload() bool {
	cwd := m.sessionManager().GetCwd()
	if m.autoTrustOnReloadCwd != cwd {
		return false
	}
	if !m.settingsManager().IsProjectTrusted() || !core.HasTrustRequiringProjectResources(cwd) {
		return false
	}

	trustStore := core.NewProjectTrustStore(m.runtimeHost.Services().AgentDir)
	decision, err := trustStore.Get(cwd)
	if err == nil && decision != nil {
		m.autoTrustOnReloadCwd = ""
		return false
	}
	if err := trustStore.Set(cwd, lo.ToPtr(true)); err != nil {
		m.ShowWarning(fmt.Sprintf("Could not save project trust after reload: %s", errorMessageOf(err)))
		return false
	}
	m.autoTrustOnReloadCwd = ""
	return true
}

// handleResumeSession resumes a session file, resolving missing-cwd and
// trust UI (pi: handleResumeSession).
func (m *InteractiveMode) handleResumeSession(ctx context.Context, sessionPath string,
	options *core.SwitchSessionCommandOptions) (bool, error) {
	m.clearStatusIndicator("")
	switchOptions := &core.SwitchSessionOptions{
		WithSession: func(replacedCtx *core.ReplacedSessionContext) error {
			if options == nil || options.WithSession == nil {
				return nil
			}
			return options.WithSession(ctx, replacedCtx)
		},
		ProjectTrustContextFactory: func(cwd string) any { return m.createProjectTrustContext(cwd) },
	}
	cancelled, err := m.runtimeHost.SwitchSession(ctx, sessionPath, switchOptions)
	if err != nil {
		var missing *core.MissingSessionCwdError
		if errors.As(err, &missing) {
			selectedCwd, ok := m.promptForMissingSessionCwd(err)
			if !ok {
				m.showStatus("Resume cancelled")
				return true, nil
			}
			switchOptions.CwdOverride = &selectedCwd
			cancelled, err := m.runtimeHost.SwitchSession(ctx, sessionPath, switchOptions)
			if err != nil {
				return false, m.handleFatalRuntimeError("Failed to resume session", err)
			}
			if cancelled {
				return true, nil
			}
			m.showStatus("Resumed session in current cwd")
			return false, nil
		}
		_ = missing
		return false, m.handleFatalRuntimeError("Failed to resume session", err)
	}
	if cancelled {
		return true, nil
	}
	m.showStatus("Resumed session")
	return false, nil
}

// handleFatalRuntimeError reports a fatal runtime error and exits (pi:
// handleFatalRuntimeError). pi stops the theme watcher first; pig's theme
// watcher is an in-process goroutine that dies with the process (declared).
func (m *InteractiveMode) handleFatalRuntimeError(prefix string, err error) error {
	message := errorMessageOf(err)
	m.ShowError(fmt.Sprintf("%s: %s", prefix, message))
	m.stopInteractive()
	os.Exit(1)
	return fmt.Errorf("unreachable")
}

// rebindCurrentSession rebinds UI subscriptions after a session replacement
// (pi: rebindCurrentSession).
func (m *InteractiveMode) rebindCurrentSession(renderBeforeBind bool) error {
	session := m.session()

	if m.unsubscribe != nil {
		m.unsubscribe()
		m.unsubscribe = nil
	}

	if renderBeforeBind {
		m.renderCurrentSessionState()
		m.subscribeToAgent()
	}

	if err := m.bindCurrentSessionExtensions(); err != nil {
		return err
	}

	if m.session() != session {
		return nil
	}

	if !renderBeforeBind {
		m.subscribeToAgent()
	}

	m.updateAvailableProviderCount()
	m.updateEditorBorderColor()
	m.updateTerminalTitle()
	return nil
}

// renderCurrentSessionState rebuilds the transcript after a session swap
// (pi: renderCurrentSessionState).
func (m *InteractiveMode) renderCurrentSessionState() {
	m.loadedResourcesContainer.Clear()
	m.chatContainer.Clear()
	m.pendingMessagesContainer.Clear()
	m.compactionQueuedMessages = nil
	m.streamingComponent = nil
	m.streamingMessage = nil
	m.pendingTools = map[string]*components.ToolExecutionComponent{}
	m.renderInitialMessages()
}

// =========================================================================
// Clipboard / external editor / suspend (pi: handleRightClickPaste,
// handleClipboardPaste, handleCtrlZ, handleOpenExternalEditor)
// =========================================================================

// handleRightClickPaste pastes clipboard text into the focused component
// (pi: handleRightClickPaste; unreachable until pig's TuiMainScreen grows a
// right-click hook — declared).
func (m *InteractiveMode) handleRightClickPaste() {
	// pi resolves the focused component and replays handleInput; pig's TUI
	// does not expose right-click yet. Text goes to the editor when it is
	// still focused.
	text := utils.ReadClipboardText()
	if text == "" {
		return
	}
	m.editor.HandleInput(fmt.Sprintf("\x1b[200~%s\x1b[201~", text))
	m.ui.RequestRender(false)
}

// handleClipboardPaste pastes a clipboard image (attached by path) or text
// at the cursor (pi: handleClipboardPaste).
func (m *InteractiveMode) handleClipboardPaste() {
	image := utils.ReadClipboardImage(nil)
	if image != nil {
		ext := utils.ExtensionForImageMimeType(image.MimeType)
		if ext == "" {
			ext = "png"
		}
		fileName := fmt.Sprintf("pi-clipboard-%s.%s", randomUUID(), ext)
		filePath := filepath.Join(os.TempDir(), fileName)
		if err := os.WriteFile(filePath, image.Bytes, 0o644); err != nil {
			return
		}

		m.editorInsertTextAtCursor(filePath)
		m.ui.RequestRender(false)
		return
	}

	text := utils.ReadClipboardText()
	if text != "" {
		m.editorInsertTextAtCursor(text)
		m.ui.RequestRender(false)
	}
}

// randomUUID is a UUIDv4 string (pi: crypto.randomUUID).
func randomUUID() string {
	b := make([]byte, 16)
	_, _ = crand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// handleCtrlZ suspends the process (pi: handleCtrlZ).
func (m *InteractiveMode) handleCtrlZ() {
	if isWindows() {
		m.showStatus("Suspend to background is not supported on Windows")
		return
	}

	// Stop the TUI (restore terminal to normal mode), then signal the
	// process group; SIGCONT restarts the TUI (pig: pi's keep-alive timer
	// and SIGINT ignore have no Go event-loop equivalent — signal delivery
	// is independent of timers).
	m.ui.Stop(nil)
	if err := syscall.Kill(0, syscall.SIGTSTP); err != nil {
		m.ui.Start()
		return
	}
	cont := make(chan os.Signal, 1)
	signal.Notify(cont, syscall.SIGCONT)
	go func() {
		// pig panic containment (see docs/feat/PANIC-RESILIENCE.md).
		defer pigutils.RecoverPanic("interactive.sigcont")
		<-cont
		signal.Stop(cont)
		m.ui.Start()
		m.ui.RequestRender(true)
	}()
}

// handleOpenExternalEditor edits the editor text in the external editor (pi:
// handleOpenExternalEditor).
func (m *InteractiveMode) handleOpenExternalEditor(ctx context.Context) {
	editorCmd := m.settingsManager().GetExternalEditorCommand()
	content := m.editorGetExpandedText()
	m.ui.Stop(nil)
	defer func() {
		m.ui.Start()
		m.ui.RequestRender(true)
	}()
	result, err := utils.EditInExternalEditor(ctx, editorCmd, content)
	if err != nil {
		return
	}
	if result.Status == "complete" {
		m.editor.SetText(result.Content)
	}
}

// =========================================================================
// Update notifications (pi: showNewVersionNotification /
// showPackageUpdateNotification)
// =========================================================================

// showNewVersionNotification renders the update banner (pi:
// showNewVersionNotification).
func (m *InteractiveMode) showNewVersionNotification(release *utils.LatestPiRelease) {
	t := theme.Current()
	action := t.Fg("accent", codingagent.AppName+" update")
	updateInstruction := t.Fg("muted", fmt.Sprintf("New version %s is available. Run ", release.Version)) + action
	changelogURL := "https://pi.dev/changelog"
	var changelogLink string
	if tui.GetCapabilities().Hyperlinks {
		changelogLink = tui.Hyperlink(t.Fg("accent", changelogURL), changelogURL)
	} else {
		changelogLink = t.Fg("accent", changelogURL)
	}
	changelogLine := t.Fg("muted", "Changelog: ") + changelogLink
	note := strings.TrimSpace(release.Note)

	m.chatContainer.AddChild(tui.NewSpacer(1))
	m.chatContainer.AddChild(components.NewDynamicBorder(func(text string) string { return t.Fg("warning", text) }))
	m.chatContainer.AddChild(tui.NewText(
		fmt.Sprintf("%s\n%s", t.Bold(t.Fg("warning", "Update Available")), updateInstruction), 1, 0))
	if note != "" {
		m.chatContainer.AddChild(tui.NewSpacer(1))
		m.chatContainer.AddChild(tui.NewMarkdown(note, 1, 0, m.getMarkdownThemeWithSettings(),
			&tui.DefaultTextStyle{Color: func(text string) string { return t.Fg("muted", text) }}, nil))
		m.chatContainer.AddChild(tui.NewSpacer(1))
	}
	m.chatContainer.AddChild(tui.NewText(changelogLine, 1, 0))
	m.chatContainer.AddChild(components.NewDynamicBorder(func(text string) string { return t.Fg("warning", text) }))
	m.ui.RequestRender(false)
}
