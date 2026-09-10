// ported from pi: packages/tui/test/truncate-to-width.test.ts
package tui

import (
	"strings"
	"testing"
)

func TestTruncateToWidth(t *testing.T) {
	t.Run("keeps output within width for very large unicode input", func(t *testing.T) {
		text := strings.Repeat("🙂界", 100000)
		truncated := TruncateToWidth(text, 40, "…", false)

		if w := VisibleWidth(truncated); w > 40 {
			t.Errorf("width = %d, want <= 40", w)
		}
		if !strings.HasSuffix(truncated, "…\x1b[0m") {
			t.Errorf("truncated does not end with ellipsis+reset: %q", tail(truncated, 20))
		}
	})

	t.Run("preserves ANSI styling for kept text and resets before and after ellipsis", func(t *testing.T) {
		text := "\x1b[31m" + strings.Repeat("hello ", 1000) + "\x1b[0m"
		truncated := TruncateToWidth(text, 20, "…", false)

		if w := VisibleWidth(truncated); w > 20 {
			t.Errorf("width = %d, want <= 20", w)
		}
		if !strings.Contains(truncated, "\x1b[31m") {
			t.Errorf("truncated lost SGR: %q", truncated)
		}
		if !strings.HasSuffix(truncated, "\x1b[0m…\x1b[0m") {
			t.Errorf("truncated does not end with reset+ellipsis+reset: %q", tail(truncated, 20))
		}
	})

	t.Run("closes a BEL-terminated OSC 8 link when truncating its label", func(t *testing.T) {
		open := "\x1b]8;;https://example.com\x07"
		close := "\x1b]8;;\x07"
		text := open + "some-longer-label-here" + close

		got := TruncateToWidth(text, 15, "...", false)
		want := open + "some-longer-" + close + "\x1b[0m...\x1b[0m"
		assertEqualStr(t, got, want)
	})

	t.Run("handles malformed ANSI escape prefixes without hanging", func(t *testing.T) {
		text := "abc\x1bnot-ansi " + strings.Repeat("🙂", 1000)
		truncated := TruncateToWidth(text, 20, "…", false)

		if w := VisibleWidth(truncated); w > 20 {
			t.Errorf("width = %d, want <= 20", w)
		}
	})

	t.Run("clips wide ellipsis safely and brackets it with resets", func(t *testing.T) {
		assertEqualStr(t, TruncateToWidth("abcdef", 1, "🙂", false), "")
		assertEqualStr(t, TruncateToWidth("abcdef", 2, "🙂", false), "\x1b[0m🙂\x1b[0m")
		if w := VisibleWidth(TruncateToWidth("abcdef", 2, "🙂", false)); w > 2 {
			t.Errorf("width = %d, want <= 2", w)
		}
	})

	t.Run("returns the original text when it already fits even if ellipsis is too wide", func(t *testing.T) {
		assertEqualStr(t, TruncateToWidth("a", 2, "🙂", false), "a")
		assertEqualStr(t, TruncateToWidth("界", 2, "🙂", false), "界")
	})

	t.Run("pads truncated output to requested width", func(t *testing.T) {
		truncated := TruncateToWidth("🙂界🙂界🙂界", 8, "…", true)
		if w := VisibleWidth(truncated); w != 8 {
			t.Errorf("width = %d, want 8", w)
		}
	})

	t.Run("adds a trailing reset when truncating without an ellipsis", func(t *testing.T) {
		truncated := TruncateToWidth("\x1b[31m"+strings.Repeat("hello", 100), 10, "", false)
		if w := VisibleWidth(truncated); w > 10 {
			t.Errorf("width = %d, want <= 10", w)
		}
		if !strings.HasSuffix(truncated, "\x1b[0m") {
			t.Errorf("truncated does not end with reset: %q", tail(truncated, 20))
		}
	})

	t.Run("keeps a contiguous prefix instead of skipping a wide grapheme and resuming later", func(t *testing.T) {
		truncated := TruncateToWidth("🙂\t界 \x1b_abc\x07", 7, "…", true)
		assertEqualStr(t, truncated, "🙂\t\x1b[0m…\x1b[0m ")
	})
}

func TestVisibleWidth(t *testing.T) {
	t.Run("counts tabs inline and skips ANSI inline", func(t *testing.T) {
		if got := VisibleWidth("\t\x1b[31m界\x1b[0m"); got != 5 {
			t.Errorf("width = %d, want 5", got)
		}
	})

	t.Run("counts Indic conjunct spacing code points within grapheme clusters", func(t *testing.T) {
		cases := map[string]int{
			"र्क":     2,
			"नेटवर्क": 5,
			"सर्वाधिकार सुरक्षित। ऑर्डर पर क्लिक करें": 33,
			"র্ক":  2,
			"ર્ક":  2,
			"ର୍କ":  2,
			"ర్క":  2,
			"ര്‍ക": 2,
		}
		for s, want := range cases {
			if got := VisibleWidth(s); got != want {
				t.Errorf("VisibleWidth(%q) = %d, want %d", s, got, want)
			}
		}
	})

	t.Run("keeps ordinary combining marks zero-width", func(t *testing.T) {
		cases := map[string]int{
			"e\u0301": 1,
			"čřžůú":   5,
			"שָׁ":     1,
			"بّ":      1,
			"རྐ":      1,
			"ᜠ᜴":      1,
			"가〮":      2,
			"가〯":      2,
		}
		for s, want := range cases {
			if got := VisibleWidth(s); got != want {
				t.Errorf("VisibleWidth(%q) = %d, want %d", s, got, want)
			}
		}
	})

	t.Run("keeps CJK and Japanese width accounting unchanged", func(t *testing.T) {
		cases := map[string]int{
			"网络":      4,
			"ネットワーク":  12,
			"が":       2,
			"か\u3099": 2,
		}
		for s, want := range cases {
			if got := VisibleWidth(s); got != want {
				t.Errorf("VisibleWidth(%q) = %d, want %d", s, got, want)
			}
		}
	})

	t.Run("counts Myanmar marks that terminals allocate cells for", func(t *testing.T) {
		cases := map[string]int{
			"ကာ":  2,
			"ကေ":  2,
			"က်":  2,
			"ကျ":  2,
			"ကြ":  2,
			"ကဳ":  2,
			"ကဴ":  2,
			"ကဵ":  2,
			"ကး":  2,
			"ကို": 1,
			"က္":  1,
		}
		for s, want := range cases {
			if got := VisibleWidth(s); got != want {
				t.Errorf("VisibleWidth(%q) = %d, want %d", s, got, want)
			}
		}
	})

	t.Run("keeps Thai and Lao AM clusters at their normal cell width", func(t *testing.T) {
		cases := map[string]int{
			"ำ":  1,
			"ຳ":  1,
			"กำ": 2,
			"ກຳ": 2,
		}
		for s, want := range cases {
			if got := VisibleWidth(s); got != want {
				t.Errorf("VisibleWidth(%q) = %d, want %d", s, got, want)
			}
		}
	})

	t.Run("normalizes Thai and Lao AM vowels only for terminal output", func(t *testing.T) {
		assertEqualStr(t, NormalizeTerminalOutput("ำ"), "ํา")
		assertEqualStr(t, NormalizeTerminalOutput("ຳ"), "ໍາ")
		if got, want := VisibleWidth(NormalizeTerminalOutput("ำabc")), VisibleWidth("ำabc"); got != want {
			t.Errorf("normalized width = %d, want %d", got, want)
		}
		if got, want := VisibleWidth(NormalizeTerminalOutput("ຳabc")), VisibleWidth("ຳabc"); got != want {
			t.Errorf("normalized width = %d, want %d", got, want)
		}
	})
}

// tail returns the last n bytes of s (for readable error messages).
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
