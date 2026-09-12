// tui/editor_test.go — ported from pi: packages/tui/test/editor.test.ts
//
// Declared deviations:
//   - CJK word-movement expectations are adapted to UAX #29 segmentation
//     (pi relies on ICU's CJK dictionary — see word_navigation_test.go header);
//     the regression intent (word movement stops at fullwidth punctuation,
//     issue #4972) is preserved.
//   - Cursor/column assertions are byte offsets (pi: UTF-16 code units);
//     ASCII-only cases are unchanged.
//   - Tests of the Autocomplete describe block and "undoes autocomplete" /
//     "does not trigger autocomplete during single-line paste" are deferred
//     to T2b together with the editor autocomplete subsection.
//   - stripVTControlCharacters → local stripAnsi (CSI/OSC/APC).

package tui

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stripAnsi removes ANSI escape sequences (pi test helper: stripVTControlCharacters).
var ansiSequenceRegex = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|_[^\x07\x1b]*(?:\x07|\x1b\\))`)

func stripAnsi(s string) string { return ansiSequenceRegex.ReplaceAllString(s, "") }

// testEditorTheme is the default editor theme for tests
// (pi: defaultEditorTheme — chalk.dim border, chalk level 3; selectList =
// defaultSelectListTheme with chalk blue/bold/dim).
func testEditorTheme() EditorTheme {
	return EditorTheme{
		BorderColor: func(text string) string { return "\x1b[2m" + text + "\x1b[0m" },
		SelectList: SelectListTheme{
			SelectedPrefix: func(text string) string { return "\x1b[34m" + text + "\x1b[39m" },
			SelectedText:   func(text string) string { return "\x1b[1m" + text + "\x1b[22m" },
			Description:    func(text string) string { return "\x1b[2m" + text + "\x1b[22m" },
			ScrollInfo:     func(text string) string { return "\x1b[2m" + text + "\x1b[22m" },
			NoMatch:        func(text string) string { return "\x1b[2m" + text + "\x1b[22m" },
		},
	}
}

// createTestTUI creates a TUI with a virtual terminal for testing.
func createTestTUI(cols, rows int) *TuiMainScreen {
	return NewTuiMainScreen(NewVirtualTerminal(cols, rows), nil, "")
}

// newTestEditor constructs a default editor.
func newTestEditor(cols, rows int, options *EditorOptions) *Editor {
	return NewEditor(createTestTUI(cols, rows), testEditorTheme(), options)
}

func editorText(t *testing.T, e *Editor) string {
	t.Helper()
	return e.GetText()
}

func wantText(t *testing.T, e *Editor, want string) {
	t.Helper()
	if got := e.GetText(); got != want {
		t.Fatalf("GetText() = %q, want %q", got, want)
	}
}

func wantCursor(t *testing.T, e *Editor, line, col int) {
	t.Helper()
	got := e.GetCursor()
	if got.Line != line || got.Col != col {
		t.Fatalf("GetCursor() = {line:%d col:%d}, want {line:%d col:%d}", got.Line, got.Col, line, col)
	}
}

func TestEditorHistoryNavigation(t *testing.T) {
	t.Run("does nothing on Up arrow when history is empty", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.HandleInput("\x1b[A") // Up arrow

		wantText(t, editor, "")
	})

	t.Run("shows most recent history entry on Up arrow when editor is empty", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.AddToHistory("first prompt")
		editor.AddToHistory("second prompt")

		editor.HandleInput("\x1b[A") // Up arrow

		wantText(t, editor, "second prompt")
	})

	t.Run("cycles through history entries on repeated Up arrow", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.AddToHistory("first")
		editor.AddToHistory("second")
		editor.AddToHistory("third")

		editor.HandleInput("\x1b[A") // Up - shows "third"
		wantText(t, editor, "third")

		editor.HandleInput("\x1b[A") // Up - shows "second"
		wantText(t, editor, "second")

		editor.HandleInput("\x1b[A") // Up - shows "first"
		wantText(t, editor, "first")

		editor.HandleInput("\x1b[A") // Up - stays at "first" (oldest)
		wantText(t, editor, "first")
	})

	t.Run("jumps to start before entering history from a non-empty draft", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.AddToHistory("prompt")
		editor.SetText("draft")
		editor.HandleInput("\x1b[D")
		editor.HandleInput("\x1b[D")

		editor.HandleInput("\x1b[A") // Up - jumps to start before history browsing
		wantText(t, editor, "draft")
		wantCursor(t, editor, 0, 0)

		editor.HandleInput("\x1b[A") // Up at start - shows "prompt"
		wantText(t, editor, "prompt")

		editor.HandleInput("\x1b[B") // Down - restores draft
		wantText(t, editor, "draft")
		wantCursor(t, editor, 0, 0)
	})

	t.Run("navigates forward through history with Down arrow", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.AddToHistory("first")
		editor.AddToHistory("second")
		editor.AddToHistory("third")
		editor.SetText("draft")

		// Go to oldest
		editor.HandleInput("\x1b[A") // start of draft
		editor.HandleInput("\x1b[A") // third
		editor.HandleInput("\x1b[A") // second
		editor.HandleInput("\x1b[A") // first

		// Navigate back
		editor.HandleInput("\x1b[B") // second
		wantText(t, editor, "second")

		editor.HandleInput("\x1b[B") // third
		wantText(t, editor, "third")

		editor.HandleInput("\x1b[B") // draft
		wantText(t, editor, "draft")
	})

	t.Run("exits history mode when typing a character", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.AddToHistory("old prompt")

		editor.HandleInput("\x1b[A") // Up - shows "old prompt"
		editor.HandleInput("x")      // Type a character - exits history mode

		wantText(t, editor, "xold prompt")
	})

	t.Run("exits history mode on setText", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.AddToHistory("first")
		editor.AddToHistory("second")

		editor.HandleInput("\x1b[A") // Up - shows "second"
		editor.SetText("")           // External clear

		// Up should start fresh from most recent
		editor.HandleInput("\x1b[A")
		wantText(t, editor, "second")
	})

	t.Run("does not add empty strings to history", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.AddToHistory("")
		editor.AddToHistory("   ")
		editor.AddToHistory("valid")

		editor.HandleInput("\x1b[A")
		wantText(t, editor, "valid")

		// Should not have more entries
		editor.HandleInput("\x1b[A")
		wantText(t, editor, "valid")
	})

	t.Run("does not add consecutive duplicates to history", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.AddToHistory("same")
		editor.AddToHistory("same")
		editor.AddToHistory("same")

		editor.HandleInput("\x1b[A") // "same"
		wantText(t, editor, "same")

		editor.HandleInput("\x1b[A") // stays at "same" (only one entry)
		wantText(t, editor, "same")
	})

	t.Run("allows non-consecutive duplicates in history", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.AddToHistory("first")
		editor.AddToHistory("second")
		editor.AddToHistory("first") // Not consecutive, should be added

		editor.HandleInput("\x1b[A") // "first"
		wantText(t, editor, "first")

		editor.HandleInput("\x1b[A") // "second"
		wantText(t, editor, "second")

		editor.HandleInput("\x1b[A") // "first" (older one)
		wantText(t, editor, "first")
	})

	t.Run("uses cursor movement instead of history when editor has content", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.AddToHistory("history item")
		editor.SetText("line1\nline2")

		// Cursor is at end of line2, Up should move to line1
		editor.HandleInput("\x1b[A") // Up - cursor movement

		// Insert character to verify cursor position
		editor.HandleInput("X")

		// X should be inserted in line1, not replace with history
		wantText(t, editor, "line1X\nline2")
	})

	t.Run("limits history to 100 entries", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// Add 105 entries
		for i := 0; i < 105; i++ {
			editor.AddToHistory(fmt.Sprintf("prompt %d", i))
		}

		// Navigate to oldest
		for i := 0; i < 100; i++ {
			editor.HandleInput("\x1b[A")
		}

		// Should be at entry 5 (oldest kept), not entry 0
		wantText(t, editor, "prompt 5")

		// One more Up should not change anything
		editor.HandleInput("\x1b[A")
		wantText(t, editor, "prompt 5")
	})

	t.Run("places cursor at start after browsing history upward", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.AddToHistory("older entry")
		editor.AddToHistory("line1\nline2\nline3")

		editor.HandleInput("\x1b[A") // Up - shows multi-line entry at start
		wantText(t, editor, "line1\nline2\nline3")
		wantCursor(t, editor, 0, 0)

		editor.HandleInput("\x1b[A") // Up again - immediately navigates to older entry
		wantText(t, editor, "older entry")
		wantCursor(t, editor, 0, 0)
	})

	t.Run("places cursor at end after browsing history downward", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.AddToHistory("older entry")
		editor.AddToHistory("line1\nline2\nline3")
		editor.AddToHistory("newer entry")

		editor.HandleInput("\x1b[A") // newer entry
		editor.HandleInput("\x1b[A") // multi-line entry
		editor.HandleInput("\x1b[A") // older entry

		editor.HandleInput("\x1b[B") // Down - shows multi-line entry at end
		wantText(t, editor, "line1\nline2\nline3")
		wantCursor(t, editor, 2, 5)

		editor.HandleInput("\x1b[B") // Down again - immediately navigates to newer entry
		wantText(t, editor, "newer entry")
	})

	t.Run("allows opposite-direction cursor movement within multi-line history entry", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.AddToHistory("line1\nline2\nline3")

		editor.HandleInput("\x1b[A") // Up - shows entry at start
		wantCursor(t, editor, 0, 0)

		editor.HandleInput("\x1b[B") // Down - cursor moves to line2
		wantText(t, editor, "line1\nline2\nline3")
		wantCursor(t, editor, 1, 0)

		editor.HandleInput("\x1b[A") // Up - cursor moves back to line1
		wantText(t, editor, "line1\nline2\nline3")
		wantCursor(t, editor, 0, 0)
	})
}

func TestEditorPublicStateAccessors(t *testing.T) {
	t.Run("returns cursor position", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		wantCursor(t, editor, 0, 0)

		editor.HandleInput("a")
		editor.HandleInput("b")
		editor.HandleInput("c")

		wantCursor(t, editor, 0, 3)

		editor.HandleInput("\x1b[D") // Left
		wantCursor(t, editor, 0, 2)
	})

	t.Run("returns lines as a defensive copy", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		editor.SetText("a\nb")

		lines := editor.GetLines()
		if len(lines) != 2 || lines[0] != "a" || lines[1] != "b" {
			t.Fatalf("GetLines() = %v, want [a b]", lines)
		}

		lines[0] = "mutated"
		lines2 := editor.GetLines()
		if len(lines2) != 2 || lines2[0] != "a" || lines2[1] != "b" {
			t.Fatalf("GetLines() after mutation = %v, want [a b]", lines2)
		}
	})
}

func TestEditorBackslashEnterWorkaround(t *testing.T) {
	t.Run("inserts backslash immediately (no buffering)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.HandleInput("\\")

		// Backslash should be visible immediately, not buffered
		wantText(t, editor, "\\")
	})

	t.Run("converts standalone backslash to newline on Enter", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.HandleInput("\\")
		editor.HandleInput("\r")

		wantText(t, editor, "\n")
	})

	t.Run("inserts backslash normally when followed by other characters", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.HandleInput("\\")
		editor.HandleInput("x")

		wantText(t, editor, "\\x")
	})

	t.Run("does not trigger newline when backslash is not immediately before cursor", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		submitted := false

		editor.OnSubmit = func(text string) {
			submitted = true
		}

		editor.HandleInput("\\")
		editor.HandleInput("x")
		editor.HandleInput("\r")

		// Should submit, not insert newline (backslash not at cursor)
		if !submitted {
			t.Error("expected submit")
		}
	})

	t.Run("only removes one backslash when multiple are present", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.HandleInput("\\")
		editor.HandleInput("\\")
		editor.HandleInput("\\")
		wantText(t, editor, "\\\\\\")

		editor.HandleInput("\r")
		// Only the last backslash is removed, newline inserted
		wantText(t, editor, "\\\\\n")
	})
}

func TestEditorKittyCsiUHandling(t *testing.T) {
	t.Run("ignores printable CSI-u sequences with unsupported modifiers", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.HandleInput("\x1b[99;9u")

		wantText(t, editor, "")
	})

	t.Run("inserts shifted CSI-u letters as text", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.HandleInput("\x1b[69;2u")

		wantText(t, editor, "E")
	})

	t.Run("inserts shifted xterm modifyOtherKeys letters as text", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.HandleInput("\x1b[27;2;69~")

		wantText(t, editor, "E")
	})
}

func TestEditorUnicodeTextEditing(t *testing.T) {
	t.Run("inserts mixed ASCII, umlauts, and emojis as literal text", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		for _, ch := range []string{"H", "e", "l", "l", "o", " ", "ä", "ö", "ü", " ", "😀"} {
			editor.HandleInput(ch)
		}

		wantText(t, editor, "Hello äöü 😀")
	})

	t.Run("deletes single-code-unit unicode characters (umlauts) with Backspace", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.HandleInput("ä")
		editor.HandleInput("ö")
		editor.HandleInput("ü")

		// Delete the last character (ü)
		editor.HandleInput("\x7f") // Backspace

		wantText(t, editor, "äö")
	})

	t.Run("deletes multi-code-unit emojis with single Backspace", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.HandleInput("😀")
		editor.HandleInput("👍")

		// Delete the last emoji (👍) - single backspace deletes whole grapheme cluster
		editor.HandleInput("\x7f") // Backspace

		wantText(t, editor, "😀")
	})

	t.Run("inserts characters at the correct position after cursor movement over umlauts", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.HandleInput("ä")
		editor.HandleInput("ö")
		editor.HandleInput("ü")

		// Move cursor left twice
		editor.HandleInput("\x1b[D") // Left arrow
		editor.HandleInput("\x1b[D") // Left arrow

		// Insert 'x' in the middle
		editor.HandleInput("x")

		wantText(t, editor, "äxöü")
	})

	t.Run("moves cursor across multi-code-unit emojis with single arrow key", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.HandleInput("😀")
		editor.HandleInput("👍")
		editor.HandleInput("🎉")

		// Move cursor left over last emoji (🎉) - single arrow moves over whole grapheme
		editor.HandleInput("\x1b[D") // Left arrow

		// Move cursor left over second emoji (👍)
		editor.HandleInput("\x1b[D")

		// Insert 'x' between first and second emoji
		editor.HandleInput("x")

		wantText(t, editor, "😀x👍🎉")
	})

	t.Run("preserves umlauts across line breaks", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.HandleInput("ä")
		editor.HandleInput("ö")
		editor.HandleInput("ü")
		editor.HandleInput("\n") // new line
		editor.HandleInput("Ä")
		editor.HandleInput("Ö")
		editor.HandleInput("Ü")

		wantText(t, editor, "äöü\nÄÖÜ")
	})

	t.Run("replaces the entire document with unicode text via setText (paste simulation)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// Simulate bracketed paste / programmatic replacement
		editor.SetText("Hällö Wörld! 😀 äöüÄÖÜß")

		wantText(t, editor, "Hällö Wörld! 😀 äöüÄÖÜß")
	})

	t.Run("moves cursor to document start on Ctrl+A and inserts at the beginning", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.HandleInput("a")
		editor.HandleInput("b")
		editor.HandleInput("\x01") // Ctrl+A (move to start)
		editor.HandleInput("x")    // Insert at start

		wantText(t, editor, "xab")
	})

	t.Run("deletes words correctly with Ctrl+W and Alt+Backspace", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// Basic word deletion
		editor.SetText("foo bar baz")
		editor.HandleInput("\x17") // Ctrl+W
		wantText(t, editor, "foo bar ")

		// Trailing whitespace
		editor.SetText("foo bar   ")
		editor.HandleInput("\x17")
		wantText(t, editor, "foo ")

		// Punctuation run
		editor.SetText("foo bar...")
		editor.HandleInput("\x17")
		wantText(t, editor, "foo bar")

		// ASCII punctuation inside Intl word-like segments preserves old boundaries
		editor.SetText("foo.bar")
		editor.HandleInput("\x17")
		wantText(t, editor, "foo.")

		editor.SetText("foo:bar")
		editor.HandleInput("\x17")
		wantText(t, editor, "foo:")

		// Delete across multiple lines
		editor.SetText("line one\nline two")
		editor.HandleInput("\x17")
		wantText(t, editor, "line one\nline ")

		// Delete empty line (merge)
		editor.SetText("line one\n")
		editor.HandleInput("\x17")
		wantText(t, editor, "line one")

		// Grapheme safety (emoji as a word)
		editor.SetText("foo 😀😀 bar")
		editor.HandleInput("\x17")
		wantText(t, editor, "foo 😀😀 ")
		editor.HandleInput("\x17")
		wantText(t, editor, "foo ")

		// Alt+Backspace
		editor.SetText("foo bar")
		editor.HandleInput("\x1b\x7f") // Alt+Backspace (legacy)
		wantText(t, editor, "foo ")
	})

	t.Run("navigates words correctly with Ctrl+Left/Right", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("foo bar... baz")
		// Cursor at end

		// Move left over baz
		editor.HandleInput("\x1b[1;5D") // Ctrl+Left
		wantCursor(t, editor, 0, 11)    // after '...'

		// Move left over punctuation
		editor.HandleInput("\x1b[1;5D") // Ctrl+Left
		wantCursor(t, editor, 0, 7)     // after 'bar'

		// Move left over bar
		editor.HandleInput("\x1b[1;5D") // Ctrl+Left
		wantCursor(t, editor, 0, 4)     // after 'foo '

		// Move right over bar
		editor.HandleInput("\x1b[1;5C") // Ctrl+Right
		wantCursor(t, editor, 0, 7)     // at end of 'bar'

		// Move right over punctuation run
		editor.HandleInput("\x1b[1;5C") // Ctrl+Right
		wantCursor(t, editor, 0, 10)    // after '...'

		// Move right skips space and lands after baz
		editor.HandleInput("\x1b[1;5C") // Ctrl+Right
		wantCursor(t, editor, 0, 14)    // end of line

		// Test forward from start with leading whitespace
		editor.SetText("   foo bar")
		editor.HandleInput("\x01")      // Ctrl+A to go to start
		editor.HandleInput("\x1b[1;5C") // Ctrl+Right
		wantCursor(t, editor, 0, 6)     // after 'foo'

		// ASCII punctuation inside Intl word-like segments preserves old boundaries
		editor.SetText("foo.bar baz")
		editor.HandleInput("\x1b[1;5D") // Ctrl+Left over baz
		wantCursor(t, editor, 0, 8)
		editor.HandleInput("\x1b[1;5D") // Ctrl+Left over bar
		wantCursor(t, editor, 0, 4)
		editor.HandleInput("\x1b[1;5D") // Ctrl+Left over .
		wantCursor(t, editor, 0, 3)

		editor.HandleInput("\x01")      // Ctrl+A
		editor.HandleInput("\x1b[1;5C") // Ctrl+Right over foo
		wantCursor(t, editor, 0, 3)
		editor.HandleInput("\x1b[1;5C") // Ctrl+Right over .
		wantCursor(t, editor, 0, 4)
		editor.HandleInput("\x1b[1;5C") // Ctrl+Right over bar
		wantCursor(t, editor, 0, 7)
	})

	t.Run("stops at fullwidth Chinese punctuation (issue #4972)", func(t *testing.T) {
		// pi (ICU dictionary): 你好 / ， / 世界 → cols 5(end)→3→2→0→2→3→5.
		// Byte domain with the gse dictionary grouping (cjk_segmenter.go):
		// 你好=0-5, ，=6-8, 世界=9-14 → 15→9→6→0→6→9→15. (Was adapted to the
		// UAX #29 per-char walk before the dictionary landed.)
		requireCJKDict(t)
		editor := newTestEditor(80, 24, nil)

		editor.SetText("你好，世界")
		// Cursor at end (byte 15)

		// Move left over 世界
		editor.HandleInput("\x1b[1;5D") // Ctrl+Left
		wantCursor(t, editor, 0, 9)     // after ，(pi ICU: col 3)

		// Move left over ，
		editor.HandleInput("\x1b[1;5D")
		wantCursor(t, editor, 0, 6) // pi ICU: col 2

		// Move left over 你好
		editor.HandleInput("\x1b[1;5D")
		wantCursor(t, editor, 0, 0) // start (pi ICU: col 0)

		// Move right over 你好
		editor.HandleInput("\x1b[1;5C") // Ctrl+Right
		wantCursor(t, editor, 0, 6)     // after 你好 (pi ICU: col 2)

		// Move right over ，
		editor.HandleInput("\x1b[1;5C")
		wantCursor(t, editor, 0, 9) // after ，(pi ICU: col 3)

		// Move right over 世界
		editor.HandleInput("\x1b[1;5C")
		wantCursor(t, editor, 0, 15) // end (pi ICU: col 5)
	})

	t.Run("handles mixed CJK and ASCII word movement", func(t *testing.T) {
		// pi (ICU dictionary): hello / 你好 / ， / world / 世界 → backward
		// cols 15→13→8→7→5→0, forward cols 5→7→8→13→15.
		// Byte domain with the gse dictionary grouping (cjk_segmenter.go):
		// backward 25→19→14→11→5→0, forward 5→11→14→19→25.
		requireCJKDict(t)
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello你好，world世界")
		// Cursor at end (byte 25)

		// Move left over 世界 (pi ICU: col 13 = byte 19)
		editor.HandleInput("\x1b[1;5D") // Ctrl+Left
		wantCursor(t, editor, 0, 19)

		// Move left over world (pi ICU: col 8 = byte 14)
		editor.HandleInput("\x1b[1;5D")
		wantCursor(t, editor, 0, 14)

		// Move left over ，(pi ICU: col 7 = byte 11)
		editor.HandleInput("\x1b[1;5D")
		wantCursor(t, editor, 0, 11)

		// Move left over 你好 (pi ICU: col 5 = byte 5)
		editor.HandleInput("\x1b[1;5D")
		wantCursor(t, editor, 0, 5)

		// Move left over hello (pi ICU: col 0)
		editor.HandleInput("\x1b[1;5D")
		wantCursor(t, editor, 0, 0) // start

		// Forward from start
		editor.HandleInput("\x1b[1;5C") // Ctrl+Right
		wantCursor(t, editor, 0, 5)     // after 'hello' (pi ICU: col 5)

		// Move right over 你好 (pi ICU: col 7 = byte 11)
		editor.HandleInput("\x1b[1;5C") // Ctrl+Right
		wantCursor(t, editor, 0, 11)

		// Move right over ，(pi ICU: col 8 = byte 14)
		editor.HandleInput("\x1b[1;5C") // Ctrl+Right
		wantCursor(t, editor, 0, 14)

		// Move right over world (pi ICU: col 13 = byte 19)
		editor.HandleInput("\x1b[1;5C") // Ctrl+Right
		wantCursor(t, editor, 0, 19)

		// Move right over 世界 (pi ICU: col 15 = byte 25)
		editor.HandleInput("\x1b[1;5C") // Ctrl+Right
		wantCursor(t, editor, 0, 25)    // end
	})
}

func TestEditorScrollIndicators(t *testing.T) {
	t.Run("centers scroll indicators on wide borders", func(t *testing.T) {
		width := 40
		editor := NewEditor(createTestTUI(width, 24), testEditorTheme(), nil)
		var lines []string
		for i := 0; i < 20; i++ {
			lines = append(lines, "line "+itoa(i))
		}
		editor.SetText(strings.Join(lines, "\n"))

		editor.Render(width)
		for i := 0; i < 10; i++ {
			editor.HandleInput("\x1b[A")
		}

		got := editor.Render(width)
		wantTop := strings.Repeat("─", 15) + " ↑ 9 more " + strings.Repeat("─", 15)
		wantBottom := strings.Repeat("─", 15) + " ↓ 4 more " + strings.Repeat("─", 15)
		if stripAnsi(got[0]) != wantTop {
			t.Errorf("top border = %q, want %q", stripAnsi(got[0]), wantTop)
		}
		if stripAnsi(got[len(got)-1]) != wantBottom {
			t.Errorf("bottom border = %q, want %q", stripAnsi(got[len(got)-1]), wantBottom)
		}
	})

	t.Run("keeps truncated scroll indicators within width and preserves their color (issue #6962)", func(t *testing.T) {
		width := 10
		borderColor := func(text string) string { return "\x1b[35m" + text + "\x1b[39m" }
		theme := testEditorTheme()
		theme.BorderColor = borderColor
		editor := NewEditor(createTestTUI(width, 24), theme, nil)

		var lineTexts []string
		for i := 0; i < 20; i++ {
			lineTexts = append(lineTexts, fmt.Sprintf("line %d", i))
		}
		editor.SetText(strings.Join(lineTexts, "\n"))

		// Render once to initialize wrapping, then move the cursor so content remains above and below the viewport.
		editor.Render(width)
		for i := 0; i < 10; i++ {
			editor.HandleInput("\x1b[A")
		}

		lines := editor.Render(width)
		topBorder := lines[0]
		bottomBorder := lines[len(lines)-1]

		if !strings.HasPrefix(stripAnsi(topBorder), "─── ↑") {
			t.Errorf("top border = %q, want prefix ─── ↑", stripAnsi(topBorder))
		}
		if !strings.HasPrefix(stripAnsi(bottomBorder), "─── ↓") {
			t.Errorf("bottom border = %q, want prefix ─── ↓", stripAnsi(bottomBorder))
		}
		if topBorder != borderColor(stripAnsi(topBorder)) {
			t.Errorf("top border color lost: %q", topBorder)
		}
		if bottomBorder != borderColor(stripAnsi(bottomBorder)) {
			t.Errorf("bottom border color lost: %q", bottomBorder)
		}
		for _, line := range lines {
			if w := VisibleWidth(line); w != width {
				t.Errorf("line exceeds width %d: %q (width %d)", width, line, w)
			}
		}
	})
}

func TestEditorGraphemeAwareTextWrapping(t *testing.T) {
	t.Run("wraps lines correctly when text contains wide emojis", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		width := 20

		// ✅ is 2 columns wide, so "Hello ✅ World" is 14 columns
		editor.SetText("Hello ✅ World")
		lines := editor.Render(width)

		// All content lines (between borders) should fit within width
		for i := 1; i < len(lines)-1; i++ {
			lineWidth := VisibleWidth(lines[i])
			if lineWidth != width {
				t.Errorf("Line %d has width %d, expected %d", i, lineWidth, width)
			}
		}
	})

	t.Run("wraps long text with emojis at correct positions", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		width := 10

		// Each ✅ is 2 columns. "✅✅✅✅✅" = 10 columns, fits exactly
		// "✅✅✅✅✅✅" = 12 columns, needs wrap
		editor.SetText("✅✅✅✅✅✅")
		lines := editor.Render(width)

		// Should have 2 content lines (plus 2 border lines)
		for i := 1; i < len(lines)-1; i++ {
			lineWidth := VisibleWidth(lines[i])
			if lineWidth != width {
				t.Errorf("Line %d has width %d, expected %d", i, lineWidth, width)
			}
		}
	})

	t.Run("renders isolated Thai and Lao AM clusters without width drift", func(t *testing.T) {
		for _, text := range []string{"ำabc", "ຳabc"} {
			editor := newTestEditor(80, 24, nil)
			width := 8
			editor.SetText(text)

			for _, line := range editor.Render(width) {
				if w := VisibleWidth(line); w != width {
					t.Errorf("line width drift for %q: %q (width %d)", text, line, w)
				}
			}
		}
	})

	t.Run("wraps CJK characters correctly (each is 2 columns wide)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		width := 10 + 1 // +1 col reserved for cursor

		// Each CJK char is 2 columns. "日本語テスト" = 6 chars = 12 columns
		editor.SetText("日本語テスト")
		lines := editor.Render(width)

		for i := 1; i < len(lines)-1; i++ {
			lineWidth := VisibleWidth(lines[i])
			if lineWidth != width {
				t.Errorf("Line %d has width %d, expected %d", i, lineWidth, width)
			}
		}

		// Verify content split correctly
		var contentLines []string
		for _, l := range lines[1 : len(lines)-1] {
			contentLines = append(contentLines, strings.TrimSpace(stripAnsi(l)))
		}
		if len(contentLines) != 2 {
			t.Fatalf("content lines = %d, want 2 (%v)", len(contentLines), contentLines)
		}
		if contentLines[0] != "日本語テス" { // 5 chars = 10 columns
			t.Errorf("contentLines[0] = %q", contentLines[0])
		}
		if contentLines[1] != "ト" { // 1 char = 2 columns (+ padding)
			t.Errorf("contentLines[1] = %q", contentLines[1])
		}
	})

	t.Run("handles mixed ASCII and wide characters in wrapping", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		width := 15 + 1 // +1 col reserved for cursor

		// "Test ✅ OK 日本" = 15 columns (fits in width-1=15)
		editor.SetText("Test ✅ OK 日本")
		lines := editor.Render(width)

		// Should fit in one content line
		contentLines := lines[1 : len(lines)-1]
		if len(contentLines) != 1 {
			t.Fatalf("content lines = %d, want 1", len(contentLines))
		}

		if lineWidth := VisibleWidth(contentLines[0]); lineWidth != width {
			t.Errorf("line width = %d, want %d", lineWidth, width)
		}
	})

	t.Run("renders cursor correctly on wide characters", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		width := 20

		editor.SetText("A✅B")
		// Cursor should be at end (after B)
		lines := editor.Render(width)

		// The cursor (reverse video space) should be visible
		contentLine := lines[1]
		if !strings.Contains(contentLine, "\x1b[7m") {
			t.Error("Should have reverse video cursor")
		}

		// Line should still be correct width
		if w := VisibleWidth(contentLine); w != width {
			t.Errorf("line width = %d, want %d", w, width)
		}
	})

	t.Run("does not exceed terminal width with emoji at wrap boundary", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		width := 11

		// "0123456789✅" = 10 ASCII + 2-wide emoji = 12 columns
		// Should wrap before the emoji since it would exceed width
		editor.SetText("0123456789✅")
		lines := editor.Render(width)

		for i := 1; i < len(lines)-1; i++ {
			lineWidth := VisibleWidth(lines[i])
			if lineWidth > width {
				t.Errorf("Line %d has width %d, exceeds max %d", i, lineWidth, width)
			}
		}
	})

	t.Run("shows cursor at end of line before wrap, wraps on next char", func(t *testing.T) {
		width := 10
		for _, paddingX := range []int{0, 1} {
			editor := NewEditor(createTestTUI(width+paddingX, 24), testEditorTheme(), &EditorOptions{PaddingX: paddingX})

			// Type 9 chars → fills layoutWidth exactly, cursor at end on same line
			for _, ch := range []string{"a", "a", "a", "a", "a", "a", "a", "a", "a"} {
				editor.HandleInput(ch)
			}
			lines := editor.Render(width + paddingX)
			contentLines := lines[1 : len(lines)-1]
			if len(contentLines) != 1 {
				t.Fatalf("paddingX=%d: %d content lines before wrap, want 1", paddingX, len(contentLines))
			}
			if !strings.HasSuffix(contentLines[0], "\x1b[7m \x1b[0m") {
				t.Errorf("paddingX=%d: cursor should be at end of line", paddingX)
			}

			// Type 1 more → text wraps to second line
			editor.HandleInput("a")
			lines = editor.Render(width + paddingX)
			contentLines = lines[1 : len(lines)-1]
			if len(contentLines) != 2 {
				t.Errorf("paddingX=%d: should wrap to 2 content lines, got %d", paddingX, len(contentLines))
			}
		}
	})
}

func TestEditorKillRing(t *testing.T) {
	t.Run("Ctrl+W saves deleted text to kill ring and Ctrl+Y yanks it", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("foo bar baz")
		editor.HandleInput("\x17") // Ctrl+W - deletes "baz"
		wantText(t, editor, "foo bar ")

		// Move to beginning and yank
		editor.HandleInput("\x01") // Ctrl+A
		editor.HandleInput("\x19") // Ctrl+Y
		wantText(t, editor, "bazfoo bar ")
	})

	t.Run("Ctrl+U saves deleted text to kill ring", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world")
		// Move cursor to middle
		editor.HandleInput("\x01") // Ctrl+A (start)
		for i := 0; i < 6; i++ {
			editor.HandleInput("\x1b[C") // Right 6 times
		}

		editor.HandleInput("\x15") // Ctrl+U - deletes "hello "
		wantText(t, editor, "world")

		editor.HandleInput("\x19") // Ctrl+Y
		wantText(t, editor, "hello world")
	})

	t.Run("Ctrl+K saves deleted text to kill ring", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world")
		editor.HandleInput("\x01") // Ctrl+A (start)
		editor.HandleInput("\x0b") // Ctrl+K - deletes "hello world"

		wantText(t, editor, "")

		editor.HandleInput("\x19") // Ctrl+Y
		wantText(t, editor, "hello world")
	})

	t.Run("Ctrl+Y does nothing when kill ring is empty", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("test")
		editor.HandleInput("\x19") // Ctrl+Y
		wantText(t, editor, "test")
	})

	t.Run("Alt+Y cycles through kill ring after Ctrl+Y", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// Create kill ring with multiple entries
		editor.SetText("first")
		editor.HandleInput("\x17") // Ctrl+W - deletes "first"
		editor.SetText("second")
		editor.HandleInput("\x17") // Ctrl+W - deletes "second"
		editor.SetText("third")
		editor.HandleInput("\x17") // Ctrl+W - deletes "third"

		// Kill ring now has: [first, second, third]
		wantText(t, editor, "")

		editor.HandleInput("\x19") // Ctrl+Y - yanks "third" (most recent)
		wantText(t, editor, "third")

		editor.HandleInput("\x1by") // Alt+Y - cycles to "second"
		wantText(t, editor, "second")

		editor.HandleInput("\x1by") // Alt+Y - cycles to "first"
		wantText(t, editor, "first")

		editor.HandleInput("\x1by") // Alt+Y - cycles back to "third"
		wantText(t, editor, "third")
	})

	t.Run("Alt+Y does nothing if not preceded by yank", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("test")
		editor.HandleInput("\x17") // Ctrl+W - deletes "test"
		editor.SetText("other")

		// Type something to break the yank chain
		editor.HandleInput("x")
		wantText(t, editor, "otherx")

		// Alt+Y should do nothing
		editor.HandleInput("\x1by") // Alt+Y
		wantText(t, editor, "otherx")
	})

	t.Run("Alt+Y does nothing if kill ring has <=1 entry", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("only")
		editor.HandleInput("\x17") // Ctrl+W - deletes "only"

		editor.HandleInput("\x19") // Ctrl+Y - yanks "only"
		wantText(t, editor, "only")

		editor.HandleInput("\x1by") // Alt+Y - should do nothing (only 1 entry)
		wantText(t, editor, "only")
	})

	t.Run("consecutive Ctrl+W accumulates into one kill ring entry", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("one two three")
		editor.HandleInput("\x17") // Ctrl+W - deletes "three"
		editor.HandleInput("\x17") // Ctrl+W - deletes "two " (prepended)
		editor.HandleInput("\x17") // Ctrl+W - deletes "one " (prepended)

		wantText(t, editor, "")

		// Should be one combined entry
		editor.HandleInput("\x19") // Ctrl+Y
		wantText(t, editor, "one two three")
	})

	t.Run("Ctrl+U accumulates multiline deletes including newlines", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// Start with multiline text, cursor at end
		editor.SetText("line1\nline2\nline3")

		// Delete "line3"
		editor.HandleInput("\x15") // Ctrl+U
		wantText(t, editor, "line1\nline2\n")

		// Delete newline (at start of empty line 2, merges with line1)
		editor.HandleInput("\x15") // Ctrl+U
		wantText(t, editor, "line1\nline2")

		// Delete "line2"
		editor.HandleInput("\x15") // Ctrl+U
		wantText(t, editor, "line1\n")

		// Delete newline
		editor.HandleInput("\x15") // Ctrl+U
		wantText(t, editor, "line1")

		// Delete "line1"
		editor.HandleInput("\x15") // Ctrl+U
		wantText(t, editor, "")

		// All deletions accumulated into one entry: "line1\nline2\nline3"
		editor.HandleInput("\x19") // Ctrl+Y
		wantText(t, editor, "line1\nline2\nline3")
	})

	t.Run("backward deletions prepend, forward deletions append during accumulation", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("prefix|suffix")
		// Position cursor at |
		editor.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 6; i++ {
			editor.HandleInput("\x1b[C") // Move right 6 times
		}

		editor.HandleInput("\x0b") // Ctrl+K - deletes "suffix" (forward)
		editor.HandleInput("\x0b") // Ctrl+K - deletes "|" (forward, appended)
		wantText(t, editor, "prefix")

		editor.HandleInput("\x19") // Ctrl+Y
		wantText(t, editor, "prefix|suffix")
	})

	t.Run("non-delete actions break kill accumulation", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// Delete "baz", then type "x" to break accumulation, then delete "x"
		editor.SetText("foo bar baz")
		editor.HandleInput("\x17") // Ctrl+W - deletes "baz"
		wantText(t, editor, "foo bar ")

		editor.HandleInput("x") // Typing breaks accumulation
		wantText(t, editor, "foo bar x")

		editor.HandleInput("\x17") // Ctrl+W - deletes "x" (separate entry, not accumulated)
		wantText(t, editor, "foo bar ")

		// Yank most recent - should be "x", not "xbaz"
		editor.HandleInput("\x19") // Ctrl+Y
		wantText(t, editor, "foo bar x")

		// Cycle to previous - should be "baz" (separate entry)
		editor.HandleInput("\x1by") // Alt+Y
		wantText(t, editor, "foo bar baz")
	})

	t.Run("non-yank actions break Alt+Y chain", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("first")
		editor.HandleInput("\x17") // Ctrl+W
		editor.SetText("second")
		editor.HandleInput("\x17") // Ctrl+W
		editor.SetText("")

		editor.HandleInput("\x19") // Ctrl+Y - yanks "second"
		wantText(t, editor, "second")

		editor.HandleInput("x") // Type breaks yank chain
		wantText(t, editor, "secondx")

		editor.HandleInput("\x1by") // Alt+Y - should do nothing
		wantText(t, editor, "secondx")
	})

	t.Run("kill ring rotation persists after cycling", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("first")
		editor.HandleInput("\x17") // deletes "first"
		editor.SetText("second")
		editor.HandleInput("\x17") // deletes "second"
		editor.SetText("third")
		editor.HandleInput("\x17") // deletes "third"
		editor.SetText("")

		// Ring: [first, second, third]

		editor.HandleInput("\x19")  // Ctrl+Y - yanks "third"
		editor.HandleInput("\x1by") // Alt+Y - cycles to "second", ring rotates

		// Now ring is: [third, first, second]
		wantText(t, editor, "second")

		// Do something else
		editor.HandleInput("x")
		editor.SetText("")

		// New yank should get "second" (now at end after rotation)
		editor.HandleInput("\x19") // Ctrl+Y
		wantText(t, editor, "second")
	})

	t.Run("consecutive deletions across lines coalesce into one entry", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// "1\n2\n3" with cursor at end, delete everything with Ctrl+W
		editor.SetText("1\n2\n3")
		editor.HandleInput("\x17") // Ctrl+W - deletes "3"
		wantText(t, editor, "1\n2\n")

		editor.HandleInput("\x17") // Ctrl+W - deletes newline (merge with prev line)
		wantText(t, editor, "1\n2")

		editor.HandleInput("\x17") // Ctrl+W - deletes "2"
		wantText(t, editor, "1\n")

		editor.HandleInput("\x17") // Ctrl+W - deletes newline
		wantText(t, editor, "1")

		editor.HandleInput("\x17") // Ctrl+W - deletes "1"
		wantText(t, editor, "")

		// All deletions should have accumulated into one entry
		editor.HandleInput("\x19") // Ctrl+Y
		wantText(t, editor, "1\n2\n3")
	})

	t.Run("Ctrl+K at line end deletes newline and coalesces", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// "ab" on line 1, "cd" on line 2, cursor at end of line 1
		editor.SetText("")
		editor.HandleInput("a")
		editor.HandleInput("b")
		editor.HandleInput("\n")
		editor.HandleInput("c")
		editor.HandleInput("d")
		// Move to end of first line
		editor.HandleInput("\x1b[A") // Up arrow
		editor.HandleInput("\x05")   // Ctrl+E - end of line

		// Now at end of "ab", Ctrl+K should delete newline (merge with "cd")
		editor.HandleInput("\x0b") // Ctrl+K - deletes newline
		wantText(t, editor, "abcd")

		// Continue deleting
		editor.HandleInput("\x0b") // Ctrl+K - deletes "cd"
		wantText(t, editor, "ab")

		// Both deletions should accumulate
		editor.HandleInput("\x19") // Ctrl+Y
		wantText(t, editor, "ab\ncd")
	})

	t.Run("handles yank in middle of text", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("word")
		editor.HandleInput("\x17") // Ctrl+W - deletes "word"
		editor.SetText("hello world")

		// Move to middle (after "hello ")
		editor.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 6; i++ {
			editor.HandleInput("\x1b[C")
		}

		editor.HandleInput("\x19") // Ctrl+Y
		wantText(t, editor, "hello wordworld")
	})

	t.Run("handles yank-pop in middle of text", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// Create two kill ring entries
		editor.SetText("FIRST")
		editor.HandleInput("\x17") // Ctrl+W - deletes "FIRST"
		editor.SetText("SECOND")
		editor.HandleInput("\x17") // Ctrl+W - deletes "SECOND"

		// Ring: ["FIRST", "SECOND"]

		// Set up "hello world" and position cursor after "hello "
		editor.SetText("hello world")
		editor.HandleInput("\x01") // Ctrl+A - go to start of line
		for i := 0; i < 6; i++ {
			editor.HandleInput("\x1b[C") // Move right 6
		}

		// Yank "SECOND" in the middle
		editor.HandleInput("\x19") // Ctrl+Y
		wantText(t, editor, "hello SECONDworld")

		// Yank-pop replaces "SECOND" with "FIRST"
		editor.HandleInput("\x1by") // Alt+Y
		wantText(t, editor, "hello FIRSTworld")
	})

	t.Run("multiline yank and yank-pop in middle of text", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// Create single-line entry
		editor.SetText("SINGLE")
		editor.HandleInput("\x17") // Ctrl+W - deletes "SINGLE"

		// Create multiline entry via consecutive Ctrl+U
		editor.SetText("A\nB")
		editor.HandleInput("\x15") // Ctrl+U - deletes "B"
		editor.HandleInput("\x15") // Ctrl+U - deletes newline
		editor.HandleInput("\x15") // Ctrl+U - deletes "A"
		// Ring: ["SINGLE", "A\nB"]

		// Insert in middle of "hello world"
		editor.SetText("hello world")
		editor.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 6; i++ {
			editor.HandleInput("\x1b[C")
		}

		// Yank multiline "A\nB"
		editor.HandleInput("\x19") // Ctrl+Y
		wantText(t, editor, "hello A\nBworld")

		// Yank-pop replaces with "SINGLE"
		editor.HandleInput("\x1by") // Alt+Y
		wantText(t, editor, "hello SINGLEworld")
	})

	t.Run("Alt+D deletes word forward and saves to kill ring", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world test")
		editor.HandleInput("\x01") // Ctrl+A - go to start

		editor.HandleInput("\x1bd") // Alt+D - deletes "hello"
		wantText(t, editor, " world test")

		editor.HandleInput("\x1bd") // Alt+D - deletes " world" (skips whitespace, then word)
		wantText(t, editor, " test")

		// Yank should get accumulated text
		editor.HandleInput("\x19") // Ctrl+Y
		wantText(t, editor, "hello world test")
	})

	t.Run("Alt+D at end of line deletes newline", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("line1\nline2")
		// Move to start of document, then to end of first line
		editor.HandleInput("\x1b[A") // Up arrow - go to first line
		editor.HandleInput("\x05")   // Ctrl+E - end of line

		editor.HandleInput("\x1bd") // Alt+D - deletes newline (merges lines)
		wantText(t, editor, "line1line2")

		editor.HandleInput("\x19") // Ctrl+Y
		wantText(t, editor, "line1\nline2")
	})
}

func TestEditorUndo(t *testing.T) {
	const undoKey = "\x1b[45;5u" // Ctrl+- (undo)

	t.Run("does nothing when undo stack is empty", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.HandleInput(undoKey)
		wantText(t, editor, "")
	})

	t.Run("coalesces consecutive word characters into one undo unit", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		for _, ch := range []string{"h", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d"} {
			editor.HandleInput(ch)
		}
		wantText(t, editor, "hello world")

		// Undo removes " world" (space captured state before it, so we restore to "hello")
		editor.HandleInput(undoKey)
		wantText(t, editor, "hello")

		// Undo removes "hello"
		editor.HandleInput(undoKey)
		wantText(t, editor, "")
	})

	t.Run("undoes spaces one at a time", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		for _, ch := range []string{"h", "e", "l", "l", "o", " ", " "} {
			editor.HandleInput(ch)
		}
		wantText(t, editor, "hello  ")

		editor.HandleInput(undoKey) // - removes second " "
		wantText(t, editor, "hello ")

		editor.HandleInput(undoKey) // - removes first " "
		wantText(t, editor, "hello")

		editor.HandleInput(undoKey) // - removes "hello"
		wantText(t, editor, "")
	})

	t.Run("undoes newlines and signals next word to capture state", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		for _, ch := range []string{"h", "e", "l", "l", "o", "\n", "w", "o", "r", "l", "d"} {
			editor.HandleInput(ch)
		}
		wantText(t, editor, "hello\nworld")

		editor.HandleInput(undoKey)
		wantText(t, editor, "hello\n")

		editor.HandleInput(undoKey)
		wantText(t, editor, "hello")

		editor.HandleInput(undoKey)
		wantText(t, editor, "")
	})

	t.Run("undoes backspace", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		for _, ch := range []string{"h", "e", "l", "l", "o"} {
			editor.HandleInput(ch)
		}
		editor.HandleInput("\x7f") // Backspace
		wantText(t, editor, "hell")

		editor.HandleInput(undoKey)
		wantText(t, editor, "hello")
	})

	t.Run("undoes forward delete", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		for _, ch := range []string{"h", "e", "l", "l", "o"} {
			editor.HandleInput(ch)
		}
		editor.HandleInput("\x01")    // Ctrl+A - go to start
		editor.HandleInput("\x1b[C")  // Right arrow
		editor.HandleInput("\x1b[3~") // Delete key
		wantText(t, editor, "hllo")

		editor.HandleInput(undoKey)
		wantText(t, editor, "hello")
	})

	t.Run("undoes Ctrl+W (delete word backward)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		for _, ch := range []string{"h", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d"} {
			editor.HandleInput(ch)
		}
		wantText(t, editor, "hello world")

		editor.HandleInput("\x17") // Ctrl+W
		wantText(t, editor, "hello ")

		editor.HandleInput(undoKey)
		wantText(t, editor, "hello world")
	})

	t.Run("undoes Ctrl+K (delete to line end)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		for _, ch := range []string{"h", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d"} {
			editor.HandleInput(ch)
		}
		editor.HandleInput("\x01") // Ctrl+A - go to start
		for i := 0; i < 6; i++ {
			editor.HandleInput("\x1b[C")
		}

		editor.HandleInput("\x0b") // Ctrl+K
		wantText(t, editor, "hello ")

		editor.HandleInput(undoKey)
		wantText(t, editor, "hello world")

		editor.HandleInput("|")
		wantText(t, editor, "hello |world")
	})

	t.Run("undoes Ctrl+U (delete to line start)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		for _, ch := range []string{"h", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d"} {
			editor.HandleInput(ch)
		}
		editor.HandleInput("\x01") // Ctrl+A - go to start
		for i := 0; i < 6; i++ {
			editor.HandleInput("\x1b[C")
		}

		editor.HandleInput("\x15") // Ctrl+U
		wantText(t, editor, "world")

		editor.HandleInput(undoKey)
		wantText(t, editor, "hello world")
	})

	t.Run("undoes yank", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		for _, ch := range []string{"h", "e", "l", "l", "o", " "} {
			editor.HandleInput(ch)
		}
		editor.HandleInput("\x17") // Ctrl+W - delete "hello "
		editor.HandleInput("\x19") // Ctrl+Y - yank
		wantText(t, editor, "hello ")

		editor.HandleInput(undoKey)
		wantText(t, editor, "")
	})

	t.Run("undoes single-line paste atomically", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world")
		editor.HandleInput("\x01") // Ctrl+A - go to start
		for i := 0; i < 5; i++ {
			editor.HandleInput("\x1b[C") // Move right 5 (after "hello", before space)
		}

		// Simulate bracketed paste of "beep boop"
		editor.HandleInput("\x1b[200~beep boop\x1b[201~")
		wantText(t, editor, "hellobeep boop world")

		// Single undo should restore entire pre-paste state
		editor.HandleInput(undoKey)
		wantText(t, editor, "hello world")

		editor.HandleInput("|")
		wantText(t, editor, "hello| world")
	})

	t.Run("decodes CSI-u Ctrl+letter sequences inside bracketed paste (tmux popup)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// tmux popups with extended-keys-format=csi-u re-encode \n in pastes as
		// \x1b[106;5u (Ctrl+J). See issue #3599.
		editor.HandleInput("\x1b[200~line1\x1b[106;5uline2\x1b[106;5uline3\x1b[201~")
		wantText(t, editor, "line1\nline2\nline3")
	})

	t.Run("undoes multi-line paste atomically", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world")
		editor.HandleInput("\x01") // Ctrl+A - go to start
		for i := 0; i < 5; i++ {
			editor.HandleInput("\x1b[C")
		}

		// Simulate bracketed paste of multi-line text
		editor.HandleInput("\x1b[200~line1\nline2\nline3\x1b[201~")
		wantText(t, editor, "helloline1\nline2\nline3 world")

		// Single undo should restore entire pre-paste state
		editor.HandleInput(undoKey)
		wantText(t, editor, "hello world")

		editor.HandleInput("|")
		wantText(t, editor, "hello| world")
	})

	t.Run("undoes insertTextAtCursor atomically", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world")
		editor.HandleInput("\x01") // Ctrl+A - go to start
		for i := 0; i < 5; i++ {
			editor.HandleInput("\x1b[C")
		}

		// Programmatic insertion (e.g., clipboard image path)
		editor.InsertTextAtCursor("/tmp/image.png")
		wantText(t, editor, "hello/tmp/image.png world")

		// Single undo should restore entire pre-insert state
		editor.HandleInput(undoKey)
		wantText(t, editor, "hello world")

		editor.HandleInput("|")
		wantText(t, editor, "hello| world")
	})

	t.Run("insertTextAtCursor handles multiline text", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world")
		editor.HandleInput("\x01") // Ctrl+A - go to start
		for i := 0; i < 5; i++ {
			editor.HandleInput("\x1b[C")
		}

		// Insert multiline text
		editor.InsertTextAtCursor("line1\nline2\nline3")
		wantText(t, editor, "helloline1\nline2\nline3 world")

		// Cursor should be at end of inserted text (after "line3", before " world")
		wantCursor(t, editor, 2, 5) // "line3".length

		// Single undo should restore entire pre-insert state
		editor.HandleInput(undoKey)
		wantText(t, editor, "hello world")
	})

	t.Run("insertTextAtCursor normalizes CRLF and CR line endings", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("")

		// Insert text with CRLF
		editor.InsertTextAtCursor("a\r\nb\r\nc")
		wantText(t, editor, "a\nb\nc")

		editor.HandleInput(undoKey) // Undo
		wantText(t, editor, "")

		// Insert text with CR only
		editor.InsertTextAtCursor("x\ry\rz")
		wantText(t, editor, "x\ny\nz")
	})

	t.Run("undoes setText to empty string", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		for _, ch := range []string{"h", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d"} {
			editor.HandleInput(ch)
		}
		wantText(t, editor, "hello world")

		editor.SetText("")
		wantText(t, editor, "")

		editor.HandleInput(undoKey)
		wantText(t, editor, "hello world")
	})

	t.Run("clears undo stack on submit", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		submitted := ""
		editor.OnSubmit = func(text string) {
			submitted = text
		}

		for _, ch := range []string{"h", "e", "l", "l", "o"} {
			editor.HandleInput(ch)
		}
		editor.HandleInput("\r") // Enter - submit

		if submitted != "hello" {
			t.Errorf("submitted = %q, want hello", submitted)
		}
		wantText(t, editor, "")

		// Undo should do nothing - stack was cleared
		editor.HandleInput(undoKey)
		wantText(t, editor, "")
	})

	t.Run("exits history browsing mode on undo", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// Add "hello" to history
		editor.AddToHistory("hello")
		wantText(t, editor, "")

		// Type "world"
		for _, ch := range []string{"w", "o", "r", "l", "d"} {
			editor.HandleInput(ch)
		}
		wantText(t, editor, "world")

		// Ctrl+W - delete word
		editor.HandleInput("\x17") // Ctrl+W
		wantText(t, editor, "")

		// Press Up - enter history browsing, shows "hello"
		editor.HandleInput("\x1b[A") // Up arrow
		wantText(t, editor, "hello")

		// Undo should restore to "" (state before entering history browsing)
		editor.HandleInput(undoKey)
		wantText(t, editor, "")

		// Undo again should restore to "world" (state before Ctrl+W)
		editor.HandleInput(undoKey)
		wantText(t, editor, "world")
	})

	t.Run("undo restores to pre-history state even after multiple history navigations", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// Add history entries
		editor.AddToHistory("first")
		editor.AddToHistory("second")
		editor.AddToHistory("third")

		// Type something
		for _, ch := range []string{"c", "u", "r", "r", "e", "n", "t"} {
			editor.HandleInput(ch)
		}
		wantText(t, editor, "current")

		// Clear editor
		editor.HandleInput("\x17") // Ctrl+W
		wantText(t, editor, "")

		// Navigate through history multiple times
		editor.HandleInput("\x1b[A") // Up - "third"
		wantText(t, editor, "third")
		editor.HandleInput("\x1b[A") // Up - "second"
		wantText(t, editor, "second")
		editor.HandleInput("\x1b[A") // Up - "first"
		wantText(t, editor, "first")

		// Undo should go back to "" (state before we started browsing), not intermediate states
		editor.HandleInput(undoKey)
		wantText(t, editor, "")

		// Another undo goes back to "current"
		editor.HandleInput(undoKey)
		wantText(t, editor, "current")
	})

	t.Run("cursor movement starts new undo unit", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		for _, ch := range []string{"h", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d"} {
			editor.HandleInput(ch)
		}
		wantText(t, editor, "hello world")

		// Move cursor left 5 (to after "hello ")
		for i := 0; i < 5; i++ {
			editor.HandleInput("\x1b[D")
		}

		// Type "lol" in the middle
		for _, ch := range []string{"l", "o", "l"} {
			editor.HandleInput(ch)
		}
		wantText(t, editor, "hello lolworld")

		// Undo should restore to "hello world" (before inserting "lol")
		editor.HandleInput(undoKey)
		wantText(t, editor, "hello world")

		editor.HandleInput("|")
		wantText(t, editor, "hello |world")
	})

	t.Run("no-op delete operations do not push undo snapshots", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		for _, ch := range []string{"h", "e", "l", "l", "o"} {
			editor.HandleInput(ch)
		}
		wantText(t, editor, "hello")

		// Delete word on empty - multiple times (should be no-ops)
		editor.HandleInput("\x17") // Ctrl+W - deletes "hello"
		wantText(t, editor, "")
		editor.HandleInput("\x17") // Ctrl+W - no-op (nothing to delete)
		editor.HandleInput("\x17") // Ctrl+W - no-op

		// Single undo should restore "hello"
		editor.HandleInput(undoKey)
		wantText(t, editor, "hello")
	})

	// pi: it("does not trigger autocomplete during single-line paste") —
	// ported in T2b together with the autocomplete subsection.
	t.Run("does not trigger autocomplete during single-line paste", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		var suggestionCalls atomic.Int32

		editor.SetAutocompleteProvider(&mockAutocompleteProvider{
			getSuggestions: func(ctx context.Context, lines []string, cursorLine, cursorCol int, options AutocompleteGetSuggestionsOptions) *AutocompleteSuggestions {
				suggestionCalls.Add(1)
				return nil
			},
		})
		editor.HandleInput("\x1b[200~look at @node_modules/react/index.js please\x1b[201~")

		wantText(t, editor, "look at @node_modules/react/index.js please")
		if got := suggestionCalls.Load(); got != 0 {
			t.Errorf("suggestionCalls = %d, want 0", got)
		}
		if editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be hidden")
		}
	})

	// pi: it("undoes autocomplete") — ported in T2b together with the
	// autocomplete subsection.
	t.Run("undoes autocomplete", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// Create a mock autocomplete provider
		editor.SetAutocompleteProvider(&mockAutocompleteProvider{
			getSuggestions: func(ctx context.Context, lines []string, cursorLine, cursorCol int, options AutocompleteGetSuggestionsOptions) *AutocompleteSuggestions {
				text := lines[cursorLine]
				prefix := text[:cursorCol]
				if prefix == "di" {
					return &AutocompleteSuggestions{
						Items:  []AutocompleteItem{{Value: "dist/", Label: "dist/"}},
						Prefix: "di",
					}
				}
				return nil
			},
		})

		// Type "di"
		editor.HandleInput("d")
		editor.HandleInput("i")
		wantText(t, editor, "di")

		// Press Tab to trigger autocomplete
		editor.HandleInput("\t")
		flushAutocomplete(t, editor)
		wantText(t, editor, "dist/")
		if editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be hidden")
		}

		// Undo should restore to "di"
		editor.HandleInput("\x1b[45;5u") // Ctrl+- (undo)
		wantText(t, editor, "di")
	})
}

func TestEditorCharacterJump(t *testing.T) {
	const jumpForwardKey = "\x1d"      // Ctrl+] (legacy sequence for ctrl+])
	const jumpBackwardKey = "\x1b\x1d" // Ctrl+Alt+] (ESC followed by Ctrl+])

	t.Run("jumps forward to first occurrence of character on same line", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world")
		editor.HandleInput("\x01") // Ctrl+A - go to start
		wantCursor(t, editor, 0, 0)

		editor.HandleInput(jumpForwardKey)
		editor.HandleInput("o") // Jump to first 'o'

		wantCursor(t, editor, 0, 4) // 'o' in "hello"
	})

	t.Run("jumps forward to next occurrence after cursor", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world")
		editor.HandleInput("\x01") // Ctrl+A - go to start
		// Move cursor to the 'o' in "hello" (col 4)
		for i := 0; i < 4; i++ {
			editor.HandleInput("\x1b[C")
		}
		wantCursor(t, editor, 0, 4)

		editor.HandleInput(jumpForwardKey)
		editor.HandleInput("o") // Jump to next 'o' (in "world")

		wantCursor(t, editor, 0, 7) // 'o' in "world"
	})

	t.Run("jumps forward across multiple lines", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("abc\ndef\nghi")
		// Cursor is at end (line 2, col 3). Move to line 0 via up arrows, then Ctrl+A
		editor.HandleInput("\x1b[A") // Up
		editor.HandleInput("\x1b[A") // Up - now on line 0
		editor.HandleInput("\x01")   // Ctrl+A - go to start of line
		wantCursor(t, editor, 0, 0)

		editor.HandleInput(jumpForwardKey)
		editor.HandleInput("g") // Jump to 'g' on line 3

		wantCursor(t, editor, 2, 0)
	})

	t.Run("jumps backward to first occurrence before cursor on same line", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world")
		// Cursor at end (col 11)
		wantCursor(t, editor, 0, 11)

		editor.HandleInput(jumpBackwardKey)
		editor.HandleInput("o") // Jump to last 'o' before cursor

		wantCursor(t, editor, 0, 7) // 'o' in "world"
	})

	t.Run("jumps backward across multiple lines", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("abc\ndef\nghi")
		// Cursor at end of line 3
		wantCursor(t, editor, 2, 3)

		editor.HandleInput(jumpBackwardKey)
		editor.HandleInput("a") // Jump to 'a' on line 1

		wantCursor(t, editor, 0, 0)
	})

	t.Run("does nothing when character is not found (forward)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world")
		editor.HandleInput("\x01") // Ctrl+A - go to start
		wantCursor(t, editor, 0, 0)

		editor.HandleInput(jumpForwardKey)
		editor.HandleInput("z") // 'z' doesn't exist

		wantCursor(t, editor, 0, 0) // Cursor unchanged
	})

	t.Run("does nothing when character is not found (backward)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world")
		// Cursor at end
		wantCursor(t, editor, 0, 11)

		editor.HandleInput(jumpBackwardKey)
		editor.HandleInput("z") // 'z' doesn't exist

		wantCursor(t, editor, 0, 11) // Cursor unchanged
	})

	t.Run("is case-sensitive", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("Hello World")
		editor.HandleInput("\x01") // Ctrl+A - go to start
		wantCursor(t, editor, 0, 0)

		// Search for lowercase 'h' - should not find it (only 'H' exists)
		editor.HandleInput(jumpForwardKey)
		editor.HandleInput("h")

		wantCursor(t, editor, 0, 0) // Cursor unchanged

		// Search for uppercase 'W' - should find it
		editor.HandleInput(jumpForwardKey)
		editor.HandleInput("W")

		wantCursor(t, editor, 0, 6) // 'W' in "World"
	})

	t.Run("cancels jump mode when Ctrl+] is pressed again", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world")
		editor.HandleInput("\x01") // Ctrl+A - go to start
		wantCursor(t, editor, 0, 0)

		editor.HandleInput(jumpForwardKey) // Ctrl+] - enter jump mode
		editor.HandleInput(jumpForwardKey) // Ctrl+] again - cancel

		// Type 'o' normally - should insert, not jump
		editor.HandleInput("o")
		wantText(t, editor, "ohello world")
	})

	t.Run("cancels jump mode on Escape and processes the Escape", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world")
		editor.HandleInput("\x01") // Ctrl+A - go to start
		wantCursor(t, editor, 0, 0)

		editor.HandleInput(jumpForwardKey) // Ctrl+] - enter jump mode
		editor.HandleInput("\x1b")         // Escape - cancel jump mode

		// Cursor should be unchanged (Escape itself doesn't move cursor in editor)
		wantCursor(t, editor, 0, 0)

		// Type 'o' normally - should insert, not jump
		editor.HandleInput("o")
		wantText(t, editor, "ohello world")
	})

	t.Run("cancels backward jump mode when Ctrl+Alt+] is pressed again", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world")
		// Cursor at end
		wantCursor(t, editor, 0, 11)

		editor.HandleInput(jumpBackwardKey) // Ctrl+Alt+] - enter backward jump mode
		editor.HandleInput(jumpBackwardKey) // Ctrl+Alt+] again - cancel

		// Type 'o' normally - should insert, not jump
		editor.HandleInput("o")
		wantText(t, editor, "hello worldo")
	})

	t.Run("searches for special characters", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("foo(bar) = baz;")
		editor.HandleInput("\x01") // Ctrl+A - go to start
		wantCursor(t, editor, 0, 0)

		// Jump to '('
		editor.HandleInput(jumpForwardKey)
		editor.HandleInput("(")

		wantCursor(t, editor, 0, 3)

		// Jump to '='
		editor.HandleInput(jumpForwardKey)
		editor.HandleInput("=")

		wantCursor(t, editor, 0, 9)
	})

	t.Run("handles empty text gracefully", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("")
		wantCursor(t, editor, 0, 0)

		editor.HandleInput(jumpForwardKey)
		editor.HandleInput("x")

		wantCursor(t, editor, 0, 0) // Cursor unchanged
	})

	t.Run("resets lastAction when jumping", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world")
		editor.HandleInput("\x01") // Ctrl+A - go to start

		// Type to set lastAction to "type-word"
		editor.HandleInput("x")
		wantText(t, editor, "xhello world")

		// Jump forward
		editor.HandleInput(jumpForwardKey)
		editor.HandleInput("o")

		// Type more - should start a new undo unit (lastAction was reset)
		editor.HandleInput("Y")
		wantText(t, editor, "xhellYo world")

		// Undo should only undo "Y", not "x" as well
		editor.HandleInput("\x1b[45;5u") // Ctrl+- (undo)
		wantText(t, editor, "xhello world")
	})
}

// positionCursor positions the cursor at a specific line and column
// (pi test helper in the sticky column describe).
func positionCursor(t *testing.T, editor *Editor, line, col int) {
	t.Helper()
	// Go to line 0 first
	for i := 0; i < 20; i++ {
		editor.HandleInput("\x1b[A")
	}
	// Go to target line
	for i := 0; i < line; i++ {
		editor.HandleInput("\x1b[B")
	}
	// Go to target col
	editor.HandleInput("\x01") // Ctrl+A
	for i := 0; i < col; i++ {
		editor.HandleInput("\x1b[C")
	}
}

func TestEditorStickyColumn(t *testing.T) {
	t.Run("preserves target column when moving up through a shorter line", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("2222222222x222\n\n1111111111_111111111111")

		// Position cursor on _ (line 2, col 10)
		wantCursor(t, editor, 2, 23) // At end
		editor.HandleInput("\x01")   // Ctrl+A - go to start of line
		for i := 0; i < 10; i++ {
			editor.HandleInput("\x1b[C") // Move right to col 10
		}
		wantCursor(t, editor, 2, 10)

		// Press Up - should move to empty line (col clamped to 0)
		editor.HandleInput("\x1b[A") // Up arrow
		wantCursor(t, editor, 1, 0)

		// Press Up again - should move to line 0 at col 10 (on 'x')
		editor.HandleInput("\x1b[A") // Up arrow
		wantCursor(t, editor, 0, 10)
	})

	t.Run("preserves target column when moving down through a shorter line", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("1111111111_111\n\n2222222222x222222222222")

		// Position cursor on _ (line 0, col 10)
		editor.HandleInput("\x1b[A") // Up to line 1
		editor.HandleInput("\x1b[A") // Up to line 0
		editor.HandleInput("\x01")   // Ctrl+A
		for i := 0; i < 10; i++ {
			editor.HandleInput("\x1b[C")
		}
		wantCursor(t, editor, 0, 10)

		// Press Down - should move to empty line (col clamped to 0)
		editor.HandleInput("\x1b[B") // Down arrow
		wantCursor(t, editor, 1, 0)

		// Press Down again - should move to line 2 at col 10 (on 'x')
		editor.HandleInput("\x1b[B") // Down arrow
		wantCursor(t, editor, 2, 10)
	})

	t.Run("resets sticky column on horizontal movement (left arrow)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("1234567890\n\n1234567890")

		// Start at line 2, col 5
		editor.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 5; i++ {
			editor.HandleInput("\x1b[C")
		}
		wantCursor(t, editor, 2, 5)

		// Move up through empty line
		editor.HandleInput("\x1b[A") // Up - line 1, col 0
		editor.HandleInput("\x1b[A") // Up - line 0, col 5 (sticky)
		wantCursor(t, editor, 0, 5)

		// Move left - resets sticky column
		editor.HandleInput("\x1b[D") // Left
		wantCursor(t, editor, 0, 4)

		// Move down twice
		editor.HandleInput("\x1b[B") // Down - line 1, col 0
		editor.HandleInput("\x1b[B") // Down - line 2, col 4 (new sticky from col 4)
		wantCursor(t, editor, 2, 4)
	})

	t.Run("resets sticky column on horizontal movement (right arrow)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("1234567890\n\n1234567890")

		// Start at line 0, col 5
		editor.HandleInput("\x1b[A") // Up to line 1
		editor.HandleInput("\x1b[A") // Up to line 0
		editor.HandleInput("\x01")   // Ctrl+A
		for i := 0; i < 5; i++ {
			editor.HandleInput("\x1b[C")
		}
		wantCursor(t, editor, 0, 5)

		// Move down through empty line
		editor.HandleInput("\x1b[B") // Down - line 1, col 0
		editor.HandleInput("\x1b[B") // Down - line 2, col 5 (sticky)
		wantCursor(t, editor, 2, 5)

		// Move right - resets sticky column
		editor.HandleInput("\x1b[C") // Right
		wantCursor(t, editor, 2, 6)

		// Move up twice
		editor.HandleInput("\x1b[A") // Up - line 1, col 0
		editor.HandleInput("\x1b[A") // Up - line 0, col 6 (new sticky from col 6)
		wantCursor(t, editor, 0, 6)
	})

	t.Run("resets sticky column on typing", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("1234567890\n\n1234567890")

		// Start at line 2, col 8
		editor.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 8; i++ {
			editor.HandleInput("\x1b[C")
		}

		// Move up through empty line
		editor.HandleInput("\x1b[A") // Up
		editor.HandleInput("\x1b[A") // Up - line 0, col 8
		wantCursor(t, editor, 0, 8)

		// Type a character - resets sticky column
		editor.HandleInput("X")
		wantCursor(t, editor, 0, 9)

		// Move down twice
		editor.HandleInput("\x1b[B") // Down - line 1, col 0
		editor.HandleInput("\x1b[B") // Down - line 2, col 9 (new sticky from col 9)
		wantCursor(t, editor, 2, 9)
	})

	t.Run("resets sticky column on backspace", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("1234567890\n\n1234567890")

		// Start at line 2, col 8
		editor.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 8; i++ {
			editor.HandleInput("\x1b[C")
		}

		// Move up through empty line
		editor.HandleInput("\x1b[A") // Up
		editor.HandleInput("\x1b[A") // Up - line 0, col 8
		wantCursor(t, editor, 0, 8)

		// Backspace - resets sticky column
		editor.HandleInput("\x7f") // Backspace
		wantCursor(t, editor, 0, 7)

		// Move down twice
		editor.HandleInput("\x1b[B") // Down - line 1, col 0
		editor.HandleInput("\x1b[B") // Down - line 2, col 7 (new sticky from col 7)
		wantCursor(t, editor, 2, 7)
	})

	t.Run("resets sticky column on Ctrl+A (move to line start)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("1234567890\n\n1234567890")

		// Start at line 2, col 8
		editor.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 8; i++ {
			editor.HandleInput("\x1b[C")
		}

		// Move up - establishes sticky col 8
		editor.HandleInput("\x1b[A") // Up - line 1, col 0

		// Ctrl+A - resets sticky column to 0
		editor.HandleInput("\x01") // Ctrl+A
		wantCursor(t, editor, 1, 0)

		// Move up
		editor.HandleInput("\x1b[A") // Up - line 0, col 0 (new sticky from col 0)
		wantCursor(t, editor, 0, 0)
	})

	t.Run("resets sticky column on Ctrl+E (move to line end)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("12345\n\n1234567890")

		// Start at line 2, col 3
		editor.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 3; i++ {
			editor.HandleInput("\x1b[C")
		}

		// Move up through empty line - establishes sticky col 3
		editor.HandleInput("\x1b[A") // Up - line 1, col 0
		editor.HandleInput("\x1b[A") // Up - line 0, col 3
		wantCursor(t, editor, 0, 3)

		// Ctrl+E - resets sticky column to end
		editor.HandleInput("\x05") // Ctrl+E
		wantCursor(t, editor, 0, 5)

		// Move down twice
		editor.HandleInput("\x1b[B") // Down - line 1, col 0
		editor.HandleInput("\x1b[B") // Down - line 2, col 5 (new sticky from col 5)
		wantCursor(t, editor, 2, 5)
	})

	t.Run("resets sticky column on word movement (Ctrl+Left)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world\n\nhello world")

		// Start at end of line 2 (col 11)
		wantCursor(t, editor, 2, 11)

		// Move up through empty line - establishes sticky col 11
		editor.HandleInput("\x1b[A") // Up - line 1, col 0
		editor.HandleInput("\x1b[A") // Up - line 0, col 11
		wantCursor(t, editor, 0, 11)

		// Ctrl+Left - word movement resets sticky column
		editor.HandleInput("\x1b[1;5D") // Ctrl+Left
		wantCursor(t, editor, 0, 6)     // Before "world"

		// Move down twice
		editor.HandleInput("\x1b[B") // Down - line 1, col 0
		editor.HandleInput("\x1b[B") // Down - line 2, col 6 (new sticky from col 6)
		wantCursor(t, editor, 2, 6)
	})

	t.Run("resets sticky column on word movement (Ctrl+Right)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("hello world\n\nhello world")

		// Start at line 0, col 0
		editor.HandleInput("\x1b[A") // Up
		editor.HandleInput("\x1b[A") // Up
		editor.HandleInput("\x01")   // Ctrl+A
		wantCursor(t, editor, 0, 0)

		// Move down through empty line - establishes sticky col 0
		editor.HandleInput("\x1b[B") // Down - line 1, col 0
		editor.HandleInput("\x1b[B") // Down - line 2, col 0
		wantCursor(t, editor, 2, 0)

		// Ctrl+Right - word movement resets sticky column
		editor.HandleInput("\x1b[1;5C") // Ctrl+Right
		wantCursor(t, editor, 2, 5)     // After "hello"

		// Move up twice
		editor.HandleInput("\x1b[A") // Up - line 1, col 0
		editor.HandleInput("\x1b[A") // Up - line 0, col 5 (new sticky from col 5)
		wantCursor(t, editor, 0, 5)
	})

	t.Run("resets sticky column on undo", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("1234567890\n\n1234567890")

		// Go to line 0, col 8
		editor.HandleInput("\x1b[A") // Up to line 1
		editor.HandleInput("\x1b[A") // Up to line 0
		editor.HandleInput("\x01")   // Ctrl+A
		for i := 0; i < 8; i++ {
			editor.HandleInput("\x1b[C")
		}
		wantCursor(t, editor, 0, 8)

		// Move down through empty line - establishes sticky col 8
		editor.HandleInput("\x1b[B") // Down - line 1, col 0
		editor.HandleInput("\x1b[B") // Down - line 2, col 8 (sticky)
		wantCursor(t, editor, 2, 8)

		// Type something to create undo state - this clears sticky and sets col to 9
		editor.HandleInput("X")
		wantText(t, editor, "1234567890\n\n12345678X90")
		wantCursor(t, editor, 2, 9)

		// Move up - establishes new sticky col 9
		editor.HandleInput("\x1b[A") // Up - line 1, col 0
		editor.HandleInput("\x1b[A") // Up - line 0, col 9
		wantCursor(t, editor, 0, 9)

		// Undo - resets sticky column and restores cursor to line 2, col 8
		editor.HandleInput("\x1b[45;5u") // Ctrl+- (undo)
		wantText(t, editor, "1234567890\n\n1234567890")
		wantCursor(t, editor, 2, 8)

		// Move up - should capture new sticky from restored col 8, not old col 9
		editor.HandleInput("\x1b[A") // Up - line 1, col 0
		editor.HandleInput("\x1b[A") // Up - line 0, col 8 (new sticky from restored position)
		wantCursor(t, editor, 0, 8)
	})

	t.Run("handles multiple consecutive up/down movements", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("1234567890\nab\ncd\nef\n1234567890")

		// Start at line 4, col 7
		editor.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 7; i++ {
			editor.HandleInput("\x1b[C")
		}
		wantCursor(t, editor, 4, 7)

		// Move up multiple times through short lines
		editor.HandleInput("\x1b[A") // Up - line 3, col 2 (clamped)
		editor.HandleInput("\x1b[A") // Up - line 2, col 2 (clamped)
		editor.HandleInput("\x1b[A") // Up - line 1, col 2 (clamped)
		editor.HandleInput("\x1b[A") // Up - line 0, col 7 (restored)
		wantCursor(t, editor, 0, 7)

		// Move down multiple times - sticky should still be 7
		editor.HandleInput("\x1b[B") // Down - line 1, col 2
		editor.HandleInput("\x1b[B") // Down - line 2, col 2
		editor.HandleInput("\x1b[B") // Down - line 3, col 2
		editor.HandleInput("\x1b[B") // Down - line 4, col 7 (restored)
		wantCursor(t, editor, 4, 7)
	})

	t.Run("moves correctly through wrapped visual lines without getting stuck", func(t *testing.T) {
		tui := createTestTUI(15, 24) // Narrow terminal
		editor := NewEditor(tui, testEditorTheme(), nil)

		// Line 0: short
		// Line 1: 30 chars = wraps to 3 visual lines at width 10 (after padding)
		editor.SetText("short\n123456789012345678901234567890")
		editor.Render(15) // This gives 14 layout width

		// Position at end of line 1 (col 30)
		wantCursor(t, editor, 1, 30)

		// Move up repeatedly - should traverse all visual lines of the wrapped text
		// and eventually reach line 0
		editor.HandleInput("\x1b[A") // Up - to previous visual line within line 1
		if got := editor.GetCursor().Line; got != 1 {
			t.Errorf("after 1st up: line = %d, want 1", got)
		}

		editor.HandleInput("\x1b[A") // Up - another visual line
		if got := editor.GetCursor().Line; got != 1 {
			t.Errorf("after 2nd up: line = %d, want 1", got)
		}

		editor.HandleInput("\x1b[A") // Up - should reach line 0
		if got := editor.GetCursor().Line; got != 0 {
			t.Errorf("after 3rd up: line = %d, want 0", got)
		}
	})

	t.Run("handles setText resetting sticky column", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetText("1234567890\n\n1234567890")

		// Establish sticky column
		editor.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 8; i++ {
			editor.HandleInput("\x1b[C")
		}
		editor.HandleInput("\x1b[A") // Up

		// setText should reset sticky column
		editor.SetText("abcdefghij\n\nabcdefghij")
		wantCursor(t, editor, 2, 10) // At end

		// Move up - should capture new sticky from current position (10)
		editor.HandleInput("\x1b[A") // Up - line 1, col 0
		editor.HandleInput("\x1b[A") // Up - line 0, col 10
		wantCursor(t, editor, 0, 10)
	})

	t.Run("sets preferredVisualCol when pressing right at end of prompt (last line)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// Line 0: 20 chars with 'x' at col 10
		// Line 1: empty
		// Line 2: 10 chars ending with '_'
		editor.SetText("111111111x1111111111\n\n333333333_")

		// Go to line 0, press Ctrl+E (end of line) - col 20
		editor.HandleInput("\x1b[A") // Up to line 1
		editor.HandleInput("\x1b[A") // Up to line 0
		editor.HandleInput("\x05")   // Ctrl+E - move to end of line
		wantCursor(t, editor, 0, 20)

		// Move down to line 2 - cursor clamped to col 10 (end of line)
		editor.HandleInput("\x1b[B") // Down to line 1, col 0
		editor.HandleInput("\x1b[B") // Down to line 2, col 10 (clamped)
		wantCursor(t, editor, 2, 10)

		// Press Right at end of prompt - nothing visible happens, but sets preferredVisualCol to 10
		editor.HandleInput("\x1b[C") // Right - can't move, but sets preferredVisualCol
		wantCursor(t, editor, 2, 10) // Still at same position

		// Move up twice to line 0 - should use preferredVisualCol (10) to land on 'x'
		editor.HandleInput("\x1b[A") // Up to line 1, col 0
		editor.HandleInput("\x1b[A") // Up to line 0, col 10 (on 'x')
		wantCursor(t, editor, 0, 10)
	})

	t.Run("handles editor resizes when preferredVisualCol is on the same line", func(t *testing.T) {
		// Create editor with wider terminal
		editor := newTestEditor(80, 24, nil)

		editor.SetText("12345678901234567890\n\n12345678901234567890")

		// Start at line 2, col 15
		editor.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 15; i++ {
			editor.HandleInput("\x1b[C")
		}

		// Move up through empty line - establishes sticky col 15
		editor.HandleInput("\x1b[A") // Up
		editor.HandleInput("\x1b[A") // Up - line 0, col 15
		wantCursor(t, editor, 0, 15)

		// Render with narrower width to simulate resize
		editor.Render(12) // Width 12

		// Move down - sticky should be clamped to new width
		editor.HandleInput("\x1b[B") // Down - line 1
		editor.HandleInput("\x1b[B") // Down - line 2, col should be clamped
		if got := editor.GetCursor().Col; got != 4 {
			t.Errorf("col = %d, want 4", got)
		}
	})

	t.Run("handles editor resizes when preferredVisualCol is on a different line", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// Create a line that wraps into multiple visual lines at width 10
		// "12345678901234567890" = 20 chars, wraps to 2 visual lines at width 10
		editor.SetText("short\n12345678901234567890")

		// Go to line 1, col 15
		editor.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 15; i++ {
			editor.HandleInput("\x1b[C")
		}
		wantCursor(t, editor, 1, 15)

		// Move up to establish sticky col 15
		editor.HandleInput("\x1b[A") // Up to line 0
		// Line 0 has only 5 chars, so cursor at col 5
		wantCursor(t, editor, 0, 5)

		// Narrow the editor
		editor.Render(10)

		// Move down - preferredVisualCol was 15, but width is 10
		// Should land on line 1, clamped to width (visual col 9, which is logical col 8)
		editor.HandleInput("\x1b[B") // Down to line 1
		wantCursor(t, editor, 1, 8)

		// Move up
		editor.HandleInput("\x1b[A") // Up - should go to line 0
		wantCursor(t, editor, 0, 5)  // Line 0 only has 5 chars

		// Restore the original width
		editor.Render(80)

		// Move down - preferredVisualCol was kept at 15
		editor.HandleInput("\x1b[B") // Down to line 1
		wantCursor(t, editor, 1, 15)
	})

	t.Run("rewrapped lines: target fits current visual column", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		editor.SetText("abcdefghijklmnopqr\n123456789012345678")

		positionCursor(t, editor, 0, 18)
		wantCursor(t, editor, 0, 18)

		// Narrow to width 10 (layoutWidth = 9).
		// Line 0 last segment has visual col max 9, line 1 first segment max 8
		editor.Render(10)

		// Move down: cursor clamps to 8
		editor.HandleInput("\x1b[B")
		wantCursor(t, editor, 1, 8)

		// Widen back. Move up, the current visual col wins
		editor.Render(80)
		editor.HandleInput("\x1b[A")
		wantCursor(t, editor, 0, 8)

		// Preferred was cleared by the rewrapped branch
		editor.HandleInput("\x1b[B")
		wantCursor(t, editor, 1, 8)
	})

	t.Run("rewrapped lines: target shorter than current visual column", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		editor.SetText("abcdefghijklmnopqr\n123456789012345678\nab")

		positionCursor(t, editor, 0, 18)
		wantCursor(t, editor, 0, 18)

		// Narrow to width 10 (layoutWidth = 9). Moving down clamps to col 8
		editor.Render(10)
		editor.HandleInput("\x1b[B")
		wantCursor(t, editor, 1, 8)

		// Widen the editor
		editor.Render(80)

		// Move down to short line "ab".
		// preferredVisualCol is replaced with current visual col (8), cursor clamps to 2
		editor.HandleInput("\x1b[B")
		wantCursor(t, editor, 2, 2)

		// Moving up restores to preferred col 8
		editor.HandleInput("\x1b[A")
		wantCursor(t, editor, 1, 8)
	})
}

// pasteMarkerRegexFor matches paste markers like "[paste #1 +20 lines]" in tests.
var testPasteMarkerRegex = regexp.MustCompile(`\[paste #\d+ \+\d+ lines\]`)

func findPasteMarker(t *testing.T, text string) string {
	t.Helper()
	m := testPasteMarkerRegex.FindString(text)
	if m == "" {
		t.Fatalf("no paste marker in %q", text)
	}
	return m
}

// pasteWithMarker simulates a large paste that creates a marker.
func pasteWithMarker(t *testing.T, editor *Editor) string {
	t.Helper()
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "line")
	}
	bigContent := strings.Join(lines, "\n")
	editor.HandleInput("\x1b[200~" + bigContent + "\x1b[201~")
	// The editor replaces large pastes with a marker like "[paste #1 +20 lines]"
	return editor.GetText()
}

// bigPaste returns 12-line paste content with a distinguishing tag.
func bigPaste(tag string) string {
	var lines []string
	for i := 0; i < 12; i++ {
		lines = append(lines, fmt.Sprintf("%s%d", tag, i))
	}
	return strings.Join(lines, "\n")
}

func TestEditorPasteMarkerAtomicBehavior(t *testing.T) {
	t.Run("creates a paste marker for large pastes", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		text := pasteWithMarker(t, editor)
		if !testPasteMarkerRegex.MatchString(text) {
			t.Errorf("no paste marker in %q", text)
		}
	})

	t.Run("treats paste marker as single unit for right arrow", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		editor.HandleInput("A")
		pasteWithMarker(t, editor)
		editor.HandleInput("B")
		// Text: "A[paste #1 +20 lines]B", cursor at end

		// Go to start
		editor.HandleInput("\x01") // Ctrl+A
		wantCursor(t, editor, 0, 0)

		// Right arrow: should move past "A"
		editor.HandleInput("\x1b[C")
		wantCursor(t, editor, 0, 1)

		// Right arrow: should skip the entire marker
		editor.HandleInput("\x1b[C")
		marker := findPasteMarker(t, editor.GetText())
		wantCursor(t, editor, 0, 1+len(marker))

		// Right arrow: should move past "B"
		editor.HandleInput("\x1b[C")
		wantCursor(t, editor, 0, 1+len(marker)+1)
	})

	t.Run("treats paste marker as single unit for left arrow", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		editor.HandleInput("A")
		pasteWithMarker(t, editor)
		editor.HandleInput("B")
		// Cursor at end

		// Left arrow: past "B"
		editor.HandleInput("\x1b[D")
		marker := findPasteMarker(t, editor.GetText())
		wantCursor(t, editor, 0, 1+len(marker))

		// Left arrow: skip the entire marker
		editor.HandleInput("\x1b[D")
		wantCursor(t, editor, 0, 1)

		// Left arrow: past "A"
		editor.HandleInput("\x1b[D")
		wantCursor(t, editor, 0, 0)
	})

	t.Run("treats paste marker as single unit for backspace", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		editor.HandleInput("A")
		pasteWithMarker(t, editor)
		editor.HandleInput("B")

		marker := findPasteMarker(t, editor.GetText())

		// Position cursor right after the marker (before "B")
		editor.HandleInput("\x01")   // Ctrl+A
		editor.HandleInput("\x1b[C") // past "A"
		editor.HandleInput("\x1b[C") // past marker
		wantCursor(t, editor, 0, 1+len(marker))

		// Backspace: should delete the entire marker at once
		editor.HandleInput("\x7f")
		wantText(t, editor, "AB")
		wantCursor(t, editor, 0, 1)
	})

	t.Run("treats paste marker as single unit for forward delete", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		editor.HandleInput("A")
		pasteWithMarker(t, editor)
		editor.HandleInput("B")

		// Position cursor on "A" (col 0) then move right once to be just before marker
		editor.HandleInput("\x01")   // Ctrl+A
		editor.HandleInput("\x1b[C") // past "A", now at col 1 (start of marker)

		// Forward delete: should delete the entire marker at once
		editor.HandleInput("\x1b[3~") // Delete key
		wantText(t, editor, "AB")
		wantCursor(t, editor, 0, 1)
	})

	t.Run("treats paste marker as single unit for word movement", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		editor.HandleInput("X")
		editor.HandleInput(" ")
		pasteWithMarker(t, editor)
		editor.HandleInput(" ")
		editor.HandleInput("Y")
		// Text: "X [paste #1 +20 lines] Y"

		marker := findPasteMarker(t, editor.GetText())

		// Go to start
		editor.HandleInput("\x01") // Ctrl+A

		// Ctrl+Right: skip "X"
		editor.HandleInput("\x1b[1;5C")
		wantCursor(t, editor, 0, 1)

		// Ctrl+Right: skip whitespace + marker (marker treated as single non-ws, non-punct unit)
		editor.HandleInput("\x1b[1;5C")
		wantCursor(t, editor, 0, 2+len(marker))
	})

	t.Run("undo restores marker after backspace deletion", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		editor.HandleInput("A")
		pasteWithMarker(t, editor)
		editor.HandleInput("B")

		textBefore := editor.GetText()

		// Position after marker
		editor.HandleInput("\x01")
		editor.HandleInput("\x1b[C") // past A
		editor.HandleInput("\x1b[C") // past marker

		// Delete marker
		editor.HandleInput("\x7f")
		wantText(t, editor, "AB")

		// Undo
		editor.HandleInput("\x1b[45;5u")
		wantText(t, editor, textBefore)
	})

	t.Run("undo after paste marker deletion restores the paste registry", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		submitted := ""
		editor.OnSubmit = func(text string) {
			submitted = text
		}

		paste := bigPaste("alpha")
		editor.HandleInput("\x1b[200~" + paste + "\x1b[201~")
		editor.HandleInput("\x7f")       // delete the marker
		editor.HandleInput("\x1b[45;5u") // undo: restores marker text and registry
		editor.HandleInput("\r")
		if submitted != paste {
			t.Errorf("submitted = %q, want paste", submitted)
		}
	})

	t.Run("undo after deleting the first of two paste markers restores both registry entries", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		submitted := ""
		editor.OnSubmit = func(text string) {
			submitted = text
		}

		pasteA := bigPaste("alpha")
		pasteB := bigPaste("beta")
		editor.HandleInput("\x1b[200~" + pasteA + "\x1b[201~") // #1 = A
		editor.HandleInput("\x1b[200~" + pasteB + "\x1b[201~") // #2 = B, cursor at end
		editor.HandleInput("\x01")                             // Ctrl+A
		editor.HandleInput("\x1b[C")                           // right over marker #1
		editor.HandleInput("\x7f")                             // delete marker #1, renumbers #2 -> #1
		editor.HandleInput("\x1b[45;5u")                       // undo
		editor.HandleInput("\r")
		if submitted != pasteA+pasteB {
			t.Errorf("submitted = %q, want pasteA+pasteB", submitted)
		}
	})

	t.Run("renumbers the paste registry in ascending id order when markers are out of order in text", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		submitted := ""
		editor.OnSubmit = func(text string) {
			submitted = text
		}

		pasteA := bigPaste("alpha")
		pasteB := bigPaste("beta")
		pasteC := bigPaste("gamma")
		editor.HandleInput("\x1b[200~" + pasteA + "\x1b[201~") // #1 = A
		editor.HandleInput("\x01")                             // Ctrl+A
		editor.HandleInput("\x1b[200~" + pasteB + "\x1b[201~") // #2 = B, text: [#2][#1]
		editor.HandleInput("\x01")                             // Ctrl+A
		editor.HandleInput("\x1b[200~" + pasteC + "\x1b[201~") // #3 = C, text: [#3][#2][#1]
		editor.HandleInput("\x05")                             // Ctrl+E
		editor.HandleInput("\x7f")                             // delete marker #1, renumber #3 -> #2 and #2 -> #1
		editor.HandleInput("\r")
		if submitted != pasteC+pasteB {
			t.Errorf("submitted = %q, want pasteC+pasteB", submitted)
		}
	})

	t.Run("undo after setText restores paste markers and registry", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		submitted := ""
		editor.OnSubmit = func(text string) {
			submitted = text
		}

		paste := bigPaste("alpha")
		editor.HandleInput("\x1b[200~" + paste + "\x1b[201~")
		editor.SetText("replacement")
		editor.HandleInput("\x1b[45;5u") // undo
		editor.HandleInput("\r")
		if submitted != paste {
			t.Errorf("submitted = %q, want paste", submitted)
		}
	})

	t.Run("handles multiple paste markers in same line", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		pasteWithMarker(t, editor)
		editor.HandleInput(" ")
		pasteWithMarker(t, editor)

		markers := testPasteMarkerRegex.FindAllString(editor.GetText(), -1)
		if len(markers) != 2 {
			t.Fatalf("markers = %d, want 2", len(markers))
		}

		// Go to start
		editor.HandleInput("\x01")

		// Right arrow: should skip first marker atomically
		editor.HandleInput("\x1b[C")
		wantCursor(t, editor, 0, len(markers[0]))

		// Right arrow: past space
		editor.HandleInput("\x1b[C")
		wantCursor(t, editor, 0, len(markers[0])+1)

		// Right arrow: should skip second marker atomically
		editor.HandleInput("\x1b[C")
		wantCursor(t, editor, 0, len(markers[0])+1+len(markers[1]))
	})

	t.Run("does not treat manually typed marker-like text as atomic (no valid paste ID)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		// Type text that matches the pattern but was typed manually (no paste entry)
		fakeMarker := "[paste #99 +5 lines]"
		for _, ch := range fakeMarker {
			editor.HandleInput(string(ch))
		}

		wantText(t, editor, fakeMarker)

		// No paste with ID 99 exists, so the marker is NOT treated atomically.
		// Right arrow should move one grapheme at a time.
		editor.HandleInput("\x01")   // Ctrl+A
		editor.HandleInput("\x1b[C") // Right
		wantCursor(t, editor, 0, 1)  // Just past "["
	})

	t.Run("does not crash when paste marker is wider than terminal width", func(t *testing.T) {
		// Reproduce: terminal width 8, paste marker "[paste #1 +47 lines]" (21 chars)
		editor := newTestEditor(80, 24, nil)
		var lines []string
		for i := 0; i < 47; i++ {
			lines = append(lines, "line")
		}
		bigContent := strings.Join(lines, "\n")
		editor.HandleInput("\x1b[200~" + bigContent + "\x1b[201~")

		marker := findPasteMarker(t, editor.GetText())
		if VisibleWidth(marker) <= 8 {
			t.Fatal("marker should be wider than render width")
		}

		// Render at very narrow width - should not throw
		renderLines := editor.Render(8)
		for _, line := range renderLines {
			if w := VisibleWidth(line); w > 8 {
				t.Errorf("line exceeds width 8: visible=%d text=%q", w, line)
			}
		}
	})

	t.Run("does not crash when text + paste marker exceeds terminal width with cursor on marker", func(t *testing.T) {
		// Reproduce: terminal width 54, text "b".repeat(35) + "[paste #1 +27 lines]" + "bbbb"
		editor := newTestEditor(80, 24, nil)

		// Type 35 'b' characters
		for i := 0; i < 35; i++ {
			editor.HandleInput("b")
		}

		// Paste 27 lines
		var lines []string
		for i := 0; i < 27; i++ {
			lines = append(lines, "line")
		}
		bigContent := strings.Join(lines, "\n")
		editor.HandleInput("\x1b[200~" + bigContent + "\x1b[201~")

		// Type a few more characters
		for i := 0; i < 4; i++ {
			editor.HandleInput("b")
		}

		// Move cursor left to land on the paste marker
		for i := 0; i < 5; i++ {
			editor.HandleInput("\x1b[D")
		}

		// Render at width 54 - should not throw
		renderWidth := 54
		renderLines := editor.Render(renderWidth)
		for _, line := range renderLines {
			if w := VisibleWidth(line); w > renderWidth {
				t.Errorf("line exceeds width %d: visible=%d text=%q", renderWidth, w, line)
			}
		}
	})

	t.Run("wordWrapLine re-checks overflow after backtracking to wrap opportunity", func(t *testing.T) {
		// Reproduce crash #2: " " + "b".repeat(35) + atomic_marker(20 chars) + "bbbb"
		editor := newTestEditor(80, 24, nil)

		// Type a space, then 35 b's
		editor.HandleInput(" ")
		for i := 0; i < 35; i++ {
			editor.HandleInput("b")
		}

		// Paste 27 lines to create marker
		var lines []string
		for i := 0; i < 27; i++ {
			lines = append(lines, "line")
		}
		bigContent := strings.Join(lines, "\n")
		editor.HandleInput("\x1b[200~" + bigContent + "\x1b[201~")

		// Type trailing chars
		for i := 0; i < 4; i++ {
			editor.HandleInput("b")
		}

		// Render at width 54 (contentWidth=54, layoutWidth=53 with paddingX=0)
		renderWidth := 54
		renderLines := editor.Render(renderWidth)
		for _, line := range renderLines {
			if w := VisibleWidth(line); w > renderWidth {
				t.Errorf("line exceeds width %d: visible=%d text=%q", renderWidth, w, line)
			}
		}
	})

	t.Run("expands large pasted content literally in getExpandedText", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		pastedText := strings.Join([]string{
			"line 1",
			"line 2",
			"line 3",
			"line 4",
			"line 5",
			"line 6",
			"line 7",
			"line 8",
			"line 9",
			"line 10",
			"tokens $1 $2 $& $$ $` $' end",
		}, "\n")

		editor.HandleInput("\x1b[200~" + pastedText + "\x1b[201~")

		if !testPasteMarkerRegex.MatchString(editor.GetText()) {
			t.Errorf("no paste marker in %q", editor.GetText())
		}
		if got := editor.GetExpandedText(); got != pastedText {
			t.Errorf("GetExpandedText() = %q, want %q", got, pastedText)
		}
	})

	t.Run("snaps to the paste marker start when navigating down into it", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// Line 0: long enough text to establish a sticky column
		editor.SetText("12345678901234567890\n\nhello ")

		// Create a large paste to get a marker
		bigContent := strings.Repeat("x", 2000)
		editor.HandleInput("\x1b[200~" + bigContent + "\x1b[201~")
		editor.Render(80)

		// Line 2: "hello [paste #1 2000 chars]" - marker starts at col 6

		// Navigate to line 0, col 10
		editor.HandleInput("\x1b[A") // Up to line 1
		editor.HandleInput("\x1b[A") // Up to line 0
		editor.HandleInput("\x01")   // Ctrl+A (start of line)
		for i := 0; i < 10; i++ {
			editor.HandleInput("\x1b[C") // Right 10
		}
		wantCursor(t, editor, 0, 10)

		// Down to empty line
		editor.HandleInput("\x1b[B")
		wantCursor(t, editor, 1, 0)

		// Down to paste marker line - sticky col 10 falls inside marker (starts at col 6).
		// Cursor should snap to start of marker (col 6), not end (col 6 + marker.length).
		editor.HandleInput("\x1b[B")
		wantCursor(t, editor, 2, 6)
	})

	t.Run("preserves sticky column when navigating through paste marker line", func(t *testing.T) {
		editor := NewEditor(createTestTUI(30, 24), testEditorTheme(), nil)

		// Build:
		// Line 0: "1234567890123456" (16 chars)
		// Line 1: "" (empty)
		// Line 2: "[paste #1 2000 chars]" (paste marker)
		// Line 3: "" (empty)
		// Line 4: "abcdefghijklmnop" (16 chars)
		for _, ch := range "1234567890123456" {
			editor.HandleInput(string(ch))
		}
		editor.HandleInput("\n")
		editor.HandleInput("\n")
		editor.HandleInput("\x1b[200~" + strings.Repeat("x", 2000) + "\x1b[201~")
		editor.HandleInput("\n")
		editor.HandleInput("\n")
		for _, ch := range "abcdefghijklmnop" {
			editor.HandleInput(string(ch))
		}
		editor.Render(30)

		// Navigate to line 0, col 10
		for i := 0; i < 4; i++ {
			editor.HandleInput("\x1b[A") // Up to line 0
		}
		editor.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 10; i++ {
			editor.HandleInput("\x1b[C")
		}
		wantCursor(t, editor, 0, 10)

		// Down to empty line - sticky col 10 established
		editor.HandleInput("\x1b[B")
		wantCursor(t, editor, 1, 0)

		// Down to paste marker - cursor snapped to col 0 (start of marker)
		editor.HandleInput("\x1b[B")
		wantCursor(t, editor, 2, 0)

		// Down to empty line
		editor.HandleInput("\x1b[B")
		wantCursor(t, editor, 3, 0)

		// Down to last line - should restore sticky col 10
		editor.HandleInput("\x1b[B")
		wantCursor(t, editor, 4, 10)
	})

	t.Run("does not get stuck moving down from a multi-visual-line paste marker", func(t *testing.T) {
		editor := NewEditor(createTestTUI(20, 24), testEditorTheme(), nil)

		// Build:
		// Logical line 0: "abcdefgh" + marker(21 chars) + "ijklmnopqr"
		// Logical line 1: "123456789012345678"
		//
		// Marker "[paste #1 +100 lines]" (21 chars) is wider than the
		// terminal (20). Word-wrap splits at the space before "lines",
		// producing:
		//   VL1: abcdefgh              (startCol 0,  len 8)
		//   VL2: [paste #1 +100        (startCol 8,  len 15) <- marker head
		//   VL3: lines]ijklmnopqr      (startCol 23, len 16) <- marker tail + content
		//   VL4: 123456789012345678    (line 1)
		for _, ch := range "abcdefgh" {
			editor.HandleInput(string(ch))
		}
		var lines []string
		for i := 0; i < 100; i++ {
			lines = append(lines, "line")
		}
		bigContent := strings.Join(lines, "\n")
		editor.HandleInput("\x1b[200~" + bigContent + "\x1b[201~")
		for _, ch := range "ijklmnopqr" {
			editor.HandleInput(string(ch))
		}
		editor.HandleInput("\n")
		for _, ch := range "123456789012345678" {
			editor.HandleInput(string(ch))
		}
		editor.Render(20)

		marker := findPasteMarker(t, editor.GetText())
		markerLen := len(marker) // 21
		if markerLen <= 20 {
			t.Fatal("marker should be wider than terminal")
		}
		markerStart := 8
		markerEnd := markerStart + markerLen // 29

		// Navigate to line 0, col 6 (on "g"). Preferred col 6 is past the
		// marker tail on VL3, so the cursor should land on content ("i" at
		// col 29) without snapping back.
		editor.HandleInput("\x1b[A") // Up to line 0
		editor.HandleInput("\x01")   // Ctrl+A (start of line)
		for i := 0; i < 6; i++ {
			editor.HandleInput("\x1b[C") // Right to col 6
		}
		wantCursor(t, editor, 0, 6)

		// Down: cursor lands on paste marker start
		editor.HandleInput("\x1b[B")
		wantCursor(t, editor, 0, markerStart)

		// Down again: preferred col 6 lands at VL3 col 29 ("i"), which is
		// past the marker. Cursor stays on line 0.
		editor.HandleInput("\x1b[B")
		if got := editor.GetCursor().Line; got != 0 {
			t.Errorf("line = %d, want 0", got)
		}
		if got := editor.GetCursor().Col; got != markerEnd {
			t.Errorf("col = %d, want %d (col 29 = 'i')", got, markerEnd)
		}

		// Up: back to paste marker
		editor.HandleInput("\x1b[A")
		wantCursor(t, editor, 0, markerStart)

		// Up again: back to col 6 ("g")
		editor.HandleInput("\x1b[A")
		wantCursor(t, editor, 0, 6)
	})

	t.Run("skips marker continuation VLs when preferred col falls in marker tail", func(t *testing.T) {
		editor := NewEditor(createTestTUI(20, 24), testEditorTheme(), nil)

		// Same layout. Start at col 3 ("d"). Preferred col 3 maps to VL3
		// visual col 3 which is inside the "lines]" marker tail.
		// moveToVisualLine detects the continuation VL and skips to VL4
		// (line 1).
		for _, ch := range "abcdefgh" {
			editor.HandleInput(string(ch))
		}
		var lines []string
		for i := 0; i < 100; i++ {
			lines = append(lines, "line")
		}
		bigContent := strings.Join(lines, "\n")
		editor.HandleInput("\x1b[200~" + bigContent + "\x1b[201~")
		for _, ch := range "ijklmnopqr" {
			editor.HandleInput(string(ch))
		}
		editor.HandleInput("\n")
		for _, ch := range "123456789012345678" {
			editor.HandleInput(string(ch))
		}
		editor.Render(20)

		// Navigate to line 0, col 3 (on "d")
		editor.HandleInput("\x1b[A") // Up to line 0
		editor.HandleInput("\x01")   // Ctrl+A
		for i := 0; i < 3; i++ {
			editor.HandleInput("\x1b[C")
		}
		wantCursor(t, editor, 0, 3)

		// Down: marker
		editor.HandleInput("\x1b[B")
		if got := editor.GetCursor().Col; got != 8 {
			t.Errorf("col = %d, want 8 (marker)", got)
		}

		// Down: skips VL3 (col 3 in marker tail) and lands on line 1
		editor.HandleInput("\x1b[B")
		wantCursor(t, editor, 1, 3)

		// Round-trip back
		editor.HandleInput("\x1b[A")
		if got := editor.GetCursor().Col; got != 8 {
			t.Errorf("col = %d, want 8 (marker)", got)
		}
		editor.HandleInput("\x1b[A")
		wantCursor(t, editor, 0, 3)
	})

	t.Run("submits large pasted content literally", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		pastedText := strings.Join([]string{
			"line 1",
			"line 2",
			"line 3",
			"line 4",
			"line 5",
			"line 6",
			"line 7",
			"line 8",
			"line 9",
			"line 10",
			"tokens $1 $2 $& $$ $` $' end",
		}, "\n")
		submitted := ""
		editor.OnSubmit = func(text string) {
			submitted = text
		}

		editor.HandleInput("\x1b[200~" + pastedText + "\x1b[201~")
		editor.HandleInput("\r")

		if submitted != pastedText {
			t.Errorf("submitted = %q, want %q", submitted, pastedText)
		}
	})
}

// =============================================================================
// Autocomplete (pi: describe("Autocomplete") in editor.test.ts; ported in T2b)
// =============================================================================

// mockAutocompleteProvider is a configurable AutocompleteProvider stub (pi:
// inline mockProvider object literals).
type mockAutocompleteProvider struct {
	triggerCharacters []string
	getSuggestions    func(ctx context.Context, lines []string, cursorLine, cursorCol int, options AutocompleteGetSuggestionsOptions) *AutocompleteSuggestions
	applyCompletionFn func(lines []string, cursorLine, cursorCol int, item AutocompleteItem, prefix string) AutocompleteApplyResult
}

func (m *mockAutocompleteProvider) GetSuggestions(ctx context.Context, lines []string, cursorLine, cursorCol int, options AutocompleteGetSuggestionsOptions) *AutocompleteSuggestions {
	return m.getSuggestions(ctx, lines, cursorLine, cursorCol, options)
}

func (m *mockAutocompleteProvider) ApplyCompletion(lines []string, cursorLine, cursorCol int, item AutocompleteItem, prefix string) AutocompleteApplyResult {
	if m.applyCompletionFn != nil {
		return m.applyCompletionFn(lines, cursorLine, cursorCol, item, prefix)
	}
	// pi mocks all pass the standard applyCompletion.
	return testApplyCompletion(lines, cursorLine, cursorCol, item, prefix)
}

func (m *mockAutocompleteProvider) TriggerCharacters() []string { return m.triggerCharacters }

// testApplyCompletion is the standard applyCompletion that replaces prefix
// with item.value (pi: test helper applyCompletion).
func testApplyCompletion(lines []string, cursorLine, cursorCol int, item AutocompleteItem, prefix string) AutocompleteApplyResult {
	line := lines[cursorLine]
	before := line[:cursorCol-len(prefix)]
	after := line[cursorCol:]
	newLines := make([]string, len(lines))
	copy(newLines, lines)
	newLines[cursorLine] = before + item.Value + after
	return AutocompleteApplyResult{
		Lines:      newLines,
		CursorLine: cursorLine,
		CursorCol:  cursorCol - len(prefix) + len(item.Value),
	}
}

// flushAutocomplete waits for the pending autocomplete request to settle (pi:
// async helper flushAutocomplete — microtask flush; Go: task-chain channel).
func flushAutocomplete(t *testing.T, editor *Editor) {
	t.Helper()
	for {
		editor.mu.Lock()
		task := editor.autocompleteRequestTask
		editor.mu.Unlock()
		if task == nil {
			return
		}
		<-task
		editor.mu.Lock()
		current := editor.autocompleteRequestTask
		editor.mu.Unlock()
		if current == task {
			return
		}
	}
}

// mockProviderSuggestions is a convenience wrapper for the common mock shape:
// match on the text before the cursor and return (items, prefix) or nil.
func mockProviderSuggestions(match func(beforeCursor string) *AutocompleteSuggestions) func(ctx context.Context, lines []string, cursorLine, cursorCol int, options AutocompleteGetSuggestionsOptions) *AutocompleteSuggestions {
	return func(ctx context.Context, lines []string, cursorLine, cursorCol int, options AutocompleteGetSuggestionsOptions) *AutocompleteSuggestions {
		text := lines[cursorLine]
		beforeCursor := text[:cursorCol]
		return match(beforeCursor)
	}
}

func TestEditorAutocomplete(t *testing.T) {
	t.Run("auto-applies single force-file suggestion without showing menu", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetAutocompleteProvider(&mockAutocompleteProvider{
			getSuggestions: mockProviderSuggestions(func(beforeCursor string) *AutocompleteSuggestions {
				if beforeCursor == "Work" {
					return &AutocompleteSuggestions{
						Items:  []AutocompleteItem{{Value: "Workspace/", Label: "Workspace/"}},
						Prefix: "Work",
					}
				}
				return nil
			}),
			applyCompletionFn: testApplyCompletion,
		})

		// Type "Work"
		for _, ch := range []string{"W", "o", "r", "k"} {
			editor.HandleInput(ch)
		}
		wantText(t, editor, "Work")

		// Press Tab - should auto-apply without showing menu
		editor.HandleInput("\t")
		flushAutocomplete(t, editor)
		wantText(t, editor, "Workspace/")
		if editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be hidden")
		}

		// Undo should restore to "Work"
		editor.HandleInput("\x1b[45;5u") // Ctrl+- (undo)
		wantText(t, editor, "Work")
	})

	t.Run("shows menu when force-file has multiple suggestions", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		editor.SetAutocompleteProvider(&mockAutocompleteProvider{
			getSuggestions: mockProviderSuggestions(func(beforeCursor string) *AutocompleteSuggestions {
				if beforeCursor == "src" {
					return &AutocompleteSuggestions{
						Items: []AutocompleteItem{
							{Value: "src/", Label: "src/"},
							{Value: "src.txt", Label: "src.txt"},
						},
						Prefix: "src",
					}
				}
				return nil
			}),
			applyCompletionFn: testApplyCompletion,
		})

		// Type "src"
		for _, ch := range []string{"s", "r", "c"} {
			editor.HandleInput(ch)
		}
		wantText(t, editor, "src")

		// Press Tab - should show menu because there are multiple suggestions
		editor.HandleInput("\t")
		flushAutocomplete(t, editor)
		wantText(t, editor, "src")
		if !editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be showing")
		}

		// Press Tab again to accept first suggestion
		editor.HandleInput("\t")
		wantText(t, editor, "src/")
		if editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be hidden")
		}
	})

	t.Run("keeps suggestions open when typing in force mode (Tab-triggered)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		allFiles := []AutocompleteItem{
			{Value: "readme.md", Label: "readme.md"},
			{Value: "package.json", Label: "package.json"},
			{Value: "src/", Label: "src/"},
			{Value: "dist/", Label: "dist/"},
		}

		editor.SetAutocompleteProvider(&mockAutocompleteProvider{
			getSuggestions: func(ctx context.Context, lines []string, cursorLine, cursorCol int, options AutocompleteGetSuggestionsOptions) *AutocompleteSuggestions {
				beforeCursor := lines[cursorLine][:cursorCol]
				shouldMatch := options.Force || strings.Contains(beforeCursor, "/") || strings.HasPrefix(beforeCursor, ".")
				if !shouldMatch {
					return nil
				}
				var filtered []AutocompleteItem
				for _, f := range allFiles {
					if strings.HasPrefix(strings.ToLower(f.Value), strings.ToLower(beforeCursor)) {
						filtered = append(filtered, f)
					}
				}
				if len(filtered) > 0 {
					return &AutocompleteSuggestions{Items: filtered, Prefix: beforeCursor}
				}
				return nil
			},
			applyCompletionFn: testApplyCompletion,
		})

		// Press Tab on empty prompt - should show all files (force mode)
		editor.HandleInput("\t")
		flushAutocomplete(t, editor)
		if !editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be showing")
		}

		// Type "r" - should narrow to "readme.md" (force mode keeps suggestions open)
		editor.HandleInput("r")
		flushAutocomplete(t, editor)
		wantText(t, editor, "r")
		if !editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be showing")
		}

		// Type "e" - should still show "readme.md"
		editor.HandleInput("e")
		flushAutocomplete(t, editor)
		wantText(t, editor, "re")
		if !editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be showing")
		}

		// Accept with Tab
		editor.HandleInput("\t")
		wantText(t, editor, "readme.md")
		if editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be hidden")
		}
	})

	debounceCases := []struct {
		name       string
		typeSeq    []string
		cannedItem AutocompleteItem
	}{
		{name: "debounces @ autocomplete while typing", typeSeq: []string{"@", "m", "a", "i"}, cannedItem: AutocompleteItem{Value: "@main.ts", Label: "main.ts"}},
		{name: "debounces # autocomplete while typing", typeSeq: []string{"#", "2", "9", "8"}, cannedItem: AutocompleteItem{Value: "#2983", Label: "#2983"}},
	}
	for _, tc := range debounceCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			editor := newTestEditor(80, 24, nil)
			var suggestionCalls atomic.Int32

			editor.SetAutocompleteProvider(&mockAutocompleteProvider{
				getSuggestions: func(ctx context.Context, lines []string, cursorLine, cursorCol int, options AutocompleteGetSuggestionsOptions) *AutocompleteSuggestions {
					suggestionCalls.Add(1)
					text := lines[cursorLine]
					return &AutocompleteSuggestions{
						Items:  []AutocompleteItem{tc.cannedItem},
						Prefix: text[:cursorCol],
					}
				},
				applyCompletionFn: testApplyCompletion,
			})

			for _, ch := range tc.typeSeq {
				editor.HandleInput(ch)
			}

			if got := suggestionCalls.Load(); got != 0 {
				t.Errorf("suggestionCalls = %d, want 0", got)
			}
			if editor.IsShowingAutocomplete() {
				t.Error("expected autocomplete to be hidden before debounce fires")
			}

			time.Sleep(50 * time.Millisecond)
			flushAutocomplete(t, editor)

			if got := suggestionCalls.Load(); got != 1 {
				t.Errorf("suggestionCalls = %d, want 1", got)
			}
			if !editor.IsShowingAutocomplete() {
				t.Error("expected autocomplete to be showing")
			}
		})
	}

	t.Run("debounces custom triggerCharacters autocomplete while typing", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		var suggestionCalls atomic.Int32

		editor.SetAutocompleteProvider(&mockAutocompleteProvider{
			triggerCharacters: []string{"$"},
			getSuggestions: func(ctx context.Context, lines []string, cursorLine, cursorCol int, options AutocompleteGetSuggestionsOptions) *AutocompleteSuggestions {
				suggestionCalls.Add(1)
				prefix := lines[cursorLine][:cursorCol]
				return &AutocompleteSuggestions{
					Items:  []AutocompleteItem{{Value: "$skill-name", Label: "skill-name"}},
					Prefix: prefix,
				}
			},
			applyCompletionFn: testApplyCompletion,
		})

		for _, ch := range []string{"$", "s", "k"} {
			editor.HandleInput(ch)
		}

		if got := suggestionCalls.Load(); got != 0 {
			t.Errorf("suggestionCalls = %d, want 0", got)
		}
		time.Sleep(50 * time.Millisecond)
		flushAutocomplete(t, editor)

		if got := suggestionCalls.Load(); got != 1 {
			t.Errorf("suggestionCalls = %d, want 1", got)
		}
		if !editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be showing")
		}
	})

	t.Run("resets custom triggerCharacters when provider changes", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		var suggestionCalls atomic.Int32

		editor.SetAutocompleteProvider(&mockAutocompleteProvider{
			triggerCharacters: []string{"$"},
			getSuggestions: func(ctx context.Context, lines []string, cursorLine, cursorCol int, options AutocompleteGetSuggestionsOptions) *AutocompleteSuggestions {
				return &AutocompleteSuggestions{
					Items:  []AutocompleteItem{{Value: "$skill-name", Label: "skill-name"}},
					Prefix: "$",
				}
			},
			applyCompletionFn: testApplyCompletion,
		})
		editor.SetAutocompleteProvider(&mockAutocompleteProvider{
			getSuggestions: func(ctx context.Context, lines []string, cursorLine, cursorCol int, options AutocompleteGetSuggestionsOptions) *AutocompleteSuggestions {
				suggestionCalls.Add(1)
				return &AutocompleteSuggestions{
					Items:  []AutocompleteItem{{Value: "$skill-name", Label: "skill-name"}},
					Prefix: "$",
				}
			},
			applyCompletionFn: testApplyCompletion,
		})

		editor.HandleInput("$")
		editor.HandleInput("s")
		time.Sleep(50 * time.Millisecond)
		flushAutocomplete(t, editor)

		if got := suggestionCalls.Load(); got != 0 {
			t.Errorf("suggestionCalls = %d, want 0", got)
		}
		if editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be hidden")
		}
	})

	t.Run("aborts active @ autocomplete when typing continues", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		var aborts atomic.Int32

		editor.SetAutocompleteProvider(&mockAutocompleteProvider{
			getSuggestions: func(ctx context.Context, lines []string, cursorLine, cursorCol int, options AutocompleteGetSuggestionsOptions) *AutocompleteSuggestions {
				select {
				case <-time.After(500 * time.Millisecond):
					return &AutocompleteSuggestions{
						Items:  []AutocompleteItem{{Value: "@main.ts", Label: "main.ts"}},
						Prefix: "@main",
					}
				case <-ctx.Done():
					aborts.Add(1)
					return nil
				}
			},
			applyCompletionFn: testApplyCompletion,
		})

		for _, ch := range []string{"@", "m", "a", "i"} {
			editor.HandleInput(ch)
		}
		time.Sleep(250 * time.Millisecond)
		editor.HandleInput("n")
		time.Sleep(50 * time.Millisecond)

		if got := aborts.Load(); got != 1 {
			t.Errorf("aborts = %d, want 1", got)
		}
	})

	t.Run("hides autocomplete when backspacing slash command to empty", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// Mock provider with slash commands
		editor.SetAutocompleteProvider(&mockAutocompleteProvider{
			getSuggestions: mockProviderSuggestions(func(beforeCursor string) *AutocompleteSuggestions {
				// Only return slash command suggestions when line starts with /
				if strings.HasPrefix(beforeCursor, "/") {
					commands := []AutocompleteItem{
						{Value: "/model", Label: "model", Description: "Change model"},
						{Value: "/help", Label: "help", Description: "Show help"},
					}
					query := beforeCursor[1:] // Remove leading /
					var filtered []AutocompleteItem
					for _, c := range commands {
						if strings.HasPrefix(c.Value, query) {
							filtered = append(filtered, c)
						}
					}
					if len(filtered) > 0 {
						return &AutocompleteSuggestions{Items: filtered, Prefix: beforeCursor}
					}
				}
				return nil
			}),
			applyCompletionFn: testApplyCompletion,
		})

		// Type "/" - should show slash command suggestions
		editor.HandleInput("/")
		flushAutocomplete(t, editor)
		wantText(t, editor, "/")
		if !editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be showing")
		}

		// Backspace to delete "/" - should hide autocomplete completely
		editor.HandleInput("\x7f") // Backspace
		flushAutocomplete(t, editor)
		wantText(t, editor, "")
		if editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be hidden")
		}
	})

	// argCompletionSubtest drives the "/argtest <prefix>" mock family (pi:
	// several tests share this shape).
	type argCompletionSubtest struct {
		name         string
		allItems     []AutocompleteItem
		filtered     bool // provider filters by startsWith (true) or returns all (false)
		typeSequence []string
		beforeEnter  string
		afterEnter   string
	}
	argSubtests := []argCompletionSubtest{
		{
			name:         "applies exact typed slash-argument value on Enter even when first item is highlighted",
			allItems:     []AutocompleteItem{{Value: "one"}, {Value: "two"}, {Value: "three"}},
			filtered:     true,
			typeSequence: []string{"/", "a", "r", "g", "t", "e", "s", "t", " ", "t", "w", "o"},
			beforeEnter:  "/argtest two",
			afterEnter:   "/argtest two",
		},
		{
			name:         "selects first prefix match on Enter when typed arg is not exact match",
			allItems:     []AutocompleteItem{{Value: "two"}, {Value: "three"}, {Value: "twelve"}},
			filtered:     true,
			typeSequence: []string{"/", "a", "r", "g", "t", "e", "s", "t", " ", "t"},
			beforeEnter:  "/argtest t",
			afterEnter:   "/argtest two",
		},
		{
			name:         "highlights unique prefix match as user types (before full exact match)",
			allItems:     []AutocompleteItem{{Value: "one"}, {Value: "two"}, {Value: "three"}},
			filtered:     false,
			typeSequence: []string{"/", "a", "r", "g", "t", "e", "s", "t", " ", "t", "w"},
			beforeEnter:  "/argtest tw",
			afterEnter:   "/argtest two",
		},
		{
			name:         "selects first prefix match when multiple items match",
			allItems:     []AutocompleteItem{{Value: "one"}, {Value: "two"}, {Value: "three"}},
			filtered:     false,
			typeSequence: []string{"/", "a", "r", "g", "t", "e", "s", "t", " ", "t"},
			beforeEnter:  "/argtest t",
			afterEnter:   "/argtest two",
		},
	}
	for _, sub := range argSubtests {
		sub := sub
		t.Run(sub.name, func(t *testing.T) {
			editor := newTestEditor(80, 24, nil)

			argtestRegex := regexp.MustCompile(`^/argtest\s+(\S+)$`)
			editor.SetAutocompleteProvider(&mockAutocompleteProvider{
				getSuggestions: mockProviderSuggestions(func(beforeCursor string) *AutocompleteSuggestions {
					match := argtestRegex.FindStringSubmatch(beforeCursor)
					if match == nil {
						return nil
					}
					argumentText := match[1]
					var items []AutocompleteItem
					if sub.filtered {
						for _, arg := range sub.allItems {
							if strings.HasPrefix(arg.Value, argumentText) {
								items = append(items, arg)
							}
						}
						if len(items) == 0 {
							return nil
						}
					} else {
						// Return all items - provider does not filter
						items = sub.allItems
					}
					return &AutocompleteSuggestions{Items: items, Prefix: argumentText}
				}),
				applyCompletionFn: testApplyCompletion,
			})

			for _, ch := range sub.typeSequence {
				editor.HandleInput(ch)
			}

			wantText(t, editor, sub.beforeEnter)
			flushAutocomplete(t, editor)
			if !editor.IsShowingAutocomplete() {
				t.Fatal("expected autocomplete to be showing")
			}

			// Press Enter - the best match should be applied
			editor.HandleInput("\r")
			wantText(t, editor, sub.afterEnter)
		})
	}

	t.Run("works for built-in-style command argument completion path (model-like)", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)

		// Mock provider for /model command with model completions
		modelRegex := regexp.MustCompile(`^/model\s+(\S+)$`)
		editor.SetAutocompleteProvider(&mockAutocompleteProvider{
			getSuggestions: mockProviderSuggestions(func(beforeCursor string) *AutocompleteSuggestions {
				match := modelRegex.FindStringSubmatch(beforeCursor)
				if match == nil {
					return nil
				}
				modelText := match[1]
				allModels := []AutocompleteItem{
					{Value: "gpt-4o", Label: "gpt-4o"},
					{Value: "gpt-4o-mini", Label: "gpt-4o-mini"},
					{Value: "claude-sonnet", Label: "claude-sonnet"},
				}
				var filtered []AutocompleteItem
				for _, m := range allModels {
					if strings.HasPrefix(m.Value, modelText) {
						filtered = append(filtered, m)
					}
				}
				if len(filtered) > 0 {
					return &AutocompleteSuggestions{Items: filtered, Prefix: modelText}
				}
				return nil
			}),
			applyCompletionFn: testApplyCompletion,
		})

		// Type "/model gpt-4o-mini" - exact match for second item in list
		for _, ch := range strings.Split("/model gpt-4o-mini", "") {
			editor.HandleInput(ch)
		}

		wantText(t, editor, "/model gpt-4o-mini")
		flushAutocomplete(t, editor)
		if !editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be showing")
		}

		// Press Enter - should retain exact typed value, not apply first
		// highlighted item
		editor.HandleInput("\r")
		wantText(t, editor, "/model gpt-4o-mini")
	})

	t.Run("awaits async slash command argument completions", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		provider := NewCombinedAutocompleteProvider(
			[]SlashCommandOrItem{
				SlashCommand{
					Name:        "load-skills",
					Description: "Load skills",
					GetArgumentCompletions: func(prefix string) []AutocompleteItem {
						if strings.HasPrefix(prefix, "s") {
							return []AutocompleteItem{{Value: "skill-a", Label: "skill-a"}}
						}
						return nil
					},
				},
			},
			cwd,
			"",
		)
		editor.SetAutocompleteProvider(provider)
		editor.SetText("/load-skills ")

		editor.HandleInput("s")
		flushAutocomplete(t, editor)
		if !editor.IsShowingAutocomplete() {
			t.Fatal("expected autocomplete to be showing")
		}

		editor.HandleInput("\t")
		wantText(t, editor, "/load-skills skill-a")
		if editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be hidden")
		}
	})

	t.Run("ignores invalid slash command argument completion results", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		// pi: getArgumentCompletions returns the non-array "not-an-array";
		// Go has no non-array representation — nil (no completions) covers
		// the same ignore path (declared in autocomplete_test.go header).
		provider := NewCombinedAutocompleteProvider(
			[]SlashCommandOrItem{
				SlashCommand{
					Name:        "load-skills",
					Description: "Load skills",
					GetArgumentCompletions: func(prefix string) []AutocompleteItem {
						return nil
					},
				},
			},
			cwd,
			"",
		)
		editor.SetAutocompleteProvider(provider)
		editor.SetText("/load-skills ")

		editor.HandleInput("s")
		flushAutocomplete(t, editor)
		if editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be hidden")
		}
		wantText(t, editor, "/load-skills s")
	})

	t.Run("does not show argument completions when command has no argument completer", func(t *testing.T) {
		editor := newTestEditor(80, 24, nil)
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		provider := NewCombinedAutocompleteProvider(
			[]SlashCommandOrItem{
				SlashCommand{Name: "help", Description: "Show help"},
				SlashCommand{
					Name:        "model",
					Description: "Switch model",
					GetArgumentCompletions: func(prefix string) []AutocompleteItem {
						return []AutocompleteItem{{Value: "claude-opus", Label: "claude-opus"}}
					},
				},
			},
			cwd,
			"",
		)
		editor.SetAutocompleteProvider(provider)

		for _, ch := range []string{"/", "h", "e"} {
			editor.HandleInput(ch)
		}
		flushAutocomplete(t, editor)
		if !editor.IsShowingAutocomplete() {
			t.Fatal("expected autocomplete to be showing")
		}

		editor.HandleInput("\t")
		wantText(t, editor, "/help ")
		if editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be hidden")
		}
	})

	t.Run("re-queries the autocomplete picker when the cursor moves back into the command name", func(t *testing.T) {
		// Regression for earendil-works/pi#5496: arrowing left out of a slash
		// command's argument region must re-query the picker, not leave the
		// stale argument list showing.
		editor := newTestEditor(80, 24, nil)

		editor.SetAutocompleteProvider(&mockAutocompleteProvider{
			getSuggestions: mockProviderSuggestions(func(beforeCursor string) *AutocompleteSuggestions {
				if !strings.HasPrefix(beforeCursor, "/") {
					return nil
				}
				// Past the command name (a space before the cursor): offer arguments.
				if strings.Contains(beforeCursor, " ") {
					return &AutocompleteSuggestions{
						Items: []AutocompleteItem{
							{Value: "repo", Label: "repo"},
							{Value: "message", Label: "message"},
							{Value: "help", Label: "help"},
						},
						Prefix: beforeCursor[strings.Index(beforeCursor, " ")+1:],
					}
				}
				// Inside the command name: offer the command name only.
				return &AutocompleteSuggestions{
					Items:  []AutocompleteItem{{Value: "cmd", Label: "cmd"}},
					Prefix: beforeCursor,
				}
			}),
			applyCompletionFn: testApplyCompletion,
		})

		// Type "/cmd " so the picker ends up showing the argument list.
		for _, ch := range strings.Split("/cmd ", "") {
			editor.HandleInput(ch)
			flushAutocomplete(t, editor)
		}
		wantText(t, editor, "/cmd ")
		if !editor.IsShowingAutocomplete() {
			t.Error("expected autocomplete to be showing at /cmd ")
		}
		atArg := stripAnsi(strings.Join(editor.Render(80), "\n"))
		if !strings.Contains(atArg, "repo") {
			t.Errorf("argument menu should be visible at `/cmd `, got %q", atArg)
		}

		// Arrow Left back into the command name (`/cmd`).
		editor.HandleInput("\x1b[D")
		flushAutocomplete(t, editor)

		// The picker must have re-queried: the stale argument items are gone.
		afterMove := stripAnsi(strings.Join(editor.Render(80), "\n"))
		if strings.Contains(afterMove, "repo") || strings.Contains(afterMove, "message") {
			t.Errorf("stale argument menu must not survive the cursor move, got %q", afterMove)
		}
	})
}
