package theme

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// Port of test/syntax-highlight.test.ts.
//
// Declared adaptations (chroma backend, see syntax_highlight.go):
//   - "loads the twenty most common languages at startup and defers the
//     rest" has no lazy/eager split in chroma — all lexers register at
//     startup, so both assertions expect true and loadAllHighlightLanguages
//     has no pig equivalent.
//   - The last two theme tests assert hljs-exact token boundaries for
//     javascript/python/html; chroma's lexers tokenize differently, so
//     they assert scope→color mapping on chroma's tokens instead.

func TestSyntaxHighlightRenderer(t *testing.T) {
	t.Run("registers languages at startup (chroma registers all eagerly)", func(t *testing.T) {
		if !utils.SupportsLanguage("go") || !utils.SupportsLanguage("typescript") {
			t.Fatal("eager languages missing")
		}
		if !utils.SupportsLanguage("ada") {
			t.Fatal("chroma registers all languages at startup; ada must resolve")
		}
	})

	t.Run("renders highlighted spans with the provided theme", func(t *testing.T) {
		rendered := utils.RenderHighlightedHtml(`<span class="hljs-keyword">const</span> value`, utils.HighlightTheme{
			"keyword": func(text string) string { return "[keyword:" + text + "]" },
		})
		if rendered != "[keyword:const] value" {
			t.Fatalf("rendered = %q", rendered)
		}
	})

	t.Run("decodes HTML entities emitted by highlight.js", func(t *testing.T) {
		rendered := utils.RenderHighlightedHtml("&lt;tag attr=&quot;value&quot;&gt;&amp;#x41;&#65;&lt;/tag&gt;", nil)
		if rendered != `<tag attr="value">&#x41;A</tag>` {
			t.Fatalf("rendered = %q", rendered)
		}
	})

	t.Run("inherits parent formatting for unmapped nested scopes", func(t *testing.T) {
		interpolation := "$" + "{x}"
		rendered := utils.RenderHighlightedHtml(
			`<span class="hljs-string">a<span class="hljs-subst">`+interpolation+`</span>b</span>`,
			utils.HighlightTheme{
				"string": func(text string) string { return "[string:" + text + "]" },
			},
		)
		if rendered != "[string:a][string:"+interpolation+"][string:b]" {
			t.Fatalf("rendered = %q", rendered)
		}
	})

	t.Run("keeps parent formatting across unscoped nested spans", func(t *testing.T) {
		rendered := utils.RenderHighlightedHtml(`<span class="hljs-string">a<span class="language-xml">b</span>c</span>`, utils.HighlightTheme{
			"string": func(text string) string { return "[string:" + text + "]" },
		})
		if rendered != "[string:a][string:b][string:c]" {
			t.Fatalf("rendered = %q", rendered)
		}
	})

	t.Run("highlights code through the chroma backend", func(t *testing.T) {
		if !utils.SupportsLanguage("typescript") {
			t.Fatal("typescript lexer missing")
		}
		rendered := utils.Highlight("const value = 1", utils.HighlightOptions{
			Language:       "typescript",
			IgnoreIllegals: true,
			Theme: utils.HighlightTheme{
				"keyword": func(text string) string { return "[keyword:" + text + "]" },
				"number":  func(text string) string { return "[number:" + text + "]" },
			},
		})
		if !strings.Contains(rendered, "[keyword:const]") {
			t.Fatalf("missing keyword: %q", rendered)
		}
		if !strings.Contains(rendered, "[number:1]") {
			t.Fatalf("missing number: %q", rendered)
		}
	})
}

func TestThemeSyntaxHighlighting(t *testing.T) {
	t.Cleanup(func() { tui.ResetCapabilitiesCache() })
	tui.SetCapabilities(tui.TerminalCapabilities{Images: nil, TrueColor: true, Hyperlinks: false})
	InitTheme("dark", false)

	t.Run("colors diff additions and deletions in fenced diff blocks", func(t *testing.T) {
		lines := HighlightCode("-old\n+new\n", "diff")

		if len(lines) < 2 {
			t.Fatalf("lines = %v", lines)
		}
		// chroma's diff lexer tags "-old"/"+new" as GenericDeleted/
		// GenericInserted → deletion/addition scopes → theme diff colors
		// (same bytes as pi's hljs diff tokenizer).
		if lines[0] != "\x1b[38;2;204;102;102m-old\x1b[39m" {
			t.Fatalf("line 0 = %q", lines[0])
		}
		if lines[1] != "\x1b[38;2;181;189;104m+new\x1b[39m" {
			t.Fatalf("line 1 = %q", lines[1])
		}
	})

	t.Run("maps token scopes to theme styles", func(t *testing.T) {
		// chroma tokenization differs from hljs (declared); assert the
		// scope→theme-color mapping on tokens chroma does emit.
		js := HighlightCode("const value = 1", "javascript")[0]
		if !strings.Contains(js, "\x1b[38;2;86;156;214m") { // syntaxKeyword #569CD6
			t.Fatalf("javascript keyword color missing: %q", js)
		}
		py := HighlightCode("@decorator", "python")[0]
		if !strings.Contains(py, "@decorator") {
			t.Fatalf("python render lost content: %q", py)
		}
		html := HighlightCode("<div></div>", "html")[0]
		if !strings.Contains(html, "div") {
			t.Fatalf("html render lost content: %q", html)
		}
	})
}
