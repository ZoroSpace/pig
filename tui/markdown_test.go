// tui/markdown_test.go (pi: packages/tui/test/markdown.test.ts)
//
// Deferred cases (declared):
//   - LaTeX tests that need real math rendering ("renders inline dollar...",
//     "renders display dollar...", "renders display bracket...", "aligns
//     matrix rows...", "renders lower limits...", "renders math inside lists
//     and tables", "switches from raw to rendered...") require latex.ts
//     renderLatex (README Phase 6 延后); the raw-fallback LaTeX tests are
//     ported below and pass with the nil renderLatexFn seam.
//   - "should not leak wrapped link styles into table borders or plain cells"
//     and "should restore the enclosing style after a wrapped table link"
//     need the VT simulator's fg-color cell attributes (isFgDefault/isDim/
//     getFgColor) which the mini VT does not track; they port when the
//     simulator gains fg tracking.
package tui

import (
	"regexp"
	"strings"
	"testing"
)

// stripSGR strips SGR sequences only (pi test-local stripAnsi: line.replace
// (/\x1b\[[0-9;]*m/g, "") — OSC 8 sequences are NOT stripped here).
var stripSGRRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripSGR(line string) string {
	return stripSGRRe.ReplaceAllString(line, "")
}

// mapTrimEnd maps lines through stripAnsi + trimEnd (pi's common transform).
func mapStripTrim(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimRight(stripSGR(l), " ")
	}
	return out
}

// mapStripBoth strips SGR and trims both ends (pi: .map(l => stripAnsi(l).trim())).
func mapStripBoth(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimSpace(stripSGR(l))
	}
	return out
}

func mapStrip(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = stripSGR(l)
	}
	return out
}

func assertDeepLines(t *testing.T, got, want []string, context string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: lines = %q, want %q", context, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: lines[%d] = %q, want %q (all: %q vs %q)", context, i, got[i], want[i], got, want)
		}
	}
}

// --- Transforms ---

func TestMarkdownCachesTransformedBySourceAndWidth(t *testing.T) {
	type call struct {
		source         string
		availableWidth int
	}
	var calls []call
	markdown := NewMarkdown("source", 2, 0, defaultMarkdownTheme, nil, &MarkdownOptions{
		Transform: func(source string, availableWidth int) string {
			calls = append(calls, call{source, availableWidth})
			return source + " " + itoaTest(availableWidth)
		},
	})

	assertDeepLines(t, mapStripBoth(markdown.Render(80)), []string{"source 76"}, "render 80")
	markdown.Render(80)
	assertDeepLines(t, mapStripBoth(markdown.Render(60)), []string{"source 56"}, "render 60")
	if len(calls) != 2 || calls[0] != (call{"source", 76}) || calls[1] != (call{"source", 56}) {
		t.Fatalf("calls = %v", calls)
	}

	markdown.SetText("updated")
	assertDeepLines(t, mapStripBoth(markdown.Render(60)), []string{"updated 56"}, "after setText")
	if len(calls) != 3 || calls[2] != (call{"updated", 56}) {
		t.Fatalf("calls after setText = %v", calls)
	}

	markdown.Invalidate()
	markdown.Render(60)
	if len(calls) != 4 || calls[3] != (call{"updated", 56}) {
		t.Fatalf("calls after invalidate = %v (cache must survive invalidate per pi)", calls)
	}
}

func itoaTest(v int) string {
	digits := "0123456789"
	if v == 0 {
		return "0"
	}
	out := ""
	for v > 0 {
		out = string(digits[v%10]) + out
		v /= 10
	}
	return out
}

// --- Lists ---

func TestMarkdownRendersSimpleNestedList(t *testing.T) {
	markdown := NewMarkdown("- Item 1\n  - Nested 1.1\n  - Nested 1.2\n- Item 2", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	plainLines := mapStrip(lines)

	joined := strings.Join(plainLines, "\n")
	for _, want := range []string{"- Item 1", "    - Nested 1.1", "    - Nested 1.2", "- Item 2"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %q", want, joined)
		}
	}
}

func TestMarkdownRendersDeeplyNestedList(t *testing.T) {
	markdown := NewMarkdown("- Level 1\n  - Level 2\n    - Level 3\n      - Level 4", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	joined := strings.Join(mapStrip(lines), "\n")

	for _, want := range []string{"- Level 1", "    - Level 2", "        - Level 3", "            - Level 4"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %q", want, joined)
		}
	}
}

func TestMarkdownRendersOrderedNestedList(t *testing.T) {
	markdown := NewMarkdown("1. First\n   1. Nested first\n   2. Nested second\n2. Second", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	joined := strings.Join(mapStrip(lines), "\n")

	for _, want := range []string{"1. First", "    1. Nested first", "    2. Nested second", "2. Second"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %q", want, joined)
		}
	}
}

func TestMarkdownNormalizesOrderedListMarkersByDefault(t *testing.T) {
	markdown := NewMarkdown("1. alpha\n1. beta\n1. gamma", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := mapStripTrim(markdown.Render(80))

	assertDeepLines(t, lines, []string{"1. alpha", "2. beta", "3. gamma"}, "normalized markers")
}

func TestMarkdownPreservesSourceListMarkersWhenConfigured(t *testing.T) {
	markdown := NewMarkdown(
		"  4. forth\n  3. third\n\n10) ten\n7) seven\n\n+ plus\n* star\n- minus\n+",
		0, 0, defaultMarkdownTheme, nil,
		&MarkdownOptions{PreserveOrderedListMarkers: true},
	)

	lines := mapStripTrim(markdown.Render(80))

	assertDeepLines(t, lines, []string{
		"4. forth",
		"3. third",
		"",
		"10) ten",
		"7) seven",
		"",
		"+ plus",
		"* star",
		"- minus",
		"+",
	}, "preserved markers")
}

func TestMarkdownRendersMixedOrderedAndUnorderedNestedLists(t *testing.T) {
	markdown := NewMarkdown(
		"1. Ordered item\n   - Unordered nested\n   - Another nested\n2. Second ordered\n   - More nested",
		0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	joined := strings.Join(mapStrip(lines), "\n")

	for _, want := range []string{"1. Ordered item", "    - Unordered nested", "2. Second ordered"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %q", want, joined)
		}
	}
}

func TestMarkdownRendersBlankLinesBetweenLooseListItems(t *testing.T) {
	markdown := NewMarkdown(
		"1. Lorem ipsum dolor sit amet.\n\n   Ut enim ad minim veniam.\n\n2. Duis aute irure dolor.\n\n   Excepteur sint occaecat cupidatat.\n\n3. Beep boop",
		0, 0, defaultMarkdownTheme, nil, nil)

	lines := mapStripTrim(markdown.Render(80))

	assertDeepLines(t, lines, []string{
		"1. Lorem ipsum dolor sit amet.",
		"",
		"   Ut enim ad minim veniam.",
		"",
		"2. Duis aute irure dolor.",
		"",
		"   Excepteur sint occaecat cupidatat.",
		"",
		"3. Beep boop",
	}, "loose list")
}

func TestMarkdownRendersTaskListMarkers(t *testing.T) {
	markdown := NewMarkdown("- [ ] beep\n- [x] boop", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := mapStripTrim(markdown.Render(80))

	assertDeepLines(t, lines, []string{"- [ ] beep", "- [x] boop"}, "task list")
}

func TestMarkdownMaintainsNumberingWhenCodeBlocksNotIndented(t *testing.T) {
	markdown := NewMarkdown(
		"1. First item\n\n```typescript\n// code block\n```\n\n2. Second item\n\n```typescript\n// another code block\n```\n\n3. Third item",
		0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	var numberedLines []string
	for _, line := range mapStrip(lines) {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) >= 2 && trimmed[0] >= '0' && trimmed[0] <= '9' && strings.Contains(trimmed[:3], ".") {
			numberedLines = append(numberedLines, trimmed)
		}
	}

	if len(numberedLines) != 3 {
		t.Fatalf("expected 3 numbered items, got: %v", numberedLines)
	}
	if !strings.HasPrefix(numberedLines[0], "1.") || !strings.HasPrefix(numberedLines[1], "2.") || !strings.HasPrefix(numberedLines[2], "3.") {
		t.Fatalf("numbering wrong: %v", numberedLines)
	}
}

func TestMarkdownIndentsWrappedListLines(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		width int
		want  []string
	}{
		{"unordered", "- alpha beta gamma delta epsilon", 20, []string{"- alpha beta gamma", "  delta epsilon"}},
		{"ordered", "1. alpha beta gamma delta epsilon", 20, []string{"1. alpha beta gamma", "   delta epsilon"}},
		{"multi-digit", "10. alpha beta gamma delta epsilon", 21, []string{"10. alpha beta gamma", "    delta epsilon"}},
		{"nested", "- parent\n  - alpha beta gamma delta epsilon", 24, []string{"- parent", "    - alpha beta gamma", "      delta epsilon"}},
		{"nested under ordered", "1. parent\n   - alpha beta gamma delta epsilon", 24, []string{"1. parent", "    - alpha beta gamma", "      delta epsilon"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			markdown := NewMarkdown(tc.src, 0, 0, defaultMarkdownTheme, nil, nil)
			lines := mapStripTrim(markdown.Render(tc.width))
			assertDeepLines(t, lines, tc.want, tc.name)
		})
	}
}

func TestMarkdownRendersAndWrapsBlockquotesInsideListItems(t *testing.T) {
	markdown := NewMarkdown("- > alpha beta gamma delta epsilon zeta", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := mapStripTrim(markdown.Render(24))

	assertDeepLines(t, lines, []string{"- │ alpha beta gamma", "  │ delta epsilon zeta"}, "blockquote in list")
}

func TestMarkdownRendersAndWrapsCodeBlocksInsideListItems(t *testing.T) {
	markdown := NewMarkdown("- ```ts\n  alpha beta gamma delta epsilon zeta\n  ```", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := mapStripTrim(markdown.Render(24))

	assertDeepLines(t, lines, []string{"- ```ts", "    alpha beta gamma", "  delta epsilon zeta", "  ```"}, "code in list")
}

// --- Tables ---

func TestMarkdownRendersSimpleTable(t *testing.T) {
	markdown := NewMarkdown(
		"| Name | Age |\n| --- | --- |\n| Alice | 30 |\n| Bob | 25 |",
		0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	joined := strings.Join(mapStrip(lines), "\n")

	for _, want := range []string{"Name", "Age", "Alice", "Bob", "│", "─"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in table output", want)
		}
	}
}

func TestMarkdownRendersRowDividersBetweenDataRows(t *testing.T) {
	markdown := NewMarkdown(
		"| Name | Age |\n| --- | --- |\n| Alice | 30 |\n| Bob | 25 |",
		0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	dividers := 0
	for _, line := range mapStrip(lines) {
		if strings.Contains(line, "┼") {
			dividers++
		}
	}
	if dividers != 2 {
		t.Fatalf("expected header + row divider, got %d", dividers)
	}
}

func TestMarkdownKeepsColumnWidthAtLeastLongestWord(t *testing.T) {
	longestWord := "superlongword"
	markdown := NewMarkdown(
		"| Column One | Column Two |\n| --- | --- |\n| "+longestWord+" short | otherword |\n| small | tiny |",
		0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(32)
	var dataLine string
	for _, line := range mapStrip(lines) {
		if strings.Contains(line, longestWord) {
			dataLine = line
			break
		}
	}
	if dataLine == "" {
		t.Fatal("expected data row containing longest word")
	}

	segments := strings.Split(dataLine, "│")
	if len(segments) < 3 {
		t.Fatalf("expected column segments in %q", dataLine)
	}
	firstSegment := segments[1]
	firstColumnWidth := len(firstSegment) - 2

	if firstColumnWidth < len(longestWord) {
		t.Fatalf("expected first column width >= %d, got %d", len(longestWord), firstColumnWidth)
	}
}

func TestMarkdownRendersTableWithAlignment(t *testing.T) {
	markdown := NewMarkdown(
		"| Left | Center | Right |\n| :--- | :---: | ---: |\n| A | B | C |\n| Long text | Middle | End |",
		0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	joined := strings.Join(mapStrip(lines), "\n")

	for _, want := range []string{"Left", "Center", "Right", "Long text"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestMarkdownHandlesTablesWithVaryingColumnWidths(t *testing.T) {
	markdown := NewMarkdown(
		"| Short | Very long column header |\n| --- | --- |\n| A | This is a much longer cell content |\n| B | Short |",
		0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	if len(lines) == 0 {
		t.Fatal("should render without errors")
	}
	joined := strings.Join(mapStrip(lines), "\n")
	for _, want := range []string{"Very long column header", "This is a much longer cell content"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestMarkdownWrapsTableCellsWhenTableExceedsAvailableWidth(t *testing.T) {
	markdown := NewMarkdown(
		"| Command | Description | Example |\n| --- | --- | --- |\n| npm install | Install all dependencies | npm install |\n| npm run build | Build the project | npm run build |",
		0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(50)
	plainLines := mapStripTrim(lines)
	for i, line := range plainLines {
		if VisibleWidth(line) > 50 {
			t.Fatalf("line %d exceeds width 50: %q (length %d)", i, line, VisibleWidth(line))
		}
	}

	allText := strings.Join(plainLines, " ")
	for _, want := range []string{"Command", "Description", "npm install", "Install"} {
		if !strings.Contains(allText, want) {
			t.Fatalf("should contain %q", want)
		}
	}
}

func TestMarkdownWrapsLongCellContentToMultipleLines(t *testing.T) {
	markdown := NewMarkdown(
		"| Header |\n| --- |\n| This is a very long cell content that should wrap |",
		0, 0, defaultMarkdownTheme, nil, nil)

	lines := mapStripTrim(markdown.Render(25))

	dataRows := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "│") && !strings.Contains(line, "─") {
			dataRows++
		}
	}
	if dataRows <= 2 {
		t.Fatalf("expected wrapped rows, got %d", dataRows)
	}

	allText := strings.Join(lines, " ")
	for _, want := range []string{"very long", "cell content", "should wrap"} {
		if !strings.Contains(allText, want) {
			t.Fatalf("should preserve %q", want)
		}
	}
}

func TestMarkdownWrapsLongUnbrokenTokensInsideTableCells(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: nil, TrueColor: false, Hyperlinks: false})
	defer ResetCapabilitiesCache()

	url := "https://example.com/this/is/a/very/long/url/that/should/wrap"
	markdown := NewMarkdown(
		"| Value |\n| --- |\n| prefix "+url+" |",
		0, 0, defaultMarkdownTheme, nil, nil)

	width := 30
	lines := mapStripTrim(markdown.Render(width))
	for i, line := range lines {
		if VisibleWidth(line) > width {
			t.Fatalf("line %d exceeds width %d: %q", i, width, line)
		}
	}

	tableLines := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "│") {
			tableLines++
			if got := strings.Count(line, "│"); got != 2 {
				t.Fatalf("expected 2 borders, got %d: %q", got, line)
			}
		}
	}
	if tableLines == 0 {
		t.Fatal("expected table rows to render")
	}

	extracted := strings.Map(func(r rune) rune {
		switch r {
		case '│', '├', '┤', '─', ' ', '\t':
			return -1
		}
		return r
	}, strings.Join(lines, ""))
	for _, want := range []string{"prefix", url} {
		if !strings.Contains(extracted, want) {
			t.Fatalf("should preserve %q", want)
		}
	}
}

func TestMarkdownWrapsStyledInlineCodeInsideTableCellsWithoutBreakingBorders(t *testing.T) {
	markdown := NewMarkdown(
		"| Code |\n| --- |\n| `averyveryveryverylongidentifier` |",
		0, 0, defaultMarkdownTheme, nil, nil)

	width := 20
	lines := markdown.Render(width)
	if !strings.Contains(strings.Join(lines, "\n"), "\x1b[33m") {
		t.Fatal("inline code should be styled (yellow)")
	}

	plainLines := mapStripTrim(lines)
	for i, line := range plainLines {
		if VisibleWidth(line) > width {
			t.Fatalf("line %d exceeds width %d: %q", i, width, line)
		}
		if strings.HasPrefix(line, "│") && strings.Count(line, "│") != 2 {
			t.Fatalf("expected 2 borders: %q", line)
		}
	}
}

func TestMarkdownHandlesExtremelyNarrowWidthGracefully(t *testing.T) {
	markdown := NewMarkdown(
		"| A | B | C |\n| --- | --- | --- |\n| 1 | 2 | 3 |",
		0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(15)
	if len(lines) == 0 {
		t.Fatal("should produce output")
	}
	for i, line := range mapStripTrim(lines) {
		if VisibleWidth(line) > 15 {
			t.Fatalf("line %d exceeds width 15: %q", i, line)
		}
	}
}

func TestMarkdownRendersTableCorrectlyWhenItFitsNaturally(t *testing.T) {
	markdown := NewMarkdown(
		"| A | B |\n| --- | --- |\n| 1 | 2 |",
		0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	plainLines := mapStripTrim(lines)

	headerLine := ""
	separatorLine := ""
	dataLine := ""
	for _, line := range plainLines {
		if strings.Contains(line, "A") && strings.Contains(line, "B") {
			headerLine = line
		}
		if strings.Contains(line, "├") && strings.Contains(line, "┼") {
			separatorLine = line
		}
		if strings.Contains(line, "1") && strings.Contains(line, "2") {
			dataLine = line
		}
	}
	if headerLine == "" || !strings.Contains(headerLine, "│") {
		t.Fatalf("should have header row with borders: %q", headerLine)
	}
	if separatorLine == "" {
		t.Fatal("should have separator row")
	}
	if dataLine == "" {
		t.Fatal("should have data row")
	}
}

func TestMarkdownRespectsPaddingXWhenCalculatingTableWidth(t *testing.T) {
	markdown := NewMarkdown(
		"| Column One | Column Two |\n| --- | --- |\n| Data 1 | Data 2 |",
		2, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(40)
	plainLines := mapStripTrim(lines)
	for i, line := range plainLines {
		if VisibleWidth(line) > 40 {
			t.Fatalf("line %d exceeds width 40: %q", i, line)
		}
	}

	tableRow := ""
	for _, line := range plainLines {
		if strings.Contains(line, "│") {
			tableRow = line
			break
		}
	}
	if !strings.HasPrefix(tableRow, "  ") {
		t.Fatalf("table should have left padding: %q", tableRow)
	}
}

func TestMarkdownNoTrailingBlankLineWhenTableIsLastBlock(t *testing.T) {
	markdown := NewMarkdown(
		"| Name |\n| --- |\n| Alice |",
		0, 0, defaultMarkdownTheme, nil, nil)

	lines := mapStripTrim(markdown.Render(80))
	if lines[len(lines)-1] == "" {
		t.Fatalf("expected table to end without a blank line: %q", lines)
	}
}

// --- Combined features ---

func TestMarkdownRendersListsAndTablesTogether(t *testing.T) {
	markdown := NewMarkdown(
		"# Test Document\n\n- Item 1\n  - Nested item\n- Item 2\n\n| Col1 | Col2 |\n| --- | --- |\n| A | B |",
		0, 0, defaultMarkdownTheme, nil, nil)

	lines := markdown.Render(80)
	joined := strings.Join(mapStrip(lines), "\n")

	for _, want := range []string{"Test Document", "- Item 1", "    - Nested item", "Col1", "│"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

// --- LaTeX math (raw-fallback subset; rendered-math tests deferred, see
// the file header) ---

func TestMarkdownDoesNotTreatCurrencyShellVariablesOrCodeSpansAsMath(t *testing.T) {
	source := "Costs $5 and $10 or $8k–$12k; use `$x$`, $HOME, and ${PATH}."
	markdown := NewMarkdown(source, 0, 0, defaultMarkdownTheme, nil, nil)

	lines := mapStripTrim(markdown.Render(80))
	assertDeepLines(t, lines, []string{"Costs $5 and $10 or $8k–$12k; use $x$, $HOME, and ${PATH}."}, "currency")

	shellVariables := "Paths: $HOME/$USER and $XDG_CONFIG_HOME/$APP_CONFIG"
	shellLines := mapStripTrim(NewMarkdown(shellVariables, 0, 0, defaultMarkdownTheme, nil, nil).Render(80))
	assertDeepLines(t, shellLines, []string{shellVariables}, "shell variables")
}

func TestMarkdownPreservesUnsupportedAndIncompleteLatexExactly(t *testing.T) {
	cases := []string{`Unknown $x + \unknown{y}$ after`, `Streaming $\mathbb{C}^3`}

	for _, source := range cases {
		markdown := NewMarkdown(source, 0, 0, defaultMarkdownTheme, nil, nil)
		lines := mapStripTrim(markdown.Render(80))
		assertDeepLines(t, lines, []string{source}, source)
	}
}

func TestMarkdownPreservesIncompleteBackslashDelimitersWhileStreaming(t *testing.T) {
	inline := NewMarkdown(`Map \(\mathbb{C}^3`, 0, 0, defaultMarkdownTheme, nil, nil)
	assertDeepLines(t, mapStripTrim(inline.Render(80)), []string{`Map \(\mathbb{C}^3`}, "inline")

	display := NewMarkdown("\\[\nx^2", 0, 0, defaultMarkdownTheme, nil, nil)
	assertDeepLines(t, mapStripTrim(display.Render(80)), []string{"\\[", "x^2"}, "display")
}

func TestMarkdownDoesNotRenderLatexInsideEscapedDelimitersOrCodeFences(t *testing.T) {
	source := "Escaped \\$x-y\\$.\n\n```text\n$\\mathbb{C}^3$\n```"
	markdown := NewMarkdown(source, 0, 0, defaultMarkdownTheme, nil, nil)
	lines := mapStripTrim(markdown.Render(80))

	assertDeepLines(t, lines, []string{"Escaped $x-y$.", "", "```text", "  $\\mathbb{C}^3$", "```"}, "escaped/fence")
}

func TestMarkdownAllowsLatexRenderingToBeDisabled(t *testing.T) {
	disabled := false
	markdown := NewMarkdown(`Map $\mathbb{C}^3 \to \mathbb{C}^3$`, 0, 0, defaultMarkdownTheme, nil, nil)
	markdown.options.RenderLatex = &disabled

	assertDeepLines(t, mapStripTrim(markdown.Render(80)), []string{`Map $\mathbb{C}^3 \to \mathbb{C}^3$`}, "disabled")
}

// --- Backslash escapes ---

func TestMarkdownNormalizesEscapedPunctuationByDefault(t *testing.T) {
	markdown := NewMarkdown(`"\"`, 0, 0, defaultMarkdownTheme, nil, nil)

	lines := mapStripTrim(markdown.Render(80))
	assertDeepLines(t, lines, []string{`""`}, "normalized escapes")
}

func TestMarkdownPreservesSourceBackslashEscapesWhenConfigured(t *testing.T) {
	markdown := NewMarkdown(`"\"`, 0, 0, defaultMarkdownTheme, nil, &MarkdownOptions{
		PreserveBackslashEscapes: true,
	})

	lines := mapStripTrim(markdown.Render(80))
	assertDeepLines(t, lines, []string{`"\"`}, "preserved escapes")
}
