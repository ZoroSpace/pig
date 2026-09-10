// tui/truncated_text_test.go (pi: packages/tui/test/truncated-text.test.ts)
package tui

import (
	"strings"
	"testing"
)

// chalk level-3 color helpers (pi: new Chalk({ level: 3 }) — literal ANSI bytes,
// testEditorTheme precedent).
func chalkRed(text string) string  { return "\x1b[31m" + text + "\x1b[39m" }
func chalkBlue(text string) string { return "\x1b[34m" + text + "\x1b[39m" }
func TestTruncatedTextPadsOutputLinesToExactlyMatchWidth(t *testing.T) {
	text := NewTruncatedText("Hello world", 1, 0)
	lines := text.Render(50)

	// Should have exactly one content line (no vertical padding)
	if len(lines) != 1 {
		t.Fatalf("lines.length = %d, want 1", len(lines))
	}

	// Line should be exactly 50 visible characters
	if got := VisibleWidth(lines[0]); got != 50 {
		t.Fatalf("visibleWidth = %d, want 50", got)
	}
}

func TestTruncatedTextPadsOutputWithVerticalPaddingLinesToWidth(t *testing.T) {
	text := NewTruncatedText("Hello", 0, 2)
	lines := text.Render(40)

	// Should have 2 padding lines + 1 content line + 2 padding lines = 5 total
	if len(lines) != 5 {
		t.Fatalf("lines.length = %d, want 5", len(lines))
	}

	// All lines should be exactly 40 characters
	for i, line := range lines {
		if got := VisibleWidth(line); got != 40 {
			t.Fatalf("lines[%d] visibleWidth = %d, want 40", i, got)
		}
	}
}

func TestTruncatedTextTruncatesLongTextAndPadsToWidth(t *testing.T) {
	longText := "This is a very long piece of text that will definitely exceed the available width"
	text := NewTruncatedText(longText, 1, 0)
	lines := text.Render(30)

	if len(lines) != 1 {
		t.Fatalf("lines.length = %d, want 1", len(lines))
	}

	// Should be exactly 30 characters
	if got := VisibleWidth(lines[0]); got != 30 {
		t.Fatalf("visibleWidth = %d, want 30", got)
	}

	// Should contain ellipsis
	stripped := stripAnsi(lines[0])
	if !strings.Contains(stripped, "...") {
		t.Fatalf("expected ellipsis in %q", stripped)
	}
}

func TestTruncatedTextPreservesAnsiCodesInOutputAndPadsCorrectly(t *testing.T) {
	styledText := chalkRed("Hello") + " " + chalkBlue("world")
	text := NewTruncatedText(styledText, 1, 0)
	lines := text.Render(40)

	if len(lines) != 1 {
		t.Fatalf("lines.length = %d, want 1", len(lines))
	}

	// Should be exactly 40 visible characters (ANSI codes don't count)
	if got := VisibleWidth(lines[0]); got != 40 {
		t.Fatalf("visibleWidth = %d, want 40", got)
	}

	// Should preserve the color codes
	if !strings.Contains(lines[0], "\x1b[") {
		t.Fatalf("expected ANSI codes in output")
	}
}

func TestTruncatedTextTruncatesStyledTextAndAddsResetCodeBeforeEllipsis(t *testing.T) {
	longStyledText := chalkRed("This is a very long red text that will be truncated")
	text := NewTruncatedText(longStyledText, 1, 0)
	lines := text.Render(20)

	if len(lines) != 1 {
		t.Fatalf("lines.length = %d, want 1", len(lines))
	}

	// Should be exactly 20 visible characters
	if got := VisibleWidth(lines[0]); got != 20 {
		t.Fatalf("visibleWidth = %d, want 20", got)
	}

	// Should contain reset code before ellipsis
	if !strings.Contains(lines[0], "\x1b[0m...") {
		t.Fatalf("expected reset before ellipsis in %q", lines[0])
	}
}

func TestTruncatedTextHandlesTextThatFitsExactly(t *testing.T) {
	// With paddingX=1, available width is 30-2=28
	// "Hello world" is 11 chars, fits comfortably
	text := NewTruncatedText("Hello world", 1, 0)
	lines := text.Render(30)

	if len(lines) != 1 {
		t.Fatalf("lines.length = %d, want 1", len(lines))
	}
	if got := VisibleWidth(lines[0]); got != 30 {
		t.Fatalf("visibleWidth = %d, want 30", got)
	}

	// Should NOT contain ellipsis
	stripped := stripAnsi(lines[0])
	if strings.Contains(stripped, "...") {
		t.Fatalf("did not expect ellipsis in %q", stripped)
	}
}

func TestTruncatedTextHandlesEmptyText(t *testing.T) {
	text := NewTruncatedText("", 1, 0)
	lines := text.Render(30)

	if len(lines) != 1 {
		t.Fatalf("lines.length = %d, want 1", len(lines))
	}
	if got := VisibleWidth(lines[0]); got != 30 {
		t.Fatalf("visibleWidth = %d, want 30", got)
	}
}

func TestTruncatedTextStopsAtNewlineAndOnlyShowsFirstLine(t *testing.T) {
	multilineText := "First line\nSecond line\nThird line"
	text := NewTruncatedText(multilineText, 1, 0)
	lines := text.Render(40)

	if len(lines) != 1 {
		t.Fatalf("lines.length = %d, want 1", len(lines))
	}
	if got := VisibleWidth(lines[0]); got != 40 {
		t.Fatalf("visibleWidth = %d, want 40", got)
	}

	// Should only contain "First line"
	stripped := strings.TrimSpace(stripAnsi(lines[0]))
	if !strings.Contains(stripped, "First line") {
		t.Fatalf("expected first line in %q", stripped)
	}
	if strings.Contains(stripped, "Second line") || strings.Contains(stripped, "Third line") {
		t.Fatalf("did not expect later lines in %q", stripped)
	}
}

func TestTruncatedTextTruncatesFirstLineEvenWithNewlinesInText(t *testing.T) {
	longMultilineText := "This is a very long first line that needs truncation\nSecond line"
	text := NewTruncatedText(longMultilineText, 1, 0)
	lines := text.Render(25)

	if len(lines) != 1 {
		t.Fatalf("lines.length = %d, want 1", len(lines))
	}
	if got := VisibleWidth(lines[0]); got != 25 {
		t.Fatalf("visibleWidth = %d, want 25", got)
	}

	// Should contain ellipsis and not second line
	stripped := stripAnsi(lines[0])
	if !strings.Contains(stripped, "...") {
		t.Fatalf("expected ellipsis in %q", stripped)
	}
	if strings.Contains(stripped, "Second line") {
		t.Fatalf("did not expect second line in %q", stripped)
	}
}
