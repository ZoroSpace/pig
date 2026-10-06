// Package utils: syntax_highlight.go (pi: src/utils/syntax-highlight.ts +
// src/utils/html.ts decodeHtmlEntityAt/decodeHtmlEntity).
//
// Backend deviation (declared): pi uses highlight.js; pig uses
// github.com/alecthomas/chroma/v2 (the repo's chosen highlighter, also
// slated for export-html). The two-stage pipeline is preserved verbatim:
// the lexer produces HTML spans whose class names are chroma token
// categories mapped onto highlight.js scope names (keyword, string,
// comment, function, ...), and RenderHighlightedHtml applies the caller's
// HighlightTheme formatters over that HTML. Language coverage therefore
// follows chroma's lexer registry (broader than pi's 20 eager languages);
// language names resolve through chroma's aliases.
//
// HTML entity decoding is ported verbatim from html.ts (used by
// RenderHighlightedHtml; the rest of html.ts has no consumer yet).
package utils

import (
	"html"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// HighlightFormatter is pi's HighlightFormatter.
type HighlightFormatter = func(text string) string

// HighlightTheme is pi's HighlightTheme
// (Partial<Record<string, HighlightFormatter>> → map; missing keys fall
// through scope prefixes and finally to the "default" entry).
type HighlightTheme map[string]HighlightFormatter

// HighlightOptions mirrors pi's HighlightOptions.
type HighlightOptions struct {
	Language       string
	IgnoreIllegals bool
	LanguageSubset []string
	Theme          HighlightTheme
}

// chromaScopeNames maps chroma token categories (and specific types) onto
// highlight.js scope names, which is what the theme's highlight theme map
// is keyed by (pi: buildCliHighlightTheme in theme.ts).
var chromaScopeNames = map[chroma.TokenType]string{
	chroma.Keyword:            "keyword",
	chroma.NameBuiltin:        "built_in",
	chroma.NameBuiltinPseudo:  "built_in",
	chroma.Literal:            "literal",
	chroma.LiteralDate:        "literal",
	chroma.LiteralOther:       "literal",
	chroma.LiteralString:      "string",
	chroma.LiteralStringRegex: "regexp",
	// pi maps highlight.js's `subst` scope (string interpolation delimiters
	// and content) to the theme's text color (#10356); chroma's closest
	// token is LiteralStringInterpol (declared adaptation).
	chroma.LiteralStringInterpol: "subst",
	chroma.LiteralNumber:         "number",
	chroma.Comment:               "comment",
	chroma.CommentPreprocFile:    "doctag",
	chroma.GenericHeading:        "meta",
	chroma.GenericSubheading:     "meta",
	chroma.GenericDeleted:        "deletion",
	chroma.GenericInserted:       "addition",
	chroma.GenericEmph:           "emphasis",
	chroma.GenericStrong:         "strong",
	chroma.GenericUnderline:      "link",
	chroma.NameFunction:          "function",
	chroma.NameFunctionMagic:     "function",
	chroma.NameDecorator:         "title",
	chroma.NameClass:             "class",
	chroma.NameException:         "class",
	chroma.NameNamespace:         "class",
	chroma.NameTag:               "tag",
	chroma.NameLabel:             "name",
	chroma.NameAttribute:         "attr",
	chroma.NameEntity:            "attr",
	chroma.NameProperty:          "attr",
	chroma.NameConstant:          "variable",
	chroma.NameVariable:          "variable",
	chroma.NameOther:             "variable",
	chroma.Operator:              "operator",
	chroma.OperatorWord:          "operator",
	chroma.Punctuation:           "punctuation",
	chroma.GenericPrompt:         "punctuation",
}

// chromaScopeForToken resolves the highlight.js scope name for a chroma
// token, walking sub-category categories up to the root (pi equivalent: the
// nested span scopes of hljs HTML). Note: chroma's Category() returns the
// token itself for top-level categories (t/1000*1000), so the walk stops
// when the category stops changing — the previous `t != chroma.None` loop
// spun forever on those tokens.
func chromaScopeForToken(tokenType chroma.TokenType) string {
	for t := tokenType; t != chroma.None; {
		if name, ok := chromaScopeNames[t]; ok {
			return name
		}
		// Walk the two-level chroma hierarchy: sub-category first
		// (t/100*100, e.g. LiteralNumber* → LiteralNumber), then category
		// (t/1000*1000, e.g. LiteralNumber → Literal).
		if sub := t.SubCategory(); sub != t && sub != t.Category() {
			if name, ok := chromaScopeNames[sub]; ok {
				return name
			}
		}
		category := t.Category()
		if category == t {
			break
		}
		t = category
	}
	return ""
}

// escapeHtmlBody escapes text for an HTML body context the way
// highlight.js does (&, <, >, "; quotes get escaped so class attribute
// parsing stays unambiguous).
func escapeHtmlBody(text string) string {
	return html.EscapeString(text)
}

// highlightToHtml runs the chroma lexer for language over code and emits
// highlight.js-shaped HTML: `<span class="scope">text</span>` runs with raw
// text between spans (pi: hljs.highlight(...).value).
//
// Multi-line tokens are split so each output line carries its own span
// open/close (hljs closes spans within a line; pi's theme tests lock
// per-line reset sequences, and chroma token values include trailing
// newlines).
func highlightToHtml(code string, language string) (string, bool) {
	lexer := lexers.Get(language)
	if lexer == nil {
		return "", false
	}
	lexer = chroma.Coalesce(lexer)
	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return "", false
	}
	return highlightToHtmlTokens(iterator.Tokens()), true
}

// highlightToHtmlTokens renders a pre-tokenised stream.
func highlightToHtmlTokens(tokens []chroma.Token) string {
	var out strings.Builder
	for _, token := range tokens {
		emitTokenSpans(&out, token.Value, chromaScopeForToken(token.Type))
	}
	return out.String()
}

// emitTokenSpans writes one token's value, splitting on newlines so spans
// never cross a line boundary.
func emitTokenSpans(out *strings.Builder, value string, scope string) {
	if value == "" {
		return
	}
	segments := strings.SplitAfter(value, "\n")
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		endsWithNewline := strings.HasSuffix(segment, "\n")
		text := segment
		if endsWithNewline {
			text = strings.TrimSuffix(segment, "\n")
		}
		if scope != "" {
			out.WriteString(`<span class="hljs-`)
			out.WriteString(scope)
			out.WriteString(`">`)
			out.WriteString(escapeHtmlBody(text))
			out.WriteString("</span>")
		} else {
			out.WriteString(escapeHtmlBody(text))
		}
		if endsWithNewline {
			out.WriteString("\n")
		}
	}
}

// Highlight mirrors pi's highlight(): lex code (or auto-detect when no
// language is given) and render the token stream through the theme
// formatters. Auto-detection maps to chroma's AnalyseLexer (pi:
// hljs.highlightAuto with languageSubset; the subset hint is accepted but
// chroma's analysis does not consume it — declared deviation).
func Highlight(code string, options HighlightOptions) string {
	var htmlOut string
	if options.Language != "" {
		htmlOut, _ = highlightToHtml(code, options.Language)
	} else {
		lexer := lexers.Analyse(code)
		if lexer == nil {
			htmlOut = escapeHtmlBody(code)
		} else {
			iterator, err := chroma.Coalesce(lexer).Tokenise(nil, code)
			if err != nil {
				htmlOut = escapeHtmlBody(code)
			} else {
				htmlOut = highlightToHtmlTokens(iterator.Tokens())
			}
		}
	}
	return RenderHighlightedHtml(htmlOut, options.Theme)
}

// SupportsLanguage mirrors pi's supportsLanguage (hljs.getLanguage !==
// undefined → chroma lexers.Get !== nil). Names resolve through chroma's
// alias table (e.g. "js", "golang", "sh").
func SupportsLanguage(name string) bool {
	if name == "" {
		return false
	}
	return lexers.Get(name) != nil
}

// ============================================================================
// HTML span renderer (verbatim port of syntax-highlight.ts's
// renderHighlightedHtml section)
// ============================================================================

const spanClose = "</span>"
const highlightClassPrefix = "hljs-"

// getScopeFromSpanTag extracts the hljs- prefixed scope from a span tag
// (pi: getScopeFromSpanTag).
func getScopeFromSpanTag(tag string) string {
	// pi: /\sclass\s*=\s*(?:"([^"]*)"|'([^']*)')/ — RE2 cannot backtrack the
	// quoted alternatives, so scan manually: find "class", then =, then the
	// quoted value.
	classIdx := -1
	for i := 0; i+5 < len(tag); i++ {
		if tag[i] == ' ' && strings.HasPrefix(tag[i+1:], "class") {
			rest := tag[i+1:]
			if len(rest) > 5 && (rest[5] == ' ' || rest[5] == '=') {
				// ensure the word ends here (pi's \sclass would not match
				// "xclass" because of the leading \s; " class=x" is matched
				// by \s*=\s* tolerance)
				classIdx = i + 1
				break
			}
		}
	}
	if classIdx == -1 {
		return ""
	}
	eq := strings.Index(tag[classIdx:], "=")
	if eq == -1 {
		return ""
	}
	eq += classIdx
	j := eq + 1
	for j < len(tag) && (tag[j] == ' ' || tag[j] == '\t' || tag[j] == '\n' || tag[j] == '\r') {
		j++
	}
	if j >= len(tag) {
		return ""
	}
	quote := tag[j]
	if quote != '"' && quote != '\'' {
		return ""
	}
	end := strings.IndexByte(tag[j+1:], quote)
	if end == -1 {
		return ""
	}
	classValue := tag[j+1 : j+1+end]

	var scope string
	for _, className := range strings.FieldsFunc(classValue, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f' }) {
		if strings.HasPrefix(className, highlightClassPrefix) {
			scope = className[len(highlightClassPrefix):]
			break
		}
	}
	return scope
}

// getScopeFormatter resolves a scope to a formatter: exact match, then the
// dot-prefix, then the dash-prefix (pi: getScopeFormatter).
func getScopeFormatter(scope string, theme HighlightTheme) HighlightFormatter {
	if formatter, ok := theme[scope]; ok {
		return formatter
	}
	if dotIndex := strings.Index(scope, "."); dotIndex != -1 {
		if formatter, ok := theme[scope[:dotIndex]]; ok {
			return formatter
		}
	}
	if dashIndex := strings.Index(scope, "-"); dashIndex != -1 {
		if formatter, ok := theme[scope[:dashIndex]]; ok {
			return formatter
		}
	}
	return nil
}

// getActiveFormatter resolves the innermost mapped scope, falling back to
// the theme's "default" entry (pi: getActiveFormatter).
func getActiveFormatter(scopes []string, theme HighlightTheme) HighlightFormatter {
	for i := len(scopes) - 1; i >= 0; i-- {
		scope := scopes[i]
		if scope == "" {
			continue
		}
		if formatter := getScopeFormatter(scope, theme); formatter != nil {
			return formatter
		}
	}
	if theme == nil {
		return nil
	}
	return theme["default"]
}

// isSpanOpenTagStart reports whether html starts an open span tag at index
// (pi: isSpanOpenTagStart).
func isSpanOpenTagStart(htmlStr string, index int) bool {
	if !strings.HasPrefix(htmlStr[index:], "<span") {
		return false
	}
	next := index + len("<span")
	if next >= len(htmlStr) {
		return false
	}
	switch htmlStr[next] {
	case '>', ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

// RenderHighlightedHtml walks highlighter HTML output, replaces span
// nesting with the mapped theme formatters and decodes HTML entities
// (pi: renderHighlightedHtml).
func RenderHighlightedHtml(htmlStr string, theme HighlightTheme) string {
	var output strings.Builder
	var textBuffer strings.Builder
	var scopes []string

	flushText := func() {
		if textBuffer.Len() == 0 {
			return
		}
		text := textBuffer.String()
		formatter := getActiveFormatter(scopes, theme)
		if formatter != nil {
			// Format each line separately so a span whose text crosses a
			// line boundary keeps per-line color resets (pi #10356); empty
			// lines pass through unformatted (pi: line ? formatter(line) : line).
			for i, line := range strings.Split(text, "\n") {
				if i > 0 {
					output.WriteString("\n")
				}
				if line != "" {
					output.WriteString(formatter(line))
				}
			}
		} else {
			output.WriteString(text)
		}
		textBuffer.Reset()
	}

	index := 0
	for index < len(htmlStr) {
		if isSpanOpenTagStart(htmlStr, index) {
			tagEndIndex := strings.IndexByte(htmlStr[index+len("<span"):], '>')
			if tagEndIndex != -1 {
				tagEndIndex += index + len("<span")
				flushText()
				tag := htmlStr[index : tagEndIndex+1]
				scope := getScopeFromSpanTag(tag)
				scopes = append(scopes, scope)
				index = tagEndIndex + 1
				continue
			}
		}

		if strings.HasPrefix(htmlStr[index:], spanClose) {
			flushText()
			if len(scopes) > 0 {
				scopes = scopes[:len(scopes)-1]
			}
			index += len(spanClose)
			continue
		}

		if htmlStr[index] == '&' {
			if decoded := DecodeHtmlEntityAt(htmlStr, index); decoded != nil {
				textBuffer.WriteString(decoded.Text)
				index += decoded.Length
				continue
			}
		}

		r, size := utf8.DecodeRuneInString(htmlStr[index:])
		if r == utf8.RuneError && size <= 1 {
			textBuffer.WriteByte(htmlStr[index])
			index++
			continue
		}
		textBuffer.WriteString(htmlStr[index : index+size])
		index += size
	}

	flushText()
	return output.String()
}

// ============================================================================
// HTML entity decoding (pi: src/utils/html.ts)
// ============================================================================

// DecodedHtmlEntity is pi's DecodedHtmlEntity.
type DecodedHtmlEntity struct {
	Text   string
	Length int
}

func decodeCodePoint(codePoint int64) (string, bool) {
	if codePoint < 0 || codePoint > 0x10ffff {
		return "", false
	}
	r := rune(codePoint)
	if r == utf8.RuneError {
		// JS String.fromCodePoint(0xfffd) returns the replacement char
		// itself; Go's conversion also yields it. Distinguish invalid
		// surrogates: JS allows them (returns lone surrogate), Go encodes
		// them as RuneError. Accept them as U+FFFD — no pi test covers
		// lone surrogates (declared deviation).
		return string(utf8.RuneError), true
	}
	return string(r), true
}

// DecodeHtmlEntity mirrors pi's decodeHtmlEntity.
func DecodeHtmlEntity(entity string) (string, bool) {
	switch entity {
	case "amp":
		return "&", true
	case "lt":
		return "<", true
	case "gt":
		return ">", true
	case "quot":
		return "\"", true
	case "apos":
		return "'", true
	}

	if strings.HasPrefix(entity, "#x") || strings.HasPrefix(entity, "#X") {
		if v, err := strconv.ParseInt(entity[2:], 16, 64); err == nil {
			return decodeCodePoint(v)
		}
		return "", false
	}

	if strings.HasPrefix(entity, "#") {
		if v, err := strconv.ParseInt(entity[1:], 10, 64); err == nil {
			return decodeCodePoint(v)
		}
		return "", false
	}

	return "", false
}

// DecodeHtmlEntityAt mirrors pi's decodeHtmlEntityAt.
func DecodeHtmlEntityAt(htmlStr string, index int) *DecodedHtmlEntity {
	semicolonIndex := strings.IndexByte(htmlStr[index+1:], ';')
	if semicolonIndex == -1 {
		return nil
	}
	semicolonIndex += index + 1
	if semicolonIndex-index > 16 {
		return nil
	}

	entity := htmlStr[index+1 : semicolonIndex]
	decoded, ok := DecodeHtmlEntity(entity)
	if !ok {
		return nil
	}

	return &DecodedHtmlEntity{Text: decoded, Length: semicolonIndex - index + 1}
}
