// Ported from pi node_modules/grok-mermaid/src/ansi.ts (grok-mermaid v0.2.2,
// Apache-2.0) as part of T5b-4. ANSI colouring of rendered art.
package grokmermaid

import "strings"

// AnsiTheme maps an SGR parameter per semantic class, e.g. "2" for dim, "36"
// for cyan, "38;5;244" for a 256-colour index. A class left out is printed
// unstyled.
type AnsiTheme map[Cls]string

// DefaultTheme: dim frame, plain labels, cyan connectors. Readable on light
// and dark.
var DefaultTheme = AnsiTheme{
	ClsBorder:    "2",
	ClsEdge:      "36",
	ClsEdgeLabel: "2;36",
	ClsTitle:     "1",
}

const esc = "\x1b"

// ToAnsi renders art to ANSI-coloured lines.
//
// A convenience over mapping art.Styled yourself — reach for that directly
// when your TUI has its own styling model.
func ToAnsi(art *MermaidArt, theme AnsiTheme) []string {
	if theme == nil {
		theme = DefaultTheme
	}
	out := make([]string, len(art.Styled))
	for i, row := range art.Styled {
		var b strings.Builder
		for _, span := range row {
			if sgr, ok := theme[span.Cls]; ok {
				b.WriteString(esc + "[" + sgr + "m" + span.Text + esc + "[0m")
			} else {
				b.WriteString(span.Text)
			}
		}
		out[i] = b.String()
	}
	return out
}
