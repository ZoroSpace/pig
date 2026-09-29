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

// TestSettingsListSpaceShortcutAcceptsAllKeyEncodings: the same space key
// arrives as a raw " " on legacy terminals, as a Kitty CSI-u sequence
// (\x1b[32u) under the Kitty keyboard protocol, and as an xterm
// modifyOtherKeys sequence (\x1b[27;1;32~) under pig's fallback — all three
// must change the selected setting, not type a space into the search box
// (which re-filters and snaps the cursor back to the first match).
func TestSettingsListSpaceShortcutAcceptsAllKeyEncodings(t *testing.T) {
	cases := []struct{ name, encoding string }{
		{"legacy", " "},
		{"kitty-csi-u", "\x1b[32u"},
		{"modify-other-keys", "\x1b[27;1;32~"},
	}
	for _, tc := range cases {
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

			list.HandleInput(tc.encoding)

			want := []altSettingsChange{{id: "tui-mode", value: "fullscreen"}}
			if !reflect.DeepEqual(changes, want) {
				t.Fatalf("changes = %v, want %v", changes, want)
			}
			if got := list.searchInput.GetValue(); got != "" {
				t.Fatalf("search value = %q, want empty (a space shortcut must not reach the search input)", got)
			}
		})
	}
}

// TestSettingsListSpaceWithActiveQueryDoesNotChangeTheSetting: once a
// search query is present the space shortcut is disabled, so the space must
// never toggle a setting in any encoding (pi semantics preserved by the
// encoding-aware shortcut).
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
