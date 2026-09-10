// tui/marked.go (pi dependency: npm marked@18.0.5 — src/Lexer.js subset)
//
// pi's Markdown component consumes `marked`'s Lexer only (it renders the
// token stream itself), so pig ports the Lexer: token model, block/inline
// tokenizers, and the lex loop. The rule tables live in marked_rules.go
// (generated from node dumps of the final composed regex sources).
//
// Porting notes (declared deviations):
//   - JS strings index by UTF-16 code units; pig operates in rune space
//     (helpers below). Identical for BMP characters; astral characters
//     (emoji) shift absolute offsets — no pi test covers emoji in
//     markdown, and token boundaries remain correct.
//   - JS `g`-flag regexes carry stateful lastIndex; pig passes explicit
//     start positions (reExecFrom / scan loops).
//   - normal/pedantic rule variants are not ported (pi pins gfm=true,
//     breaks=false, pedantic=false); the corresponding option checks are
//     resolved to their gfm branches.
//   - marked throws on infinite-loop guards; pig panics with the same
//     message (doRender-throw precedent).
package tui

import (
	"strconv"
	"strings"

	"github.com/dlclark/regexp2"
)

// =============================================================================
// JS string semantics helpers (rune-index space)
// =============================================================================

func jsRuneLen(s string) int { return len([]rune(s)) }

// jsSlice is JS String.prototype.slice in rune space (negative indices wrap).
func jsSlice(s string, start, end int) string {
	r := []rune(s)
	n := len(r)
	if start < 0 {
		start = n + start
	}
	if end < 0 {
		end = n + end
	}
	if start < 0 {
		start = 0
	}
	if end > n {
		end = n
	}
	if start >= end {
		return ""
	}
	return string(r[start:end])
}

func firstRuneOf(s string) string {
	for _, r := range s {
		return string(r)
	}
	return ""
}

func lastRuneOf(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	return string(r[len(r)-1])
}

// runeIndexOfRE is JS String.prototype.search (first match index, -1 if none).
func runeIndexOfRE(s string, re *regexp2.Regexp) int {
	m, err := re.FindStringMatch(s)
	if err != nil || m == nil {
		return -1
	}
	return m.Index
}

// runeLastIndexOf is JS lastIndexOf in rune space.
func runeLastIndexOf(s, sub string) int {
	idx := strings.LastIndex(s, sub)
	if idx < 0 {
		return -1
	}
	return jsRuneLen(s[:idx])
}

// =============================================================================
// regexp2 helpers (JS RegExp.exec/test/replace equivalents)
// =============================================================================

func reExec(re *regexp2.Regexp, s string) *regexp2.Match {
	m, err := re.FindStringMatchStartingAt(s, 0)
	if err != nil {
		return nil
	}
	return m
}

// nextMatch is the JS g-flag exec continuation (exec after the previous
// match's lastIndex). regexp2's FindStringMatchStartingAt mis-handles
// mid-string starts, so scan loops chain FindNextMatch instead.
func nextMatch(re *regexp2.Regexp, prev *regexp2.Match) *regexp2.Match {
	m, err := re.FindNextMatch(prev)
	if err != nil {
		return nil
	}
	return m
}

func reTest(re *regexp2.Regexp, s string) bool { return reExec(re, s) != nil }

// mGroup returns match group n (1-based); "" when the group didn't participate.
func mGroup(m *regexp2.Match, n int) string {
	if m == nil {
		return ""
	}
	g := m.GroupByNumber(n)
	if g == nil || len(g.Captures) == 0 {
		return ""
	}
	return g.String()
}

// reReplaceAll is JS String.replace(re, replacement) with /g semantics;
// replacement supports $N group references (the only forms marked uses).
func reReplaceAll(re *regexp2.Regexp, s string, repl string) string {
	var b strings.Builder
	pos := 0
	m, err := re.FindStringMatchStartingAt(s, 0)
	for err == nil && m != nil {
		b.WriteString(s[pos:m.Index])
		b.WriteString(expandRepl(repl, m))
		pos = m.Index + m.Length
		if m.Length == 0 {
			if pos >= len(s) {
				break
			}
			// JS zero-length match: consume one char to avoid an infinite loop.
			_, size := decodeRuneAt(s, pos)
			b.WriteString(s[pos : pos+size])
			pos += size
		}
		m, err = re.FindNextMatch(m)
	}
	b.WriteString(s[pos:])
	return b.String()
}

func expandRepl(repl string, m *regexp2.Match) string {
	var b strings.Builder
	runes := []rune(repl)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '$' && i+1 < len(runes) && runes[i+1] >= '0' && runes[i+1] <= '9' {
			n := int(runes[i+1] - '0')
			b.WriteString(mGroup(m, n))
			i++
			continue
		}
		b.WriteRune(runes[i])
	}
	return b.String()
}

func decodeRuneAt(s string, pos int) (rune, int) {
	for i, r := range s {
		if i >= pos {
			return r, len(string(r))
		}
	}
	return 0, 0
}

// =============================================================================
// marked helper functions (src/Lexer.js module scope)
// =============================================================================

// markedRtrim is marked's rtrim(l, e, t=false): trim trailing `e` chars (or,
// with t=true, trailing chars that are NOT `e`).
func markedRtrim(l string, e string, t bool) string {
	n := jsRuneLen(l)
	if n == 0 {
		return ""
	}
	s := 0
	for s < n {
		r := lastRuneOf(jsSlice(l, 0, n-s))
		if r == e && !t {
			s++
		} else if r != e && t {
			s++
		} else {
			break
		}
	}
	return jsSlice(l, 0, n-s)
}

// markedChop is marked's chop(l): remove trailing blank lines (no-op when
// fewer than 2 lines would be removed).
func markedChop(l string) string {
	e := strings.Split(l, "\n")
	t := len(e) - 1
	for ; t >= 0 && reTest(markedBlankLineRe, e[t]); t-- {
	}
	if len(e)-t <= 2 {
		return l
	}
	return strings.Join(e[:t+1], "\n")
}

var markedBlankLineRe = markedCompileRegexOptions("^[ \t]*$", regexp2.ECMAScript)

// markedSplitCells is marked's splitCells(tableRow, count) (src/helpers.ts).
// count == 0 mirrors pi's undefined (no slice/pad).
func markedSplitCells(tableRow string, count int, other *markedOther) []string {
	// Replace each pipe: escaped pipes stay, unescaped pipes get a space
	// prefix so they can't be confused with escaped ones.
	var b strings.Builder
	runes := []rune(tableRow)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '|' {
			escaped := false
			for j := i - 1; j >= 0 && runes[j] == '\\'; j-- {
				escaped = !escaped
			}
			if escaped {
				b.WriteRune('|')
			} else {
				b.WriteString(" |")
			}
			continue
		}
		b.WriteRune(runes[i])
	}
	cells := strings.Split(b.String(), " |")
	// First/last cell in a row cannot be empty if it has no leading/trailing pipe.
	if strings.TrimSpace(cells[0]) == "" {
		cells = cells[1:]
	}
	if len(cells) > 0 && strings.TrimSpace(cells[len(cells)-1]) == "" {
		cells = cells[:len(cells)-1]
	}
	if count != 0 { // pi: if (count)
		if len(cells) > count {
			cells = cells[:count]
		} else {
			for len(cells) < count {
				cells = append(cells, "")
			}
		}
	}
	// Leading/trailing whitespace is ignored per the gfm spec.
	for i := range cells {
		cells[i] = reReplaceAll(other.SlashPipe, strings.TrimSpace(cells[i]), "|")
	}
	return cells
}

// markedFindClosingBracket is marked's findClosingBracket(source, bracket).
func markedFindClosingBracket(source string, bracket string) int {
	if !strings.Contains(source, bracket[1:]) {
		return -1
	}
	level := 0
	runes := []rune(source)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' {
			i++
		} else if runes[i] == rune(bracket[0]) {
			level++
		} else if runes[i] == rune(bracket[1]) {
			level--
			if level < 0 {
				return i
			}
		}
	}
	if level > 0 {
		return -2
	}
	return -1
}

// markedOutdentTab is marked's outdent... (me in the minified source): expand
// tabs to 4-column stops starting at column `e`.
func markedOutdentTab(l string, e int) string {
	t := e
	n := ""
	for _, s := range l {
		if s == '\t' {
			r := 4 - t%4
			n += strings.Repeat(" ", r)
			t += r
		} else {
			n += string(s)
			t++
		}
	}
	return n
}

// markedIndentCodeCompensation is marked's indentCodeCompensation helper.
func markedIndentCodeCompensation(raw string, text string, other *markedOther) string {
	m := reExec(other.IndentCodeCompensation, raw)
	if m == nil {
		return text
	}
	indentation := mGroup(m, 1)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		im := reExec(other.BeginningSpace, line)
		if im == nil {
			continue
		}
		indent := im.String()
		if jsRuneLen(indent) >= jsRuneLen(indentation) {
			lines[i] = jsSlice(line, jsRuneLen(indentation), jsRuneLen(line))
		}
	}
	return strings.Join(lines, "\n")
}

// =============================================================================
// Token model (marked's dynamic token objects → one struct)
// =============================================================================

// MarkedToken models marked's token objects. Fields are zero for token types
// that don't carry them (pi: `token.text`, `token.tokens`, ... optional
// members). The latex extension (markdown.ts) uses Pending.
type MarkedToken struct {
	Type   string
	Raw    string
	Text   string
	Tokens []*MarkedToken

	Depth      int    // heading
	Lang       string // code (fences)
	Tag        string // def
	Href       string // link
	Title      string // def / link title ("" = none; pi: string|null)
	Block      bool   // html
	InLink     bool   // html (inline)
	InRawBlock bool   // html (inline)
	Escaped    bool   // text (inline): follows a raw block

	Ordered bool // list
	Start   any  // list: int when ordered (pi: typeof start === "number"), "" otherwise
	Loose   bool // list
	Items   []*MarkedToken

	Task    bool // list_item
	Checked bool // list_item / checkbox

	Header *bool  // table cell (header vs body)
	Align  string // table cell: "right"|"center"|"" (left and null share "")

	// table
	HeaderCells []*MarkedToken
	Rows        [][]*MarkedToken
	Aligns      []string

	// extensions (latex, markdown.ts)
	Pending bool
}

// HasTextProperty reports whether the underlying marked token object carries
// a `text` property (pi: `'text' in token`). Go structs always have their
// fields, so this is derived from the token type; the checkbox token is the
// case observable through renderToken/renderInlineTokens defaults.
func (t *MarkedToken) HasTextProperty() bool {
	return t.Type != "checkbox"
}

// StartNumber returns the list start when numeric (pi: typeof token.start === "number").
func (t *MarkedToken) StartNumber() (int, bool) {
	if n, ok := t.Start.(int); ok {
		return n, true
	}
	return 0, false
}

// markedLinkRef is a link definition (marked: tokens.links[tag]).
type markedLinkRef struct {
	href  string
	title string
}

// markedLexerState mirrors marked's lexer state.
type markedLexerState struct {
	inLink     bool
	inRawBlock bool
	top        bool
}

// markedInlineQueueEntry is one deferred inline-tokenization task. JS shares
// the token's `tokens` array by reference; pig records the owning token and
// backfills MarkedToken.Tokens when the queue drains (same observable
// behavior — every queued array is assigned to exactly one token field).
type markedInlineQueueEntry struct {
	src   string
	token *MarkedToken
}

// markedExtensions carries pi's tokenizer extensions (latex, markdown.ts).
type markedExtensions struct {
	// Block/Inline extension tokenizers: return a token to consume (marked:
	// options.extensions.block/inline; tried before builtin tokenizers).
	Block  []func(src string, tokens *[]*MarkedToken) *MarkedToken
	Inline []func(src string, tokens *[]*MarkedToken) *MarkedToken
	// StartBlock/StartInline hint the earliest possible extension match in
	// src[1:] (marked: startBlock/startInline; >= 0 = candidate index).
	StartBlock  []func(src string) int
	StartInline []func(src string) int
}

// markedLexerOptions fixes the options pi uses (gfm=true, breaks=false,
// pedantic=false) plus extensions.
type markedLexerOptions struct {
	Extensions *markedExtensions
}

// markedLexer is marked's Lexer class.
type markedLexer struct {
	tokens      []*MarkedToken
	links       map[string]markedLinkRef
	options     markedLexerOptions
	state       markedLexerState
	inlineQueue []*markedInlineQueueEntry
	tokenizer   *markedTokenizer
	other       *markedOther
	block       *markedBlockRules
	inline      *markedInlineRules
}

// newMarkedLexer mirrors marked's Lexer constructor with pi's option set.
func newMarkedLexer(options markedLexerOptions) *markedLexer {
	l := &markedLexer{
		links:   map[string]markedLinkRef{},
		options: options,
		state:   markedLexerState{top: true},
		other:   newMarkedOther(),
		block:   newMarkedBlockRules(),
		inline:  newMarkedInlineRules(),
	}
	l.tokenizer = &markedTokenizer{lexer: l, other: l.other, block: l.block, inlineRules: l.inline}
	return l
}

// markedLex is marked's Lexer.lex (pi: markdownParser.lexer(normalizedText)).
func markedLex(src string, options markedLexerOptions) []*MarkedToken {
	return newMarkedLexer(options).Lex(src)
}

// MarkedLex is the stock marked lexer entry (pi: `new Marked().lexer(src)` in
// components/mermaid.ts — the npm marked package re-exported by pi-tui).
// pig addition: markedLex is unexported and the markdown component's latex
// extension must NOT run here, so this wrapper lexes with no extensions
// (stock marked defaults: gfm=true, breaks=false).
func MarkedLex(src string) []*MarkedToken {
	return markedLex(src, markedLexerOptions{})
}

// Lex normalizes newlines, tokenizes blocks, then drains the inline queue.
func (l *markedLexer) Lex(src string) []*MarkedToken {
	src = reReplaceAll(l.other.CarriageReturn, src, "\n")
	l.blockTokens(src, &l.tokens, false)
	for _, entry := range l.inlineQueue {
		toks := []*MarkedToken{}
		l.inlineTokens(entry.src, &toks)
		entry.token.Tokens = toks
	}
	l.inlineQueue = nil
	return l.tokens
}

// queueInline registers deferred inline tokenization (marked: lexer.inline;
// renamed — the lexer struct already has an `inline` rules field).
func (l *markedLexer) queueInline(src string, token *MarkedToken) {
	l.inlineQueue = append(l.inlineQueue, &markedInlineQueueEntry{src: src, token: token})
}

// inlineTokensNow tokenizes inline text immediately (marked: tokenizer
// methods calling this.lexer.inlineTokens directly, outside the queue).
func (l *markedLexer) inlineTokensNow(src string) []*MarkedToken {
	tokens := []*MarkedToken{}
	l.inlineTokens(src, &tokens)
	return tokens
}

// blockTokens is marked's Lexer.blockTokens.
func (l *markedLexer) blockTokens(src string, tokens *[]*MarkedToken, topParagraphMerge bool) {
	ext := l.options.Extensions
	lastIndex := -1
	for src != "" {
		if lastIndex == -1 || len(src) < lastIndex {
			lastIndex = len(src)
		} else {
			l.infiniteLoopError(src)
			break
		}

		if ext != nil {
			matched := false
			for _, fn := range ext.Block {
				if token := fn(src, tokens); token != nil {
					src = src[len(token.Raw):]
					*tokens = append(*tokens, token)
					matched = true
					break
				}
			}
			if matched {
				continue
			}
		}

		if token := l.tokenizer.space(src); token != nil {
			src = src[len(token.Raw):]
			if len(token.Raw) == 1 && len(*tokens) > 0 {
				(*tokens)[len(*tokens)-1].Raw += "\n"
			} else {
				*tokens = append(*tokens, token)
			}
			continue
		}

		if token := l.tokenizer.code(src); token != nil {
			src = src[len(token.Raw):]
			if n := len(*tokens); n > 0 && ((*tokens)[n-1].Type == "paragraph" || (*tokens)[n-1].Type == "text") {
				last := (*tokens)[n-1]
				if !strings.HasSuffix(last.Raw, "\n") {
					last.Raw += "\n"
				}
				last.Raw += token.Raw
				last.Text += "\n" + token.Text
				(*tokens)[n-1] = last
				if len(l.inlineQueue) > 0 {
					l.inlineQueue[len(l.inlineQueue)-1].src = last.Text
				}
			} else {
				*tokens = append(*tokens, token)
			}
			continue
		}

		if token := l.tokenizer.fences(src); token != nil {
			src = src[len(token.Raw):]
			*tokens = append(*tokens, token)
			continue
		}

		if token := l.tokenizer.heading(src); token != nil {
			src = src[len(token.Raw):]
			*tokens = append(*tokens, token)
			continue
		}

		if token := l.tokenizer.hr(src); token != nil {
			src = src[len(token.Raw):]
			*tokens = append(*tokens, token)
			continue
		}

		if token := l.tokenizer.blockquote(src); token != nil {
			src = src[len(token.Raw):]
			*tokens = append(*tokens, token)
			continue
		}

		if token := l.tokenizer.list(src); token != nil {
			src = src[len(token.Raw):]
			*tokens = append(*tokens, token)
			continue
		}

		if token := l.tokenizer.html(src); token != nil {
			src = src[len(token.Raw):]
			*tokens = append(*tokens, token)
			continue
		}

		if token := l.tokenizer.def(src); token != nil {
			src = src[len(token.Raw):]
			if n := len(*tokens); n > 0 && ((*tokens)[n-1].Type == "paragraph" || (*tokens)[n-1].Type == "text") {
				last := (*tokens)[n-1]
				if !strings.HasSuffix(last.Raw, "\n") {
					last.Raw += "\n"
				}
				last.Raw += token.Raw
				last.Text += "\n" + token.Raw
				(*tokens)[n-1] = last
				if len(l.inlineQueue) > 0 {
					l.inlineQueue[len(l.inlineQueue)-1].src = last.Text
				}
			} else if _, ok := l.links[token.Tag]; !ok {
				l.links[token.Tag] = markedLinkRef{href: token.Href, title: token.Title}
				*tokens = append(*tokens, token)
			}
			continue
		}

		if token := l.tokenizer.table(src); token != nil {
			src = src[len(token.Raw):]
			*tokens = append(*tokens, token)
			continue
		}

		if token := l.tokenizer.lheading(src); token != nil {
			src = src[len(token.Raw):]
			*tokens = append(*tokens, token)
			continue
		}

		candidate := src
		if ext != nil && len(ext.StartBlock) > 0 {
			minIdx := -1
			rest := ""
			if len(src) > 0 {
				rest = jsSlice(src, 1, jsRuneLen(src))
			}
			for _, fn := range ext.StartBlock {
				if idx := fn(rest); idx >= 0 && (minIdx == -1 || idx < minIdx) {
					minIdx = idx
				}
			}
			if minIdx >= 0 {
				candidate = jsSlice(src, 0, minIdx+1)
			}
		}

		if l.state.top {
			if token := l.tokenizer.paragraph(candidate); token != nil {
				if n := len(*tokens); topParagraphMerge && n > 0 && (*tokens)[n-1].Type == "paragraph" {
					last := (*tokens)[n-1]
					if !strings.HasSuffix(last.Raw, "\n") {
						last.Raw += "\n"
					}
					last.Raw += token.Raw
					last.Text += "\n" + token.Text
					(*tokens)[n-1] = last
					if len(l.inlineQueue) > 0 {
						l.inlineQueue = l.inlineQueue[:len(l.inlineQueue)-1]
						if len(l.inlineQueue) > 0 {
							l.inlineQueue[len(l.inlineQueue)-1].src = last.Text
						}
					}
				} else {
					*tokens = append(*tokens, token)
				}
				topParagraphMerge = jsRuneLen(candidate) != jsRuneLen(src)
				src = src[len(token.Raw):]
				continue
			}
		}

		if token := l.tokenizer.text(src); token != nil {
			src = src[len(token.Raw):]
			if n := len(*tokens); n > 0 && (*tokens)[n-1].Type == "text" {
				last := (*tokens)[n-1]
				if !strings.HasSuffix(last.Raw, "\n") {
					last.Raw += "\n"
				}
				last.Raw += token.Raw
				last.Text += "\n" + token.Text
				(*tokens)[n-1] = last
				if len(l.inlineQueue) > 0 {
					l.inlineQueue = l.inlineQueue[:len(l.inlineQueue)-1]
					if len(l.inlineQueue) > 0 {
						l.inlineQueue[len(l.inlineQueue)-1].src = last.Text
					}
				}
			} else {
				*tokens = append(*tokens, token)
			}
			continue
		}

		if src != "" {
			l.infiniteLoopError(src)
			break
		}
	}
	l.state.top = true
}

// inlineTokens is marked's Lexer.inlineTokens.
func (l *markedLexer) inlineTokens(src string, tokens *[]*MarkedToken) {
	ext := l.options.Extensions

	masked := src
	// Mask reflinks so emphasis cannot match inside them. Matches are
	// collected on the unmodified string first; the length-preserving
	// rewrites are then applied by index (equivalent to JS continuing exec
	// on the rewritten string — content beyond each match is unchanged).
	if len(l.links) > 0 {
		var matches []*regexp2.Match
		for m := reExec(l.inline.ReflinkSearch, masked); m != nil; m = nextMatch(l.inline.ReflinkSearch, m) {
			matches = append(matches, m)
		}
		for _, m := range matches {
			label := m.String()
			if i := runeLastIndexOf(label, "["); i >= 0 {
				label = jsSlice(label, i+1, jsRuneLen(label)-1)
			}
			if _, ok := l.links[strings.ToLower(label)]; ok {
				// Replacement preserves length: "[" + a*(len-2) + "]".
				masked = jsSlice(masked, 0, m.Index) + "[" + strings.Repeat("a", m.Length-2) + "]" + jsSlice(masked, m.Index+m.Length, jsRuneLen(masked))
			}
		}
	}
	// Mask escaped punctuation (same collect-then-apply strategy).
	{
		var matches []*regexp2.Match
		for m := reExec(l.inline.AnyPunctuation, masked); m != nil; m = nextMatch(l.inline.AnyPunctuation, m) {
			matches = append(matches, m)
		}
		for _, m := range matches {
			masked = jsSlice(masked, 0, m.Index) + "++" + jsSlice(masked, m.Index+m.Length, jsRuneLen(masked))
		}
	}
	// Mask inline code and link bodies (collect-then-apply).
	{
		type skipMatch struct {
			index, length, skipLen int
		}
		var matches []skipMatch
		for m := reExec(l.inline.BlockSkip, masked); m != nil; m = nextMatch(l.inline.BlockSkip, m) {
			skipLen := 0
			if g := m.GroupByNumber(2); g != nil && len(g.Captures) > 0 {
				skipLen = jsRuneLen(g.String())
			}
			matches = append(matches, skipMatch{m.Index, m.Length, skipLen})
		}
		for _, sm := range matches {
			masked = jsSlice(masked, 0, sm.index+sm.skipLen) + "[" + strings.Repeat("a", sm.length-sm.skipLen-2) + "]" + jsSlice(masked, sm.index+sm.length, jsRuneLen(masked))
		}
	}

	prevChar := ""
	prevCharValid := false
	lastIndex := -1
	for src != "" {
		if lastIndex == -1 || len(src) < lastIndex {
			lastIndex = len(src)
		} else {
			l.infiniteLoopError(src)
			break
		}
		if !prevCharValid {
			prevChar = ""
		}
		prevCharValid = false

		if ext != nil {
			matched := false
			for _, fn := range ext.Inline {
				if token := fn(src, tokens); token != nil {
					src = src[len(token.Raw):]
					*tokens = append(*tokens, token)
					matched = true
					break
				}
			}
			if matched {
				continue
			}
		}

		if token := l.tokenizer.escape(src); token != nil {
			src = src[len(token.Raw):]
			*tokens = append(*tokens, token)
			continue
		}

		if token := l.tokenizer.tag(src); token != nil {
			src = src[len(token.Raw):]
			*tokens = append(*tokens, token)
			continue
		}

		if token := l.tokenizer.link(src); token != nil {
			src = src[len(token.Raw):]
			*tokens = append(*tokens, token)
			continue
		}

		if token := l.tokenizer.reflink(src); token != nil {
			src = src[len(token.Raw):]
			if n := len(*tokens); token.Type == "text" && n > 0 && (*tokens)[n-1].Type == "text" {
				last := (*tokens)[n-1]
				last.Raw += token.Raw
				last.Text += token.Text
				(*tokens)[n-1] = last
			} else {
				*tokens = append(*tokens, token)
			}
			continue
		}

		if token := l.tokenizer.emStrong(src, masked, prevChar); token != nil {
			src = src[len(token.Raw):]
			*tokens = append(*tokens, token)
			continue
		}

		if token := l.tokenizer.codespan(src); token != nil {
			src = src[len(token.Raw):]
			*tokens = append(*tokens, token)
			continue
		}

		if token := l.tokenizer.br(src); token != nil {
			src = src[len(token.Raw):]
			*tokens = append(*tokens, token)
			continue
		}

		// pi routes del through the StrictStrikethroughTokenizer (setOptions
		// override), which requires double tildes — single ~ stays literal.
		if token := l.tokenizer.markedStrictDel(src); token != nil {
			src = src[len(token.Raw):]
			*tokens = append(*tokens, token)
			continue
		}

		if token := l.tokenizer.autolink(src); token != nil {
			src = src[len(token.Raw):]
			*tokens = append(*tokens, token)
			continue
		}

		if !l.state.inLink {
			if token := l.tokenizer.url(src); token != nil {
				src = src[len(token.Raw):]
				*tokens = append(*tokens, token)
				continue
			}
		}

		candidate := src
		if ext != nil && len(ext.StartInline) > 0 {
			minIdx := -1
			rest := ""
			if len(src) > 0 {
				rest = jsSlice(src, 1, jsRuneLen(src))
			}
			for _, fn := range ext.StartInline {
				if idx := fn(rest); idx >= 0 && (minIdx == -1 || idx < minIdx) {
					minIdx = idx
				}
			}
			if minIdx >= 0 {
				candidate = jsSlice(src, 0, minIdx+1)
			}
		}

		if token := l.tokenizer.inlineText(candidate); token != nil {
			src = src[len(token.Raw):]
			if tr := lastRuneOf(token.Raw); tr != "_" {
				prevChar = tr
			}
			prevCharValid = true
			if n := len(*tokens); n > 0 && (*tokens)[n-1].Type == "text" {
				last := (*tokens)[n-1]
				last.Raw += token.Raw
				last.Text += token.Text
				(*tokens)[n-1] = last
			} else {
				*tokens = append(*tokens, token)
			}
			continue
		}

		if src != "" {
			l.infiniteLoopError(src)
			break
		}
	}
}

func (l *markedLexer) infiniteLoopError(src string) {
	code := 0
	for _, r := range src {
		code = int(r)
		break
	}
	panic("Infinite loop on byte: " + strconv.Itoa(code))
}
