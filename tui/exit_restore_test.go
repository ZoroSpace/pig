// exit_restore_test.go — pi: test/exit-restore.test.ts (#11170).
package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestProcessTerminalRestoreOnExit(t *testing.T) {
	var out bytes.Buffer
	terminal := &ProcessTerminal{
		out:                    &out,
		keyboardProtocolPushed: true,
		modifyOtherKeysActive:  true,
	}

	terminal.RestoreOnExit("BEFORE", "AFTER")
	sequence := out.String()
	for _, want := range []string{"BEFORE", "\x1b[?2004l", "\x1b[<u", "\x1b[>4;0m", "AFTER"} {
		if count := strings.Count(sequence, want); count != 1 {
			t.Fatalf("%q appears %d times in %q", want, count, sequence)
		}
	}
	if !strings.HasPrefix(sequence, "BEFORE") || !strings.HasSuffix(sequence, "AFTER") {
		t.Fatalf("restore order = %q", sequence)
	}
}

func TestAltScreenExitRestoreLeavesAltScreenAfterKittyPop(t *testing.T) {
	terminal := &TuiAltScreen{TuiBase: TuiBase{terminal: NewProcessTerminal()}, altScreenActive: true, mouseEnabled: true}
	basePrefix := "\x1b[?25h"
	baseSuffix := ""
	prefix := terminal.exitRestorePrefix(basePrefix)
	suffix := terminal.exitRestoreSuffix(baseSuffix)

	if !strings.Contains(prefix, "\x1b[?1006l") || !strings.Contains(prefix, "\x1b[?7h") || !strings.HasSuffix(prefix, basePrefix) {
		t.Fatalf("alt-screen prefix = %q", prefix)
	}
	if !strings.HasSuffix(suffix, "\x1b[?1049l\x1b[?25h") {
		t.Fatalf("alt-screen suffix = %q", suffix)
	}
	// TuiBase writes <u in the terminal reset between prefix and suffix.
	sequence := prefix + "\x1b[<u" + suffix
	if strings.Index(sequence, "\x1b[<u") > strings.Index(sequence, "\x1b[?1049l") {
		t.Fatalf("Kitty pop must precede leaving alt screen: %q", sequence)
	}
}
