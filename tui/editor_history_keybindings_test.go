// tui/editor_history_keybindings_test.go — ported from pi:
// packages/tui/test/editor-history-keybindings.test.ts

package tui

import (
	"testing"
)

func TestEditorPromptHistoryKeybindings(t *testing.T) {
	t.Run("browses history directly without first moving the cursor", func(t *testing.T) {
		SetKeybindings(NewKeybindingsManager(TUIKeyBindings, KeybindingsConfig{
			"tui.editor.historyPrevious": []KeyId{"ctrl+p"},
			"tui.editor.historyNext":     []KeyId{"ctrl+n"},
		}))
		defer SetKeybindings(NewKeybindingsManager(TUIKeyBindings, nil))

		editor := NewEditor(createTestTUI(80, 24), testEditorTheme(), nil)
		editor.AddToHistory("older prompt")
		editor.AddToHistory("newer\nmultiline prompt")
		editor.SetText("draft")
		editor.HandleInput("\x1b[D")
		editor.HandleInput("\x1b[D")

		editor.HandleInput("\x10") // Ctrl+P
		wantText(t, editor, "newer\nmultiline prompt")
		wantCursor(t, editor, 0, 0)

		editor.HandleInput("\x10") // Ctrl+P
		wantText(t, editor, "older prompt")

		editor.HandleInput("\x0e") // Ctrl+N
		wantText(t, editor, "newer\nmultiline prompt")
		wantCursor(t, editor, 1, 16)

		editor.HandleInput("\x0e") // Ctrl+N
		wantText(t, editor, "draft")
		wantCursor(t, editor, 0, 3)
	})
}
