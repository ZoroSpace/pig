// pi: packages/tui/test/markdown.test.ts "LaTeX math" describe — the
// rendered-math subset, deferred at markdown_test.go port time because
// renderLatex (latex.ts) had not been ported yet. Ported 2026-09-10 when
// tui/latex.go landed (seam auto-wired via init).
package tui

import (
	"strings"
	"testing"
)

func TestMarkdownRendersInlineDollarAndParenthesisDelimiters(t *testing.T) {
	markdown := NewMarkdown(`A map $\mathbb{C}^3 \to \mathbb{C}^3$, $xy$, $x-y$, $-x$, $\frac{1}{2}$, and \(s \to \infty\).`, 0, 0, defaultMarkdownTheme, nil, nil)

	lines := mapStripTrim(markdown.Render(80))
	assertDeepLines(t, lines, []string{"A map ℂ³ → ℂ³, xy, x-y, -x, 1/2, and s → ∞."}, "inline dollar/paren")
}

func TestMarkdownRendersDisplayDollarWithoutEscapeCorruption(t *testing.T) {
	markdown := NewMarkdown("Before\n\n$$\\{3x+2y,\\; x \\in \\{0, \\pm 1\\}\\}$$\n\nafter", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := mapStripTrim(markdown.Render(80))
	assertDeepLines(t, lines, []string{"Before", "", "{3x+2y, x ∈ {0, ± 1}}", "", "after"}, "display dollar")
}

func TestMarkdownRendersDisplayBracketDelimiters(t *testing.T) {
	markdown := NewMarkdown("Before\n\n\\[\nE \\approx \\frac{0.1\\ \\text{lux}}{100\\ \\text{lm/W}}\n\\]\n\nafter", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := mapStripTrim(markdown.Render(80))
	assertDeepLines(t, lines, []string{"Before", "", "    0.1 lux", "E ≈ ────────", "    100 lm/W", "", "after"}, "display bracket")
}

func TestMarkdownAlignsMatrixRowsWithOpeningDelimiter(t *testing.T) {
	markdown := NewMarkdown("Consider the matrix\n\n\\[\nA=\n\\begin{pmatrix}\n\\pi & 0\\\\\n0 & \\frac{1}{\\pi}\n\\end{pmatrix}.\n\\]", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := mapStripTrim(markdown.Render(80))
	assertDeepLines(t, lines, []string{"Consider the matrix", "", "A = ⎛ π │ 0   ⎞", "    ⎝ 0 │ 1/π ⎠."}, "matrix align")
}

func TestMarkdownRendersLowerLimitsBeneathDisplayOperators(t *testing.T) {
	markdown := NewMarkdown("\\[\n\\lim_{x\\to 0}\\frac{\\frac{\\sin x}{x}-1}{\\frac{e^x-1}{x}-1}=0\n\\]", 0, 0, defaultMarkdownTheme, nil, nil)

	lines := mapStripTrim(markdown.Render(80))
	assertDeepLines(t, lines, []string{"     (sin x)/x-1", "lim  ─────────── = 0", "x→0  (eˣ-1)/x-1"}, "lower limits")
}

func TestMarkdownRendersMathInsideListsAndTables(t *testing.T) {
	markdown := NewMarkdown("- Formula: $F_1 = u^2$\n\n| Value |\n| --- |\n| $\\mathbb{C}^3$ |", 0, 0, defaultMarkdownTheme, nil, nil)

	output := strings.Join(mapStripTrim(markdown.Render(80)), "\n")
	if !strings.Contains(output, "- Formula: F₁ = u²") {
		t.Errorf("list math missing: %q", output)
	}
	if !strings.Contains(output, "│ ℂ³") {
		t.Errorf("table math missing: %q", output)
	}
}

// pi: "switches from raw to rendered math when a streamed delimiter closes".
func TestMarkdownSwitchesFromRawToRenderedMathWhenDelimiterCloses(t *testing.T) {
	markdown := NewMarkdown(`Map $\mathbb{C}^3`, 0, 0, defaultMarkdownTheme, nil, nil)
	assertDeepLines(t, mapStripTrim(markdown.Render(80)), []string{`Map $\mathbb{C}^3`}, "streaming raw")

	markdown.SetText(`Map $\mathbb{C}^3$`)
	assertDeepLines(t, mapStripTrim(markdown.Render(80)), []string{"Map ℂ³"}, "rendered after close")
}

// Regression (pig-only, 2026-09-10): the StartInline/StartBlock hints
// returned BYTE indices from strings.Index/FindStringIndex while the
// cutSrc clipping consumer (jsSlice) works in runes — any multi-byte
// (CJK) character before a math delimiter made the candidate over-extend
// past the delimiter and the math never tokenized. pi is unaffected (its
// indexOf/substring units are consistent UTF-16). Locks the conversion.

func TestMarkdownRendersInlineMathAfterCJKText(t *testing.T) {
	cases := []struct{ source, want string }{
		{`公式：$y = x^2 + 2$，以及 $y = x \cdot x + 2$。`, "公式：y = x² + 2，以及 y = x · x + 2。"},
		{`值：$xy$`, "值：xy"},
		{`中文说明 \(s \to \infty\) 结束`, "中文说明 s → ∞ 结束"},
	}
	for _, c := range cases {
		markdown := NewMarkdown(c.source, 0, 0, defaultMarkdownTheme, nil, nil)
		lines := mapStripTrim(markdown.Render(120))
		assertDeepLines(t, lines, []string{c.want}, c.source)
	}
}

func TestMarkdownRendersBlockMathAfterCJKParagraph(t *testing.T) {
	source := "中文说明如下\n\n\\[\ny = x^2 + 2\n\\]\n\n结束"
	markdown := NewMarkdown(source, 0, 0, defaultMarkdownTheme, nil, nil)
	lines := mapStripTrim(markdown.Render(80))
	assertDeepLines(t, lines, []string{"中文说明如下", "", "y = x² + 2", "", "结束"}, "block after CJK")
}
