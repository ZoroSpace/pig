// custom_editor.go (pi: components/custom-editor.ts) — custom editor that
// handles app-level keybindings for coding-agent.
package components

import (
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/tui"
)

// CustomEditor embeds the tui Editor and intercepts app keybindings before
// editor handling (pi: class CustomEditor extends Editor).
type CustomEditor struct {
	*tui.Editor

	keybindings *core.KeybindingsManager
	// actionHandlers maps app actions to handlers (pi: actionHandlers).
	actionHandlers map[core.AppKeybinding]func()

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
func NewCustomEditor(tui_ tui.TUI, editorTheme tui.EditorTheme, keybindings *core.KeybindingsManager, options *tui.EditorOptions) *CustomEditor {
	e := &CustomEditor{
		Editor:         tui.NewEditor(tui_, editorTheme, options),
		keybindings:    keybindings,
		actionHandlers: map[core.AppKeybinding]func(){},
	}
	return e
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
