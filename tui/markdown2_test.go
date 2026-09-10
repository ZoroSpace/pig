// tui/markdown2_test.go — second half of pi's markdown.test.ts port
// (blockquote styling, heading inline-code restore, strikethrough, links,
// HTML-like tags, streaming fences, pre-styled thinking text).
package tui

import (
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

// --- Pre-styled text (thinking traces) ---

func TestMarkdownPreservesGrayItalicStylingAfterInlineCode(t *testing.T) {
	markdown := NewMarkdown(
		"This is thinking with `inline code` and more text after",
		1, 0, defaultMarkdownTheme,
		&DefaultTextStyle{Color: chalkGray, Italic: true}, nil)

	lines := markdown.Render(80)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "inline code") {
		t.Fatal("should contain the inline code block")
	}
	if !strings.Contains(joined, "\x1b[90m") {
		t.Fatal("should have gray color code")
	}
	if !strings.Contains(joined, "\x1b[3m") {
		t.Fatal("should have italic code")
	}
	if !strings.Contains(joined, "\x1b[33m") {
		t.Fatal("should style inline code (yellow)")
	}
}

func TestMarkdownPreservesGrayItalicStylingAfterBoldText(t *testing.T) {
	markdown := NewMarkdown(
		"This is thinking with **bold text** and more after",
		1, 0, defaultMarkdownTheme,
		&DefaultTextStyle{Color: chalkGray, Italic: true}, nil)

	lines := markdown.Render(80)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "bold text") {
		t.Fatal("should contain bold text")
	}
	if !strings.Contains(joined, "\x1b[90m") {
		t.Fatal("should have gray color code")
	}
	if !strings.Contains(joined, "\x1b[3m") {
		t.Fatal("should have italic code")
	}
	if !strings.Contains(joined, "\x1b[1m") {
		t.Fatal("should have bold code")
	}
}

func TestMarkdownDoesNotLeakStylesIntoFollowingLinesInTUI(t *testing.T) {
	var markdownLineCount atomic.Int32
	component := &markdownWithInput{
		markdown: NewMarkdown("This is thinking with `inline code`", 1, 0, defaultMarkdownTheme,
			&DefaultTextStyle{Color: chalkGray, Italic: true}, nil),
		count: &markdownLineCount,
	}

	terminal := NewVirtualTerminal(80, 6)
	tui := NewTuiMainScreen(terminal, nil, "")
	tui.AddChild(component)
	tui.Start()
	defer tui.Stop(&TuiStopOptions{})
	terminal.WaitForRender()

	if markdownLineCount.Load() <= 0 {
		t.Fatal("expected markdown line count > 0")
	}
	inputRow := int(markdownLineCount.Load())
	if terminal.GetCellItalic(inputRow, 0) {
		t.Fatal("input row should not be italic")
	}
}

// markdownWithInput mirrors pi's local MarkdownWithInput helper (the counter
// is atomic: pi's test reads it from the event loop, pig's from the test
// goroutine — see the goroutine-written counter precedent in autocomplete
// tests).
type markdownWithInput struct {
	markdown *Markdown
	count    *atomic.Int32
}

func (c *markdownWithInput) Render(width int) []string {
	lines := c.markdown.Render(width)
	c.count.Store(int32(len(lines)))
	return append(append([]string{}, lines...), "INPUT")
}

func (c *markdownWithInput) Invalidate() {
	c.markdown.Invalidate()
}

// --- Spacing after code blocks ---

func TestMarkdownOneBlankLineBetweenCodeBlockAndFollowingParagraph(t *testing.T) {
	markdown := NewMarkdown(
		"hello world\n\n```js\nconst hello = \"world\";\n```\n\nagain, hello world",
		0, 0, defaultMarkdownTheme, nil, nil)

	plainLines := mapStripTrim(markdown.Render(80))

	closingIndex := -1
	for i, line := range plainLines {
		if line == "```" {
			closingIndex = i
			break
		}
	}
	if closingIndex == -1 {
		t.Fatal("should have closing backticks")
	}

	afterBackticks := plainLines[closingIndex+1:]
	emptyLineCount := -1
	for i, line := range afterBackticks {
		if line != "" {
			emptyLineCount = i
			break
		}
	}
	if emptyLineCount != 1 {
		t.Fatalf("expected 1 empty line after code block, found %d; after: %q", emptyLineCount, afterBackticks)
	}
}

func TestMarkdownNormalizesParagraphAndCodeBlockSpacingToOneBlankLine(t *testing.T) {
	cases := []string{
		"hello this is text\n```\ncode block\n```\nmore text",
		"hello this is text\n\n```\ncode block\n```\n\nmore text",
	}
	expectedLines := []string{"hello this is text", "", "```", "  code block", "```", "", "more text"}

	for _, text := range cases {
		markdown := NewMarkdown(text, 0, 0, defaultMarkdownTheme, nil, nil)
		plainLines := mapStripTrim(markdown.Render(80))
		assertDeepLines(t, plainLines, expectedLines, "spacing for "+text)
	}
}

func TestMarkdownNoTrailingBlankLineWhenCodeBlockIsLastBlock(t *testing.T) {
	cases := []string{"```js\nconst hello = 'world';\n```", "hello world\n\n```js\nconst hello = 'world';\n```"}

	for _, text := range cases {
		markdown := NewMarkdown(text, 0, 0, defaultMarkdownTheme, nil, nil)
		plainLines := mapStripTrim(markdown.Render(80))
		if plainLines[len(plainLines)-1] == "" {
			t.Fatalf("expected code block to end without a blank line: %q", plainLines)
		}
	}
}

// --- Spacing after dividers ---

func TestMarkdownOneBlankLineBetweenDividerAndFollowingParagraph(t *testing.T) {
	markdown := NewMarkdown("hello world\n\n---\n\nagain, hello world", 0, 0, defaultMarkdownTheme, nil, nil)

	plainLines := mapStripTrim(markdown.Render(80))

	dividerIndex := -1
	for i, line := range plainLines {
		if strings.Contains(line, "─") {
			dividerIndex = i
			break
		}
	}
	if dividerIndex == -1 {
		t.Fatal("should have divider")
	}

	afterDivider := plainLines[dividerIndex+1:]
	emptyLineCount := -1
	for i, line := range afterDivider {
		if line != "" {
			emptyLineCount = i
			break
		}
	}
	if emptyLineCount != 1 {
		t.Fatalf("expected 1 empty line after divider, found %d", emptyLineCount)
	}
}

func TestMarkdownNoTrailingBlankLineWhenDividerIsLastBlock(t *testing.T) {
	markdown := NewMarkdown("---", 0, 0, defaultMarkdownTheme, nil, nil)
	plainLines := mapStripTrim(markdown.Render(80))
	if plainLines[len(plainLines)-1] == "" {
		t.Fatalf("expected divider to end without a blank line: %q", plainLines)
	}
}

// --- Spacing after headings ---

func TestMarkdownOneBlankLineBetweenHeadingAndFollowingParagraph(t *testing.T) {
	markdown := NewMarkdown("# Hello\n\nThis is a paragraph", 0, 0, defaultMarkdownTheme, nil, nil)

	plainLines := mapStripTrim(markdown.Render(80))

	headingIndex := -1
	for i, line := range plainLines {
		if strings.Contains(line, "Hello") {
			headingIndex = i
			break
		}
	}
	if headingIndex == -1 {
		t.Fatal("should have heading")
	}

	afterHeading := plainLines[headingIndex+1:]
	emptyLineCount := -1
	for i, line := range afterHeading {
		if line != "" {
			emptyLineCount = i
			break
		}
	}
	if emptyLineCount != 1 {
		t.Fatalf("expected 1 empty line after heading, found %d", emptyLineCount)
	}
}

func TestMarkdownNoTrailingBlankLineWhenHeadingIsLastBlock(t *testing.T) {
	markdown := NewMarkdown("# Hello", 0, 0, defaultMarkdownTheme, nil, nil)
	plainLines := mapStripTrim(markdown.Render(80))
	if plainLines[len(plainLines)-1] == "" {
		t.Fatalf("expected heading to end without a blank line: %q", plainLines)
	}
}

// --- Spacing after blockquotes ---

func TestMarkdownOneBlankLineBetweenBlockquoteAndFollowingParagraph(t *testing.T) {
	markdown := NewMarkdown("hello world\n\n> This is a quote\n\nagain, hello world", 0, 0, defaultMarkdownTheme, nil, nil)

	plainLines := mapStripTrim(markdown.Render(80))

	quoteIndex := -1
	for i, line := range plainLines {
		if strings.Contains(line, "This is a quote") {
			quoteIndex = i
			break
		}
	}
	if quoteIndex == -1 {
		t.Fatal("should have blockquote")
	}

	afterQuote := plainLines[quoteIndex+1:]
	emptyLineCount := -1
	for i, line := range afterQuote {
		if line != "" {
			emptyLineCount = i
			break
		}
	}
	if emptyLineCount != 1 {
		t.Fatalf("expected 1 empty line after blockquote, found %d", emptyLineCount)
	}
}

func TestMarkdownNoTrailingBlankLineWhenBlockquoteIsLastBlock(t *testing.T) {
	markdown := NewMarkdown("> This is a quote", 0, 0, defaultMarkdownTheme, nil, nil)
	plainLines := mapStripTrim(markdown.Render(80))
	if plainLines[len(plainLines)-1] == "" {
		t.Fatalf("expected blockquote to end without a blank line: %q", plainLines)
	}
}

// --- Blockquotes with multiline content ---

func TestMarkdownConsistentStylingInLazyContinuationBlockquote(t *testing.T) {
	markdown := NewMarkdown(">Foo\nbar", 0, 0, defaultMarkdownTheme,
		&DefaultTextStyle{Color: chalkMagenta}, nil)

	lines := markdown.Render(80)

	plainLines := mapStrip(lines)
	quoted := 0
	for _, line := range plainLines {
		if strings.HasPrefix(line, "│ ") {
			quoted++
		}
	}
	if quoted != 2 {
		t.Fatalf("expected 2 quoted lines, got %d: %q", quoted, plainLines)
	}

	fooLine := ""
	barLine := ""
	for _, line := range lines {
		if strings.Contains(line, "Foo") {
			fooLine = line
		}
		if strings.Contains(line, "bar") {
			barLine = line
		}
	}
	if fooLine == "" || barLine == "" {
		t.Fatal("should have Foo and bar lines")
	}
	if !strings.Contains(fooLine, "\x1b[3m") {
		t.Fatalf("Foo line should have italic: %q", fooLine)
	}
	if !strings.Contains(barLine, "\x1b[3m") {
		t.Fatalf("bar line should have italic: %q", barLine)
	}
	if strings.Contains(fooLine, "\x1b[35m") {
		t.Fatalf("Foo line should NOT have magenta: %q", fooLine)
	}
	if strings.Contains(barLine, "\x1b[35m") {
		t.Fatalf("bar line should NOT have magenta: %q", barLine)
	}
}

func TestMarkdownConsistentStylingInExplicitMultilineBlockquote(t *testing.T) {
	markdown := NewMarkdown(">Foo\n>bar", 0, 0, defaultMarkdownTheme,
		&DefaultTextStyle{Color: chalkCyan}, nil)

	lines := markdown.Render(80)

	plainLines := mapStrip(lines)
	quoted := 0
	for _, line := range plainLines {
		if strings.HasPrefix(line, "│ ") {
			quoted++
		}
	}
	if quoted != 2 {
		t.Fatalf("expected 2 quoted lines, got %d: %q", quoted, plainLines)
	}

	fooLine := ""
	barLine := ""
	for _, line := range lines {
		if strings.Contains(line, "Foo") {
			fooLine = line
		}
		if strings.Contains(line, "bar") {
			barLine = line
		}
	}
	if !strings.Contains(fooLine, "\x1b[3m") {
		t.Fatalf("Foo line should have italic: %q", fooLine)
	}
	if !strings.Contains(barLine, "\x1b[3m") {
		t.Fatalf("bar line should have italic: %q", barLine)
	}
	if strings.Contains(fooLine, "\x1b[36m") {
		t.Fatalf("Foo line should NOT have cyan: %q", fooLine)
	}
	if strings.Contains(barLine, "\x1b[36m") {
		t.Fatalf("bar line should NOT have cyan: %q", barLine)
	}
}

func TestMarkdownRendersListContentInsideBlockquotes(t *testing.T) {
	markdown := NewMarkdown("> 1. bla bla\n> - nested bullet", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	quoted := []string{}
	for _, line := range mapStrip(lines) {
		if strings.HasPrefix(line, "│ ") {
			quoted = append(quoted, line)
		}
	}

	joined := strings.Join(quoted, "\n")
	if !strings.Contains(joined, "1. bla bla") {
		t.Fatalf("missing ordered list item: %q", quoted)
	}
	if !strings.Contains(joined, "- nested bullet") {
		t.Fatalf("missing unordered list item: %q", quoted)
	}
}

func TestMarkdownWrapsLongBlockquoteLinesAndAddsBorderToEachWrappedLine(t *testing.T) {
	longText := "This is a very long blockquote line that should wrap to multiple lines when rendered"
	markdown := NewMarkdown("> "+longText, 0, 0, defaultMarkdownTheme, nil, nil)

	lines := mapStripTrim(markdown.Render(30))

	contentLines := []string{}
	for _, line := range lines {
		if line != "" {
			contentLines = append(contentLines, line)
		}
	}
	if len(contentLines) <= 1 {
		t.Fatalf("expected multiple wrapped lines, got: %q", contentLines)
	}
	for _, line := range contentLines {
		if !strings.HasPrefix(line, "│ ") {
			t.Fatalf("wrapped line should have quote border: %q", line)
		}
	}
	allText := strings.Join(contentLines, " ")
	for _, want := range []string{"very long", "blockquote", "multiple"} {
		if !strings.Contains(allText, want) {
			t.Fatalf("should preserve %q", want)
		}
	}
}

func TestMarkdownProperlyIndentsWrappedBlockquoteLinesWithStyling(t *testing.T) {
	markdown := NewMarkdown("> This is styled text that is long enough to wrap", 0, 0, defaultMarkdownTheme,
		&DefaultTextStyle{Color: chalkYellow, Italic: true}, nil)

	lines := markdown.Render(25)
	plainLines := mapStripTrim(lines)

	contentLines := []string{}
	for _, line := range plainLines {
		if line != "" {
			contentLines = append(contentLines, line)
		}
	}
	for _, line := range contentLines {
		if !strings.HasPrefix(line, "│ ") {
			t.Fatalf("line should have quote border: %q", line)
		}
	}

	allOutput := strings.Join(lines, "\n")
	if !strings.Contains(allOutput, "\x1b[3m") {
		t.Fatal("should have italic")
	}
	if strings.Contains(allOutput, "\x1b[33m") {
		t.Fatal("should NOT have yellow from default style")
	}
}

func TestMarkdownReappliesQuoteStylingAfterInlineFormatting(t *testing.T) {
	markdown := NewMarkdown("> Quote with **bold** and `code`", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	plainLines := mapStrip(lines)

	hasBorder := false
	for _, line := range plainLines {
		if strings.HasPrefix(line, "│ ") {
			hasBorder = true
		}
	}
	if !hasBorder {
		t.Fatal("should have quote border")
	}

	allPlain := strings.Join(plainLines, " ")
	for _, want := range []string{"Quote with", "bold", "code"} {
		if !strings.Contains(allPlain, want) {
			t.Fatalf("should preserve %q", want)
		}
	}

	allOutput := strings.Join(lines, "\n")
	if !strings.Contains(allOutput, "\x1b[1m") {
		t.Fatal("should have bold styling")
	}
	if !strings.Contains(allOutput, "\x1b[33m") {
		t.Fatal("should have code styling (yellow)")
	}
	if !strings.Contains(allOutput, "\x1b[3m") {
		t.Fatal("should have italic from quote styling")
	}
}

// --- Heading with inline code ---

func TestMarkdownPreservesHeadingStylingAfterInlineCode(t *testing.T) {
	markdown := NewMarkdown("### Why `sourceInfo` should not be optional", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "\x1b[33m") {
		t.Fatal("should have yellow for inline code")
	}

	afterCodeIndex := strings.Index(joined, "should not be optional")
	if afterCodeIndex <= 0 {
		t.Fatal("should contain text after inline code")
	}

	start := afterCodeIndex - 40
	if start < 0 {
		start = 0
	}
	precedingChunk := joined[start:afterCodeIndex]
	if !strings.Contains(precedingChunk, "\x1b[1m") {
		t.Fatalf("should re-apply bold before text after code: %q", precedingChunk)
	}
	if !strings.Contains(precedingChunk, "\x1b[36m") {
		t.Fatalf("should re-apply cyan before text after code: %q", precedingChunk)
	}
}

func TestMarkdownPreservesHeadingStylingAfterInlineCodeForH1(t *testing.T) {
	markdown := NewMarkdown("# Title with `code` inside", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	joined := strings.Join(lines, "\n")

	afterCodeIndex := strings.Index(joined, "inside")
	if afterCodeIndex <= 0 {
		t.Fatal("should contain text after inline code")
	}

	start := afterCodeIndex - 40
	if start < 0 {
		start = 0
	}
	precedingChunk := joined[start:afterCodeIndex]
	if !strings.Contains(precedingChunk, "\x1b[1m") {
		t.Fatalf("should re-apply bold for h1: %q", precedingChunk)
	}
	if !strings.Contains(precedingChunk, "\x1b[36m") {
		t.Fatalf("should re-apply cyan for h1: %q", precedingChunk)
	}
	if !strings.Contains(precedingChunk, "\x1b[4m") {
		t.Fatalf("should re-apply underline for h1: %q", precedingChunk)
	}
}

func TestMarkdownDoesNotLeakH1UnderlineIntoPadding(t *testing.T) {
	markdown := NewMarkdown("# Important distinction from `open()`", 0, 0, defaultMarkdownTheme, nil, nil)
	terminal := NewVirtualTerminal(80, 4)
	tui := NewTuiMainScreen(terminal, nil, "")
	tui.AddChild(markdown)
	tui.Start()
	defer tui.Stop(&TuiStopOptions{})
	terminal.WaitForRender()

	renderedLine := markdown.Render(80)[0]
	contentWidth := len(strings.TrimRight(stripSGR(renderedLine), " "))
	if contentWidth <= 0 {
		t.Fatal("should have visible heading content")
	}

	for col := contentWidth; col < 80; col++ {
		if terminal.GetCellUnderline(0, col) {
			t.Fatalf("expected no underline in padding at col %d", col)
		}
	}
}

func TestMarkdownPreservesHeadingStylingAfterBoldText(t *testing.T) {
	markdown := NewMarkdown("## Heading with **bold** and more", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	joined := strings.Join(lines, "\n")

	afterBoldIndex := strings.Index(joined, "and more")
	if afterBoldIndex <= 0 {
		t.Fatal("should contain text after bold")
	}

	start := afterBoldIndex - 40
	if start < 0 {
		start = 0
	}
	precedingChunk := joined[start:afterBoldIndex]
	if !strings.Contains(precedingChunk, "\x1b[1m") {
		t.Fatalf("should re-apply bold for h2: %q", precedingChunk)
	}
	if !strings.Contains(precedingChunk, "\x1b[36m") {
		t.Fatalf("should re-apply cyan for h2: %q", precedingChunk)
	}
}

// --- Strikethrough syntax ---

func TestMarkdownRendersDoubleTildeAsStrikethrough(t *testing.T) {
	markdown := NewMarkdown("Use ~~strikethrough~~ here", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	joinedOutput := strings.Join(lines, "\n")
	joinedPlain := strings.Join(mapStrip(lines), " ")

	if !strings.Contains(joinedOutput, "\x1b[9m") {
		t.Fatal("should apply strikethrough styling")
	}
	if !strings.Contains(joinedPlain, "strikethrough") {
		t.Fatal("should include struck text content")
	}
	if strings.Contains(joinedPlain, "~~strikethrough~~") {
		t.Fatal("should not render delimiters as text")
	}
}

func TestMarkdownKeepsSingleTildeAsPlainText(t *testing.T) {
	markdown := NewMarkdown("Use ~strikethrough~ literally", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	joinedOutput := strings.Join(lines, "\n")
	joinedPlain := strings.Join(mapStrip(lines), " ")

	if !strings.Contains(joinedPlain, "~strikethrough~") {
		t.Fatal("single-tilde delimiters should remain visible")
	}
	if strings.Contains(joinedOutput, "\x1b[9m") {
		t.Fatal("single-tilde text should not use strikethrough styling")
	}
}

// --- Links ---

func TestMarkdownNoDuplicateURLForAutolinkedEmails(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: nil, TrueColor: false, Hyperlinks: false})
	defer ResetCapabilitiesCache()

	markdown := NewMarkdown("Contact user@example.com for help", 0, 0, defaultMarkdownTheme, nil, nil)

	joinedPlain := strings.Join(mapStrip(markdown.Render(80)), " ")
	if !strings.Contains(joinedPlain, "user@example.com") {
		t.Fatal("should contain email")
	}
	if strings.Contains(joinedPlain, "mailto:") {
		t.Fatal("should not show mailto: prefix for autolinked emails")
	}
}

func TestMarkdownNoDuplicateURLForBareURLs(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: nil, TrueColor: false, Hyperlinks: false})
	defer ResetCapabilitiesCache()

	markdown := NewMarkdown("Visit https://example.com for more", 0, 0, defaultMarkdownTheme, nil, nil)

	joinedPlain := strings.Join(mapStrip(markdown.Render(80)), " ")
	if strings.Count(joinedPlain, "https://example.com") != 1 {
		t.Fatal("URL should appear exactly once")
	}
}

func TestMarkdownShowsURLInParenthesesWhenHyperlinksNotSupported(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: nil, TrueColor: false, Hyperlinks: false})
	defer ResetCapabilitiesCache()

	markdown := NewMarkdown("[click here](https://example.com)", 0, 0, defaultMarkdownTheme, nil, nil)

	joinedPlain := strings.Join(mapStrip(markdown.Render(80)), " ")
	if !strings.Contains(joinedPlain, "click here") {
		t.Fatal("should contain link text")
	}
	if !strings.Contains(joinedPlain, "(https://example.com)") {
		t.Fatal("should show URL in parentheses")
	}
}

func TestMarkdownShowsMailtoURLInParenthesesWhenHyperlinksNotSupported(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: nil, TrueColor: false, Hyperlinks: false})
	defer ResetCapabilitiesCache()

	markdown := NewMarkdown("[Email me](mailto:test@example.com)", 0, 0, defaultMarkdownTheme, nil, nil)

	joinedPlain := strings.Join(mapStrip(markdown.Render(80)), " ")
	if !strings.Contains(joinedPlain, "Email me") {
		t.Fatal("should contain link text")
	}
	if !strings.Contains(joinedPlain, "(mailto:test@example.com)") {
		t.Fatal("should show mailto URL in parentheses")
	}
}

func TestMarkdownEmitsOSC8HyperlinkWhenSupported(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: nil, TrueColor: false, Hyperlinks: true})
	defer ResetCapabilitiesCache()

	markdown := NewMarkdown("[click here](https://example.com)", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	joined := strings.Join(lines, "")

	if !strings.Contains(joined, "\x1b]8;;https://example.com\x1b\\") {
		t.Fatal("should contain OSC 8 open sequence")
	}
	if !strings.Contains(joined, "\x1b]8;;\x1b\\") {
		t.Fatal("should contain OSC 8 close sequence")
	}

	// Visible text present (strip OSC + SGR)
	var plain strings.Builder
	for _, line := range lines {
		t := osc8Regex.ReplaceAllString(line, "")
		t = stripSGR(t)
		plain.WriteString(t)
	}
	if !strings.Contains(plain.String(), "click here") {
		t.Fatal("should contain link text")
	}
	if strings.Contains(plain.String(), "(https://example.com)") {
		t.Fatal("URL should not appear inline in parentheses")
	}
}

var osc8Regex = regexp.MustCompile(`\x1b\]8;;[^\x1b]*\x1b\\`)

func TestMarkdownUsesOSC8ForMailtoLinksWhenSupported(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: nil, TrueColor: false, Hyperlinks: true})
	defer ResetCapabilitiesCache()

	markdown := NewMarkdown("[Email me](mailto:test@example.com)", 0, 0, defaultMarkdownTheme, nil, nil)

	joined := strings.Join(markdown.Render(80), "")

	if !strings.Contains(joined, "\x1b]8;;mailto:test@example.com\x1b\\") {
		t.Fatal("should contain OSC 8 open with mailto URL")
	}
	if !strings.Contains(joined, "\x1b]8;;\x1b\\") {
		t.Fatal("should contain OSC 8 close sequence")
	}
}

func TestMarkdownUsesOSC8ForBareURLsWhenSupported(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: nil, TrueColor: false, Hyperlinks: true})
	defer ResetCapabilitiesCache()

	markdown := NewMarkdown("Visit https://example.com for more", 0, 0, defaultMarkdownTheme, nil, nil)

	joined := strings.Join(markdown.Render(80), "")

	if !strings.Contains(joined, "\x1b]8;;https://example.com\x1b\\") {
		t.Fatal("should contain OSC 8 hyperlink")
	}
	var plain strings.Builder
	for _, line := range markdown.Render(80) {
		plain.WriteString(stripSGR(osc8Regex.ReplaceAllString(line, "")))
	}
	if strings.Contains(plain.String(), "(https://example.com)") {
		t.Fatal("URL should not appear twice")
	}
}

// --- HTML-like tags in text ---

func TestMarkdownRendersContentWithHTMLLikeTagsAsText(t *testing.T) {
	markdown := NewMarkdown(
		"This is text with <thinking>hidden content</thinking> that should be visible",
		0, 0, defaultMarkdownTheme, nil, nil)

	joinedPlain := strings.Join(mapStrip(markdown.Render(80)), " ")

	if !strings.Contains(joinedPlain, "hidden content") && !strings.Contains(joinedPlain, "<thinking>") {
		t.Fatal("should render HTML-like tags or their content as text, not hide them")
	}
}

func TestMarkdownRendersHTMLTagsInCodeBlocksCorrectly(t *testing.T) {
	markdown := NewMarkdown("```html\n<div>Some HTML</div>\n```", 0, 0, defaultMarkdownTheme, nil, nil)

	joinedPlain := strings.Join(mapStrip(markdown.Render(80)), "\n")

	if !strings.Contains(joinedPlain, "<div>") || !strings.Contains(joinedPlain, "</div>") {
		t.Fatal("should render HTML in code blocks")
	}
}

// --- Streaming code fences ---

func TestMarkdownStabilizesPartialClosingFenceRendering(t *testing.T) {
	cases := []struct {
		input    string
		expected []string
	}{
		{"```ts\nconst x = 1;\n``", []string{"```ts", "  const x = 1;", "```"}},
		{"```md\nnot a closing fence:\n``\n```", []string{"```md", "  not a closing fence:", "  ``", "```"}},
		{"```ts\n``", []string{"```ts", "", "```"}},
		{"````\n```", []string{"```", "", "```"}},
		{"~~~~~\n~~~~", []string{"```", "", "```"}},
		{"```md\nnot a closing fence:\n``\n```\n\nafter", []string{"```md", "  not a closing fence:", "  ``", "```", "", "after"}},
	}

	for i, tc := range cases {
		markdown := NewMarkdown(tc.input, 0, 0, defaultMarkdownTheme, nil, nil)
		lines := mapStripTrim(markdown.Render(80))
		assertDeepLines(t, lines, tc.expected, fmt.Sprintf("fence case %d", i))
	}

	partial := NewMarkdown("```ts\nconst x = 1;\n``", 0, 0, defaultMarkdownTheme, nil, nil)
	complete := NewMarkdown("```ts\nconst x = 1;\n```", 0, 0, defaultMarkdownTheme, nil, nil)

	if len(partial.Render(80)) != len(complete.Render(80)) {
		t.Fatal("partial and complete fence renders should have equal line counts")
	}
}
