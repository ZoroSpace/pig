// tui/settings_list_test.go (pi: packages/tui/test/settings-list.test.ts)
package tui

import (
	"reflect"
	"strings"
	"testing"
)

func altSettingsTestTheme() SettingsListTheme {
	return SettingsListTheme{
		Label:       func(text string, selected bool) string { return text },
		Value:       func(text string, selected bool) string { return text },
		Description: func(text string) string { return text },
		Cursor:      "> ",
		Hint:        func(text string) string { return text },
	}
}

func altSettingsItems() []*SettingItem {
	return []*SettingItem{
		{
			Id:           "tui-mode",
			Label:        "TUI mode",
			CurrentValue: "regular",
			Values:       []string{"regular", "fullscreen"},
		},
	}
}

type altSettingsChange struct {
	id    string
	value string
}

func TestSettingsListIncludesSpacesInAnActiveSearchInsteadOfChangingTheSelectedSetting(t *testing.T) {
	var changes []altSettingsChange
	list := NewSettingsList(
		altSettingsItems(),
		10,
		altSettingsTestTheme(),
		func(id, value string) { changes = append(changes, altSettingsChange{id: id, value: value}) },
		func() {},
		&SettingsListOptions{EnableSearch: true},
	)

	for _, character := range "TUI mode" {
		list.HandleInput(string(character))
	}

	if len(changes) != 0 {
		t.Fatalf("changes = %v, want none", changes)
	}
	lines := list.Render(80)
	if len(lines) == 0 || !strings.Contains(lines[0], "TUI mode") {
		t.Fatalf("render = %q, want first line matching TUI mode", lines)
	}

	list.HandleInput("\r")
	want := []altSettingsChange{{id: "tui-mode", value: "fullscreen"}}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %v, want %v", changes, want)
	}
}

func TestSettingsListKeepsSpaceAsAChangeShortcutBeforeASearchQueryIsEntered(t *testing.T) {
	var changes []altSettingsChange
	list := NewSettingsList(
		altSettingsItems(),
		10,
		altSettingsTestTheme(),
		func(id, value string) { changes = append(changes, altSettingsChange{id: id, value: value}) },
		func() {},
		&SettingsListOptions{EnableSearch: true},
	)

	list.HandleInput(" ")

	want := []altSettingsChange{{id: "tui-mode", value: "fullscreen"}}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %v, want %v", changes, want)
	}
}
