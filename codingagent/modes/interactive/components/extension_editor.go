// extension_editor.go (pi: components/extension-editor.ts) — multi-line
// editor component for extensions. Supports Ctrl+G for external editor.
package components

import (
	"context"
	"os"
	"runtime"

	core "github.com/ZoroSpace/pig/codingagent/core"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// ExtensionEditorComponent is a multi-line editor dialog for extensions (pi:
// ExtensionEditorComponent implements Focusable via SetFocused).
type ExtensionEditorComponent struct {
	tui.Container

	editor                *tui.Editor
	onSubmitCallback      func(value string)
	onCancelCallback      func()
	tui                   tui.TUI
	keybindings           *core.KeybindingsManager
	externalEditorCommand string

	focused bool
}

// ExtensionEditorOptions carries ExtensionEditorComponent-specific options
// (pi: ExtensionEditorOptions extends EditorOptions with description).
type ExtensionEditorOptions struct {
	*tui.EditorOptions
	Description string
}

// NewExtensionEditorComponent builds the editor (pi: constructor). prefill ""
// = pi's undefined; externalEditorCommand "" falls back to VISUAL/EDITOR or
// notepad/nano per platform.
func NewExtensionEditorComponent(
	tui_ tui.TUI,
	keybindings *core.KeybindingsManager,
	title string,
	prefill string,
	onSubmit func(value string),
	onCancel func(),
	options *ExtensionEditorOptions,
	externalEditorCommand string,
) *ExtensionEditorComponent {
	c := &ExtensionEditorComponent{
		onSubmitCallback:      onSubmit,
		onCancelCallback:      onCancel,
		tui:                   tui_,
		keybindings:           keybindings,
		externalEditorCommand: externalEditorCommand,
	}
	if c.externalEditorCommand == "" {
		c.externalEditorCommand = os.Getenv("VISUAL")
		if c.externalEditorCommand == "" {
			c.externalEditorCommand = os.Getenv("EDITOR")
		}
		if c.externalEditorCommand == "" {
			if runtime.GOOS == "windows" {
				c.externalEditorCommand = "notepad"
			} else {
				c.externalEditorCommand = "nano"
			}
		}
	}
	var editorOptions *tui.EditorOptions
	description := ""
	if options != nil {
		editorOptions = options.EditorOptions
		description = options.Description
	}
	currentTheme := theme.Current()

	// Add top border
	c.AddChild(NewDynamicBorder(nil))
	c.AddChild(tui.NewSpacer(1))

	// Add title and optional description
	c.AddChild(tui.NewText(currentTheme.Fg("accent", title), 1, 0))
	if description != "" {
		c.AddChild(tui.NewSpacer(1))
		c.AddChild(tui.NewText(currentTheme.Fg("text", description), 1, 0))
	}
	c.AddChild(tui.NewSpacer(1))

	// Create editor
	c.editor = tui.NewEditor(tui_, theme.GetEditorTheme(), editorOptions)
	if prefill != "" {
		c.editor.SetText(prefill)
	}
	// Wire up Enter to submit (Shift+Enter for newlines, like the main editor)
	c.editor.OnSubmit = func(text string) {
		c.onSubmitCallback(text)
	}
	c.AddChild(c.editor)

	c.AddChild(tui.NewSpacer(1))

	// Add hint
	hint := coretools.KeyHint("tui.select.confirm", "submit") +
		"  " +
		coretools.KeyHint("tui.input.newLine", "newline") +
		"  " +
		coretools.KeyHint("tui.select.cancel", "cancel") +
		"  " + coretools.KeyHint("app.editor.external", "external editor")
	c.AddChild(tui.NewText(hint, 1, 0))

	c.AddChild(tui.NewSpacer(1))

	// Add bottom border
	c.AddChild(NewDynamicBorder(nil))

	return c
}

// SetFocused is called by the TUI when focus changes (pi: focused
// accessor).
func (c *ExtensionEditorComponent) SetFocused(value bool) {
	c.focused = value
	c.editor.Focused = value
}

// HandleInput processes keys: cancel, external editor, then editor handling
// (pi: handleInput).
func (c *ExtensionEditorComponent) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	// Escape or Ctrl+C to cancel
	if kb.Matches(keyData, "tui.select.cancel") {
		c.onCancelCallback()
		return
	}

	// External editor (app keybinding)
	if c.keybindings.Matches(keyData, core.AppKeybindingEditorExternal) {
		c.handleOpenExternalEditor()
		return
	}

	// Forward to editor
	c.editor.HandleInput(keyData)
}

// handleOpenExternalEditor edits the content in an external editor (pi:
// handleOpenExternalEditor — pi's async void is synchronous here; the TUI is
// stopped for the duration either way).
func (c *ExtensionEditorComponent) handleOpenExternalEditor() {
	content := c.editor.GetText()
	c.tui.Stop(&tui.TuiStopOptions{})
	defer func() {
		c.tui.Start()
		c.tui.RequestRender(true)
	}()
	result, err := utils.EditInExternalEditor(context.Background(), c.externalEditorCommand, content)
	if err != nil {
		return
	}
	if result.Status == "complete" {
		c.editor.SetText(result.Content)
	}
}
