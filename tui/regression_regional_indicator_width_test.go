// ported from pi: packages/tui/test/regression-regional-indicator-width.test.ts
package tui

import "testing"

func TestRegionalIndicatorWidthRegression(t *testing.T) {
	t.Run("treats partial flag grapheme as full-width to avoid streaming render drift", func(t *testing.T) {
		// Repro context:
		// During streaming, "🇨🇳" often appears as an intermediate "🇨" first.
		// If "🇨" is measured as width 1 while terminal renders it as width 2,
		// differential rendering can drift and leave stale characters on screen.
		partialFlag := "🇨"
		listLine := "      - 🇨"

		if got := VisibleWidth(partialFlag); got != 2 {
			t.Errorf("partial flag width = %d, want 2", got)
		}
		if got := VisibleWidth(listLine); got != 10 {
			t.Errorf("list line width = %d, want 10", got)
		}
	})

	t.Run("wraps intermediate partial-flag list line before overflow", func(t *testing.T) {
		// Width 9 cannot fit "      - 🇨" if 🇨 is width 2 (8 + 2 = 10).
		// This must wrap to avoid terminal auto-wrap mismatch.
		wrapped := WrapTextWithAnsi("      - 🇨", 9)

		if len(wrapped) != 2 {
			t.Fatalf("expected 2 lines, got %v", wrapped)
		}
		if got := VisibleWidth(wrapped[0]); got != 7 {
			t.Errorf("line 0 width = %d, want 7", got)
		}
		if got := VisibleWidth(wrapped[1]); got != 2 {
			t.Errorf("line 1 width = %d, want 2", got)
		}
	})

	t.Run("treats all regional-indicator singleton graphemes as width 2", func(t *testing.T) {
		for cp := 0x1f1e6; cp <= 0x1f1ff; cp++ {
			regionalIndicator := string(rune(cp))
			if got := VisibleWidth(regionalIndicator); got != 2 {
				t.Errorf("Expected %s (U+%X) to be width 2, got %d", regionalIndicator, cp, got)
			}
		}
	})

	t.Run("keeps full flag pairs at width 2", func(t *testing.T) {
		samples := []string{"🇯🇵", "🇺🇸", "🇬🇧", "🇨🇳", "🇩🇪", "🇫🇷"}
		for _, flag := range samples {
			if got := VisibleWidth(flag); got != 2 {
				t.Errorf("Expected %s to be width 2, got %d", flag, got)
			}
		}
	})

	t.Run("keeps common streaming emoji intermediates at stable width", func(t *testing.T) {
		samples := []string{"👍", "👍🏻", "✅", "⚡", "⚡️", "👨", "👨‍💻", "🏳️‍🌈"}
		for _, sample := range samples {
			if got := VisibleWidth(sample); got != 2 {
				t.Errorf("Expected %s to be width 2, got %d", sample, got)
			}
		}
	})
}
