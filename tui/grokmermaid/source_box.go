// Ported from pi node_modules/grok-mermaid/src/source-box.ts (grok-mermaid
// v0.2.2, Apache-2.0) as part of T5b-4. The raw source in a framed box.
//
// What to show when Render returns nil, or returns art too wide for the space
// at hand. Both are the caller's call, so this is theirs to invoke — and
// theirs to caption, since only they know whether some other view of the
// diagram exists to point the reader at.
package grokmermaid

import (
	"regexp"
	"strings"
)

// sourceBox frames src in a titled box, hard-wrapping its lines to maxWidth
// columns.
//
// The result can still exceed maxWidth: the body wraps to
// max(8, maxWidth - 4) and the ` mermaid: <kind> ` title is never truncated,
// so a long first token sets a floor. Check Width if it matters.
func SourceBox(src string, maxWidth *int) *MermaidArt {
	src = stripControls(src)
	header := "diagram"
	if w := words(src); len(w) > 0 {
		header = w[0]
	}
	title := " mermaid: " + header + " "
	limit := 0
	limitSet := false
	if maxWidth != nil {
		limit = max(8, sat(*maxWidth, 4))
		limitSet = true
	}

	var body []string
	started := false
	for _, l := range srcLines(src) {
		l = trailingWsRe.ReplaceAllString(l, "")
		if !started && l == "" {
			continue
		}
		started = true
		body = append(body, chunkLine(l, limit, limitSet)...)
	}

	contentW := stringWidth(title)
	for _, line := range body {
		contentW = max(contentW, stringWidth(line))
	}
	inner := contentW + 2

	var plain []string
	var styled [][]Span

	rule := strings.Repeat("─", sat(inner, stringWidth(title)))
	plain = append(plain, "╭"+title+rule+"╮")
	styled = append(styled, []Span{
		{Text: "╭", Cls: ClsBorder},
		{Text: title, Cls: ClsTitle},
		{Text: rule + "╮", Cls: ClsBorder},
	})

	for _, line := range body {
		pad := strings.Repeat(" ", sat(contentW, stringWidth(line)))
		plain = append(plain, "│ "+line+pad+" │")
		styled = append(styled, []Span{
			{Text: "│ ", Cls: ClsBorder},
			{Text: line, Cls: ClsText},
			{Text: pad + " │", Cls: ClsBorder},
		})
	}

	bottom := "╰" + strings.Repeat("─", inner) + "╯"
	plain = append(plain, bottom)
	styled = append(styled, []Span{{Text: bottom, Cls: ClsBorder}})

	return &MermaidArt{Plain: plain, Styled: styled, Width: inner + 2, Warnings: nil}
}

// trailingWsRe matches /\s+$/ (line trailing whitespace).
var trailingWsRe = regexp.MustCompile(`[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+$`)

// chunkLine hard-breaks a line at limit columns, never splitting a wide glyph.
func chunkLine(line string, limit int, limitSet bool) []string {
	if !limitSet || stringWidth(line) <= limit {
		return []string{line}
	}
	var out []string
	cur := ""
	curW := 0
	for _, ch := range measured(line) {
		if curW+ch.w > limit && cur != "" {
			out = append(out, cur)
			cur = ""
			curW = 0
		}
		cur += ch.text
		curW += ch.w
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
