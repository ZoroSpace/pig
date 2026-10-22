// interactive_mode_extension_dialogs_test.go — pi:
// test/interactive-mode-extension-dialogs.test.ts (#11166).
package interactive

import (
	"testing"

	"github.com/ZoroSpace/pig/tui"
)

type dialogTestComponent struct {
	tui.Container
	label      string
	disposed   bool
	disposeHit *int
}

func (c *dialogTestComponent) Render(int) []string { return []string{c.label} }
func (c *dialogTestComponent) GetText() string     { return "" }
func (c *dialogTestComponent) SetText(string)      {}
func (c *dialogTestComponent) HandleInput(string)  {}

func (c *dialogTestComponent) Dispose() {
	c.disposed = true
	if c.disposeHit != nil {
		*c.disposeHit++
	}
}

func newDialogTestMode() (*InteractiveMode, *dialogTestComponent) {
	editor := &dialogTestComponent{label: "EDITOR"}
	container := &tui.Container{}
	container.AddChild(editor)
	ui := tui.NewTuiMainScreen(tui.NewVirtualTerminal(80, 24), nil, "")
	mode := &InteractiveMode{
		editor:          editor,
		editorContainer: container,
		ui:              ui,
	}
	return mode, editor
}

func TestExtensionDialogsStackInsteadOfReplacing(t *testing.T) {
	mode, editor := newDialogTestMode()
	first := &dialogTestComponent{label: "FIRST"}
	second := &dialogTestComponent{label: "SECOND"}

	mode.pushExtensionDialog(first, &blockedStatus{kind: tui.ProgramStatusBlockedPermission, message: "First"}, nil)
	mode.pushExtensionDialog(second, &blockedStatus{kind: tui.ProgramStatusBlockedPermission, message: "Second"}, nil)

	if len(mode.editorContainer.Children) != 1 || mode.editorContainer.Children[0] != second {
		t.Fatalf("visible dialog = %v, want second", mode.editorContainer.Children)
	}
	if len(mode.extensionDialogs) != 2 {
		t.Fatalf("dialog stack = %d, want 2", len(mode.extensionDialogs))
	}

	mode.removeExtensionDialog(second)
	if len(mode.editorContainer.Children) != 1 || mode.editorContainer.Children[0] != first {
		t.Fatalf("visible dialog after closing second = %v, want first", mode.editorContainer.Children)
	}

	mode.removeExtensionDialog(first)
	if len(mode.editorContainer.Children) != 1 || mode.editorContainer.Children[0] != editor {
		t.Fatalf("visible dialog after closing first = %v, want editor", mode.editorContainer.Children)
	}
}

func TestExtensionDialogsRemoveHiddenWithoutChangingVisible(t *testing.T) {
	mode, _ := newDialogTestMode()
	first := &dialogTestComponent{label: "FIRST"}
	second := &dialogTestComponent{label: "SECOND"}
	mode.pushExtensionDialog(first, nil, nil)
	mode.pushExtensionDialog(second, nil, nil)

	mode.removeExtensionDialog(first)
	if len(mode.editorContainer.Children) != 1 || mode.editorContainer.Children[0] != second {
		t.Fatalf("hidden removal changed visible dialog: %v", mode.editorContainer.Children)
	}
}
