// tui/keybindings_test.go — ported from pi: packages/tui/test/keybindings.test.ts

package tui

import (
	"reflect"
	"testing"
)

func TestKeybindingsCtrlJNewlineAlias(t *testing.T) {
	keybindings := NewKeybindingsManager(TUIKeyBindings, nil)

	if got := keybindings.GetKeys("tui.input.newLine"); !reflect.DeepEqual(got, []KeyId{"shift+enter", "ctrl+j"}) {
		t.Errorf("GetKeys(newLine) = %v, want [shift+enter ctrl+j]", got)
	}
	if !keybindings.Matches("\n", "tui.input.newLine") {
		t.Error("Matches(\"\\n\", newLine) = false, want true")
	}
	if !keybindings.Matches("\x1b[106;5u", "tui.input.newLine") {
		t.Error("Matches(csi-u ctrl+j, newLine) = false, want true")
	}
}

func TestKeybindingsModifiedAndUnmodifiedViewportNavigation(t *testing.T) {
	keybindings := NewKeybindingsManager(TUIKeyBindings, nil)

	if got := keybindings.GetKeys("tui.editor.cursorLineStart"); !reflect.DeepEqual(got, []KeyId{"home", "ctrl+a"}) {
		t.Errorf("GetKeys(cursorLineStart) = %v", got)
	}
	if got := keybindings.GetKeys("tui.editor.cursorLineEnd"); !reflect.DeepEqual(got, []KeyId{"end", "ctrl+e"}) {
		t.Errorf("GetKeys(cursorLineEnd) = %v", got)
	}
	if got := keybindings.GetKeys("tui.editor.pageUp"); !reflect.DeepEqual(got, []KeyId{"pageUp", "ctrl+pageUp"}) {
		t.Errorf("GetKeys(pageUp) = %v", got)
	}
	if got := keybindings.GetKeys("tui.editor.pageDown"); !reflect.DeepEqual(got, []KeyId{"pageDown", "ctrl+pageDown"}) {
		t.Errorf("GetKeys(pageDown) = %v", got)
	}
}

func TestKeybindingsDedicatedHistoryUnboundByDefault(t *testing.T) {
	keybindings := NewKeybindingsManager(TUIKeyBindings, nil)

	if got := keybindings.GetKeys("tui.editor.historyPrevious"); len(got) != 0 {
		t.Errorf("GetKeys(historyPrevious) = %v, want empty", got)
	}
	if got := keybindings.GetKeys("tui.editor.historyNext"); len(got) != 0 {
		t.Errorf("GetKeys(historyNext) = %v, want empty", got)
	}
}

func TestKeybindingsUnmodifiedViewportShortcutsBindAltScreen(t *testing.T) {
	keybindings := NewKeybindingsManager(TUIKeyBindings, nil)

	expect := map[string][]KeyId{
		"tui.altScreen.pageUp":         {"pageUp"},
		"tui.altScreen.pageDown":       {"pageDown"},
		"tui.altScreen.halfPageUp":     {},
		"tui.altScreen.halfPageDown":   {},
		"tui.altScreen.lineUp":         {},
		"tui.altScreen.lineDown":       {},
		"tui.altScreen.previousPrompt": {"ctrl+shift+up", "ctrl+up"},
		"tui.altScreen.nextPrompt":     {"ctrl+shift+down", "ctrl+down"},
		"tui.altScreen.search":         {"ctrl+shift+f"},
		"tui.altScreen.searchNext":     {"enter", "ctrl+g"},
		"tui.altScreen.searchPrevious": {"shift+enter", "ctrl+shift+g"},
		"tui.altScreen.searchClose":    {"escape"},
		"tui.altScreen.top":            {"ctrl+home"},
		"tui.altScreen.bottom":         {"ctrl+end"},
	}
	for id, want := range expect {
		if got := keybindings.GetKeys(id); !reflect.DeepEqual(got, want) {
			t.Errorf("GetKeys(%s) = %v, want %v", id, got, want)
		}
	}
}

func TestKeybindingsSelectorConfirmNotEvictedBySubmitRebind(t *testing.T) {
	keybindings := NewKeybindingsManager(TUIKeyBindings, KeybindingsConfig{
		"tui.input.submit": {"enter", "ctrl+enter"},
	})

	if got := keybindings.GetKeys("tui.input.submit"); !reflect.DeepEqual(got, []KeyId{"enter", "ctrl+enter"}) {
		t.Errorf("GetKeys(submit) = %v", got)
	}
	if got := keybindings.GetKeys("tui.select.confirm"); !reflect.DeepEqual(got, []KeyId{"enter"}) {
		t.Errorf("GetKeys(select.confirm) = %v", got)
	}
}

func TestKeybindingsCursorBindingsNotEvictedByKeyReuse(t *testing.T) {
	keybindings := NewKeybindingsManager(TUIKeyBindings, KeybindingsConfig{
		"tui.select.up": {"up", "ctrl+p"},
	})

	if got := keybindings.GetKeys("tui.select.up"); !reflect.DeepEqual(got, []KeyId{"up", "ctrl+p"}) {
		t.Errorf("GetKeys(select.up) = %v", got)
	}
	if got := keybindings.GetKeys("tui.editor.cursorUp"); !reflect.DeepEqual(got, []KeyId{"up"}) {
		t.Errorf("GetKeys(editor.cursorUp) = %v", got)
	}
}

func TestKeybindingsDirectUserConflictsReportedWithoutEvictingDefaults(t *testing.T) {
	keybindings := NewKeybindingsManager(TUIKeyBindings, KeybindingsConfig{
		"tui.input.submit":   {"ctrl+x"},
		"tui.select.confirm": {"ctrl+x"},
	})

	conflicts := keybindings.GetConflicts()
	if len(conflicts) != 1 {
		t.Fatalf("GetConflicts() = %v, want 1 conflict", conflicts)
	}
	if conflicts[0].Key != "ctrl+x" {
		t.Errorf("conflict key = %q, want ctrl+x", conflicts[0].Key)
	}
	// pi asserts insertion order [tui.input.submit, tui.select.confirm];
	// pig resolves claiming order alphabetically (declared deviation),
	// which yields the same order here.
	if !reflect.DeepEqual(conflicts[0].Keybindings, []string{"tui.input.submit", "tui.select.confirm"}) {
		t.Errorf("conflict keybindings = %v", conflicts[0].Keybindings)
	}
	if got := keybindings.GetKeys("tui.editor.cursorLeft"); !reflect.DeepEqual(got, []KeyId{"left", "ctrl+b"}) {
		t.Errorf("GetKeys(editor.cursorLeft) = %v", got)
	}
}
