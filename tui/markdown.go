// tui/markdown.go (pi: packages/tui/src/components/markdown.ts)
//
// Markdown component: parses with the marked Lexer port (marked.go /
// marked_tokenizer.go / marked_rules.go) and renders the token stream to
// styled terminal lines itself.
//
// Deferred (declared): pi imports renderLatex from ../latex.ts (~1380 lines,
// README Phase 6 "可砍/延后"). The seam below is a package-level var; the nil
// default makes every LaTeX token fall back to its raw text — the same
// rendering path pi takes for unsupported/incomplete expressions. Tests that
// need real math rendering stay skipped until latex.ts lands.
package tui

import (
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/dlclark/regexp2"
)

// =============================================================================
// Strict strikethrough (pi: StrictStrikethroughTokenizer — replaces marked's
// builtin del tokenizer entirely via setOptions)
// =============================================================================

// markedStrictDel is pi's StrictStrikethroughTokenizer.del. Uses RE2 directly:
// the pattern has a backreference — compiled via regexp2 instead.
func (t *markedTokenizer) markedStrictDel(src string) *MarkedToken {
	re := regexp2.MustCompile(`^(~~)(?=[^\s~])((?:\\.|[^\\])*?(?:\\.|[^\s~\\]))\1(?=[^~]|$)`, regexp2.ECMAScript)
	m := reExec(re, src)
	if m == nil {
		return nil
	}
	text := mGroup(m, 2)
	return &MarkedToken{Type: "del", Raw: m.String(), Text: text,
		Tokens: t.lexer.inlineTokensNow(text)}
}

// =============================================================================
// LaTeX tokenizer extensions (pi: markdown.ts LATEX_MARKDOWN_EXTENSIONS)
// =============================================================================

// renderLatexFn is the seam for pi's latex.ts renderLatex (deferred). nil =
// unsupported → the caller falls back to raw text (pi: `?? token.raw`).
var renderLatexFn func(text string, display bool) (string, bool)

var latexPendingDollarRe = regexp.MustCompile(`\\[A-Za-z]+|[_^=+*/<>()[\]|±≤≥≠≈∈→⇒∞∫∑√-]`)

func latexIsEscaped(source string, index int) bool {
	backslashes := 0
	runes := []rune(source)
	for position := index - 1; position >= 0 && runes[position] == '\\'; position-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func latexFindClosingDelimiter(source string, closing string, start int) int {
	index := strings.Index(source[start:], closing)
	if index >= 0 {
		index += start
	} else {
		return -1
	}
	for index >= 0 && latexIsEscaped(source, index) {
		next := strings.Index(source[index+len(closing):], closing)
		if next >= 0 {
			index += len(closing) + next
		} else {
			index = -1
		}
	}
	return index
}

func latexLooksLikePendingDollarMath(source string) bool {
	return latexPendingDollarRe.MatchString(source)
}

func latexTokenizeInlineLatex(source string) *MarkedToken {
	opening := ""
	closing := ""
	switch {
	case strings.HasPrefix(source, "$$"):
		opening, closing = "$$", "$$"
	case strings.HasPrefix(source, "\\("):
		opening, closing = "\\(", "\\)"
	case strings.HasPrefix(source, "\\["):
		opening, closing = "\\[", "\\]"
	case strings.HasPrefix(source, "$") && !regexp.MustCompile(`^\$\s`).MatchString(source):
		opening, closing = "$", "$"
	default:
		return nil
	}

	closingIndex := latexFindClosingDelimiter(source, closing, len(opening))
	if closingIndex >= 0 && opening == "$" {
		inner := source[len(opening):closingIndex]
		after := source[closingIndex+len(closing):]
		if regexp.MustCompile(`\s$`).MatchString(inner) ||
			regexp.MustCompile(`^\d`).MatchString(after) ||
			(regexp.MustCompile(`^[A-Z_][A-Z0-9_]*(?:[^A-Za-z0-9_\s])?$`).MatchString(inner) &&
				regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`).MatchString(after)) ||
			strings.Contains(inner, "`") {
			return nil
		}
	}

	if closingIndex < 0 {
		pendingSource := source[len(opening):]
		if strings.HasPrefix(opening, "\\") || latexLooksLikePendingDollarMath(pendingSource) {
			return &MarkedToken{Type: "latex", Raw: source, Text: pendingSource, Pending: true}
		}
		return nil
	}

	text := source[len(opening):closingIndex]
	if text == "" || strings.Contains(text, "\n") {
		return nil
	}

	raw := source[:closingIndex+len(closing)]
	return &MarkedToken{Type: "latex", Raw: raw, Text: text}
}

func latexTokenizeBlockLatex(source string) *MarkedToken {
	dollarRe := regexp.MustCompile(`(?s)^ {0,3}\$\$[ \t]*(?:\n)?(.*?)\$\$[ \t]*(?:\n|$)`)
	if m := dollarRe.FindStringSubmatch(source); m != nil && m[1] != "" {
		return &MarkedToken{Type: "latexBlock", Raw: m[0], Text: strings.TrimSpace(m[1])}
	}

	if m := regexp.MustCompile(`(?s)^ {0,3}\\\[[ \t]*(?:\n)?([\s\S]*?)\\\][ \t]*(?:\n|$)`).FindStringSubmatch(source); m != nil && m[1] != "" {
		return &MarkedToken{Type: "latexBlock", Raw: m[0], Text: strings.TrimSpace(m[1])}
	}

	if m := regexp.MustCompile(`(?s)^ {0,3}\\\[[ \t]*(?:\n)?([\s\S]*)$`).FindStringSubmatch(source); m != nil {
		return &MarkedToken{Type: "latexBlock", Raw: m[0], Text: m[1], Pending: true}
	}
	if m := regexp.MustCompile(`(?s)^ {0,3}\$\$[ \t]*(?:\n)?([\s\S]*)$`).FindStringSubmatch(source); m != nil && m[1] != "" && latexLooksLikePendingDollarMath(m[1]) {
		return &MarkedToken{Type: "latexBlock", Raw: m[0], Text: m[1], Pending: true}
	}
	return nil
}

// newMarkdownExtensions builds pi's LATEX_MARKDOWN_EXTENSIONS wired into the
// marked extension slots (tokenizers tried before builtins; start hints clip
// cutSrc so paragraphs don't swallow pending latex).
func newMarkdownExtensions() *markedExtensions {
	return &markedExtensions{
		Block: []func(src string, tokens *[]*MarkedToken) *MarkedToken{
			func(src string, tokens *[]*MarkedToken) *MarkedToken {
				return latexTokenizeBlockLatex(src)
			},
		},
		Inline: []func(src string, tokens *[]*MarkedToken) *MarkedToken{
			func(src string, tokens *[]*MarkedToken) *MarkedToken {
				return latexTokenizeInlineLatex(src)
			},
		},
		StartBlock: []func(src string) int{
			func(src string) int {
				m := regexp.MustCompile(`(?:^|\n) {0,3}(?:\$\$|\\\[)`).FindStringIndex(src)
				if m == nil {
					return -1
				}
				// FindStringIndex returns a BYTE index; the consumer (marked's
				// cutSrc clipping, jsSlice) works in runes — pi's UTF-16 index is
				// rune-equivalent here, so convert (fixes CJK before $$ \\[ in a
				// paragraph swallowing the math block).
				index := utf8RuneCount(src[:m[0]])
				if strings.HasPrefix(src[m[0]:], "\n") {
					return index + 1
				}
				return index
			},
		},
		StartInline: []func(src string) int{
			func(src string) int {
				indices := []int{}
				for _, needle := range []string{"$", "\\(", "\\["} {
					if i := strings.Index(src, needle); i >= 0 {
						// Byte → rune index (see StartBlock note): without the
						// conversion the inlineText candidate over-extends past
						// the delimiter whenever multi-byte text (CJK) precedes
						// it, and inline math after CJK never tokenizes.
						indices = append(indices, utf8RuneCount(src[:i]))
					}
				}
				if len(indices) == 0 {
					return -1
				}
				minIdx := indices[0]
				for _, i := range indices[1:] {
					if i < minIdx {
						minIdx = i
					}
				}
				return minIdx
			},
		},
	}
}

// markdownLexer is pi's module-level markdownParser (Marked instance with
// strict strikethrough tokenizer + latex extensions; gfm defaults).
func markdownLexer() *markedLexer {
	lexer := newMarkedLexer(markedLexerOptions{Extensions: newMarkdownExtensions()})
	// pi: setOptions({tokenizer: new StrictStrikethroughTokenizer()}) — the
	// strict del replaces the builtin one. marked dispatches tokenizer calls
	// through the options.tokenizer object; pig routes del at the call site
	// (Lexer.inlineTokens calls markedStrictDel instead of builtin del).
	return lexer
}

// trimPartialClosingFences trims streamed partial closing fences so code
// blocks do not shrink/flicker when the final fence character arrives
// (pi: trimPartialClosingFences; see pi issue #5825).
func trimPartialClosingFences(tokens []*MarkedToken) {
	if len(tokens) == 0 {
		return
	}
	token := tokens[len(tokens)-1]
	switch token.Type {
	case "list":
		var items []*MarkedToken
		if token.Items != nil {
			items = token.Items
		}
		var lastTokens []*MarkedToken
		if len(items) > 0 {
			lastTokens = items[len(items)-1].Tokens
		}
		trimPartialClosingFences(lastTokens)
		return
	case "blockquote":
		trimPartialClosingFences(token.Tokens)
		return
	case "code":
	default:
		return
	}

	markerRe := regexp.MustCompile("^(`{3,}|~{3,})")
	marker := ""
	if m := markerRe.FindStringSubmatch(token.Raw); m != nil {
		marker = m[1]
	}
	lines := strings.Split(token.Raw, "\n")
	lastLine := lines[len(lines)-1]
	if marker == "" || lastLine == "" || len(lastLine) >= len(marker) || lastLine != strings.Repeat(marker[:1], len(lastLine)) {
		return
	}

	token.Text = strings.TrimSuffix(strings.TrimSuffix(token.Text, lastLine), "\n")
}

// =============================================================================
// Component
// =============================================================================

// DefaultTextStyle is the base styling applied to all text unless overridden
// by markdown formatting (pi: DefaultTextStyle).
type DefaultTextStyle struct {
	// Foreground color function
	Color func(text string) string
	// Background color function
	BgColor func(text string) string
	// Bold text
	Bold bool
	// Italic text
	Italic bool
	// Strikethrough text
	Strikethrough bool
	// Underline text
	Underline bool
}

// MarkdownTheme holds theme functions for markdown elements. Each function
// takes text and returns styled text with ANSI codes (pi: MarkdownTheme).
type MarkdownTheme struct {
	Heading         func(text string) string
	Link            func(text string) string
	LinkURL         func(text string) string
	Code            func(text string) string
	CodeBlock       func(text string) string
	CodeBlockBorder func(text string) string
	Quote           func(text string) string
	QuoteBorder     func(text string) string
	Hr              func(text string) string
	ListBullet      func(text string) string
	Bold            func(text string) string
	Italic          func(text string) string
	Strikethrough   func(text string) string
	Underline       func(text string) string
	HighlightCode   func(code string, lang string) []string
	// CodeBlockIndent is the prefix applied to each rendered code block line
	// (default "  ").
	CodeBlockIndent string
}

// MarkdownOptions mirrors pi's MarkdownOptions.
type MarkdownOptions struct {
	// Preserve source list markers instead of normalizing them.
	PreserveOrderedListMarkers bool
	// Preserve source backslash escapes instead of normalizing escaped
	// punctuation.
	PreserveBackslashEscapes bool
	// Transform source Markdown before parsing, with the exact width
	// available for content.
	Transform func(markdown string, availableWidth int) string
	// RenderLatex renders supported LaTeX math expressions as Unicode text
	// (default true; nil = default).
	RenderLatex *bool
}

// inlineStyleContext is pi's InlineStyleContext.
type inlineStyleContext struct {
	applyText   func(text string) string
	stylePrefix string
}

// Markdown is the markdown component (pi: class Markdown implements
// Component).
type Markdown struct {
	mu                    sync.Mutex
	text                  string
	paddingX              int // Left/right padding
	paddingY              int // Top/bottom padding
	defaultTextStyle      *DefaultTextStyle
	theme                 MarkdownTheme
	options               MarkdownOptions
	defaultStylePrefixSet bool
	defaultStylePrefix    string

	// Cache for rendered output
	cachedText     string
	cachedTextSet  bool
	cachedWidth    int
	cachedWidthSet bool
	cachedLines    []string
}

// NewMarkdown mirrors pi's constructor (defaultTextStyle/options may be nil).
func NewMarkdown(text string, paddingX int, paddingY int, theme MarkdownTheme, defaultTextStyle *DefaultTextStyle, options *MarkdownOptions) *Markdown {
	m := &Markdown{
		text:             text,
		paddingX:         paddingX,
		paddingY:         paddingY,
		defaultTextStyle: defaultTextStyle,
		theme:            theme,
	}
	if options != nil {
		m.options = *options
	}
	return m
}

func (m *Markdown) SetText(text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.text = text
	m.invalidateLocked()
}

func (m *Markdown) Invalidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invalidateLocked()
}

func (m *Markdown) invalidateLocked() {
	m.cachedTextSet = false
	m.cachedWidthSet = false
	m.cachedLines = nil
}

func (m *Markdown) Render(width int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Check cache
	if m.cachedLines != nil && m.cachedTextSet && m.cachedText == m.text && m.cachedWidthSet && m.cachedWidth == width {
		return m.cachedLines
	}

	// Calculate available width for content (subtract horizontal padding)
	contentWidth := max(1, width-m.paddingX*2)
	text := m.text
	if m.options.Transform != nil {
		text = m.options.Transform(m.text, contentWidth)
	}

	// Don't render anything if there's no actual text
	if text == "" || strings.TrimSpace(text) == "" {
		m.cachedText = m.text
		m.cachedTextSet = true
		m.cachedWidth = width
		m.cachedWidthSet = true
		m.cachedLines = []string{}
		return m.cachedLines
	}

	// Replace tabs with 3 spaces for consistent rendering
	normalizedText := strings.ReplaceAll(text, "\t", "   ")

	// Parse markdown to tokens
	lexer := markdownLexer()
	tokens := lexer.Lex(normalizedText)
	trimPartialClosingFences(tokens)

	// Convert tokens to styled terminal output
	renderedLines := []string{}
	for i, token := range tokens {
		nextTokenType := ""
		if i+1 < len(tokens) {
			nextTokenType = tokens[i+1].Type
		}
		renderedLines = append(renderedLines, m.renderToken(token, contentWidth, nextTokenType, nil)...)
	}

	// Wrap lines (NO padding, NO background yet)
	wrappedLines := []string{}
	for _, line := range renderedLines {
		if IsImageLine(line) {
			wrappedLines = append(wrappedLines, line)
			continue
		}
		wrappedLines = append(wrappedLines, WrapTextWithAnsi(line, contentWidth)...)
	}

	// Add margins and background to each wrapped line
	leftMargin := strings.Repeat(" ", m.paddingX)
	rightMargin := strings.Repeat(" ", m.paddingX)
	bgFn := (*func(string) string)(nil)
	if m.defaultTextStyle != nil && m.defaultTextStyle.BgColor != nil {
		fn := m.defaultTextStyle.BgColor
		bgFn = &fn
	}
	contentLines := []string{}
	for _, line := range wrappedLines {
		if IsImageLine(line) {
			contentLines = append(contentLines, line)
			continue
		}
		lineWithMargins := leftMargin + line + rightMargin
		if bgFn != nil {
			contentLines = append(contentLines, ApplyBackgroundToLine(lineWithMargins, width, *bgFn))
		} else {
			// No background - just pad to width
			visibleLen := VisibleWidth(lineWithMargins)
			paddingNeeded := max(0, width-visibleLen)
			contentLines = append(contentLines, lineWithMargins+strings.Repeat(" ", paddingNeeded))
		}
	}

	// Add top/bottom padding (empty lines)
	emptyLine := strings.Repeat(" ", width)
	emptyLines := []string{}
	for i := 0; i < m.paddingY; i++ {
		line := emptyLine
		if bgFn != nil {
			line = ApplyBackgroundToLine(emptyLine, width, *bgFn)
		}
		emptyLines = append(emptyLines, line)
	}

	// Combine top padding, content, and bottom padding
	result := append(append(append([]string{}, emptyLines...), contentLines...), emptyLines...)

	// Update cache
	m.cachedText = m.text
	m.cachedTextSet = true
	m.cachedWidth = width
	m.cachedWidthSet = true
	m.cachedLines = result

	if len(result) > 0 {
		return result
	}
	return []string{""}
}

// applyDefaultStyle applies the default text style to a string. Background
// color is NOT applied here — it's applied at the padding stage to ensure it
// extends to the full line width.
func (m *Markdown) applyDefaultStyle(text string) string {
	if m.defaultTextStyle == nil {
		return text
	}

	styled := text

	// Apply foreground color (NOT background - that's applied at padding stage)
	if m.defaultTextStyle.Color != nil {
		styled = m.defaultTextStyle.Color(styled)
	}

	// Apply text decorations using this.theme
	if m.defaultTextStyle.Bold {
		styled = m.theme.Bold(styled)
	}
	if m.defaultTextStyle.Italic {
		styled = m.theme.Italic(styled)
	}
	if m.defaultTextStyle.Strikethrough {
		styled = m.theme.Strikethrough(styled)
	}
	if m.defaultTextStyle.Underline {
		styled = m.theme.Underline(styled)
	}

	return styled
}

func (m *Markdown) getDefaultStylePrefix() string {
	if m.defaultTextStyle == nil {
		return ""
	}
	if m.defaultStylePrefixSet {
		return m.defaultStylePrefix
	}

	const sentinel = "\x00"
	styled := sentinel
	if m.defaultTextStyle.Color != nil {
		styled = m.defaultTextStyle.Color(styled)
	}
	if m.defaultTextStyle.Bold {
		styled = m.theme.Bold(styled)
	}
	if m.defaultTextStyle.Italic {
		styled = m.theme.Italic(styled)
	}
	if m.defaultTextStyle.Strikethrough {
		styled = m.theme.Strikethrough(styled)
	}
	if m.defaultTextStyle.Underline {
		styled = m.theme.Underline(styled)
	}

	sentinelIndex := strings.Index(styled, sentinel)
	if sentinelIndex >= 0 {
		m.defaultStylePrefix = styled[:sentinelIndex]
	} else {
		m.defaultStylePrefix = ""
	}
	m.defaultStylePrefixSet = true
	return m.defaultStylePrefix
}

func (m *Markdown) getStylePrefix(styleFn func(text string) string) string {
	const sentinel = "\x00"
	styled := styleFn(sentinel)
	sentinelIndex := strings.Index(styled, sentinel)
	if sentinelIndex >= 0 {
		return styled[:sentinelIndex]
	}
	return ""
}

func (m *Markdown) getDefaultInlineStyleContext() inlineStyleContext {
	return inlineStyleContext{
		applyText:   func(text string) string { return m.applyDefaultStyle(text) },
		stylePrefix: m.getDefaultStylePrefix(),
	}
}

func (m *Markdown) renderToken(token *MarkedToken, width int, nextTokenType string, styleContext *inlineStyleContext) []string {
	lines := []string{}

	switch token.Type {
	case "heading":
		headingLevel := token.Depth
		headingPrefix := strings.Repeat("#", headingLevel) + " "

		// Build a heading-specific style context so inline tokens (codespan,
		// bold, etc.) restore heading styling after their own ANSI resets
		// instead of falling back to the default text style.
		var headingStyleFn func(text string) string
		if headingLevel == 1 {
			headingStyleFn = func(text string) string { return m.theme.Heading(m.theme.Bold(m.theme.Underline(text))) }
		} else {
			headingStyleFn = func(text string) string { return m.theme.Heading(m.theme.Bold(text)) }
		}

		headingStyleContext := inlineStyleContext{
			applyText:   headingStyleFn,
			stylePrefix: m.getStylePrefix(headingStyleFn),
		}

		if token.Tokens == nil {
			token.Tokens = []*MarkedToken{}
		}
		headingText := m.renderInlineTokens(token.Tokens, &headingStyleContext)
		if headingLevel >= 3 {
			headingText = headingStyleFn(headingPrefix) + headingText
		}
		lines = append(lines, headingText)
		if nextTokenType != "" && nextTokenType != "space" {
			lines = append(lines, "") // Add spacing after headings (unless space token follows)
		}

	case "paragraph":
		if token.Tokens == nil {
			token.Tokens = []*MarkedToken{}
		}
		paragraphText := m.renderInlineTokens(token.Tokens, styleContext)
		lines = append(lines, paragraphText)
		// Don't add spacing if next token is space or list
		if nextTokenType != "" && nextTokenType != "list" && nextTokenType != "space" {
			lines = append(lines, "")
		}

	case "text":
		lines = append(lines, m.renderInlineTokens([]*MarkedToken{token}, styleContext))

	case "latexBlock":
		var rendered string
		if !token.Pending && (m.options.RenderLatex == nil || *m.options.RenderLatex) && renderLatexFn != nil {
			if r, ok := renderLatexFn(token.Text, true); ok {
				rendered = r
			} else {
				rendered = strings.TrimSpace(token.Raw)
			}
		} else {
			rendered = strings.TrimSpace(token.Raw)
		}
		for _, line := range strings.Split(rendered, "\n") {
			lines = append(lines, m.applyDefaultStyle(line))
		}
		if nextTokenType != "" && nextTokenType != "space" {
			lines = append(lines, "")
		}

	case "code":
		indent := m.theme.CodeBlockIndent
		if indent == "" {
			indent = "  "
		}
		lines = append(lines, m.theme.CodeBlockBorder("```"+token.Lang))
		if m.theme.HighlightCode != nil {
			for _, hlLine := range m.theme.HighlightCode(token.Text, token.Lang) {
				lines = append(lines, indent+hlLine)
			}
		} else {
			// Split code by newlines and style each line
			for _, codeLine := range strings.Split(token.Text, "\n") {
				lines = append(lines, indent+m.theme.CodeBlock(codeLine))
			}
		}
		lines = append(lines, m.theme.CodeBlockBorder("```"))
		if nextTokenType != "" && nextTokenType != "space" {
			lines = append(lines, "") // Add spacing after code blocks (unless space token follows)
		}

	case "list":
		listLines := m.renderList(token, 0, width, styleContext)
		lines = append(lines, listLines...)
		// Don't add spacing after lists if a space token follows
		// (the space token will handle it)

	case "table":
		tableLines := m.renderTable(token, width, nextTokenType, styleContext)
		lines = append(lines, tableLines...)

	case "blockquote":
		quoteStyle := func(text string) string { return m.theme.Quote(m.theme.Italic(text)) }
		quoteStylePrefix := m.getStylePrefix(quoteStyle)
		applyQuoteStyle := func(line string) string {
			if quoteStylePrefix == "" {
				return quoteStyle(line)
			}
			lineWithReappliedStyle := strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+quoteStylePrefix)
			return quoteStyle(lineWithReappliedStyle)
		}

		// Calculate available width for quote content (subtract border "│ " = 2 chars)
		quoteContentWidth := max(1, width-2)

		// Blockquotes contain block-level tokens, so render children with
		// renderToken(). Default message style should not apply inside
		// blockquotes.
		quoteInlineStyleContext := inlineStyleContext{
			applyText:   func(text string) string { return text },
			stylePrefix: quoteStylePrefix,
		}
		quoteTokens := token.Tokens
		if quoteTokens == nil {
			quoteTokens = []*MarkedToken{}
		}
		renderedQuoteLines := []string{}
		for i, quoteToken := range quoteTokens {
			nextQuoteTokenType := ""
			if i+1 < len(quoteTokens) {
				nextQuoteTokenType = quoteTokens[i+1].Type
			}
			renderedQuoteLines = append(renderedQuoteLines,
				m.renderToken(quoteToken, quoteContentWidth, nextQuoteTokenType, &quoteInlineStyleContext)...)
		}

		// Avoid rendering an extra empty quote line before the outer
		// blockquote spacing.
		for len(renderedQuoteLines) > 0 && renderedQuoteLines[len(renderedQuoteLines)-1] == "" {
			renderedQuoteLines = renderedQuoteLines[:len(renderedQuoteLines)-1]
		}

		for _, quoteLine := range renderedQuoteLines {
			styledLine := applyQuoteStyle(quoteLine)
			for _, wrappedLine := range WrapTextWithAnsi(styledLine, quoteContentWidth) {
				lines = append(lines, m.theme.QuoteBorder("│ ")+wrappedLine)
			}
		}
		if nextTokenType != "" && nextTokenType != "space" {
			lines = append(lines, "") // Add spacing after blockquotes (unless space token follows)
		}

	case "hr":
		lines = append(lines, m.theme.Hr(strings.Repeat("─", min(width, 80))))
		if nextTokenType != "" && nextTokenType != "space" {
			lines = append(lines, "") // Add spacing after horizontal rules (unless space token follows)
		}

	case "html":
		// Render HTML as plain text (block form)
		lines = append(lines, m.applyDefaultStyle(strings.TrimSpace(token.Raw)))

	case "space":
		// Space tokens represent blank lines in markdown
		lines = append(lines, "")

	default:
		// Handle any other token types as plain text
		if token.HasTextProperty() {
			lines = append(lines, token.Text)
		}
	}

	return lines
}

func (m *Markdown) renderInlineTokens(tokens []*MarkedToken, styleContext *inlineStyleContext) string {
	result := ""
	resolved := styleContext
	if resolved == nil {
		ctx := m.getDefaultInlineStyleContext()
		resolved = &ctx
	}
	applyText, stylePrefix := resolved.applyText, resolved.stylePrefix
	applyTextWithNewlines := func(text string) string {
		segments := strings.Split(text, "\n")
		for i, segment := range segments {
			segments[i] = applyText(segment)
		}
		return strings.Join(segments, "\n")
	}

	for _, token := range tokens {
		switch token.Type {
		case "latex":
			rendered := token.Raw
			if !token.Pending && (m.options.RenderLatex == nil || *m.options.RenderLatex) && renderLatexFn != nil {
				if r, ok := renderLatexFn(token.Text, false); ok {
					rendered = r
				}
			}
			result += applyTextWithNewlines(rendered)

		case "escape":
			if m.options.PreserveBackslashEscapes {
				result += applyTextWithNewlines(token.Raw)
			} else {
				result += applyTextWithNewlines(token.Text)
			}

		case "text":
			// Text tokens in list items can have nested tokens for inline formatting
			if token.Tokens != nil && len(token.Tokens) > 0 {
				result += m.renderInlineTokens(token.Tokens, resolved)
			} else {
				result += applyTextWithNewlines(token.Text)
			}

		case "paragraph":
			// Paragraph tokens contain nested inline tokens
			if token.Tokens == nil {
				token.Tokens = []*MarkedToken{}
			}
			result += m.renderInlineTokens(token.Tokens, resolved)

		case "strong":
			if token.Tokens == nil {
				token.Tokens = []*MarkedToken{}
			}
			boldContent := m.renderInlineTokens(token.Tokens, resolved)
			result += m.theme.Bold(boldContent) + stylePrefix

		case "em":
			if token.Tokens == nil {
				token.Tokens = []*MarkedToken{}
			}
			italicContent := m.renderInlineTokens(token.Tokens, resolved)
			result += m.theme.Italic(italicContent) + stylePrefix

		case "codespan":
			result += m.theme.Code(token.Text) + stylePrefix

		case "link":
			if token.Tokens == nil {
				token.Tokens = []*MarkedToken{}
			}
			linkText := m.renderInlineTokens(token.Tokens, resolved)
			styledLink := m.theme.Link(m.theme.Underline(linkText))
			if GetCapabilities().Hyperlinks {
				// OSC 8: render as a clickable hyperlink. The URL is not
				// printed inline, so we always show only the link text
				// regardless of whether it matches href.
				result += Hyperlink(styledLink, token.Href) + stylePrefix
			} else {
				// Fallback: print URL in parentheses when text differs from
				// href. Compare raw token.text (not styled) against href for
				// the equality check. For mailto: links strip the prefix
				// (autolinked emails use text="foo@bar.com" but
				// href="mailto:foo@bar.com").
				hrefForComparison := token.Href
				if strings.HasPrefix(token.Href, "mailto:") {
					hrefForComparison = token.Href[7:]
				}
				if token.Text == token.Href || token.Text == hrefForComparison {
					result += styledLink + stylePrefix
				} else {
					result += styledLink + m.theme.LinkURL(" ("+token.Href+")") + stylePrefix
				}
			}

		case "br":
			result += "\n"

		case "del":
			if token.Tokens == nil {
				token.Tokens = []*MarkedToken{}
			}
			delContent := m.renderInlineTokens(token.Tokens, resolved)
			result += m.theme.Strikethrough(delContent) + stylePrefix

		case "html":
			// Render inline HTML as plain text
			result += applyTextWithNewlines(token.Raw)

		default:
			// Handle any other inline token types as plain text
			if token.HasTextProperty() {
				result += applyTextWithNewlines(token.Text)
			}
		}
	}

	for stylePrefix != "" && strings.HasSuffix(result, stylePrefix) {
		result = result[:len(result)-len(stylePrefix)]
	}

	return result
}

func (m *Markdown) getOrderedListMarker(item *MarkedToken) string {
	match := regexp.MustCompile(`^(?: {0,3})(\d{1,9}[.)])[ \t]+`).FindStringSubmatch(item.Raw)
	if match == nil {
		return ""
	}
	return match[1] + " "
}

// getUnorderedListMarker keeps pi's lookahead form, so it compiles with
// regexp2 (RE2 has no lookahead).
func (m *Markdown) getUnorderedListMarker(item *MarkedToken) string {
	re := regexp2.MustCompile(`^(?: {0,3})([-+*])(?:[ \t]+|(?=\r?\n|$))`, regexp2.ECMAScript)
	match := reExec(re, item.Raw)
	if match == nil {
		return ""
	}
	return mGroup(match, 1) + " "
}

// renderList renders a list with proper nesting support.
func (m *Markdown) renderList(token *MarkedToken, depth int, width int, styleContext *inlineStyleContext) []string {
	lines := []string{}
	indent := strings.Repeat("    ", depth)
	// Use the list's start property (defaults to 1 for ordered lists)
	startNumber := 1
	if n, ok := token.StartNumber(); ok {
		startNumber = n
	}

	for i, item := range token.Items {
		isLastItem := i == len(token.Items)-1
		var bullet string
		if token.Ordered {
			if m.options.PreserveOrderedListMarkers {
				bullet = m.getOrderedListMarker(item)
				if bullet == "" {
					bullet = strconv.Itoa(startNumber+i) + ". "
				}
			} else {
				bullet = strconv.Itoa(startNumber+i) + ". "
			}
		} else {
			if m.options.PreserveOrderedListMarkers {
				bullet = m.getUnorderedListMarker(item)
				if bullet == "" {
					bullet = "- "
				}
			} else {
				bullet = "- "
			}
		}
		taskMarker := ""
		if item.Task {
			if item.Checked {
				taskMarker = "[x] "
			} else {
				taskMarker = "[ ] "
			}
		}
		marker := bullet + taskMarker
		firstPrefix := indent + m.theme.ListBullet(marker)
		continuationPrefix := indent + strings.Repeat(" ", VisibleWidth(marker))
		itemWidth := max(1, width-VisibleWidth(firstPrefix))
		renderedAnyLine := false

		if item.Tokens == nil {
			item.Tokens = []*MarkedToken{}
		}
		for _, itemToken := range item.Tokens {
			if itemToken.Type == "list" {
				lines = append(lines, m.renderList(itemToken, depth+1, width, styleContext)...)
				renderedAnyLine = true
				continue
			}

			itemLines := m.renderToken(itemToken, itemWidth, "", styleContext)
			for _, line := range itemLines {
				for _, wrappedLine := range WrapTextWithAnsi(line, itemWidth) {
					linePrefix := continuationPrefix
					if !renderedAnyLine {
						linePrefix = firstPrefix
					}
					lines = append(lines, linePrefix+wrappedLine)
					renderedAnyLine = true
				}
			}
		}

		if !renderedAnyLine {
			lines = append(lines, firstPrefix)
		}

		if token.Loose && !isLastItem {
			lines = append(lines, "")
		}
	}

	return lines
}

// getLongestWordWidth returns the visible width of the longest word in a
// string, clamped to maxWidth when provided.
func (m *Markdown) getLongestWordWidth(text string, maxWidth *int) int {
	words := []string{}
	for _, word := range jsWhitespaceSplit(text) {
		if word != "" {
			words = append(words, word)
		}
	}
	longest := 0
	for _, word := range words {
		longest = max(longest, VisibleWidth(word))
	}
	if maxWidth == nil {
		return longest
	}
	return min(longest, *maxWidth)
}

// wrapCellText wraps a table cell to fit into a column, delegating to
// WrapTextWithAnsi so ANSI codes + long tokens are handled consistently with
// the rest of the renderer.
func (m *Markdown) wrapCellText(text string, maxWidth int, stylePrefix string) []string {
	lines := WrapTextWithAnsi(text, max(1, maxWidth))
	out := make([]string, len(lines))
	for index, line := range lines {
		// Reset text styles after each non-final fragment, then restore the
		// surrounding style before padding and borders.
		styleReset := ""
		if index < len(lines)-1 {
			styleReset = "\x1b[22;23;24;25;27;28;29;39m"
		}
		out[index] = line + styleReset + stylePrefix
	}
	return out
}

// renderTable renders a table with width-aware cell wrapping. Cells that
// don't fit are wrapped to multiple lines.
func (m *Markdown) renderTable(token *MarkedToken, availableWidth int, nextTokenType string, styleContext *inlineStyleContext) []string {
	lines := []string{}
	numCols := len(token.HeaderCells)

	if numCols == 0 {
		return lines
	}

	// Calculate border overhead: "│ " + (n-1) * " │ " + " │" = 3n + 1
	borderOverhead := 3*numCols + 1
	availableForCells := availableWidth - borderOverhead
	if availableForCells < numCols {
		// Too narrow to render a stable table. Fall back to raw markdown.
		fallbackLines := []string{}
		if token.Raw != "" {
			fallbackLines = WrapTextWithAnsi(token.Raw, availableWidth)
		}
		if nextTokenType != "" && nextTokenType != "space" {
			fallbackLines = append(fallbackLines, "")
		}
		return fallbackLines
	}

	maxUnbrokenWordWidth := 30

	// Calculate natural column widths (what each column needs without constraints)
	naturalWidths := make([]int, numCols)
	minWordWidths := make([]int, numCols)
	for i := 0; i < numCols; i++ {
		cell := token.HeaderCells[i]
		if cell.Tokens == nil {
			cell.Tokens = []*MarkedToken{}
		}
		headerText := m.renderInlineTokens(cell.Tokens, styleContext)
		naturalWidths[i] = VisibleWidth(headerText)
		minWordWidths[i] = max(1, m.getLongestWordWidth(headerText, &maxUnbrokenWordWidth))
	}
	for _, row := range token.Rows {
		for i := 0; i < len(row) && i < numCols; i++ {
			cell := row[i]
			if cell.Tokens == nil {
				cell.Tokens = []*MarkedToken{}
			}
			cellText := m.renderInlineTokens(cell.Tokens, styleContext)
			naturalWidths[i] = max(naturalWidths[i], VisibleWidth(cellText))
			minWordWidths[i] = max(minWordWidths[i], m.getLongestWordWidth(cellText, &maxUnbrokenWordWidth))
		}
	}

	minColumnWidths := append([]int{}, minWordWidths...)
	minCellsWidth := 0
	for _, w := range minColumnWidths {
		minCellsWidth += w
	}

	if minCellsWidth > availableForCells {
		minColumnWidths = make([]int, numCols)
		for i := range minColumnWidths {
			minColumnWidths[i] = 1
		}
		remaining := availableForCells - numCols

		if remaining > 0 {
			totalWeight := 0
			for _, width := range minWordWidths {
				totalWeight += max(0, width-1)
			}
			growth := make([]int, numCols)
			for i, width := range minWordWidths {
				weight := max(0, width-1)
				if totalWeight > 0 {
					growth[i] = weight * remaining / totalWeight
				}
			}

			allocated := 0
			for i := range minColumnWidths {
				minColumnWidths[i] += growth[i]
				allocated += growth[i]
			}
			leftover := remaining - allocated
			for i := 0; leftover > 0 && i < numCols; i++ {
				minColumnWidths[i]++
				leftover--
			}
		}

		minCellsWidth = 0
		for _, w := range minColumnWidths {
			minCellsWidth += w
		}
	}

	// Calculate column widths that fit within available width
	totalNaturalWidth := borderOverhead
	for _, w := range naturalWidths {
		totalNaturalWidth += w
	}
	var columnWidths []int

	if totalNaturalWidth <= availableWidth {
		// Everything fits naturally
		columnWidths = make([]int, numCols)
		for i := range columnWidths {
			columnWidths[i] = max(naturalWidths[i], minColumnWidths[i])
		}
	} else {
		// Need to shrink columns to fit
		totalGrowPotential := 0
		for i := range naturalWidths {
			totalGrowPotential += max(0, naturalWidths[i]-minColumnWidths[i])
		}
		extraWidth := max(0, availableForCells-minCellsWidth)
		columnWidths = make([]int, numCols)
		for i := range columnWidths {
			minWidthDelta := max(0, naturalWidths[i]-minColumnWidths[i])
			grow := 0
			if totalGrowPotential > 0 {
				grow = minWidthDelta * extraWidth / totalGrowPotential
			}
			columnWidths[i] = minColumnWidths[i] + grow
		}

		// Adjust for rounding errors - distribute remaining space
		allocated := 0
		for _, w := range columnWidths {
			allocated += w
		}
		remaining := availableForCells - allocated
		for remaining > 0 {
			grew := false
			for i := 0; i < numCols && remaining > 0; i++ {
				if columnWidths[i] < naturalWidths[i] {
					columnWidths[i]++
					remaining--
					grew = true
				}
			}
			if !grew {
				break
			}
		}
	}

	// Render top border
	topBorderCells := make([]string, numCols)
	for i, w := range columnWidths {
		topBorderCells[i] = strings.Repeat("─", w)
	}
	lines = append(lines, "┌─"+strings.Join(topBorderCells, "─┬─")+"─┐")

	// Render header with wrapping
	headerCellLines := make([][]string, numCols)
	for i, cell := range token.HeaderCells {
		if cell.Tokens == nil {
			cell.Tokens = []*MarkedToken{}
		}
		text := m.renderInlineTokens(cell.Tokens, styleContext)
		prefix := ""
		if styleContext != nil {
			prefix = styleContext.stylePrefix
		}
		headerCellLines[i] = m.wrapCellText(text, columnWidths[i], prefix)
	}
	headerLineCount := 0
	for _, c := range headerCellLines {
		headerLineCount = max(headerLineCount, len(c))
	}

	for lineIdx := 0; lineIdx < headerLineCount; lineIdx++ {
		rowParts := make([]string, numCols)
		for colIdx, cellLines := range headerCellLines {
			text := ""
			if lineIdx < len(cellLines) {
				text = cellLines[lineIdx]
			}
			padded := text + strings.Repeat(" ", max(0, columnWidths[colIdx]-VisibleWidth(text)))
			rowParts[colIdx] = m.theme.Bold(padded)
		}
		lines = append(lines, "│ "+strings.Join(rowParts, " │ ")+" │")
	}

	// Render separator
	separatorCells := make([]string, numCols)
	for i, w := range columnWidths {
		separatorCells[i] = strings.Repeat("─", w)
	}
	// pi: `├─${separatorCells.join("─┼─")}─┤` — note the ─ before ┤; dropping
	// it makes the separator one column narrower than the top/bottom borders
	// (misaligned right edge with CJK column content, 2026-09-11).
	separatorLine := "├─" + strings.Join(separatorCells, "─┼─") + "─┤"
	lines = append(lines, separatorLine)

	// Render rows with wrapping
	for rowIndex, row := range token.Rows {
		rowCellLines := make([][]string, len(row))
		for i, cell := range row {
			if cell.Tokens == nil {
				cell.Tokens = []*MarkedToken{}
			}
			text := m.renderInlineTokens(cell.Tokens, styleContext)
			prefix := ""
			if styleContext != nil {
				prefix = styleContext.stylePrefix
			}
			rowCellLines[i] = m.wrapCellText(text, columnWidths[i], prefix)
		}
		rowLineCount := 0
		for _, c := range rowCellLines {
			rowLineCount = max(rowLineCount, len(c))
		}

		for lineIdx := 0; lineIdx < rowLineCount; lineIdx++ {
			rowParts := make([]string, len(rowCellLines))
			for colIdx, cellLines := range rowCellLines {
				text := ""
				if lineIdx < len(cellLines) {
					text = cellLines[lineIdx]
				}
				rowParts[colIdx] = text + strings.Repeat(" ", max(0, columnWidths[colIdx]-VisibleWidth(text)))
			}
			lines = append(lines, "│ "+strings.Join(rowParts, " │ ")+" │")
		}

		if rowIndex < len(token.Rows)-1 {
			lines = append(lines, separatorLine)
		}
	}

	// Render bottom border
	bottomBorderCells := make([]string, numCols)
	for i, w := range columnWidths {
		bottomBorderCells[i] = strings.Repeat("─", w)
	}
	lines = append(lines, "└─"+strings.Join(bottomBorderCells, "─┴─")+"─┘")

	if nextTokenType != "" && nextTokenType != "space" {
		lines = append(lines, "") // Add spacing after table
	}
	return lines
}

// jsWhitespaceSplit splits on runs of ECMAScript whitespace (pi:
// text.split(/\s+/)).
var jsWhitespaceSplitRe = regexp.MustCompile(`[\s]+`)

func jsWhitespaceSplit(text string) []string {
	return jsWhitespaceSplitRe.Split(text, -1)
}
