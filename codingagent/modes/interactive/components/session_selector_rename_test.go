// session_selector_rename_test.go (pi: test/session-selector-rename.test.ts)
// — /resume picker rename flow tests.
package components

import (
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/tui"
)

func makeSessionForSelector(overrides func(*core.SessionInfo)) core.SessionInfo {
	s := core.SessionInfo{
		MessageCount:    1,
		FirstMessage:    "hello",
		AllMessagesText: "hello",
		Created:         time.Unix(0, 0),
		Modified:        time.Unix(0, 0),
	}
	if overrides != nil {
		overrides(&s)
	}
	if s.Path == "" {
		s.Path = "/tmp/" + s.ID + ".jsonl"
	}
	return s
}

func newTestSelector(t *testing.T, sessions []core.SessionInfo, options *SessionSelectorOptions) (*SessionSelectorComponent, *testMutator) {
	t.Helper()
	initTestTheme(t)
	tui.SetKeybindings(core.NewKeybindingsManager(nil, "").KeybindingsManager)
	mutator := &testMutator{}
	selector := NewSessionSelectorComponent(
		func(func(loaded, total int)) []core.SessionInfo { return sessions },
		func(func(loaded, total int)) []core.SessionInfo { return nil },
		func(string) {}, func() {}, func() {}, func() {},
		options,
		"",
		mutator.mutate,
	)
	return selector, mutator
}

func TestSessionSelectorRenameHint(t *testing.T) {
	t.Run("shows rename hint in interactive /resume picker configuration", func(t *testing.T) {
		sessions := []core.SessionInfo{makeSessionForSelector(func(s *core.SessionInfo) { s.ID = "a" })}
		keybindings := core.NewKeybindingsManager(nil, "")
		selector, mutator := newTestSelector(t, sessions, &SessionSelectorOptions{ShowRenameHint: boolPtr(true), Keybindings: keybindings})
		waitLoaded(t, mutator, selector)

		output := strings.Join(selector.Render(120), "\n")
		if !strings.Contains(output, "ctrl+r") {
			t.Fatalf("render missing ctrl+r:\n%s", output)
		}
		if !strings.Contains(output, "rename") {
			t.Fatalf("render missing rename:\n%s", output)
		}
	})

	t.Run("does not show rename hint in --resume picker configuration", func(t *testing.T) {
		sessions := []core.SessionInfo{makeSessionForSelector(func(s *core.SessionInfo) { s.ID = "a" })}
		keybindings := core.NewKeybindingsManager(nil, "")
		selector, mutator := newTestSelector(t, sessions, &SessionSelectorOptions{ShowRenameHint: boolPtr(false), Keybindings: keybindings})
		waitLoaded(t, mutator, selector)

		output := strings.Join(selector.Render(120), "\n")
		if strings.Contains(output, "ctrl+r") {
			t.Fatalf("render should not contain ctrl+r:\n%s", output)
		}
		if strings.Contains(output, "rename") {
			t.Fatalf("render should not contain rename:\n%s", output)
		}
	})

	t.Run("enters rename mode on Ctrl+R and submits with Enter", func(t *testing.T) {
		sessions := []core.SessionInfo{makeSessionForSelector(func(s *core.SessionInfo) { s.ID = "a"; s.Name = strPtr("Old") })}
		renameCalls := 0
		var renamedPath, renamedName string
		keybindings := core.NewKeybindingsManager(nil, "")
		selector, mutator := newTestSelector(t, sessions, &SessionSelectorOptions{
			RenameSession: func(sessionPath, nextName string) error {
				renameCalls++
				renamedPath = sessionPath
				renamedName = nextName
				return nil
			},
			ShowRenameHint: boolPtr(true),
			Keybindings:    keybindings,
		})

		// Wait for the initial async load to finish (pi: flushPromises after
		// construction).
		waitLoaded(t, mutator, selector)

		// Kitty keyboard protocol encoding for Ctrl+R (pi: "\x1b[114;5u") —
		// pig matches the legacy encoding in tests.

		selector.GetSessionList().HandleInput("\x12")

		// Rename mode layout
		output := strings.Join(selector.Render(120), "\n")
		if !strings.Contains(output, "Rename Session") {
			t.Fatalf("render missing Rename Session:\n%s", output)
		}
		if strings.Contains(output, "Resume Session") {
			t.Fatalf("render should not show Resume Session in rename mode:\n%s", output)
		}

		// Type and submit
		selector.HandleInput("X")
		selector.HandleInput("\r")

		if renameCalls != 1 {
			t.Fatalf("renameSession calls = %d, want 1", renameCalls)
		}
		if renamedName != "XOld" {
			t.Fatalf("renamed to %q, want XOld", renamedName)
		}
		if renamedPath != sessions[0].Path {
			t.Fatalf("renamed path = %q, want %q", renamedPath, sessions[0].Path)
		}
	})
}
