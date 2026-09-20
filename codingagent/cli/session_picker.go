// session_picker.go (pi: cli/session-picker.ts) — TUI session selector for
// the --resume flag. Runs in a temporary startup TUI before interactive mode
// mounts; returns the selected session path (ok=false when cancelled).
package cli

import (
	"os"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/samber/lo"
)

// SessionsLoader loads the session list (pi: SessionsLoader).
type SessionsLoader = func(onProgress core.SessionListProgress) []core.SessionInfo

// SelectSession shows the TUI session selector and returns the selected
// session path (ok=false if cancelled). The onExit callback exits the process
// like pi (ui.stop + process.exit(0) inside the component's exit handler).
func SelectSession(currentSessionsLoader SessionsLoader, allSessionsLoader SessionsLoader,
	settingsManager *core.SettingsManager) (string, bool) {
	ui := CreateStartupTui(settingsManager)
	resultCh := make(chan string, 1)
	settled := false
	finish := func(path string) {
		if settled {
			return
		}
		settled = true
		resultCh <- path
	}

	keybindings := core.CreateKeybindingsManager("")
	selector := components.NewSessionSelectorComponent(
		currentSessionsLoader,
		allSessionsLoader,
		func(path string) {
			finish(path)
			ui.Stop(nil)
		},
		func() {
			finish("")
			ui.Stop(nil)
		},
		func() {
			ui.Stop(nil)
			os.Exit(0)
		},
		func() { ui.RequestRender(false) },
		&components.SessionSelectorOptions{ShowRenameHint: lo.ToPtr(false), Keybindings: keybindings},
		// pi passes no current-session path here (undefined).
		"",
		// pig concurrency note: the interactive caller passes m.mutateUI;
		// the startup TUI serializes the selector's background mutations
		// against its render goroutine via RunExclusive directly.
		func(fn func()) { ui.RunExclusive(fn) },
	)

	ui.AddChild(selector)
	ui.SetFocus(selector.GetSessionList())
	StartStartupTui(ui, settingsManager)

	selected := <-resultCh
	if selected == "" {
		return "", false
	}
	return selected, true
}
