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

// TestSettingsListOnlyEnterChangesTheSetting locks the pig deviation
// (2026-09-29): Enter is the only change shortcut, in every space encoding.
// pi also lets a plain space change a value while the search query is empty;
// pig routes every space to the search box so the behaviour does not depend
// on whether a query happens to be active.
func TestSettingsListOnlyEnterChangesTheSetting(t *testing.T) {
	spaces := []struct{ name, data string }{
		{"legacy", " "},
		{"kitty-csi-u", "\x1b[32u"},
		{"modify-other-keys", "\x1b[27;1;32~"},
	}
	for _, tc := range spaces {
		t.Run(tc.name, func(t *testing.T) {
			var changes []altSettingsChange
			list := NewSettingsList(
				altSettingsItems(),
				10,
				altSettingsTestTheme(),
				func(id, value string) { changes = append(changes, altSettingsChange{id: id, value: value}) },
				func() {},
				&SettingsListOptions{EnableSearch: true},
			)

			list.HandleInput(tc.data)
			if len(changes) != 0 {
				t.Fatalf("space changed a setting: %v", changes)
			}

			list.HandleInput("\r")
			want := []altSettingsChange{{id: "tui-mode", value: "fullscreen"}}
			if !reflect.DeepEqual(changes, want) {
				t.Fatalf("changes after Enter = %v, want %v", changes, want)
			}
		})
	}
}

// TestSettingsListSpaceWithEmptyQueryKeepsSelection: since space is always
// search text now, pressing it on an unfiltered list must not yank the cursor
// back to the first row (pi's applyFilter resets to 0 — the "selection jumps"
// symptom).
func TestSettingsListSpaceWithEmptyQueryKeepsSelection(t *testing.T) {
	items := []*SettingItem{
		{Id: "a", Label: "Alpha", CurrentValue: "x", Values: []string{"x", "y"}},
		{Id: "b", Label: "Beta", CurrentValue: "x", Values: []string{"x", "y"}},
		{Id: "c", Label: "Gamma", CurrentValue: "x", Values: []string{"x", "y"}},
	}
	var changes []altSettingsChange
	list := NewSettingsList(
		items,
		10,
		altSettingsTestTheme(),
		func(id, value string) { changes = append(changes, altSettingsChange{id: id, value: value}) },
		func() {},
		&SettingsListOptions{EnableSearch: true},
	)

	list.HandleInput("\x1b[B") // down
	list.HandleInput("\x1b[B") // down -> Gamma
	if list.selectedIndex != 2 {
		t.Fatalf("selectedIndex = %d, want 2", list.selectedIndex)
	}

	list.HandleInput(" ")

	if len(changes) != 0 {
		t.Fatalf("space changed a setting: %v", changes)
	}
	if list.selectedIndex != 2 {
		t.Fatalf("space moved the selection to %d, want 2 (unchanged)", list.selectedIndex)
	}
	if items := list.displayItems(); len(items) != 3 {
		t.Fatalf("display items = %d, want 3", len(items))
	}
}

// TestSettingsListSpaceWithActiveQueryDoesNotChangeTheSetting: space is never
// a change shortcut, so a space typed while searching must not toggle the
// selected setting.
func TestSettingsListSpaceWithActiveQueryDoesNotChangeTheSetting(t *testing.T) {
	for _, encoding := range []string{" ", "\x1b[32u", "\x1b[27;1;32~"} {
		var changes []altSettingsChange
		list := NewSettingsList(
			altSettingsItems(),
			10,
			altSettingsTestTheme(),
			func(id, value string) { changes = append(changes, altSettingsChange{id: id, value: value}) },
			func() {},
			&SettingsListOptions{EnableSearch: true},
		)

		list.HandleInput("m")
		list.HandleInput(encoding)

		if len(changes) != 0 {
			t.Fatalf("encoding %q changed a setting with an active query: %v", encoding, changes)
		}
		if got := list.searchInput.GetValue(); !strings.HasPrefix(got, "m") {
			t.Fatalf("encoding %q search value = %q, want prefix %q", encoding, got, "m")
		}
	}
}

// TestSettingsListHintSaysConfirmAndGoBackNotCancel locks the pig deviation
// (2026-09-30, reworded 2026-10-01): Esc closes the panel but does not revert
// anything (values are applied immediately), so the hint must neither promise
// a "cancel" nor leave it ambiguous — it confirms the edits and goes back.
// pi says "Esc to cancel"; the settings submenu still says "Esc to go back"
// (its Esc can abort a preview).
func TestSettingsListHintSaysConfirmAndGoBackNotCancel(t *testing.T) {
	render := func(enableSearch bool) string {
		list := NewSettingsList(
			altSettingsItems(),
			10,
			altSettingsTestTheme(),
			func(string, string) {},
			func() {},
			&SettingsListOptions{EnableSearch: enableSearch},
		)
		return strings.Join(list.Render(80), "\n")
	}

	for _, tc := range []struct {
		name         string
		enableSearch bool
		want         string
	}{
		{"without-search", false, "  Enter to change · Esc to confirm and go back"},
		{"with-search", true, "  Type to search · Enter to change · Esc to confirm and go back"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := render(tc.enableSearch)
			if !strings.Contains(out, tc.want) {
				t.Fatalf("render = %q, want hint %q", out, tc.want)
			}
			if strings.Contains(out, "Esc to cancel") {
				t.Fatalf("render = %q, must not say %q", out, "Esc to cancel")
			}
		})
	}
}
