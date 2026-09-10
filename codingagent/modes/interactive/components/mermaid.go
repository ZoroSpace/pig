// Ported from pi packages/coding-agent/src/modes/interactive/components/mermaid.ts
// as part of T5b-4. Transformer that replaces top-level Mermaid code blocks
// with Unicode terminal diagrams.
package components

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/tui"
	"github.com/ZoroSpace/pig/tui/grokmermaid"
)

// MermaidTheme is the theme surface the mermaid transformer needs (pig
// addition: pi passes the Theme object directly; Go needs an interface so the
// test can stub it — *theme.Theme satisfies it since ThemeColor is a string
// alias).
type MermaidTheme interface {
	Fg(color string, text string) string
	Bold(text string) string
}

// MermaidTransformerOptions carries the transformer configuration (pi:
// MermaidTransformerOptions).
type MermaidTransformerOptions struct {
	// GetMode returns the current mermaid rendering mode.
	GetMode func() core.MermaidRenderingMode
	// Theme resolves the active theme at transform time. pig: pi captures the
	// live global theme object; pig resolves lazily per the T5b-3 decision 11
	// precedent (the global can be replaced via SetTheme). Nil = plain output.
	Theme func() MermaidTheme
}

func isMermaidToken(token *tui.MarkedToken) bool {
	if token.Type != "code" {
		return false
	}
	lang := strings.TrimSpace(token.Lang)
	first := ""
	if fields := strings.Fields(lang); len(fields) > 0 {
		first = fields[0]
	}
	return strings.ToLower(first) == "mermaid"
}

// mermaidBacktickRun matches runs of backticks for the code span fence choice.
var mermaidBacktickRun = regexp.MustCompile("`+")

// codeSpan encodes one diagram row as inline code (` ... `) so Markdown
// preserves its spacing and box-drawing characters. Use a non-breaking space
// for blank rows because an empty code span has no visible height.
func codeSpan(line string) string {
	content := line
	if content == "" {
		content = "\u00a0"
	}
	// CommonMark code spans use matching backtick delimiters, so choose one
	// longer than any backtick run in the content (``hel`lo`` -> <code>hel`lo</code>).
	// If the content starts or ends with a backtick, separating it from the
	// delimiter with a space keeps that backtick as content; CommonMark removes
	// the padding when rendering (`` `edge` `` -> <code>`edge`</code>).
	// Mermaid labels can preserve backticks, for example:
	//   `┌──────────────┐    ┌──────────────┐`
	// ```│ plain ` tick ├───▶│ two `` ticks │```
	//   `└──────────────┘    └──────────────┘`
	longestBacktickRun := 0
	for _, match := range mermaidBacktickRun.FindAllString(content, -1) {
		longestBacktickRun = max(longestBacktickRun, len(match))
	}
	fence := strings.Repeat("`", longestBacktickRun+1)
	padding := ""
	if strings.HasPrefix(content, "`") || strings.HasSuffix(content, "`") {
		padding = " "
	}
	return fence + padding + content + padding + fence
}

func mermaidStyleSpan(span grokmermaid.Span, th MermaidTheme) string {
	switch span.Cls {
	case grokmermaid.ClsBorder:
		return th.Fg("borderMuted", span.Text)
	case grokmermaid.ClsText:
		return th.Fg("text", span.Text)
	case grokmermaid.ClsEdge:
		return th.Fg("accent", span.Text)
	case grokmermaid.ClsEdgeLabel:
		return th.Fg("muted", span.Text)
	case grokmermaid.ClsTitle:
		return th.Fg("accent", th.Bold(span.Text))
	default: // "none"
		return span.Text
	}
}

func mermaidThemedLines(art *grokmermaid.MermaidArt, th MermaidTheme) []string {
	lines := make([]string, len(art.Styled))
	for i, row := range art.Styled {
		var b strings.Builder
		for _, span := range row {
			b.WriteString(mermaidStyleSpan(span, th))
		}
		lines[i] = b.String()
	}
	return lines
}

// CreateMermaidMarkdownTransformer creates a transformer that replaces
// top-level Mermaid code blocks with Unicode terminal diagrams.
func CreateMermaidMarkdownTransformer(options MermaidTransformerOptions) core.MarkdownTransformer {
	return func(markdown string, context core.MarkdownTransformContext) string {
		mode := core.MermaidRenderingStreaming
		if options.GetMode != nil {
			mode = options.GetMode()
		}
		if mode == core.MermaidRenderingOff ||
			context.MessageType == "assistant-thinking" ||
			(context.IsStreaming && mode != core.MermaidRenderingStreaming) {
			return markdown
		}

		var out strings.Builder
		for _, token := range tui.MarkedLex(markdown) {
			if !isMermaidToken(token) {
				out.WriteString(token.Raw)
				continue
			}
			art := grokmermaid.Render(token.Text)
			if art == nil || art.Width > context.AvailableWidth {
				out.WriteString(token.Raw)
				continue
			}
			if !context.IsStreaming && len(art.Warnings) > 0 {
				suffix := ""
				if len(art.Warnings) > 1 {
					suffix = fmt.Sprintf(" (+%d more)", len(art.Warnings)-1)
				}
				warning := "Mermaid diagram not rendered: " + art.Warnings[0] + suffix
				styledWarning := warning
				if options.Theme != nil {
					if th := options.Theme(); th != nil {
						styledWarning = th.Fg("warning", warning)
					}
				}
				out.WriteString(token.Raw)
				out.WriteString("\n")
				out.WriteString(codeSpan(styledWarning))
				out.WriteString("  \n")
				continue
			}
			lines := art.Plain
			if options.Theme != nil {
				if th := options.Theme(); th != nil {
					lines = mermaidThemedLines(art, th)
				}
			}
			// Markdown hard breaks keep every diagram row on its own line (pi:
			// lines.map(codeSpan).join("  \n") + "\n" — separator between rows
			// only, then a single trailing newline).
			spans := make([]string, len(lines))
			for i, line := range lines {
				spans[i] = codeSpan(line)
			}
			out.WriteString(strings.Join(spans, "  \n"))
			out.WriteString("\n")
		}
		return out.String()
	}
}
