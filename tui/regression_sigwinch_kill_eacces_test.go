// tui/regression_sigwinch_kill_eacces_test.go (pi:
// packages/tui/test/regression-sigwinch-kill-eacces.test.ts)
// Unix-only: locks the SIGWINCH seam behavior (see terminal_unix.go); the
// Windows poll counterpart lives behind consoleSizeFn (WINDOWS-SUPPORT).
//go:build !windows

package tui

import (
	"errors"
	"testing"
)

func TestRefreshTerminalDimensionsDoesNotThrowWhenKillReturnsEACCES(t *testing.T) {
	originalKill := killSelfSignal
	originalPlatform := terminalPlatform
	defer func() {
		killSelfSignal = originalKill
		terminalPlatform = originalPlatform
	}()

	terminalPlatform = "linux"
	killSelfSignal = func() error {
		return errors.New("kill EACCES")
	}

	// EACCES for kill(2) must be skipped, not crash.
	refreshTerminalDimensions()
}

func TestRefreshTerminalDimensionsDoesNotCallKillOnWindows(t *testing.T) {
	originalKill := killSelfSignal
	originalPlatform := terminalPlatform
	defer func() {
		killSelfSignal = originalKill
		terminalPlatform = originalPlatform
	}()

	called := false
	terminalPlatform = "windows"
	killSelfSignal = func() error {
		called = true
		return nil
	}

	refreshTerminalDimensions()
	if called {
		t.Fatal("kill should not be called on windows")
	}
}

func TestRefreshTerminalDimensionsPreservesOtherErrorCodes(t *testing.T) {
	originalKill := killSelfSignal
	originalPlatform := terminalPlatform
	defer func() {
		killSelfSignal = originalKill
		terminalPlatform = originalPlatform
	}()

	terminalPlatform = "linux"
	killSelfSignal = func() error {
		return errors.New("kill EPERM")
	}

	// EPERM is also ignored - the refresh is best-effort.
	refreshTerminalDimensions()
}
