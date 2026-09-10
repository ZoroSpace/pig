// custom_editor.go (pi: components/custom-editor.ts) — custom editor that
// handles app-level keybindings for coding-agent.
package components

import (
	"fmt"
	"strings"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/tui"
)

// CustomEditorOptions carries the editor options plus the working-status
// embed flag (pi: CustomEditorOptions = EditorOptions & { embedWorkingStatus
// ?: boolean }, v0.85.1).
type CustomEditorOptions struct {
	tui.EditorOptions
	// EmbedWorkingStatus renders the streaming working status in the editor's
	// top border instead of a standalone row (pi: embedWorkingStatus).
	EmbedWorkingStatus bool
}

// CustomEditor embeds the tui Editor and intercepts app keybindings before
// editor handling (pi: class CustomEditor extends Editor).
type CustomEditor struct {
	*tui.Editor

	keybindings *core.KeybindingsManager
	// actionHandlers maps app actions to handlers (pi: actionHandlers).
	actionHandlers map[core.AppKeybinding]func()

	workingStatusIndicator *WorkingStatusIndicator
	// EmbedWorkingStatus reports whether the working status is rendered in
	// the editor top border (pi: readonly embedWorkingStatus).
	EmbedWorkingStatus bool

	// Special handlers that can be dynamically replaced (pi: public fields).
	OnEscape func()
	OnCtrlD  func()
	// OnPasteImage handles the clipboard-paste keybinding.
	OnPasteImage func()
	// OnExtensionShortcut handles extension-registered shortcuts. Returns
	// true if handled.
	OnExtensionShortcut func(data string) bool
}

// NewCustomEditor builds the editor (pi: constructor).
func NewCustomEditor(tui_ tui.TUI, editorTheme tui.EditorTheme, keybindings *core.KeybindingsManager, options *CustomEditorOptions) *CustomEditor {
	e := &CustomEditor{
		Editor:         tui.NewEditor(tui_, editorTheme, unwrapEditorOptions(options)),
		keybindings:    keybindings,
		actionHandlers: map[core.AppKeybinding]func(){},
	}
	if options != nil {
		e.EmbedWorkingStatus = options.EmbedWorkingStatus
	}
	// pi overrides protected renderTopBorder; pig injects the override as a
	// callback seam (see tui.Editor.TopBorderOverride).
	e.Editor.TopBorderOverride = e.renderTopBorder
	return e
}

// unwrapEditorOptions flattens CustomEditorOptions into the tui options
// shape (pig helper; pi spreads the object).
func unwrapEditorOptions(options *CustomEditorOptions) *tui.EditorOptions {
	if options == nil {
		return nil
	}
	return &options.EditorOptions
}

// SetWorkingStatusIndicator attaches the streaming indicator to embed in the
// top border (pi: setWorkingStatusIndicator; nil = none).
func (e *CustomEditor) SetWorkingStatusIndicator(indicator *WorkingStatusIndicator) {
	e.workingStatusIndicator = indicator
}

// renderTopBorder renders the top border with the working status embedded
// (pi: protected renderTopBorder override, v0.85.1). Returns ok=false to
// fall through to the default border (the super call equivalent).
func (e *CustomEditor) renderTopBorder(width int, hiddenLineCount int) (string, bool) {
	if !e.EmbedWorkingStatus || e.workingStatusIndicator == nil || width <= 0 {
		return "", false
	}

	status := e.workingStatusIndicator.RenderInBorder(maxInt(1, width-5))
	statusWidth := tui.VisibleWidth(status)
	if statusWidth == 0 {
		return "", false
	}

	var overflowLabel string
	if hiddenLineCount > 0 {
		overflowLabel = fmt.Sprintf(" ↑ %d more ", hiddenLineCount)
	}
	overflowLabelWidth := tui.VisibleWidth(overflowLabel)
	overflowStart := (width - overflowLabelWidth) / 2
	canFitOverflow := func() bool {
		return overflowLabel != "" && overflowLabelWidth+2 <= width && overflowStart-(3+statusWidth+1) >= 1
	}

	if overflowLabel != "" && !canFitOverflow() {
		status = e.workingStatusIndicator.RenderSpinnerInBorder(width)
		statusWidth = tui.VisibleWidth(status)
	}

	if canFitOverflow() {
		leftBlockWidth := 3 + statusWidth + 1
		return e.BorderColor("── ") + status + e.BorderColor(
			" "+strings.Repeat("─", overflowStart-leftBlockWidth)+overflowLabel+strings.Repeat("─", width-overflowStart-overflowLabelWidth),
		), true
	}

	if width >= statusWidth+5 {
		return e.BorderColor("── ") + status + e.BorderColor(" "+strings.Repeat("─", width-statusWidth-4)), true
	}

	status = e.workingStatusIndicator.RenderSpinnerInBorder(width)
	statusWidth = tui.VisibleWidth(status)
	prefixWidth := minInt(3, maxInt(0, width-statusWidth))
	return e.BorderColor(strings.Repeat("─", prefixWidth)) +
		status +
		e.BorderColor(strings.Repeat("─", maxInt(0, width-prefixWidth-statusWidth))), true
}

// OnAction registers a handler for an app action (pi: onAction).
func (e *CustomEditor) OnAction(action core.AppKeybinding, handler func()) {
	e.actionHandlers[action] = handler
}

// CopyAppHandlersFrom copies all action handlers from another editor (pig
// addition for setCustomEditorComponent: pi copies the actionHandlers map
// via duck typing, which needs the same package here).
func (e *CustomEditor) CopyAppHandlersFrom(from *CustomEditor) {
	for action, handler := range from.actionHandlers {
		e.actionHandlers[action] = handler
	}
}

// HandleInput processes keys: extension shortcuts, paste, app keybindings,
// then editor handling (pi: handleInput).
func (e *CustomEditor) HandleInput(data string) {
	// Check extension-registered shortcuts first
	if e.OnExtensionShortcut != nil && e.OnExtensionShortcut(data) {
		return
	}

	// Check for clipboard paste keybinding
	if e.keybindings.Matches(data, core.AppKeybindingClipboardPasteImage) {
		if e.OnPasteImage != nil {
			e.OnPasteImage()
		}
		return
	}

	// Check app keybindings first

	// Escape/interrupt - only if autocomplete is NOT active
	if e.keybindings.Matches(data, core.AppKeybindingInterrupt) {
		if !e.IsShowingAutocomplete() {
			// Use dynamic onEscape if set, otherwise registered handler
			handler := e.OnEscape
			if handler == nil {
				handler = e.actionHandlers[core.AppKeybindingInterrupt]
			}
			if handler != nil {
				handler()
				return
			}
		}
		// Let parent handle escape for autocomplete cancellation
		e.Editor.HandleInput(data)
		return
	}

	// Exit (Ctrl+D) - only when editor is empty
	if e.keybindings.Matches(data, core.AppKeybindingExit) {
		if e.GetText() == "" {
			handler := e.OnCtrlD
			if handler == nil {
				handler = e.actionHandlers[core.AppKeybindingExit]
			}
			if handler != nil {
				handler()
			}
			return
		}
		// Fall through to editor handling for delete-char-forward when not
		// empty
	}

	// Explicit history bindings take precedence over app actions while the
	// editor is focused. This lets users bind Ctrl+P even though it cycles
	// models by default.
	if e.keybindings.Matches(data, "tui.editor.historyPrevious") ||
		e.keybindings.Matches(data, "tui.editor.historyNext") {
		e.Editor.HandleInput(data)
		return
	}

	// Check all other app actions
	for action, handler := range e.actionHandlers {
		if action != core.AppKeybindingInterrupt && action != core.AppKeybindingExit &&
			e.keybindings.Matches(data, action) {
			handler()
			return
		}
	}

	// Pass to parent for editor handling
	e.Editor.HandleInput(data)
}
