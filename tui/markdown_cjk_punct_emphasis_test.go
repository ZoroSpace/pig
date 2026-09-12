// tui/markdown_cjk_punct_emphasis_test.go — regression tests for the
// em/strong closer-scan group-5/6 branch of the marked port. The
// multiple-of-3 condition was ported inverted ((i+u)%3 != 0 instead of
// == 0), so any closing delimiter flanked by punctuation on both sides —
// the typical CJK pattern `……）*斜体*。` — was wrongly rejected and the
// whole run fell back to literal text. Upstream truth verified against
// marked@18.0.5 (pi's pinned Lexer). See docs/fix/2026-09-12-*.md.
package tui

import (
	"strings"
	"testing"
)

// inlineKinds returns the first block's inline tokens as "type:text"
// joined by "|", matching the shape asserted against marked@18.0.5 output.
func inlineKinds(t *testing.T, src string) string {
	t.Helper()
	tokens := markedLex(src, markedLexerOptions{})
	if len(tokens) == 0 {
		t.Fatalf("no block tokens for %q", src)
	}
	parts := make([]string, 0, len(tokens[0].Tokens))
	for _, tok := range tokens[0].Tokens {
		parts = append(parts, tok.Type+":"+tok.Text)
	}
	return strings.Join(parts, "|")
}

func TestMarkdownEmphasisCloseFlankedByPunctuation(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "em close between fullwidth punct (minimal repro)",
			src:  "结论：*技术（公开）*。需要动代码。",
			want: "text:结论：|em:技术（公开）|text:。需要动代码。",
		},
		{
			name: "strong close between fullwidth punct",
			src:  "结论：**技术（公开）**。需要动代码。",
			want: "text:结论：|strong:技术（公开）|text:。需要动代码。",
		},
		{
			name: "multiple-of-3 rule rejects closer (marked skips, no em)",
			src:  "x *a（b）**。y",
			want: "text:x *a（b）**。y",
		},
		{
			name: "close followed by space hits group 1, unaffected",
			src:  "x *a（b + c + d）* y",
			want: "text:x |em:a（b + c + d）|text: y",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inlineKinds(t, tc.src); got != tc.want {
				t.Fatalf("tokenize %q\n  got  %s\n  want %s", tc.src, got, tc.want)
			}
		})
	}
}

// TestMarkdownEmphasisCloseFlankedByPunctuationRealSentence locks the
// original 2026-09-12 dogfooding sentence that surfaced the bug.
func TestMarkdownEmphasisCloseFlankedByPunctuationRealSentence(t *testing.T) {
	src := "结论：*技术上零障碍，剩下的全是仓库治理动作（公开 + LICENSE + 版本纪律）*。需要动代码的只有可选的版本注入改进那一条。"
	got := inlineKinds(t, src)
	want := "text:结论：|em:技术上零障碍，剩下的全是仓库治理动作（公开 + LICENSE + 版本纪律）|text:。需要动代码的只有可选的版本注入改进那一条。"
	if got != want {
		t.Fatalf("tokenize real-world sentence\n  got  %s\n  want %s", got, want)
	}
}
