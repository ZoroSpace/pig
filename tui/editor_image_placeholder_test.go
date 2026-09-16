// editor_image_placeholder_test.go — regression tests for atomic [Image N]
// clipboard placeholders (pig-only, docs/feat/CLIPBOARD-IMAGE-PLACEHOLDER.md).
//
// Registered placeholders share the paste-marker mechanics
// (segmentWithMarkers): they merge into a single segment, so cursor
// movement, backspace, and forward delete act on the whole unit instead of
// single characters. Unregistered occurrences (hand-typed text) stay fully
// editable, mirroring how paste markers only merge for valid ids.
package tui

import "testing"

func TestImagePlaceholderMergesIntoSingleSegment(t *testing.T) {
	editor := newTestEditor(80, 24, nil)
	editor.ImagePlaceholderValidIds = func() map[int]bool { return map[int]bool{1: true} }

	segments := editor.segment("a [Image 1] b", "grapheme")
	got := make([]string, 0, len(segments))
	for _, s := range segments {
		got = append(got, s.Segment)
	}
	want := []string{"a", " ", "[Image 1]", " ", "b"}
	if len(got) != len(want) {
		t.Fatalf("segments = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("segments[%d] = %q, want %q (all: %q)", i, got[i], want[i], got)
		}
	}
}

func TestImagePlaceholderUnregisteredStaysEditable(t *testing.T) {
	editor := newTestEditor(80, 24, nil)
	editor.ImagePlaceholderValidIds = func() map[int]bool { return map[int]bool{1: true} }

	// [Image 2] is not registered — it must segment as plain characters.
	segments := editor.segment("[Image 2]", "grapheme")
	for _, s := range segments {
		if s.Segment == "[Image 2]" {
			t.Fatalf("unregistered placeholder should not merge into one segment")
		}
	}
	if len(segments) != len("[Image 2]") {
		t.Fatalf("unregistered placeholder should segment per character, got %d segments", len(segments))
	}
}

func TestImagePlaceholderDeletesAsWholeUnit(t *testing.T) {
	editor := newTestEditor(80, 24, nil)
	editor.ImagePlaceholderValidIds = func() map[int]bool { return map[int]bool{1: true} }

	editor.SetText("a [Image 1] b")
	// Cursor is at the end: two backspaces remove "b" and " ", the third
	// removes the whole placeholder in one step.
	editor.HandleInput("\x7f") // Backspace → "a [Image 1] "
	editor.HandleInput("\x7f") // Backspace → "a [Image 1]"
	editor.HandleInput("\x7f") // Backspace → "a " (whole placeholder)
	wantText(t, editor, "a ")
}

func TestImagePlaceholderForwardDeletesAsWholeUnitAndSkipsCursor(t *testing.T) {
	editor := newTestEditor(80, 24, nil)
	editor.ImagePlaceholderValidIds = func() map[int]bool { return map[int]bool{1: true} }

	editor.SetText("a [Image 1] b")
	// Move left three times: after "b", after " ", then before the whole
	// placeholder (the atomic segment is skipped as one unit).
	editor.HandleInput("\x1b[D")
	editor.HandleInput("\x1b[D")
	editor.HandleInput("\x1b[D")
	editor.HandleInput("\x1b[3~") // Delete key → removes the whole placeholder
	wantText(t, editor, "a  b")
}

func TestImagePlaceholderSingleBackspaceOnUnregisteredText(t *testing.T) {
	editor := newTestEditor(80, 24, nil)
	editor.ImagePlaceholderValidIds = func() map[int]bool { return map[int]bool{1: true} }

	// Hand-typed [Image 2] (unregistered) deletes character by character.
	editor.SetText("[Image 2]")
	editor.HandleInput("\x7f")
	wantText(t, editor, "[Image 2")
}
