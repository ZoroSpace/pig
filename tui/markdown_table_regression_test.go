// tui/markdown_table_regression_test.go — regression tests for the CJK table
// rendering bugs reported 2026-09-11 (see docs/fix/2026-09-11-markdown-table-cjk-rendering.md):
//
//  1. reReplaceAll mixed regexp2's rune-based Match.Index with byte-based
//     string slicing — any match located after a multi-byte rune corrupted
//     UTF-8 (放松心态 → 放??心态 via the table's TableRowBlankLine strip, and
//     mangled row text also changed the \n structure, producing a phantom
//     empty table row).
//  2. renderTable's separator line dropped the trailing ─ (pi:
//     `├─${cells.join("─┼─")}─┤`) so separators were one column narrower
//     than the top/bottom borders — misaligned right edge.
//  3. Phantom empty row — consequence of bug 1 (corrupted TableRowBlankLine
//     replace left the trailing \n in place, splitting an extra empty row).
package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dlclark/regexp2"
)

const cjkBenefitsTable = "| 序号 | 好处 | 具体说明 |\n" +
	"| --- | --- | --- |\n" +
	"| 1 | 提升自我认知 | 通过回顾每天的想法和行为，更清楚地了解自己的情绪模式和思维习惯 |\n" +
	"| 2 | 缓解压力与负面情绪 | 把烦恼和焦虑写下来，相当于情绪的“宣泄出口”，有助于放松心态 |\n" +
	"| 3 | 提高写作与表达能力 | 每天练习组织语言、梳理思路，文字表达会越来越流畅清晰 |\n" +
	"| 4 | 增强记忆与反思能力 | 记录并复盘当天经历，加深记忆，还能从错误中总结经验教训 |\n" +
	"| 5 | 见证个人成长 | 日记是时间的记录者，翻看过去的文字能直观看到自己的进步与变化 |\n"

// borderRunWidths returns the widths of the ─ runs in a table border line
// (stripped of SGR), e.g. "┌─aa─┬─bb─┐" → [1+2, 2, ...] merged per column gap.
func borderRunWidths(t *testing.T, line string) []int {
	t.Helper()
	var runs []int
	cur := 0
	for _, r := range line {
		if r == '─' {
			cur++
		} else {
			if cur > 0 {
				runs = append(runs, cur)
			}
			cur = 0
		}
	}
	if cur > 0 {
		runs = append(runs, cur)
	}
	return runs
}

// TestMarkdownTableBorderWidthsAligned locks bug 2: every border line (top,
// separators, bottom) must have the same visible width and the same ─ run
// structure, with the right-edge characters (┐ ┤ ┘) in the same column.
func TestMarkdownTableBorderWidthsAligned(t *testing.T) {
	for _, width := range []int{40, 60, 80, 120} {
		m := NewMarkdown(cjkBenefitsTable, 0, 0, defaultMarkdownTheme, nil, nil)
		lines := m.Render(width)

		var borderLines []string
		for _, l := range lines {
			s := strings.TrimRight(stripSGR(l), " ") // renderer pads narrow tables to full width
			if strings.HasPrefix(s, "┌") || strings.HasPrefix(s, "├") || strings.HasPrefix(s, "└") {
				borderLines = append(borderLines, s)
			}
		}
		if len(borderLines) == 0 {
			t.Fatalf("width %d: no border lines found", width)
		}

		top := borderLines[0]
		topRuns := borderRunWidths(t, top)
		for i, b := range borderLines {
			if got := VisibleWidth(b); got != VisibleWidth(top) {
				t.Errorf("width %d: border %d visible width = %d, top border = %d\n  top: %q\n  got: %q", width, i, got, VisibleWidth(top), top, b)
			}
			if got := borderRunWidths(t, b); !equalInts(got, topRuns) {
				t.Errorf("width %d: border %d dash runs = %v, top border = %v\n  top: %q\n  got: %q", width, i, got, topRuns, top, b)
			}
			if last := []rune(b)[len([]rune(b))-1]; last != '┐' && last != '┤' && last != '┘' {
				t.Errorf("width %d: border %d does not end with a border junction: %q", width, i, b)
			}
		}
		// The last separator must be followed by exactly one content row (row
		// 5), never a phantom empty row (bug 3).
		if len(borderLines) >= 2 {
			lastSep := borderLines[len(borderLines)-2]
			// The last separator is textually identical to interior ones — take
			// the LAST occurrence in the output.
			sepIdx := -1
			for i, l := range lines {
				if strings.TrimRight(stripSGR(l), " ") == lastSep {
					sepIdx = i
				}
			}
			if sepIdx == -1 || sepIdx+2 >= len(lines) {
				t.Fatalf("width %d: could not locate last separator in output", width)
			}
			after := strings.TrimRight(stripSGR(lines[sepIdx+1]), " ")
			// The line after the last separator must be a content row with
			// actual cell text — a phantom empty row renders as borders and
			// spaces only. (Row content may wrap, so don't match a phrase here;
			// width 40 wraps row 5 mid-phrase.)
			content := strings.Trim(strings.Map(func(r rune) rune {
				if r == '│' {
					return -1
				}
				return r
			}, after), " ")
			if !strings.HasPrefix(after, "│") || content == "" {
				t.Errorf("width %d: line after last separator = %q, want non-empty row content (phantom empty row?)", width, after)
			}
		}
	}
}

// TestMarkdownTableNoPhantomEmptyRow locks bug 3 at the tokenizer level: a
// table whose last row ends with \n must lex to exactly the written number of
// rows (marked strips one trailing \n via other.tableRowBlankLine).
func TestMarkdownTableNoPhantomEmptyRow(t *testing.T) {
	for _, test := range []struct {
		name     string
		md       string
		wantRows int
	}{
		{"ascii trailing newline", "| a | b |\n| - | - |\n| 1 | 2 |\n", 1},
		{"cjk trailing newline", "| 序号 | 好处 |\n| --- | --- |\n| 1 | 提升自我认知 |\n| 2 | 缓解压力 |\n", 2},
		{"cjk followed by paragraph", cjkBenefitsTable + "\n如果你需要，我也可以把它转成 LaTeX 表格（`tabular`）格式。", 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			tokens := MarkedLex(test.md)
			var table *MarkedToken
			for _, tok := range tokens {
				if tok.Type == "table" {
					table = tok
					break
				}
			}
			if table == nil {
				t.Fatalf("no table token found")
			}
			if len(table.Rows) != test.wantRows {
				t.Fatalf("rows = %d, want %d", len(table.Rows), test.wantRows)
			}
			for i, row := range table.Rows {
				for j, cell := range row {
					if cell.Text == "" {
						t.Errorf("row %d cell %d is empty (phantom row?)", i, j)
					}
				}
			}
		})
	}
}

// TestMarkdownTableCJKCellsIntact locks bug 1 end-to-end: rendered table lines
// must be valid UTF-8 with no U+FFFD, and — with no wrapping (wide terminal,
// width 200) — cell text must appear verbatim.
func TestMarkdownTableCJKCellsIntact(t *testing.T) {
	for _, width := range []int{80, 200} {
		m := NewMarkdown(cjkBenefitsTable, 0, 0, defaultMarkdownTheme, nil, nil)
		lines := m.Render(width)
		for _, l := range lines {
			if !utf8.ValidString(l) {
				t.Errorf("width %d: invalid UTF-8 in rendered line: %q", width, l)
			}
			if strings.ContainsRune(l, '\uFFFD') {
				t.Errorf("width %d: U+FFFD in rendered line: %q", width, l)
			}
		}
		if width == 200 {
			joined := strings.Join(mapStripTrim(lines), "")
			for _, phrase := range []string{"放松心态", "情绪模式和思维习惯", "见证个人成长", "宣泄出口"} {
				if !strings.Contains(joined, phrase) {
					t.Errorf("width %d: rendered output lost phrase %q (corrupted characters?)\noutput: %q", width, phrase, joined)
				}
			}
		}
	}
}

// TestReReplaceAllRuneOffsets is the focused unit test for bug 1: reReplaceAll
// must convert regexp2's rune-based match offsets to byte offsets.
func TestReReplaceAllRuneOffsets(t *testing.T) {
	other := newMarkedOther()
	for _, test := range []struct {
		name string
		re   *regexp2.Regexp
		in   string
		repl string
		want string
	}{
		{
			name: "tableRowBlankLine strips trailing newline after CJK",
			re:   other.TableRowBlankLine,
			in:   "| 序号 | 好处 |\n| 1 | 通过回顾每天的想法 |\n",
			repl: "",
			want: "| 序号 | 好处 |\n| 1 | 通过回顾每天的想法 |",
		},
		{
			name: "escaped pipe unescape after CJK (SlashPipe)",
			re:   other.SlashPipe,
			in:   "中文标题\\|副标题",
			repl: "|",
			want: "中文标题|副标题",
		},
		{
			name: "global replace with matches after CJK",
			re:   markedCompile("\\d+", "g"),
			in:   "中文123中文4567",
			repl: "X",
			want: "中文X中文X",
		},
		{
			name: "group reference replacement after CJK",
			re:   markedCompile("(\\d+)", "g"),
			in:   "中文1234中文56",
			repl: "$1",
			want: "中文1234中文56",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := reReplaceAll(test.re, test.in, test.repl)
			if got != test.want {
				t.Errorf("reReplaceAll = %q, want %q", got, test.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("reReplaceAll produced invalid UTF-8: %q", got)
			}
		})
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
