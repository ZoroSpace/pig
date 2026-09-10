// ported from pi: packages/tui/test/tab-width.test.ts
//
// The fourth pi subtest ("keeps tab-containing overlays on one physical
// terminal row") needs TuiMainScreen + VirtualTerminal and is deferred to the
// tui_main_screen batch (T1 step 5); see README §4 Phase 6.
package tui

import "testing"

func TestTabWidthAccounting(t *testing.T) {
	t.Run("keeps slice helper widths consistent with visible width", func(t *testing.T) {
		text := "out 192M\t.pi/skill-tests/results-ha"
		slice := SliceWithWidth(text, 0, 10, true)

		assertEqualStr(t, slice.Text, "out 192M")
		if slice.Width != 8 {
			t.Errorf("slice width = %d, want 8", slice.Width)
		}
		if got := VisibleWidth(slice.Text); got != slice.Width {
			t.Errorf("visible width = %d, want %d", got, slice.Width)
		}
	})

	t.Run("keeps overlay segment widths consistent with visible width", func(t *testing.T) {
		text := "out 192M\t.pi/skill-tests/results-ha"
		segments := ExtractSegments(text, 10, 13, 10, true)

		assertEqualStr(t, segments.Before, "out 192M")
		if segments.BeforeWidth != 8 {
			t.Errorf("before width = %d, want 8", segments.BeforeWidth)
		}
		if got := VisibleWidth(segments.Before); got != segments.BeforeWidth {
			t.Errorf("visible width = %d, want %d", got, segments.BeforeWidth)
		}

		tabFits := ExtractSegments(text, 11, 13, 10, true)
		assertEqualStr(t, tabFits.Before, "out 192M\t")
		if tabFits.BeforeWidth != 11 {
			t.Errorf("before width = %d, want 11", tabFits.BeforeWidth)
		}
		if got := VisibleWidth(tabFits.Before); got != tabFits.BeforeWidth {
			t.Errorf("visible width = %d, want %d", got, tabFits.BeforeWidth)
		}
	})

	t.Run("keeps tabs inside terminal control sequences byte-identical", func(t *testing.T) {
		controlSequences := []string{
			"\x1b]8;;https://example.test/a\tb\x07",
			"\x1b]0;window\ttitle\x1b\\",
			"\x1b_payload\tdata\x1b\\",
		}

		for _, controlSequence := range controlSequences {
			got := NormalizeTerminalOutput(controlSequence + "label\ttext")
			want := controlSequence + "label   text"
			assertEqualStr(t, got, want)
		}
	})
}
