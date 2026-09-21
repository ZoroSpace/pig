// index_test.go — template-half coverage (pi index.ts's helpers have no
// dedicated pi test file; P8-EH1 builds the coverage pig-side, including
// the JS .replace $-pattern parity pins).
package exporthtml

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func loPtr[T any](v T) *T { return &v }

func TestParseColor(t *testing.T) {
	if r, g, b, ok := parseColor("#343541"); !ok || r != 0x34 || g != 0x35 || b != 0x41 {
		t.Fatalf("parseColor hex = %d,%d,%d,%v", r, g, b, ok)
	}
	if r, g, b, ok := parseColor("rgb(1, 2, 3)"); !ok || r != 1 || g != 2 || b != 3 {
		t.Fatalf("parseColor rgb = %d,%d,%d,%v", r, g, b, ok)
	}
	if r, g, b, ok := parseColor("rgb( 10 , 20 , 30 )"); !ok || r != 10 || g != 20 || b != 30 {
		t.Fatalf("parseColor spaced rgb = %d,%d,%d,%v", r, g, b, ok)
	}
	if _, _, _, ok := parseColor("not-a-color"); ok {
		t.Fatal("parseColor should reject garbage")
	}
	if _, _, _, ok := parseColor("#123"); ok {
		t.Fatal("parseColor should reject short hex")
	}
}

func TestGetLuminance(t *testing.T) {
	if lum := getLuminance(0, 0, 0); lum != 0 {
		t.Fatalf("black luminance = %v", lum)
	}
	if lum := getLuminance(255, 255, 255); lum <= 0.99 || lum > 1 {
		t.Fatalf("white luminance = %v", lum)
	}
	// Green dominates luminance (0.7152 weight).
	if getLuminance(0, 255, 0) <= getLuminance(255, 0, 0) {
		t.Fatal("green should out-luminate red")
	}
}

func TestAdjustBrightness(t *testing.T) {
	if got := adjustBrightness("#ffffff", 0.5); got != "rgb(128, 128, 128)" {
		t.Fatalf("adjustBrightness(#ffffff, 0.5) = %s", got)
	}
	if got := adjustBrightness("#000000", 2); got != "rgb(0, 0, 0)" {
		t.Fatalf("adjustBrightness(#000000, 2) = %s", got)
	}
	if got := adjustBrightness("rgb(100, 200, 50)", 2); got != "rgb(200, 255, 100)" {
		t.Fatalf("adjustBrightness rgb input = %s", got)
	}
	// Unparseable colors pass through unchanged.
	if got := adjustBrightness("hsl(1,2,3)", 2); got != "hsl(1,2,3)" {
		t.Fatalf("adjustBrightness passthrough = %s", got)
	}
}

func TestDeriveExportColors(t *testing.T) {
	// Unparseable → fixed dark defaults.
	dark := deriveExportColors("nonsense")
	if dark.PageBg != "rgb(24, 24, 30)" || dark.CardBg != "rgb(30, 30, 36)" || dark.InfoBg != "rgb(60, 55, 40)" {
		t.Fatalf("deriveExportColors fallback = %+v", dark)
	}

	// Light base (#ffffff): page darkened to 0.96, card = base, info shifted.
	light := deriveExportColors("#ffffff")
	if light.PageBg != "rgb(245, 245, 245)" || light.CardBg != "#ffffff" || light.InfoBg != "rgb(255, 255, 235)" {
		t.Fatalf("deriveExportColors light = %+v", light)
	}

	// Dark base (#1a1a20): page darkened to 0.7, card to 0.85.
	d := deriveExportColors("#1a1a20")
	if d.PageBg != "rgb(18, 18, 22)" || d.CardBg != "rgb(22, 22, 27)" || d.InfoBg != "rgb(46, 41, 32)" {
		t.Fatalf("deriveExportColors dark = %+v", d)
	}
}

func TestJsStringReplacePatterns(t *testing.T) {
	tests := []struct {
		name        string
		s, search   string
		replacement string
		want        string
	}{
		{"no match is identity", "abc", "zz", "X", "abc"},
		{"plain replacement", "abc", "b", "X", "aXc"},
		{"dollar dollar collapses", "abc", "b", "$$", "a$c"},
		{"dollar ampersand is the match", "abc", "b", "[$&]", "a[b]c"},
		{"backtick is the prefix", "hello", "ll", "$`", "heheo"},
		{"quote is the suffix", "hello", "ll", "$'", "heoo"}, // suffix + the natural tail
		{"dollar digit is literal (no capture groups)", "abc", "b", "$1", "a$1c"},
		{"dollar digit zero literal", "abc", "b", "$0", "a$0c"},
		{"dollar name literal", "abc", "b", "$<name>", "a$<name>c"},
		{"trailing dollar literal", "abc", "b", "X$", "aX$c"},
		{"only first occurrence replaced", "abcb", "b", "X", "aXcb"},
		{"multiple patterns in one replacement", "xy", "y", "$&$&", "xyy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jsStringReplace(tt.s, tt.search, tt.replacement); got != tt.want {
				t.Fatalf("jsStringReplace(%q, %q, %q) = %q, want %q", tt.s, tt.search, tt.replacement, got, tt.want)
			}
		})
	}
}

// TestJsStringReplaceMatchesJS pins the semantics against the real JS
// engine via a precomputed sample (the vendor highlight.min.js "$&" case).
func TestJsStringReplaceMatchesJS(t *testing.T) {
	// node -e 'console.log("A{{JS}}B".replace("{{JS}}", "x$&y`z$1"))'
	if got := jsStringReplace("A{{JS}}B", "{{JS}}", "x$&y`z$1"); got != "Ax{{JS}}y`z$1B" {
		t.Fatalf("jsStringReplace parity sample = %q", got)
	}
}

func TestJsonEncodeNoHTMLEscape(t *testing.T) {
	encoded, err := jsonEncodeNoHTMLEscape(map[string]any{"s": "<a>&\"b\""})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `\u003c`) || strings.Contains(string(encoded), `\u0026`) {
		t.Fatalf("encoder escaped HTML characters: %s", encoded)
	}
	if strings.HasSuffix(string(encoded), "\n") {
		t.Fatalf("encoder left the trailing newline: %q", encoded)
	}
}

var sessionDataScriptRegex = regexp.MustCompile(`<script id="session-data" type="application/json">([A-Za-z0-9+/=]+)</script>`)

func TestGenerateHtmlEndToEnd(t *testing.T) {
	sessionData := SessionData{
		LeafID:        loPtr("leaf-1"),
		SystemPrompt:  "You are a test.",
		Tools:         loPtr([]ToolSummary{{Name: "bash", Description: "Run bash", Parameters: json.RawMessage(`{"type":"object"}`)}}),
		RenderedTools: map[string]RenderedToolHtml{"call-1": {CallHTML: "<div>custom</div>"}},
	}
	html, err := GenerateHtml(sessionData, "dark")
	if err != nil {
		t.Fatal(err)
	}

	for _, placeholder := range []string{"{{CSS}}", "{{JS}}", "{{SESSION_DATA}}", "{{MARKED_JS}}"} {
		if strings.Contains(html, placeholder) {
			t.Errorf("placeholder %s not replaced", placeholder)
		}
	}
	// {{HIGHLIGHT_JS}} is checked by exact count below: the vendored JS's
	// "$&" expands to the literal search string, so exactly one artifact
	// must survive (the original placeholder itself is replaced).
	for _, marker := range []string{"<!DOCTYPE html>", "<style>", "marked", "highlight.js", "--exportPageBg"} {
		if !strings.Contains(html, marker) {
			t.Errorf("output missing %q", marker)
		}
	}

	// The vendored highlight.min.js "$&" quirk: pi's .replace() expands it
	// to the literal search string — exactly one artifact must survive.
	if got := strings.Count(html, "{{HIGHLIGHT_JS}}"); got != 1 {
		t.Fatalf("expected exactly 1 $& artifact, got %d", got)
	}

	match := sessionDataScriptRegex.FindStringSubmatch(html)
	if match == nil {
		t.Fatal("session-data script tag not found or payload not plain base64")
	}
	decoded, err := base64.StdEncoding.DecodeString(match[1])
	if err != nil {
		t.Fatalf("payload base64: %v", err)
	}
	var parsed struct {
		Header       any     `json:"header"`
		Entries      []any   `json:"entries"`
		LeafID       *string `json:"leafId"`
		SystemPrompt string  `json:"systemPrompt"`
		Tools        []any   `json:"tools"`
	}
	if err := json.Unmarshal(decoded, &parsed); err != nil {
		t.Fatalf("payload JSON: %v (bytes: %.80s)", err, decoded)
	}
	if parsed.LeafID == nil || *parsed.LeafID != "leaf-1" {
		t.Fatalf("leafId = %v", parsed.LeafID)
	}
	if parsed.SystemPrompt != "You are a test." {
		t.Fatalf("systemPrompt = %q", parsed.SystemPrompt)
	}
	if len(parsed.Tools) != 1 {
		t.Fatalf("tools = %v", parsed.Tools)
	}
	// Header nil (omitted here) must not appear; omitted fields stay absent.
	if strings.Contains(string(decoded), `"renderedTools"`) == false {
		t.Fatalf("renderedTools missing from payload: %.120s", decoded)
	}
}

func TestGenerateHtmlEmptySession(t *testing.T) {
	html, err := GenerateHtml(SessionData{}, "")
	if err != nil {
		t.Fatal(err)
	}
	match := sessionDataScriptRegex.FindStringSubmatch(html)
	if match == nil {
		t.Fatal("session-data payload missing")
	}
	decoded, err := base64.StdEncoding.DecodeString(match[1])
	if err != nil {
		t.Fatal(err)
	}
	want := `{"header":null,"entries":null,"leafId":null}`
	if string(decoded) != want {
		t.Fatalf("empty session payload = %s, want %s", decoded, want)
	}
}
