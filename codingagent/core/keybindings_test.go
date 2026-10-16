// keybindings_test.go (pi: test/keybindings.test.ts +
// test/keybindings-migration.test.ts).
//
// pi's migration rewrite tests drive runMigrations (Phase 8 batch,
// deferred); pig ports the in-memory migration-load case and the Windows
// default assertions.
package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ZoroSpace/pig/tui"
)

func TestUseWindowsKeybindings(t *testing.T) {
	envOf := func(m map[string]string) func(string) (string, bool) {
		return func(key string) (string, bool) {
			v, ok := m[key]
			return v, ok
		}
	}

	t.Run("uses Windows keybindings on native Windows", func(t *testing.T) {
		if !UseWindowsKeybindings("windows", envOf(nil)) {
			t.Fatal("want true on win32")
		}
	})

	t.Run("uses Windows keybindings in WSL without relying on Windows Terminal detection", func(t *testing.T) {
		if !UseWindowsKeybindings("linux", envOf(map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})) {
			t.Fatal("want true with WSL_DISTRO_NAME")
		}
		if !UseWindowsKeybindings("linux", envOf(map[string]string{"WSL_INTEROP": "/run/WSL/123_interop"})) {
			t.Fatal("want true with WSL_INTEROP")
		}
	})

	t.Run("does not use Windows keybindings from WT_SESSION alone", func(t *testing.T) {
		if UseWindowsKeybindings("linux", envOf(map[string]string{"WT_SESSION": "session"})) {
			t.Fatal("want false with WT_SESSION only")
		}
	})

	t.Run("keeps non-Windows defaults on other platforms", func(t *testing.T) {
		if UseWindowsKeybindings("linux", envOf(nil)) {
			t.Fatal("want false on linux")
		}
		if UseWindowsKeybindings("darwin", envOf(nil)) {
			t.Fatal("want false on darwin")
		}
	})

	t.Run("applies the detected defaults consistently", func(t *testing.T) {
		// pig: the merged table is built once at init from the host
		// platform, so this asserts consistency with the detected value.
		windowsKeybindings := useWindowsKeybindings()
		nativeWindows := windowsKeybindings && !isWSLForTest()

		pasteImage := Keybindings["app.clipboard.pasteImage"].DefaultKeys
		if want := oneOf(windowsKeybindings, "alt+v", "ctrl+v"); !keyIdsEqual(pasteImage, want) {
			t.Fatalf("pasteImage = %v, want %v", pasteImage, want)
		}
		search := Keybindings["tui.altScreen.search"].DefaultKeys
		if want := oneOf(windowsKeybindings, "ctrl+f", "ctrl+shift+f"); !keyIdsEqual(search, want) {
			t.Fatalf("search = %v, want %v", search, want)
		}
		followUp := Keybindings["app.message.followUp"].DefaultKeys
		if want := oneOf(windowsKeybindings, "ctrl+q", "alt+enter"); !keyIdsEqual(followUp, want) {
			t.Fatalf("followUp = %v, want %v", followUp, want)
		}
		cycleBackward := Keybindings["app.model.cycleBackward"].DefaultKeys
		if want := oneOf(windowsKeybindings, "alt+p", "shift+ctrl+p"); !keyIdsEqual(cycleBackward, want) {
			t.Fatalf("cycleBackward = %v, want %v", cycleBackward, want)
		}
		undo := Keybindings["tui.editor.undo"].DefaultKeys
		wantUndo := "ctrl+-"
		if windowsKeybindings {
			wantUndo = "alt+z"
		}
		if nativeWindows {
			wantUndo = "ctrl+z"
		}
		if !keyIdsEqual(undo, []tui.KeyId{wantUndo}) {
			t.Fatalf("undo = %v, want %v", undo, wantUndo)
		}
		previousPrompt := Keybindings["tui.altScreen.previousPrompt"].DefaultKeys
		wantPrevious := []tui.KeyId{"ctrl+shift+up", "ctrl+up"}
		if windowsKeybindings {
			wantPrevious = []tui.KeyId{"ctrl+up"}
		}
		if !keyIdsEqual(previousPrompt, wantPrevious) {
			t.Fatalf("previousPrompt = %v, want %v", previousPrompt, wantPrevious)
		}
		nextPrompt := Keybindings["tui.altScreen.nextPrompt"].DefaultKeys
		wantNext := []tui.KeyId{"ctrl+shift+down", "ctrl+down"}
		if windowsKeybindings {
			wantNext = []tui.KeyId{"ctrl+down"}
		}
		if !keyIdsEqual(nextPrompt, wantNext) {
			t.Fatalf("nextPrompt = %v, want %v", nextPrompt, wantNext)
		}
		dequeue := Keybindings["app.message.dequeue"].DefaultKeys
		if want := oneOf(windowsKeybindings, "alt+q", "alt+up"); !keyIdsEqual(dequeue, want) {
			t.Fatalf("dequeue = %v, want %v", dequeue, want)
		}
	})
}

func isWSLForTest() bool {
	if _, ok := os.LookupEnv("WSL_DISTRO_NAME"); ok {
		return true
	}
	_, ok := os.LookupEnv("WSL_INTEROP")
	return ok
}

func oneOf(windows bool, windowsValue string, otherValue string) []tui.KeyId {
	if windows {
		return []tui.KeyId{windowsValue}
	}
	return []tui.KeyId{otherValue}
}

func keyIdsEqual(a, b []tui.KeyId) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestKeybindingsMigrationLoadsLegacyNames(t *testing.T) {
	t.Run("loads old key names in memory before the file is rewritten", func(t *testing.T) {
		agentDir := t.TempDir()
		config := `{
  "selectConfirm": "enter",
  "interrupt": "ctrl+x"
}
`
		if err := os.WriteFile(filepath.Join(agentDir, "keybindings.json"), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}

		keybindings := CreateKeybindingsManager(agentDir)

		userBindings := keybindings.GetUserBindings()
		if !keyConfigEqual(userBindings, map[string][]tui.KeyId{
			"tui.select.confirm": {"enter"},
			"app.interrupt":      {"ctrl+x"},
		}) {
			t.Fatalf("userBindings = %v", userBindings)
		}
		effective := keybindings.GetEffectiveConfig()
		if got := effective["tui.select.confirm"]; len(got) != 1 || got[0] != "enter" {
			t.Fatalf("effective tui.select.confirm = %v, want [enter]", got)
		}
		if got := effective["app.interrupt"]; len(got) != 1 || got[0] != "ctrl+x" {
			t.Fatalf("effective app.interrupt = %v, want [ctrl+x]", got)
		}
	})
}

// TestKeybindingsConfigValueValidation ports pi #9880: toKeybindingsConfig
// validates each binding against the KeyId grammar, drops invalid values, and
// skips the $schema marker.
func TestKeybindingsConfigValueValidation(t *testing.T) {
	agentDir := t.TempDir()
	config := `{
  "$schema": "https://example.com/keybindings.schema.json",
  "app.session.new": "ctrl+n",
  "extension.action": ["alt+x"],
  "app.clear": 42,
  "app.exit": "not a key!",
  "app.interrupt": "ctrl+ctrl+c",
  "app.model.select": ["escape", 7]
}
`
	if err := os.WriteFile(filepath.Join(agentDir, "keybindings.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	got := CreateKeybindingsManager(agentDir).GetUserBindings()
	want := map[string][]tui.KeyId{
		"app.session.new":  {"ctrl+n"},
		"extension.action": {"alt+x"},
	}
	if !keyConfigEqual(got, want) {
		t.Fatalf("userBindings = %v, want %v", got, want)
	}
}

func keyConfigEqual(a, b tui.KeybindingsConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || !keyIdsEqual(va, vb) {
			return false
		}
	}
	return true
}
