// ansi_to_html.go (pi: core/export-html/ansi-to-html.ts, v0.86.0)
//
// ANSI escape code to HTML converter.
//
// Converts terminal ANSI color/style codes to HTML with inline styles.
// Supports:
//   - Standard foreground colors (30-37) and bright variants (90-97)
//   - Standard background colors (40-47) and bright variants (100-107)
//   - 256-color palette (38;5;N and 48;5;N)
//   - RGB true color (38;2;R;G;B and 48;2;R;G;B)
//   - Text styles: bold (1), dim (2), italic (3), underline (4)
//   - Reset (0)
package exporthtml

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ansiColors is the standard ANSI color palette (0-15).
var ansiColors = [16]string{
	"#000000", // 0: black
	"#800000", // 1: red
	"#008000", // 2: green
	"#808000", // 3: yellow
	"#000080", // 4: blue
	"#800080", // 5: magenta
	"#008080", // 6: cyan
	"#c0c0c0", // 7: white
	"#808080", // 8: bright black
	"#ff0000", // 9: bright red
	"#00ff00", // 10: bright green
	"#ffff00", // 11: bright yellow
	"#0000ff", // 12: bright blue
	"#ff00ff", // 13: bright magenta
	"#00ffff", // 14: bright cyan
	"#ffffff", // 15: bright white
}

// color256ToHex converts a 256-color index to hex.
func color256ToHex(index int) string {
	// Standard colors (0-15)
	if index >= 0 && index < 16 {
		return ansiColors[index]
	}

	// Color cube (16-231): 6x6x6 = 216 colors
	if index < 232 {
		cubeIndex := index - 16
		r := cubeIndex / 36
		g := (cubeIndex % 36) / 6
		b := cubeIndex % 6
		toHex := func(n int) string {
			component := 0
			if n != 0 {
				component = 55 + n*40
			}
			return fmt.Sprintf("%02x", component)
		}
		return "#" + toHex(r) + toHex(g) + toHex(b)
	}

	// Grayscale (232-255): 24 shades
	gray := 8 + (index-232)*10
	return fmt.Sprintf("#%02x%02x%02x", gray, gray, gray)
}

// escapeHtml escapes HTML special characters. The single-pass replacer is
// equivalent to the TS sequential replaces (no replacement output feeds a
// later pattern).
func escapeHtml(text string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#039;",
	).Replace(text)
}

// textStyle is the SGR style state (pi: TextStyle).
type textStyle struct {
	fg        string
	bg        string
	bold      bool
	dim       bool
	italic    bool
	underline bool
}

func createEmptyStyle() textStyle {
	return textStyle{}
}

func styleToInlineCSS(style textStyle) string {
	var parts []string
	if style.fg != "" {
		parts = append(parts, "color:"+style.fg)
	}
	if style.bg != "" {
		parts = append(parts, "background-color:"+style.bg)
	}
	if style.bold {
		parts = append(parts, "font-weight:bold")
	}
	if style.dim {
		parts = append(parts, "opacity:0.6")
	}
	if style.italic {
		parts = append(parts, "font-style:italic")
	}
	if style.underline {
		parts = append(parts, "text-decoration:underline")
	}
	return strings.Join(parts, ";")
}

func hasStyle(style textStyle) bool {
	return style.fg != "" || style.bg != "" || style.bold || style.dim || style.italic || style.underline
}

// jsParseInt10 replicates JS parseInt(p, 10) for SGR parameter tokens:
// parse the longest leading digit run; "" (or no digits) yields 0 via the
// `|| 0` fallback (NaN → 0). Leading zeros are decimal, never octal.
func jsParseInt10(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	v, err := strconv.Atoi(s[:end])
	if err != nil {
		return 0
	}
	return v
}

// applySgrCode parses ANSI SGR (Select Graphic Rendition) codes and updates
// the style.
func applySgrCode(params []int, style *textStyle) {
	i := 0
	for i < len(params) {
		code := params[i]

		switch {
		case code == 0:
			// Reset all
			*style = textStyle{}
		case code == 1:
			style.bold = true
		case code == 2:
			style.dim = true
		case code == 3:
			style.italic = true
		case code == 4:
			style.underline = true
		case code == 22:
			// Reset bold/dim
			style.bold = false
			style.dim = false
		case code == 23:
			style.italic = false
		case code == 24:
			style.underline = false
		case code >= 30 && code <= 37:
			// Standard foreground colors
			style.fg = ansiColors[code-30]
		case code == 38:
			// Extended foreground color
			if i+1 < len(params) && params[i+1] == 5 && len(params) > i+2 {
				// 256-color: 38;5;N
				style.fg = color256ToHex(params[i+2])
				i += 2
			} else if i+1 < len(params) && params[i+1] == 2 && len(params) > i+4 {
				// RGB: 38;2;R;G;B
				style.fg = fmt.Sprintf("rgb(%d,%d,%d)", params[i+2], params[i+3], params[i+4])
				i += 4
			}
		case code == 39:
			// Default foreground
			style.fg = ""
		case code >= 40 && code <= 47:
			// Standard background colors
			style.bg = ansiColors[code-40]
		case code == 48:
			// Extended background color
			if i+1 < len(params) && params[i+1] == 5 && len(params) > i+2 {
				// 256-color: 48;5;N
				style.bg = color256ToHex(params[i+2])
				i += 2
			} else if i+1 < len(params) && params[i+1] == 2 && len(params) > i+4 {
				// RGB: 48;2;R;G;B
				style.bg = fmt.Sprintf("rgb(%d,%d,%d)", params[i+2], params[i+3], params[i+4])
				i += 4
			}
		case code == 49:
			// Default background
			style.bg = ""
		case code >= 90 && code <= 97:
			// Bright foreground colors
			style.fg = ansiColors[code-90+8]
		case code >= 100 && code <= 107:
			// Bright background colors
			style.bg = ansiColors[code-100+8]
		}
		// Ignore unrecognized codes

		i++
	}
}

// ansiRegex matches ANSI escape sequences: ESC[ followed by params and
// ending with 'm' (pi: /\x1b\[([\d;]*)m/g).
var ansiRegex = regexp.MustCompile(`\x1b\[([\d;]*)m`)

// AnsiToHtml converts ANSI-escaped text to HTML with inline styles.
func AnsiToHtml(text string) string {
	style := createEmptyStyle()
	var result strings.Builder
	lastIndex := 0
	inSpan := false

	for _, loc := range ansiRegex.FindAllStringSubmatchIndex(text, -1) {
		matchStart, matchEnd := loc[0], loc[1]
		// Add text before this escape sequence
		if matchStart > lastIndex {
			result.WriteString(escapeHtml(text[lastIndex:matchStart]))
		}

		// Parse SGR parameters (group 1)
		paramStr := ""
		if loc[2] >= 0 {
			paramStr = text[loc[2]:loc[3]]
		}
		var params []int
		if paramStr != "" {
			for _, p := range strings.Split(paramStr, ";") {
				params = append(params, jsParseInt10(p))
			}
		} else {
			params = []int{0}
		}

		// Close existing span if we have one
		if inSpan {
			result.WriteString("</span>")
			inSpan = false
		}

		// Apply the codes
		applySgrCode(params, &style)

		// Open new span if we have any styling
		if hasStyle(style) {
			result.WriteString(`<span style="` + styleToInlineCSS(style) + `">`)
			inSpan = true
		}

		lastIndex = matchEnd
	}

	// Add remaining text
	if lastIndex < len(text) {
		result.WriteString(escapeHtml(text[lastIndex:]))
	}

	// Close any open span
	if inSpan {
		result.WriteString("</span>")
	}

	return result.String()
}

// AnsiLinesToHtml converts an array of ANSI-escaped lines to HTML. Each
// line is wrapped in a div element.
func AnsiLinesToHtml(lines []string) string {
	var sb strings.Builder
	for _, line := range lines {
		html := AnsiToHtml(line)
		if html == "" {
			html = "&nbsp;"
		}
		sb.WriteString(`<div class="ansi-line">` + html + `</div>`)
	}
	return sb.String()
}
