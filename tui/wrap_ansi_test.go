// ported from pi: packages/tui/test/wrap-ansi.test.ts
package tui

import (
	"regexp"
	"strings"
	"testing"
)

func assertEqualStr(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWrapTextWithAnsiUnderlineStyling(t *testing.T) {
	t.Run("should not apply underline style before the styled text", func(t *testing.T) {
		underlineOn := "\x1b[4m"
		underlineOff := "\x1b[24m"
		url := "https://example.com/very/long/path/that/will/wrap"
		text := "read this thread " + underlineOn + url + underlineOff

		wrapped := WrapTextWithAnsi(text, 40)

		// First line should NOT contain underline code - it's just "read this thread"
		assertEqualStr(t, wrapped[0], "read this thread")

		// Second line should start with underline, have URL content
		if !strings.HasPrefix(wrapped[1], underlineOn) {
			t.Errorf("line 1 does not start with underline: %q", wrapped[1])
		}
		if !strings.Contains(wrapped[1], "https://") {
			t.Errorf("line 1 missing URL: %q", wrapped[1])
		}
	})

	t.Run("should not have whitespace before underline reset code", func(t *testing.T) {
		underlineOn := "\x1b[4m"
		underlineOff := "\x1b[24m"
		text := underlineOn + "underlined text here " + underlineOff + "more"

		wrapped := WrapTextWithAnsi(text, 18)

		if strings.Contains(wrapped[0], " "+underlineOff) {
			t.Errorf("whitespace before underline reset: %q", wrapped[0])
		}
	})

	t.Run("should not bleed underline to padding - each line should end with reset for underline only", func(t *testing.T) {
		underlineOn := "\x1b[4m"
		underlineOff := "\x1b[24m"
		url := "https://example.com/very/long/path/that/will/definitely/wrap"
		text := "prefix " + underlineOn + url + underlineOff + " suffix"

		wrapped := WrapTextWithAnsi(text, 30)

		// Middle lines (with underlined content) should end with underline-off, not full reset
		for i := 1; i < len(wrapped)-1; i++ {
			line := wrapped[i]
			if strings.Contains(line, underlineOn) {
				// Should end with underline off, NOT full reset
				if !strings.HasSuffix(line, underlineOff) {
					t.Errorf("line %d does not end with underline off: %q", i, line)
				}
				if strings.HasSuffix(line, "\x1b[0m") {
					t.Errorf("line %d ends with full reset: %q", i, line)
				}
			}
		}
	})
}

func TestWrapTextWithAnsiBackgroundPreservation(t *testing.T) {
	t.Run("should preserve background color across wrapped lines without full reset", func(t *testing.T) {
		bgBlue := "\x1b[44m"
		reset := "\x1b[0m"
		text := bgBlue + "hello world this is blue background text" + reset

		wrapped := WrapTextWithAnsi(text, 15)

		// Each line should have background color
		for i, line := range wrapped {
			if !strings.Contains(line, bgBlue) {
				t.Errorf("line %d missing background: %q", i, line)
			}
		}

		// Middle lines should NOT end with full reset (kills background for padding)
		for i := 0; i < len(wrapped)-1; i++ {
			if strings.HasSuffix(wrapped[i], reset) {
				t.Errorf("line %d ends with full reset: %q", i, wrapped[i])
			}
		}
	})

	t.Run("should reset underline but preserve background when wrapping underlined text inside background", func(t *testing.T) {
		underlineOn := "\x1b[4m"
		underlineOff := "\x1b[24m"
		reset := "\x1b[0m"

		text := "\x1b[41mprefix " + underlineOn + "UNDERLINED_CONTENT_THAT_WRAPS" + underlineOff + " suffix" + reset

		wrapped := WrapTextWithAnsi(text, 20)

		// All lines should have background color 41 (either as \x1b[41m or combined like \x1b[4;41m)
		for i, line := range wrapped {
			hasBgColor := strings.Contains(line, "[41m") || strings.Contains(line, ";41m") || strings.Contains(line, "[41;")
			if !hasBgColor {
				t.Errorf("line %d missing background color 41: %q", i, line)
			}
		}

		// Lines with underlined content should use underline-off at end, not full reset
		for i := 0; i < len(wrapped)-1; i++ {
			line := wrapped[i]
			// If this line has underline on, it should end with underline off (not full reset)
			hasUnderline := strings.Contains(line, "[4m") || strings.Contains(line, "[4;") || strings.Contains(line, ";4m")
			if hasUnderline && !strings.Contains(line, underlineOff) {
				if !strings.HasSuffix(line, underlineOff) {
					t.Errorf("line %d does not end with underline off: %q", i, line)
				}
				if strings.HasSuffix(line, reset) {
					t.Errorf("line %d ends with full reset: %q", i, line)
				}
			}
		}
	})
}

func TestWrapTextWithAnsiBasic(t *testing.T) {
	t.Run("should handle LF, CRLF, and CR line endings", func(t *testing.T) {
		got := WrapTextWithAnsi("first\nsecond\r\nthird\rfourth", 80)
		want := []string{"first", "second", "third", "fourth"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			assertEqualStr(t, got[i], want[i])
		}
	})

	t.Run("should preserve ANSI state across CRLF and CR line endings", func(t *testing.T) {
		red := "\x1b[31m"
		reset := "\x1b[0m"

		got := WrapTextWithAnsi(red+"first\r\nsecond\rthird"+reset, 80)
		want := []string{red + "first", red + "second", red + "third" + reset}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			assertEqualStr(t, got[i], want[i])
		}
	})

	t.Run("should wrap plain text correctly", func(t *testing.T) {
		text := "hello world this is a test"
		wrapped := WrapTextWithAnsi(text, 10)

		if len(wrapped) <= 1 {
			t.Fatalf("expected multiple lines, got %v", wrapped)
		}
		for i, line := range wrapped {
			if w := VisibleWidth(line); w > 10 {
				t.Errorf("line %d too wide (%d): %q", i, w, line)
			}
		}
	})

	t.Run("should break CJK runs at grapheme boundaries after Latin text", func(t *testing.T) {
		text := "This is an example 中文汉字测试段落内容中文汉字测试段落内容."
		wrapped := WrapTextWithAnsi(text, 40)

		want := []string{"This is an example 中文汉字测试段落内容", "中文汉字测试段落内容."}
		if len(wrapped) != len(want) {
			t.Fatalf("got %v, want %v", wrapped, want)
		}
		for i := range want {
			assertEqualStr(t, wrapped[i], want[i])
		}
		for i, line := range wrapped {
			if w := VisibleWidth(line); w > 40 {
				t.Errorf("line %d too wide (%d): %q", i, w, line)
			}
		}
	})

	t.Run("should preserve color codes when wrapping CJK runs", func(t *testing.T) {
		red := "\x1b[31m"
		reset := "\x1b[0m"
		text := red + "This is an example 中文汉字测试段落内容中文汉字测试段落内容." + reset
		wrapped := WrapTextWithAnsi(text, 40)

		if len(wrapped) != 2 {
			t.Fatalf("expected 2 lines, got %v", wrapped)
		}
		assertEqualStr(t, wrapped[0], red+"This is an example 中文汉字测试段落内容")
		assertEqualStr(t, wrapped[1], red+"中文汉字测试段落内容."+reset)
		for i, line := range wrapped {
			if w := VisibleWidth(line); w > 40 {
				t.Errorf("line %d too wide (%d): %q", i, w, line)
			}
		}
	})

	t.Run("should ignore OSC 133 semantic markers in visible width", func(t *testing.T) {
		text := "\x1b]133;A\x07hello\x1b]133;B\x07"
		if got := VisibleWidth(text); got != 5 {
			t.Errorf("width = %d, want 5", got)
		}
	})

	t.Run("should ignore OSC sequences terminated with ST in visible width", func(t *testing.T) {
		text := "\x1b]133;A\x1b\\hello\x1b]133;B\x1b\\"
		if got := VisibleWidth(text); got != 5 {
			t.Errorf("width = %d, want 5", got)
		}
	})

	t.Run("should treat isolated regional indicators as width 2", func(t *testing.T) {
		if VisibleWidth("🇨") != 2 {
			t.Errorf("🇨 width = %d, want 2", VisibleWidth("🇨"))
		}
		if VisibleWidth("🇨🇳") != 2 {
			t.Errorf("🇨🇳 width = %d, want 2", VisibleWidth("🇨🇳"))
		}
	})

	t.Run("should truncate trailing whitespace that exceeds width", func(t *testing.T) {
		twoSpacesWrappedToWidth1 := WrapTextWithAnsi("  ", 1)
		if w := VisibleWidth(twoSpacesWrappedToWidth1[0]); w > 1 {
			t.Errorf("width = %d, want <= 1", w)
		}
	})

	t.Run("should preserve color codes across wraps", func(t *testing.T) {
		red := "\x1b[31m"
		reset := "\x1b[0m"
		text := red + "hello world this is red" + reset

		wrapped := WrapTextWithAnsi(text, 10)

		// Each continuation line should start with red code
		for i := 1; i < len(wrapped); i++ {
			if !strings.HasPrefix(wrapped[i], red) {
				t.Errorf("line %d does not start with red: %q", i, wrapped[i])
			}
		}

		// Middle lines should not end with full reset
		for i := 0; i < len(wrapped)-1; i++ {
			if strings.HasSuffix(wrapped[i], "\x1b[0m") {
				t.Errorf("line %d ends with full reset: %q", i, wrapped[i])
			}
		}
	})
}

func TestWrapTextWithAnsiOsc8Hyperlinks(t *testing.T) {
	t.Run("re-emits OSC 8 open at the start of continuation lines", func(t *testing.T) {
		// A hyperlink whose text is long enough to wrap
		url := "https://example.com"
		// OSC 8 open + text that is 10 visible chars + OSC 8 close
		input := "\x1b]8;;" + url + "\x1b\\0123456789\x1b]8;;\x1b\\"
		lines := WrapTextWithAnsi(input, 6)

		// Every line that contains visible text from inside the hyperlink
		// should start with the OSC 8 open sequence (or be preceded by it).
		for _, line := range lines {
			// If the line has visible content it must begin with the OSC 8 re-open
			// OR it is the line where the close appeared with no following content.
			stripped := osc8OpenStripRegex.ReplaceAllString(line, "")
			stripped = sgrStripRegex.ReplaceAllString(stripped, "")
			if len(jsTrim(stripped)) > 0 {
				if !strings.HasPrefix(line, "\x1b]8;;"+url+"\x1b\\") && !strings.Contains(line, "\x1b]8;;"+url+"\x1b\\") {
					t.Errorf("line %q has visible text but no OSC 8 re-open", line)
				}
			}
		}
	})

	t.Run("closes OSC 8 before each line break", func(t *testing.T) {
		url := "https://example.com"
		input := "\x1b]8;;" + url + "\x1b\\0123456789\x1b]8;;\x1b\\"
		lines := WrapTextWithAnsi(input, 6)

		for i := 0; i < len(lines)-1; i++ {
			line := lines[i]
			// Every non-final line that is inside a hyperlink should end with the close
			if strings.Contains(line, "\x1b]8;;"+url+"\x1b\\") {
				if !strings.HasSuffix(line, "\x1b]8;;\x1b\\") {
					t.Errorf("non-final line %q is inside a hyperlink but does not close it", line)
				}
			}
		}
	})

	t.Run("preserves BEL terminators when wrapping OAuth-style hyperlinks", func(t *testing.T) {
		url := "https://example.com/oauth/" + strings.Repeat("a", 32)
		input := "\x1b]8;;" + url + "\x07" + url + "\x1b]8;;\x07"
		lines := WrapTextWithAnsi(input, 20)

		if len(lines) <= 1 {
			t.Fatalf("expected multiple lines, got %v", lines)
		}
		for _, line := range lines {
			if !strings.Contains(line, "\x1b]8;;"+url+"\x07") {
				t.Errorf("line %q does not reopen the hyperlink with BEL", line)
			}
			if strings.Contains(line, "\x1b]8;;"+url+"\x1b\\") {
				t.Errorf("line %q reopens the hyperlink with ST", line)
			}
		}
		for _, line := range lines[:len(lines)-1] {
			if !strings.HasSuffix(line, "\x1b]8;;\x07") {
				t.Errorf("line %q does not close the hyperlink with BEL", line)
			}
		}
	})

	t.Run("does not emit OSC 8 sequences on lines that are outside the hyperlink", func(t *testing.T) {
		url := "https://example.com"
		input := "before \x1b]8;;" + url + "\x1b\\link\x1b]8;;\x1b\\ after"
		lines := WrapTextWithAnsi(input, 80)

		// With width 80 everything fits on one line; there should be exactly one
		// OSC 8 open and one OSC 8 close.
		if len(lines) != 1 {
			t.Fatalf("expected 1 line, got %v", lines)
		}
		openCount := len(osc8UrlCountRegex.FindAllString(lines[0], -1))
		closeCount := len(osc8CloseCountRegex.FindAllString(lines[0], -1))
		if openCount != 1 {
			t.Errorf("open count = %d, want 1", openCount)
		}
		if closeCount != 1 {
			t.Errorf("close count = %d, want 1", closeCount)
		}
	})
}

var (
	// line.replace(/\x1b\]8;;[^\x1b\x07]*\x1b\\/g, "")
	osc8OpenStripRegex = regexp.MustCompile(`\x1b\]8;;[^\x1b\x07]*\x1b\\`)
	// .replace(/\x1b\[[0-9;]*m/g, "")
	sgrStripRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	// /\x1b\]8;;https:[^\x1b]+\x1b\\/g
	osc8UrlCountRegex = regexp.MustCompile(`\x1b\]8;;https:[^\x1b]+\x1b\\`)
	// /\x1b\]8;;\x1b\\/g
	osc8CloseCountRegex = regexp.MustCompile(`\x1b\]8;;\x1b\\`)
)
