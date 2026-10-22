// Package exporthtml implements the HTML session export (pi:
// core/export-html/*, v0.86.0).
//
// pi's export-html/index.ts mixes two halves: template/theme/HTML
// generation and session-manager access. Go forbids the import cycle that
// would arise (package codingagent/core must call this package for
// AgentSession.ExportToHtml, while this package needs core's
// SessionManager/SessionEntry types), so the session-facing half lives in
// codingagent/core/export_html.go (pi index.ts's exportSessionToHtml /
// exportFromFile / preRenderCustomTools) and this package keeps the
// template half: ExportOptions, the ToolHtmlRenderer interface,
// SessionData, theme color derivation, and generateHtml (pi index.go's
// parseColor/getLuminance/adjustBrightness/deriveExportColors/
// generateThemeVars/generateHtml).
//
// The template assets (template.html/css/js + vendored marked and
// highlight.js) are embedded verbatim from pi (pig ships a single static
// binary; pi reads them from disk next to the executable — declared
// deviation).
package exporthtml

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
)

// ExportOptions parameterizes HTML export (pi: ExportOptions).
type ExportOptions struct {
	// OutputPath is the target file; "" = a default next to the session
	// file (pi: outputPath?).
	OutputPath string
	// ThemeName resolves the theme for the exported page (pi: themeName?).
	ThemeName string
	// ToolRenderer renders custom tools to HTML (pi: toolRenderer?).
	ToolRenderer ToolHtmlRenderer
}

// ToolHtmlRenderer renders custom tools to HTML (pi: ToolHtmlRenderer in
// index.ts). Used by agent-session to pre-render extension tool output.
//
// "" returns mean "no custom renderer" (pi: undefined); RenderResult's
// collapsed string is "" when it equals expanded or is empty (pi: the
// collapsed key is spread in conditionally).
type ToolHtmlRenderer interface {
	// RenderCall renders a tool call to HTML (pi: renderCall).
	RenderCall(toolCallID string, toolName string, args any) string
	// RenderResult renders a tool result to collapsed/expanded HTML (pi:
	// renderResult). ok=false when the tool has no custom renderer.
	RenderResult(toolCallID string, toolName string, result []types.Content, details any, isError bool) (collapsed, expanded string, ok bool)
}

// RenderedToolHtml is pre-rendered HTML for a custom tool call and result
// (pi: RenderedToolHtml).
type RenderedToolHtml struct {
	CallHTML            string `json:"callHtml,omitempty"`
	ResultHTMLCollapsed string `json:"resultHtmlCollapsed,omitempty"`
	ResultHTMLExpanded  string `json:"resultHtmlExpanded,omitempty"`
}

// templateRenderedTools lists tools rendered directly by the HTML template
// (not pre-rendered via the TUI→ANSI→HTML pipeline) (pi:
// TEMPLATE_RENDERED_TOOLS).
var templateRenderedTools = map[string]bool{
	"bash":  true,
	"read":  true,
	"write": true,
	"edit":  true,
	"ls":    true,
}

// IsTemplateRenderedTool reports whether the named tool is rendered by the
// HTML template itself (pig addition: the set is module-private in pi, but
// the session-facing half lives in codingagent/core and needs the lookup).
func IsTemplateRenderedTool(name string) bool {
	return templateRenderedTools[name]
}

// parseColor parses a color string to RGB values. Supports hex (#RRGGBB)
// and rgb(r,g,b) formats.
func parseColor(color string) (r, g, b int, ok bool) {
	hexMatch := hexColorRegex.FindStringSubmatch(color)
	if hexMatch != nil {
		return parseHexByte(hexMatch[1]), parseHexByte(hexMatch[2]), parseHexByte(hexMatch[3]), true
	}
	rgbMatch := rgbColorRegex.FindStringSubmatch(color)
	if rgbMatch != nil {
		return atoi10(rgbMatch[1]), atoi10(rgbMatch[2]), atoi10(rgbMatch[3]), true
	}
	return 0, 0, 0, false
}

var (
	hexColorRegex = regexp.MustCompile(`^#([0-9a-fA-F]{2})([0-9a-fA-F]{2})([0-9a-fA-F]{2})$`)
	rgbColorRegex = regexp.MustCompile(`^rgb\s*\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*\)$`)
)

func parseHexByte(s string) int {
	v := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			v = v*16 + int(c-'0')
		case c >= 'a' && c <= 'f':
			v = v*16 + int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v = v*16 + int(c-'A') + 10
		}
	}
	return v
}

func atoi10(s string) int {
	v := 0
	for i := 0; i < len(s); i++ {
		v = v*10 + int(s[i]-'0')
	}
	return v
}

// getLuminance calculates the relative luminance of a color (0-1, higher =
// lighter).
func getLuminance(r, g, b int) float64 {
	toLinear := func(c int) float64 {
		s := float64(c) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*toLinear(r) + 0.7152*toLinear(g) + 0.0722*toLinear(b)
}

// adjustBrightness adjusts color brightness. Factor > 1 lightens, < 1
// darkens.
func adjustBrightness(color string, factor float64) string {
	r, g, b, ok := parseColor(color)
	if !ok {
		return color
	}
	adjust := func(c int) int {
		v := int(math.Round(float64(c) * factor))
		return min(255, max(0, v))
	}
	return "rgb(" + strconv.Itoa(adjust(r)) + ", " + strconv.Itoa(adjust(g)) + ", " + strconv.Itoa(adjust(b)) + ")"
}

// ExportColors is the derived export background colors (pi:
// deriveExportColors's { pageBg, cardBg, infoBg }).
type ExportColors struct {
	PageBg string
	CardBg string
	InfoBg string
}

// deriveExportColors derives export background colors from a base color
// (e.g. userMessageBg).
func deriveExportColors(baseColor string) ExportColors {
	r, g, b, ok := parseColor(baseColor)
	if !ok {
		return ExportColors{
			PageBg: "rgb(24, 24, 30)",
			CardBg: "rgb(30, 30, 36)",
			InfoBg: "rgb(60, 55, 40)",
		}
	}

	luminance := getLuminance(r, g, b)
	isLight := luminance > 0.5

	if isLight {
		return ExportColors{
			PageBg: adjustBrightness(baseColor, 0.96),
			CardBg: baseColor,
			InfoBg: "rgb(" + strconv.Itoa(min(255, r+10)) + ", " + strconv.Itoa(min(255, g+5)) + ", " + strconv.Itoa(max(0, b-20)) + ")",
		}
	}
	return ExportColors{
		PageBg: adjustBrightness(baseColor, 0.7),
		CardBg: adjustBrightness(baseColor, 0.85),
		InfoBg: "rgb(" + strconv.Itoa(min(255, r+20)) + ", " + strconv.Itoa(min(255, g+15)) + ", " + strconv.Itoa(b) + ")",
	}
}

// generateThemeVars generates CSS custom property declarations from theme
// colors (pi: generateThemeVars, calling getResolvedThemeColors/
// getThemeExportColors directly).
//
// Deviation: Go maps are unordered, so the --var lines are emitted in
// sorted-key order; pi emits them in the theme JSON's key order. CSS
// custom property order is semantically irrelevant.
func generateThemeVars(themeName string) string {
	colors := theme.GetResolvedThemeColors(themeName)
	keys := make([]string, 0, len(colors))
	for key := range colors {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys)+3)
	for _, key := range keys {
		lines = append(lines, "--"+key+": "+colors[key]+";")
	}

	// Use explicit theme export colors if available, otherwise derive from
	// userMessageBg
	themeExport := theme.GetThemeExportColors(themeName)
	userMessageBg := colors["userMessageBg"]
	if userMessageBg == "" {
		userMessageBg = "#343541"
	}
	derivedColors := deriveExportColors(userMessageBg)

	pageBg := derivedColors.PageBg
	if themeExport.PageBg != "" {
		pageBg = themeExport.PageBg
	}
	cardBg := derivedColors.CardBg
	if themeExport.CardBg != "" {
		cardBg = themeExport.CardBg
	}
	infoBg := derivedColors.InfoBg
	if themeExport.InfoBg != "" {
		infoBg = themeExport.InfoBg
	}

	lines = append(lines,
		"--exportPageBg: "+pageBg+";",
		"--exportCardBg: "+cardBg+";",
		"--exportInfoBg: "+infoBg+";",
	)

	return strings.Join(lines, "\n      ")
}

// ToolSummary is the tools payload entry (pi: state.tools mapped to
// { name, description, parameters }).
type ToolSummary struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// SessionData is the session payload embedded into the exported page (pi:
// SessionData). Header/Entries hold the typed session values from
// codingagent/core (marshaling goes through their pi-compatible JSON
// tags); they are typed `any` here so this package does not import
// codingagent/core.
type SessionData struct {
	Header any `json:"header"` // *core.SessionHeader or nil (pi: SessionHeader | null)
	// Entries is the []core.SessionEntry slice (pi: SessionEntry[]).
	Entries any `json:"entries"`
	// LeafID is the current branch leaf (pi: leafId: string | null).
	LeafID *string `json:"leafId"`
	// SystemPrompt is the agent's current system prompt (pi: systemPrompt?,
	// omitted when empty).
	SystemPrompt string `json:"systemPrompt,omitempty"`
	// Tools are the agent's active tools (pi: tools?). Pointer (pig
	// serialization seam): pi's map over an empty array emits "tools": [],
	// while Go's omitempty would drop an empty slice — nil = absent,
	// non-nil (even empty) = present.
	Tools *[]ToolSummary `json:"tools,omitempty"`
	// RenderedTools is pre-rendered custom tool HTML keyed by tool call ID
	// (pi: renderedTools?).
	RenderedTools map[string]RenderedToolHtml `json:"renderedTools,omitempty"`
}

// Embedded template assets (pi reads them from getExportTemplateDir() on
// disk; pig embeds — see package comment).
var (
	//go:embed templates/template.html
	templateHTML string
	//go:embed templates/template.css
	templateCSS string
	//go:embed templates/template.js
	templateJS string
	//go:embed templates/vendor/marked.min.js
	markedJS string
	//go:embed templates/vendor/marked-cjk-friendly.min.js
	markedCjkFriendlyJS string
	//go:embed templates/vendor/highlight.min.js
	highlightJS string
)

// jsStringReplace replicates String.prototype.replace(searchString,
// replacement) with a string search, including the $-pattern expansion of
// the replacement (ECMA-262 GetSubstitution: $$ → "$", $& → the match,
// "$`" → the portion before the match, $' → the portion after, $n/$nn →
// capture groups — of which a string search has none, so those stay
// literal).
//
// This matters for byte parity: pi injects the vendored JS with
// .replace(), and highlight.min.js contains one "$&" (inside a regex
// character class) which pi expands to the literal search string
// "{{HIGHLIGHT_JS}}". A plain Go strings.Replace would produce "more
// correct" but different output.
func jsStringReplace(s, search, replacement string) string {
	idx := strings.Index(s, search)
	if idx < 0 {
		return s
	}
	tailStart := idx + len(search)
	var sb strings.Builder
	sb.Grow(len(s) + len(replacement))
	sb.WriteString(s[:idx])
	i := 0
	for i < len(replacement) {
		c := replacement[i]
		if c != '$' || i+1 >= len(replacement) {
			sb.WriteByte(c)
			i++
			continue
		}
		switch replacement[i+1] {
		case '$':
			sb.WriteByte('$')
			i += 2
		case '&':
			sb.WriteString(search)
			i += 2
		case '`':
			sb.WriteString(s[:idx])
			i += 2
		case '\'':
			sb.WriteString(s[tailStart:])
			i += 2
		default:
			// $n / $<name>: no capture groups and no named groups with a
			// string search → literal '$', re-scan the next character.
			sb.WriteByte('$')
			i++
		}
	}
	sb.WriteString(s[tailStart:])
	return sb.String()
}

// jsonEncodeNoHTMLEscape serializes v the way JS JSON.stringify does
// (raw UTF-8, no <>& escaping; Go's default encoder escapes them).
func jsonEncodeNoHTMLEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	// Encode appends a trailing newline; JSON.stringify does not.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// GenerateHtml is the core HTML generation logic shared by both export
// functions (pi: generateHtml).
func GenerateHtml(sessionData SessionData, themeName string) (string, error) {
	themeVars := generateThemeVars(themeName)
	colors := theme.GetResolvedThemeColors(themeName)
	themeExport := theme.GetThemeExportColors(themeName)
	userMessageBg := colors["userMessageBg"]
	if userMessageBg == "" {
		userMessageBg = "#343541"
	}
	derivedExportColors := deriveExportColors(userMessageBg)
	bodyBg := derivedExportColors.PageBg
	if themeExport.PageBg != "" {
		bodyBg = themeExport.PageBg
	}
	containerBg := derivedExportColors.CardBg
	if themeExport.CardBg != "" {
		containerBg = themeExport.CardBg
	}
	infoBg := derivedExportColors.InfoBg
	if themeExport.InfoBg != "" {
		infoBg = themeExport.InfoBg
	}

	// Base64 encode session data to avoid escaping issues
	sessionJSON, err := jsonEncodeNoHTMLEscape(sessionData)
	if err != nil {
		return "", err
	}
	sessionDataBase64 := base64.StdEncoding.EncodeToString(sessionJSON)

	// Build the CSS with theme variables injected
	css := jsStringReplace(templateCSS, "{{THEME_VARS}}", themeVars)
	css = jsStringReplace(css, "{{BODY_BG}}", bodyBg)
	css = jsStringReplace(css, "{{CONTAINER_BG}}", containerBg)
	css = jsStringReplace(css, "{{INFO_BG}}", infoBg)

	html := jsStringReplace(templateHTML, "{{CSS}}", css)
	html = jsStringReplace(html, "{{JS}}", templateJS)
	html = jsStringReplace(html, "{{SESSION_DATA}}", sessionDataBase64)
	html = jsStringReplace(html, "{{MARKED_JS}}", markedJS)
	html = jsStringReplace(html, "{{MARKED_CJK_FRIENDLY_JS}}", markedCjkFriendlyJS)
	html = jsStringReplace(html, "{{HIGHLIGHT_JS}}", highlightJS)
	return html, nil
}
