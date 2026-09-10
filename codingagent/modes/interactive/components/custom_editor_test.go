// custom_editor_test.go (pi: test/custom-editor-history-keybindings.test.ts
// — "gives an explicit history binding precedence over model cycling").
package components

import (
	"testing"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/tui"
)

func TestCustomEditorHistoryKeybindingPrecedence(t *testing.T) {
	initTestTheme(t)

	keybindings := core.NewKeybindingsManager(tui.KeybindingsConfig{
		"tui.editor.historyPrevious": {"ctrl+p"},
		"tui.editor.historyNext":     {"ctrl+n"},
	}, "")
	tui.SetKeybindings(keybindings.KeybindingsManager)
	defer tui.SetKeybindings(core.CreateKeybindingsManager("").KeybindingsManager)

	editor := NewCustomEditor(nil, tui.EditorTheme{}, keybindings, nil)
	modelCycles := 0
	editor.OnAction(core.AppKeybindingModelCycleForward, func() { modelCycles++ })
	editor.AddToHistory("previous prompt")
	editor.SetText("draft")

	editor.HandleInput("\x10") // Ctrl+P
	if got := editor.GetText(); got != "previous prompt" {
		t.Fatalf("text = %q, want %q (history should win)", got, "previous prompt")
	}
	if modelCycles != 0 {
		t.Fatalf("model cycle handler fired %d times, want 0", modelCycles)
	}

	editor.HandleInput("\x0e") // Ctrl+N
	if got := editor.GetText(); got != "draft" {
		t.Fatalf("text = %q, want %q (back to draft)", got, "draft")
	}
}
