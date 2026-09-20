// tui/utils.go (pi: packages/tui/src/utils.ts) — terminal string width,
// ANSI-aware truncation, wrapping, column slicing, and segment extraction.
//
// Porting deviations (all declared, see also README §3.4 / §4 Phase 6):
//   - TS operates on UTF-16 code units; Go strings are UTF-8 bytes. Every
//     index/offset produced or consumed here (GetGraphemeCellRange, ExtractAnsiCode
//     positions, grapheme slicing bounds) is a BYTE offset in the Go port.
//     Visible-column arithmetic is unchanged.
//   - Intl.Segmenter (grapheme/word) → rivo/uniseg (UAX #29). Grapheme
//     segmentation is equivalent; word segmentation lacks ICU's CJK dictionary
//     breaking (documented gap, README §4 Phase 6) — WordSegmenter is an
//     interface so a dictionary-backed implementation can slot in later.
//   - get-east-asian-width → golang.org/x/text/width (W/F → 2, everything
//     else → 1, matching the npm package's default options).
//   - ECMAScript v-flag property regexes → generated range tables in
//     tables_generated.go (UCD 15.1, matching Node/Bun's ICU level):
//     zeroWidth/nonPrinting/mark use DI/Cc/Cf/M/Cs tables; Spacing_Mark and
//     Script_Extensions unions and RGI_Emoji come from the generated tables.
//   - JS `\s` and String#trimEnd/trim use the ECMAScript WhiteSpace ∪
//     LineTerminator set (broader than Go's \s) → isJsSpace/jsTrimEnd/jsTrim.
//   - ANSI state tracking (AnsiCodeTracker) is not exported in pi and stays
//     unexported here; ExtractSegments allocates its tracker per call instead
//     of pooling (behavior identical, tui rendering is single-goroutine).
//   - widthCache keeps the JS Map insertion-order FIFO eviction (512 entries).

package tui

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
	"golang.org/x/text/width"
)

// ---------------------------------------------------------------------------
// Segmenters (shared instances)
// ---------------------------------------------------------------------------

// GraphemeSegmenter mirrors the Intl.Segmenter (granularity: "grapheme") face
// pi-tui consumes: Segments splits s into extended grapheme clusters.
type GraphemeSegmenter interface {
	Segments(s string) []string
}

// WordSegmenter mirrors Intl.Segmenter (granularity: "word"). Known gap vs
// ICU: no CJK dictionary breaking (UAX #29 word rules only, CJK breaks per
// character); see README §4 Phase 6 for the enhancement plan.
type WordSegmenter interface {
	Segments(s string) []string
}

type graphemeSegmenterImpl struct{}

// Segments splits s into extended grapheme clusters (UAX #29).
func (graphemeSegmenterImpl) Segments(s string) []string {
	var out []string
	forEachGrapheme(s, func(cluster string) bool {
		out = append(out, cluster)
		return true
	})
	return out
}

type wordSegmenterImpl struct{}

// Segments splits s into words per UAX #29 (see interface comment re: CJK).
func (wordSegmenterImpl) Segments(s string) []string {
	var out []string
	state := -1
	for len(s) > 0 {
		word, rest, newState := uniseg.FirstWordInString(s, state)
		if word != "" {
			out = append(out, word)
		}
		s, state = rest, newState
	}
	return out
}

var (
	sharedGraphemeSegmenter GraphemeSegmenter = graphemeSegmenterImpl{}
	sharedWordSegmenter     WordSegmenter     = wordSegmenterImpl{}
)

// GetGraphemeSegmenter returns the shared grapheme segmenter instance.
func GetGraphemeSegmenter() GraphemeSegmenter { return sharedGraphemeSegmenter }

// GetWordSegmenter returns the shared word segmenter instance.
func GetWordSegmenter() WordSegmenter { return sharedWordSegmenter }

// forEachGrapheme iterates extended grapheme clusters of s in order; the
// callback returns false to stop iteration early.
func forEachGrapheme(s string, fn func(cluster string) bool) {
	state := -1
	for len(s) > 0 {
		cluster, rest, _, newState := uniseg.FirstGraphemeClusterInString(s, state)
		if !fn(cluster) {
			return
		}
		s, state = rest, newState
	}
}

// ---------------------------------------------------------------------------
// Unicode property helpers (replacement for v-flag regexes)
// ---------------------------------------------------------------------------

// zeroWidthTables implements pi's zeroWidthRegex:
// ^(DI | Control | Mark | Surrogate)+$
var zeroWidthTables = []*unicode.RangeTable{
	tableDefaultIgnorable, unicode.Cc, unicode.M, unicode.Cs,
}

// nonPrintingTables implements pi's leadingNonPrintingRegex /
// nonPrintingCharRegex classes: DI | Control | Format | Mark | Surrogate.
var nonPrintingTables = []*unicode.RangeTable{
	tableDefaultIgnorable, unicode.Cc, unicode.Cf, unicode.M, unicode.Cs,
}

func runeInTables(r rune, tables []*unicode.RangeTable) bool {
	for _, t := range tables {
		if unicode.Is(t, r) {
			return true
		}
	}
	return false
}

// allRunesIn reports whether every code point of s is in the union of tables.
// An empty string returns false (the TS regexes require one or more chars).
func allRunesIn(s string, tables []*unicode.RangeTable) bool {
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if !runeInTables(r, tables) {
			return false
		}
	}
	return true
}

// anyRuneIn reports whether any code point of s is in the union of tables
// (unanchored regex .test() semantics).
func anyRuneIn(s string, tables []*unicode.RangeTable) bool {
	for _, r := range s {
		if runeInTables(r, tables) {
			return true
		}
	}
	return false
}

// stripLeadingIn removes leading code points in the union of tables
// (pi's leadingNonPrintingRegex replace).
func stripLeadingIn(s string, tables []*unicode.RangeTable) string {
	for i, r := range s {
		if !runeInTables(r, tables) {
			return s[i:]
		}
	}
	return ""
}

// utf16Length returns the UTF-16 code unit count of s (TS `.length`).
func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// isRgiEmoji reports whether segment is exactly an RGI emoji sequence
// (pi's /^\p{RGI_Emoji}$/v).
func isRgiEmoji(segment string) bool {
	_, ok := rgiEmojiSet[segment]
	return ok
}

// couldBeEmoji is pi's fast heuristic to avoid the RGI_Emoji set lookup.
// The code point ranges are deliberately broad to account for future Unicode
// additions.
func couldBeEmoji(segment string) bool {
	r, _ := utf8.DecodeRuneInString(segment)
	return (r >= 0x1f000 && r <= 0x1fbff) || // Emoji and Pictograph
		(r >= 0x2300 && r <= 0x23ff) || // Misc technical
		(r >= 0x2600 && r <= 0x27bf) || // Misc symbols, dingbats
		(r >= 0x2b50 && r <= 0x2b55) || // Specific stars/circles
		strings.Contains(segment, "\uFE0F") || // Contains VS16 (emoji presentation selector)
		utf16Length(segment) > 2 // Multi-codepoint sequences (ZWJ, skin tones, etc.)
}

// eastAsianWidth maps a code point to 1 or 2 cells (get-east-asian-width
// default options: W/F → 2, ambiguous/narrow/neutral → 1).
func eastAsianWidth(r rune) int {
	p := width.LookupRune(r)
	if p.Kind() == width.EastAsianWide || p.Kind() == width.EastAsianFullwidth {
		return 2
	}
	return 1
}

// ---------------------------------------------------------------------------
// Basic classifiers
// ---------------------------------------------------------------------------

func isPrintableAscii(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// isJsSpace reports ECMAScript \s membership (WhiteSpace ∪ LineTerminators).
func isJsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsTrimEnd trims trailing ECMAScript whitespace (String#trimEnd).
func jsTrimEnd(s string) string {
	end := len(s)
	for end > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:end])
		if !isJsSpace(r) {
			break
		}
		end -= size
	}
	return s[:end]
}

// jsTrim trims leading and trailing ECMAScript whitespace (String#trim).
func jsTrim(s string) string {
	start := 0
	for start < len(s) {
		r, size := utf8.DecodeRuneInString(s[start:])
		if !isJsSpace(r) {
			break
		}
		start += size
	}
	return jsTrimEnd(s[start:])
}

// jsTrimStart trims leading ECMAScript whitespace (String#trimStart).
// Used by the editor's slash-command context check (editor.go).
func jsTrimStart(s string) string {
	start := 0
	for start < len(s) {
		r, size := utf8.DecodeRuneInString(s[start:])
		if !isJsSpace(r) {
			break
		}
		start += size
	}
	return s[start:]
}

// CJK break classification: pi's
// cjkBreakRegex =
// /[\p{Script_Extensions=Han}\p{Script_Extensions=Hiragana}\p{Script_Extensions=Katakana}\p{Script_Extensions=Hangul}\p{Script_Extensions=Bopomofo}]/u
// tested against a whole grapheme (any code point matching breaks the run).
func isCJKBreak(segment string) bool {
	return anyRuneIn(segment, []*unicode.RangeTable{tableScxCJK})
}

// isCJKBreakRune reports whether a single code point matches pi's
// cjkBreakRegex (Script_Extensions union of the five CJK scripts).
func isCJKBreakRune(r rune) bool {
	return unicode.Is(tableScxCJK, r)
}

// isJsSpaceRune matches ECMAScript \s (White_Space plus U+FEFF).
func isJsSpaceRune(r rune) bool {
	return unicode.IsSpace(r) || r == 0xFEFF
}

// cjkFullwidthPunctuation is pi's explicit list in cjkPunctuationRegex
// ([，．：；！？（）［］｛｝“”‘’…—] — Common-script fullwidth forms that are not
// part of the CJK Script_Extensions union).
var cjkFullwidthPunctuation = map[rune]bool{
	'，': true, '．': true, '：': true, '；': true, '！': true, '？': true,
	'（': true, '）': true, '［': true, '］': true, '｛': true, '｝': true,
	'“': true, '”': true, '‘': true, '’': true, '…': true, '—': true,
}

// autocompleteSeparatorRune reports whether the code point r is a token
// separator per pi's autocompleteSeparatorRegex (`(?:\s|cjkPunctuationRegex)`).
// pi's cjkPunctuationRegex =
// `(?:(?=\p{Punctuation})cjkBreak|[fullwidth list])` — RE2 has no lookahead
// and no class intersection, so the two alternatives are tested separately:
// a code point that is BOTH punctuation and CJK-script (。、「」… and ・), or
// one of the explicit fullwidth forms. The lookahead is same-position, so the
// whole regex is a single-code-point class (verified against the JS source).
func autocompleteSeparatorRune(r rune) bool {
	return isJsSpaceRune(r) ||
		(unicode.IsPunct(r) && isCJKBreakRune(r)) ||
		cjkFullwidthPunctuation[r]
}

// isAutocompleteSeparatorAtEnd reports whether text ends at a token boundary
// (pi: tokenStartRegex = `${autocompleteBoundaryRegex.source}$` — the text is
// empty (matches ^$) or its last code point is a separator).
func isAutocompleteSeparatorAtEnd(text string) bool {
	if text == "" {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text)
	return autocompleteSeparatorRune(r)
}

// containsAutocompleteSeparator reports whether any code point in the string
// is a token separator (pi: autocompleteSeparatorRegex.test(path)).
func containsAutocompleteSeparator(text string) bool {
	for _, r := range text {
		if autocompleteSeparatorRune(r) {
			return true
		}
	}
	return false
}

// isJsLineTerminatorRune matches the code points JS `.` excludes
// (\n \r \u2028 \u2029) — relevant to the tempered-dot unquoted-suffix
// construct in pi's editor trigger/debounce patterns.
func isJsLineTerminatorRune(r rune) bool {
	return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029
}

// PUNCTUATION_REGEX matches pi's /[(){}[\]<>.,;:'"!?+\-=*/\\|&%^$#@~`]/.
var PUNCTUATION_REGEX = regexp.MustCompile("[(){}\\[\\]<>.,;:'\"!?+\\-=*/\\\\|&%^$#@~\x60]")

// IsPunctuationChar reports whether the character is ASCII punctuation
// (word-boundary classification).
func IsPunctuationChar(char string) bool {
	return PUNCTUATION_REGEX.MatchString(char)
}

// IsWhitespaceChar reports whether the character is ECMAScript whitespace.
func IsWhitespaceChar(char string) bool {
	r, _ := utf8.DecodeRuneInString(char)
	return isJsSpace(r) && char != ""
}

// ---------------------------------------------------------------------------
// Width calculation
// ---------------------------------------------------------------------------

const widthCacheSize = 512

var (
	widthCache     = map[string]int{}
	widthCacheKeys []string // FIFO eviction order (JS Map insertion order)
)

// graphemeWidth calculates the terminal width of a single grapheme cluster.
// Based on code from the string-width library, but includes a possible-emoji
// check to avoid the RGI_Emoji set lookup unnecessarily.
func graphemeWidth(segment string) int {
	if segment == "\t" {
		return 3
	}

	// Some marks occupy cells even without a base character.
	// (pi: terminalSpacingMarkRegex — generated table folds the exceptions.)
	if allRunesIn(segment, []*unicode.RangeTable{tableSpacingMarkTerminal}) {
		return utf8.RuneCountInString(segment)
	}

	// Zero-width clusters
	if allRunesIn(segment, zeroWidthTables) {
		return 0
	}

	// Emoji check with pre-filter
	if couldBeEmoji(segment) && isRgiEmoji(segment) {
		return 2
	}

	// Get base visible codepoint
	base := stripLeadingIn(segment, nonPrintingTables)
	first, size := utf8.DecodeRuneInString(base)
	if size == 0 {
		return 0
	}

	// Regional indicator symbols (U+1F1E6..U+1F1FF) are often rendered as
	// full-width emoji in terminals, even when isolated during streaming.
	// Keep width conservative (2) to avoid terminal auto-wrap drift artifacts.
	if first >= 0x1f1e6 && first <= 0x1f1ff {
		return 2
	}

	w := eastAsianWidth(first)

	// Intl.Segmenter can group multiple terminal-spacing code points into one
	// grapheme. Count trailing visible code points that terminals may allocate
	// cells for: Indic consonants after marks, halfwidth/fullwidth forms, and
	// Thai/Lao AM vowels.
	followsMark := false
	for _, char := range base[size:] {
		switch {
		case runeInTables(char, []*unicode.RangeTable{tableSpacingMarkTerminal}):
			w += 1
			followsMark = false
		case runeInTables(char, []*unicode.RangeTable{unicode.M}):
			followsMark = true
		case !runeInTables(char, nonPrintingTables):
			if followsMark || (char >= 0xff00 && char <= 0xffef) {
				// halfwidth + fullwidth forms
				w += eastAsianWidth(char)
			} else if char == 0x0e33 || char == 0x0eb3 {
				w += 1
			}
			followsMark = false
		}
	}

	return w
}

// VisibleWidth calculates the visible width of a string in terminal columns.
func VisibleWidth(str string) int {
	if len(str) == 0 {
		return 0
	}

	// Fast path: pure ASCII printable
	if isPrintableAscii(str) {
		return len(str)
	}

	// Check cache
	if cached, ok := widthCache[str]; ok {
		return cached
	}

	// Normalize: tabs to 3 spaces, strip ANSI escape codes
	clean := str
	if strings.Contains(clean, "\t") {
		clean = strings.ReplaceAll(clean, "\t", "   ")
	}
	if strings.Contains(clean, "\x1b") {
		// Strip supported ANSI/OSC/APC escape sequences in one pass.
		// This covers CSI styling/cursor codes, OSC hyperlinks and prompt markers,
		// and APC sequences like CURSOR_MARKER.
		var stripped strings.Builder
		i := 0
		for i < len(clean) {
			if ansi := ExtractAnsiCode(clean, i); ansi != nil {
				i += ansi.Length
				continue
			}
			stripped.WriteByte(clean[i])
			i++
		}
		clean = stripped.String()
	}

	// Calculate width
	w := 0
	forEachGrapheme(clean, func(segment string) bool {
		w += graphemeWidth(segment)
		return true
	})

	// Cache result
	if len(widthCacheKeys) >= widthCacheSize {
		oldest := widthCacheKeys[0]
		widthCacheKeys = widthCacheKeys[1:]
		delete(widthCache, oldest)
	}
	widthCache[str] = w
	widthCacheKeys = append(widthCacheKeys, str)

	return w
}

// StripTerminalSequences removes ANSI, OSC, and APC control sequences while
// preserving visible text.
func StripTerminalSequences(str string) string {
	if !strings.Contains(str, "\x1b") {
		return str
	}
	var result strings.Builder
	i := 0
	for i < len(str) {
		if ansi := ExtractAnsiCode(str, i); ansi != nil {
			i += ansi.Length
			continue
		}
		result.WriteByte(str[i])
		i++
	}
	return result.String()
}

// ---------------------------------------------------------------------------
// Grapheme cell ranges / hyperlinks by column
// ---------------------------------------------------------------------------

// GraphemeCellRange is the terminal-cell range occupied by a grapheme
// (Start/End are visible columns, not byte offsets).
type GraphemeCellRange struct {
	Start int
	End   int
}

// GetGraphemeCellRange returns the terminal-cell range occupied by the
// grapheme at a visible column, or nil when no grapheme covers it.
func GetGraphemeCellRange(line string, column int) *GraphemeCellRange {
	currentCol := 0
	i := 0
	for i < len(line) {
		if ansi := ExtractAnsiCode(line, i); ansi != nil {
			i += ansi.Length
			continue
		}
		textEnd := i
		for textEnd < len(line) && ExtractAnsiCode(line, textEnd) == nil {
			textEnd++
		}
		var found *GraphemeCellRange
		forEachGrapheme(line[i:textEnd], func(segment string) bool {
			w := graphemeWidth(segment)
			if w > 0 && column >= currentCol && column < currentCol+w {
				found = &GraphemeCellRange{Start: currentCol, End: currentCol + w}
				return false
			}
			currentCol += w
			return true
		})
		if found != nil {
			return found
		}
		i = textEnd
	}
	return nil
}

var osc8OpenRegex = regexp.MustCompile("^\\x1b\\]8;[^;]*;([^\\x07\\x1b]*)(?:\\x07|\\x1b\\\\)$")

// GetOsc8LinkAtColumn returns the OSC 8 hyperlink URL covering a visible
// terminal column, or "" when none (TS undefined).
func GetOsc8LinkAtColumn(line string, column int) string {
	activeUrl := ""
	currentCol := 0
	i := 0
	for i < len(line) {
		if ansi := ExtractAnsiCode(line, i); ansi != nil {
			if m := osc8OpenRegex.FindStringSubmatch(ansi.Code); m != nil {
				if m[1] != "" {
					activeUrl = m[1]
				} else {
					activeUrl = ""
				}
			}
			i += ansi.Length
			continue
		}
		textEnd := i
		for textEnd < len(line) && ExtractAnsiCode(line, textEnd) == nil {
			textEnd++
		}
		hit := false
		forEachGrapheme(line[i:textEnd], func(segment string) bool {
			w := graphemeWidth(segment)
			if segment == "\t" {
				w = 3
			}
			if column >= currentCol && column < currentCol+w {
				hit = true
				return false
			}
			currentCol += w
			return true
		})
		if hit {
			return activeUrl
		}
		i = textEnd
	}
	return ""
}

// ---------------------------------------------------------------------------
// Terminal output normalization
// ---------------------------------------------------------------------------

// NormalizeTerminalOutput normalizes text for terminal output without changing
// logical editor content.
//
// Some terminals render precomposed Thai/Lao AM vowels inconsistently during
// differential repaint. Their compatibility decompositions have the same cell
// width but avoid stale-cell artifacts in terminal renderers. Visible tabs are
// expanded to the fixed width used by layout so terminal tab stops cannot wrap
// a logical line, while tabs inside terminal string sequences stay untouched.
func NormalizeTerminalOutput(str string) string {
	normalized := str
	if strings.Contains(normalized, "\u0e33") || strings.Contains(normalized, "\u0eb3") {
		normalized = strings.ReplaceAll(normalized, "\u0e33", "\u0e4d\u0e32")
		normalized = strings.ReplaceAll(normalized, "\u0eb3", "\u0ecd\u0eb2")
	}
	if !strings.Contains(normalized, "\t") {
		return normalized
	}

	var result strings.Builder
	i := 0
	for i < len(normalized) {
		if ansi := ExtractAnsiCode(normalized, i); ansi != nil {
			result.WriteString(ansi.Code)
			i += ansi.Length
			continue
		}
		if normalized[i] == '\t' {
			result.WriteString("   ")
		} else {
			result.WriteByte(normalized[i])
		}
		i++
	}
	return result.String()
}

// ---------------------------------------------------------------------------
// ANSI escape sequence extraction
// ---------------------------------------------------------------------------

// AnsiCode is an escape sequence found at a position (Length is in bytes).
type AnsiCode struct {
	Code   string
	Length int
}

// ExtractAnsiCode extracts an ANSI escape sequence from a string at the given
// byte position, or nil when there is none.
func ExtractAnsiCode(str string, pos int) *AnsiCode {
	if pos >= len(str) || str[pos] != '\x1b' {
		return nil
	}
	if pos+1 >= len(str) {
		return nil
	}
	next := str[pos+1]

	switch next {
	// CSI sequence: ESC [ ... m/G/K/H/J
	case '[':
		j := pos + 2
		for j < len(str) && !isCsiFinalByte(str[j]) {
			j++
		}
		if j < len(str) {
			return &AnsiCode{Code: str[pos : j+1], Length: j + 1 - pos}
		}
		return nil

	// OSC sequence: ESC ] ... BEL or ESC ] ... ST (ESC \)
	// Used for hyperlinks (OSC 8), window titles, etc.
	// APC sequence: ESC _ ... BEL or ESC _ ... ST (ESC \)
	// Used for cursor marker and application-specific commands
	case ']', '_':
		j := pos + 2
		for j < len(str) {
			if str[j] == '\x07' {
				return &AnsiCode{Code: str[pos : j+1], Length: j + 1 - pos}
			}
			if str[j] == '\x1b' && j+1 < len(str) && str[j+1] == '\\' {
				return &AnsiCode{Code: str[pos : j+2], Length: j + 2 - pos}
			}
			j++
		}
		return nil
	}

	return nil
}

func isCsiFinalByte(b byte) bool {
	switch b {
	case 'm', 'G', 'K', 'H', 'J':
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// OSC 8 hyperlinks
// ---------------------------------------------------------------------------

type activeHyperlink struct {
	params     string
	url        string
	terminator string // "\x07" (BEL) or "\x1b\\" (ST)
}

// parseOsc8Hyperlink maps pi's three-way return:
//   - (nil, false): not an OSC 8 code (TS undefined)
//   - (nil, true):  OSC 8 code without URL (TS null)
//   - (link, true): open hyperlink
func parseOsc8Hyperlink(ansiCode string) (*activeHyperlink, bool) {
	if !strings.HasPrefix(ansiCode, "\x1b]8;") {
		return nil, false
	}

	terminator := "\x1b\\"
	if strings.HasSuffix(ansiCode, "\x07") {
		terminator = "\x07"
	}
	body := ansiCode[4 : len(ansiCode)-len(terminator)]
	separatorIndex := strings.IndexByte(body, ';')
	if separatorIndex == -1 {
		return nil, true
	}

	params := body[:separatorIndex]
	url := body[separatorIndex+1:]
	if url == "" {
		return nil, true
	}
	return &activeHyperlink{params: params, url: url, terminator: terminator}, true
}

func formatOsc8Hyperlink(hyperlink *activeHyperlink) string {
	return "\x1b]8;" + hyperlink.params + ";" + hyperlink.url + hyperlink.terminator
}

func formatOsc8Close(terminator string) string {
	return "\x1b]8;;" + terminator
}

func getActiveOsc8Close(prefix string) string {
	if !strings.Contains(prefix, "\x1b]8;") {
		return ""
	}

	var active *activeHyperlink
	i := 0
	for i < len(prefix) {
		if ansi := ExtractAnsiCode(prefix, i); ansi != nil {
			if hyperlink, isOsc8 := parseOsc8Hyperlink(ansi.Code); isOsc8 {
				active = hyperlink
			}
			i += ansi.Length
		} else {
			i++
		}
	}
	if active != nil {
		return formatOsc8Close(active.terminator)
	}
	return ""
}

// ---------------------------------------------------------------------------
// ANSI SGR state tracking
// ---------------------------------------------------------------------------

// ansiCodeTracker tracks active ANSI SGR codes to preserve styling across
// line breaks.
type ansiCodeTracker struct {
	// Track individual attributes separately so we can reset them specifically
	bold          bool
	dim           bool
	italic        bool
	underline     bool
	blink         bool
	inverse       bool
	hidden        bool
	strikethrough bool
	fgColor       string // Stores the full code like "31" or "38;5;240"
	bgColor       string // Stores the full code like "41" or "48;5;240"
	activeLink    *activeHyperlink
}

var sgrParamsRegex = regexp.MustCompile(`\x1b\[([\d;]*)m`)

// Process consumes one escape sequence, updating tracked state.
func (t *ansiCodeTracker) Process(ansiCode string) {
	// OSC 8 hyperlink: \x1b]8;;<url>\x1b\\ (open) or \x1b]8;;\x1b\\ (close).
	// Preserve the original terminator because some terminals only make BEL-terminated
	// links clickable. OAuth login URLs use BEL, so reopening wrapped lines with ST
	// made only the first physical line clickable in those terminals.
	if hyperlink, isOsc8 := parseOsc8Hyperlink(ansiCode); isOsc8 {
		t.activeLink = hyperlink
		return
	}

	if !strings.HasSuffix(ansiCode, "m") {
		return
	}

	// Extract the parameters between \x1b[ and m
	match := sgrParamsRegex.FindStringSubmatch(ansiCode)
	if match == nil {
		return
	}

	params := match[1]
	if params == "" || params == "0" {
		// Full reset
		t.reset()
		return
	}

	// Parse parameters (can be semicolon-separated)
	parts := strings.Split(params, ";")
	i := 0
	for i < len(parts) {
		code, err := strconv.Atoi(parts[i])
		if err != nil {
			// JS Number.parseInt → NaN falls through all cases to default (no-op).
			i++
			continue
		}

		// Handle 256-color and RGB codes which consume multiple parameters
		if code == 38 || code == 48 {
			// 38;5;N (256 color fg) or 38;2;R;G;B (RGB fg)
			// 48;5;N (256 color bg) or 48;2;R;G;B (RGB bg)
			if i+1 < len(parts) && parts[i+1] == "5" && i+2 < len(parts) {
				// 256 color: 38;5;N or 48;5;N
				colorCode := parts[i] + ";" + parts[i+1] + ";" + parts[i+2]
				if code == 38 {
					t.fgColor = colorCode
				} else {
					t.bgColor = colorCode
				}
				i += 3
				continue
			} else if i+1 < len(parts) && parts[i+1] == "2" && i+4 < len(parts) {
				// RGB color: 38;2;R;G;B or 48;2;R;G;B
				colorCode := parts[i] + ";" + parts[i+1] + ";" + parts[i+2] + ";" + parts[i+3] + ";" + parts[i+4]
				if code == 38 {
					t.fgColor = colorCode
				} else {
					t.bgColor = colorCode
				}
				i += 5
				continue
			}
		}

		// Standard SGR codes
		switch code {
		case 0:
			t.reset()
		case 1:
			t.bold = true
		case 2:
			t.dim = true
		case 3:
			t.italic = true
		case 4:
			t.underline = true
		case 5:
			t.blink = true
		case 7:
			t.inverse = true
		case 8:
			t.hidden = true
		case 9:
			t.strikethrough = true
		case 21:
			t.bold = false // Some terminals
		case 22:
			t.bold = false
			t.dim = false
		case 23:
			t.italic = false
		case 24:
			t.underline = false
		case 25:
			t.blink = false
		case 27:
			t.inverse = false
		case 28:
			t.hidden = false
		case 29:
			t.strikethrough = false
		case 39:
			t.fgColor = "" // Default fg
		case 49:
			t.bgColor = "" // Default bg
		default:
			// Standard foreground colors 30-37, 90-97
			if (code >= 30 && code <= 37) || (code >= 90 && code <= 97) {
				t.fgColor = strconv.Itoa(code)
			} else if (code >= 40 && code <= 47) || (code >= 100 && code <= 107) {
				// Standard background colors 40-47, 100-107
				t.bgColor = strconv.Itoa(code)
			}
		}
		i++
	}
}

func (t *ansiCodeTracker) reset() {
	t.bold = false
	t.dim = false
	t.italic = false
	t.underline = false
	t.blink = false
	t.inverse = false
	t.hidden = false
	t.strikethrough = false
	t.fgColor = ""
	t.bgColor = ""
	// SGR reset does not affect OSC 8 hyperlink state
}

// Clear clears all state for reuse.
func (t *ansiCodeTracker) Clear() {
	t.reset()
	t.activeLink = nil
}

// GetActiveCodes returns the SGR sequence re-establishing the active style,
// plus the OSC 8 re-open when a hyperlink is active.
func (t *ansiCodeTracker) GetActiveCodes() string {
	var codes []string
	if t.bold {
		codes = append(codes, "1")
	}
	if t.dim {
		codes = append(codes, "2")
	}
	if t.italic {
		codes = append(codes, "3")
	}
	if t.underline {
		codes = append(codes, "4")
	}
	if t.blink {
		codes = append(codes, "5")
	}
	if t.inverse {
		codes = append(codes, "7")
	}
	if t.hidden {
		codes = append(codes, "8")
	}
	if t.strikethrough {
		codes = append(codes, "9")
	}
	if t.fgColor != "" {
		codes = append(codes, t.fgColor)
	}
	if t.bgColor != "" {
		codes = append(codes, t.bgColor)
	}

	result := ""
	if len(codes) > 0 {
		result = "\x1b[" + strings.Join(codes, ";") + "m"
	}
	if t.activeLink != nil {
		result += formatOsc8Hyperlink(t.activeLink)
	}
	return result
}

func (t *ansiCodeTracker) HasActiveCodes() bool {
	return t.bold || t.dim || t.italic || t.underline || t.blink || t.inverse ||
		t.hidden || t.strikethrough || t.fgColor != "" || t.bgColor != "" ||
		t.activeLink != nil
}

// getActiveBackgroundCode returns the active background color as a full SGR
// sequence, or "" when none is active (pi: AnsiCodeTracker.getActiveBackgroundCode).
func (t *ansiCodeTracker) getActiveBackgroundCode() string {
	if t.bgColor != "" {
		return "\x1b[" + t.bgColor + "m"
	}
	return ""
}

// GetLineEndReset gets reset codes for attributes that need to be turned off
// at line end. Underline must be closed to prevent bleeding into padding.
// Active OSC 8 hyperlinks must be closed and re-opened on the next line.
// Returns empty string if no attributes need closing.
func (t *ansiCodeTracker) GetLineEndReset() string {
	result := ""
	if t.underline {
		result += "\x1b[24m" // Underline off only
	}
	if t.activeLink != nil {
		result += formatOsc8Close(t.activeLink.terminator) // Re-opened at line start via GetActiveCodes()
	}
	return result
}

func updateTrackerFromText(text string, tracker *ansiCodeTracker) {
	i := 0
	for i < len(text) {
		if ansi := ExtractAnsiCode(text, i); ansi != nil {
			tracker.Process(ansi.Code)
			i += ansi.Length
		} else {
			i++
		}
	}
}

// GetActiveBackgroundAnsi returns only the background color active at the end
// of an ANSI-styled string (pi: getActiveBackgroundAnsi).
func GetActiveBackgroundAnsi(text string) string {
	tracker := &ansiCodeTracker{}
	updateTrackerFromText(text, tracker)
	return tracker.getActiveBackgroundCode()
}

// ---------------------------------------------------------------------------
// Wrapping
// ---------------------------------------------------------------------------

// splitIntoTokensWithAnsi splits text into words while keeping ANSI codes
// attached.
func splitIntoTokensWithAnsi(text string) []string {
	var tokens []string
	var current, pendingAnsi strings.Builder
	currentKind := "" // "space" | "word" | "" (TS null)

	flushCurrent := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
		currentKind = ""
	}

	i := 0
	for i < len(text) {
		if ansi := ExtractAnsiCode(text, i); ansi != nil {
			// Hold ANSI codes separately - they'll be attached to the next visible char
			pendingAnsi.WriteString(ansi.Code)
			i += ansi.Length
			continue
		}

		end := i
		for end < len(text) && ExtractAnsiCode(text, end) == nil {
			end++
		}

		forEachGrapheme(text[i:end], func(segment string) bool {
			segmentIsSpace := segment == " "
			if !segmentIsSpace && isCJKBreak(segment) {
				flushCurrent()
				tokens = append(tokens, pendingAnsi.String()+segment)
				pendingAnsi.Reset()
				return true
			}

			segmentKind := "word"
			if segmentIsSpace {
				segmentKind = "space"
			}
			if current.Len() > 0 && currentKind != segmentKind {
				flushCurrent()
			}

			// Attach any pending ANSI codes to this visible character
			if pendingAnsi.Len() > 0 {
				current.WriteString(pendingAnsi.String())
				pendingAnsi.Reset()
			}

			currentKind = segmentKind
			current.WriteString(segment)
			return true
		})

		i = end
	}

	// Handle any remaining pending ANSI codes (attach to last token)
	if pendingAnsi.Len() > 0 {
		if current.Len() > 0 {
			current.WriteString(pendingAnsi.String())
		} else if len(tokens) > 0 {
			tokens[len(tokens)-1] += pendingAnsi.String()
		} else {
			current.WriteString(pendingAnsi.String())
		}
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

var newlineSplitRegex = regexp.MustCompile(`\r\n|\r|\n`)

// WrapTextWithAnsi wraps text with ANSI codes preserved.
//
// ONLY does word wrapping - NO padding, NO background colors.
// Returns lines where each line is <= width visible chars.
// Active ANSI codes are preserved across line breaks.
func WrapTextWithAnsi(text string, width int) []string {
	if text == "" {
		return []string{""}
	}

	// Handle newlines by processing each line separately
	// Track ANSI state across lines so styles carry over after literal newlines
	inputLines := newlineSplitRegex.Split(text, -1)
	var result []string
	tracker := &ansiCodeTracker{}

	for _, inputLine := range inputLines {
		// Prepend active ANSI codes from previous lines (except for first line)
		prefix := ""
		if len(result) > 0 {
			prefix = tracker.GetActiveCodes()
		}
		result = append(result, wrapSingleLine(prefix+inputLine, width)...)
		// Update tracker with codes from this line for next iteration
		updateTrackerFromText(inputLine, tracker)
	}

	if len(result) == 0 {
		return []string{""}
	}
	return result
}

func wrapSingleLine(line string, width int) []string {
	if line == "" {
		return []string{""}
	}

	visibleLength := VisibleWidth(line)
	if visibleLength <= width {
		return []string{line}
	}

	var wrapped []string
	tracker := &ansiCodeTracker{}
	tokens := splitIntoTokensWithAnsi(line)

	var currentLine strings.Builder
	currentVisibleLength := 0

	for _, token := range tokens {
		tokenVisibleLength := VisibleWidth(token)
		isWhitespace := jsTrim(token) == ""

		// Token itself is too long - break it character by character
		if tokenVisibleLength > width && !isWhitespace {
			if currentLine.Len() > 0 {
				// Add specific reset for underline only (preserves background)
				if lineEndReset := tracker.GetLineEndReset(); lineEndReset != "" {
					currentLine.WriteString(lineEndReset)
				}
				wrapped = append(wrapped, currentLine.String())
				currentLine.Reset()
				currentVisibleLength = 0
			}

			// Break long token - breakLongWord handles its own resets
			broken := breakLongWord(token, width, tracker)
			for i := 0; i < len(broken)-1; i++ {
				wrapped = append(wrapped, broken[i])
			}
			currentLine.WriteString(broken[len(broken)-1])
			currentVisibleLength = VisibleWidth(currentLine.String())
			continue
		}

		// Check if adding this token would exceed width
		totalNeeded := currentVisibleLength + tokenVisibleLength

		if totalNeeded > width && currentVisibleLength > 0 {
			// Trim trailing whitespace, then add underline reset (not full reset, to preserve background)
			lineToWrap := jsTrimEnd(currentLine.String())
			if lineEndReset := tracker.GetLineEndReset(); lineEndReset != "" {
				lineToWrap += lineEndReset
			}
			wrapped = append(wrapped, lineToWrap)
			if isWhitespace {
				// Don't start new line with whitespace
				currentLine.Reset()
				currentLine.WriteString(tracker.GetActiveCodes())
				currentVisibleLength = 0
			} else {
				currentLine.Reset()
				currentLine.WriteString(tracker.GetActiveCodes() + token)
				currentVisibleLength = tokenVisibleLength
			}
		} else {
			// Add to current line
			currentLine.WriteString(token)
			currentVisibleLength += tokenVisibleLength
		}

		updateTrackerFromText(token, tracker)
	}

	if currentLine.Len() > 0 {
		// No reset at end of final line - let caller handle it
		wrapped = append(wrapped, currentLine.String())
	}

	// Trailing whitespace can cause lines to exceed the requested width
	if len(wrapped) > 0 {
		out := make([]string, len(wrapped))
		for i, line := range wrapped {
			out[i] = jsTrimEnd(line)
		}
		return out
	}
	return []string{""}
}

func breakLongWord(word string, width int, tracker *ansiCodeTracker) []string {
	var lines []string
	currentLine := tracker.GetActiveCodes()
	currentWidth := 0

	// First, separate ANSI codes from visible content
	// We need to handle ANSI codes specially since they're not graphemes
	type segment struct {
		isAnsi bool
		value  string
	}
	var segments []segment

	i := 0
	for i < len(word) {
		if ansi := ExtractAnsiCode(word, i); ansi != nil {
			segments = append(segments, segment{isAnsi: true, value: ansi.Code})
			i += ansi.Length
		} else {
			// Find the next ANSI code or end of string
			end := i
			for end < len(word) {
				if ExtractAnsiCode(word, end) != nil {
					break
				}
				end++
			}
			// Segment this non-ANSI portion into graphemes
			forEachGrapheme(word[i:end], func(g string) bool {
				segments = append(segments, segment{isAnsi: false, value: g})
				return true
			})
			i = end
		}
	}

	// Now process segments
	for _, seg := range segments {
		if seg.isAnsi {
			currentLine += seg.value
			tracker.Process(seg.value)
			continue
		}

		g := seg.value
		// Skip empty graphemes to avoid issues with string-width calculation
		if g == "" {
			continue
		}

		gw := VisibleWidth(g)

		if currentWidth+gw > width {
			// Add specific reset for underline only (preserves background)
			if lineEndReset := tracker.GetLineEndReset(); lineEndReset != "" {
				currentLine += lineEndReset
			}
			lines = append(lines, currentLine)
			currentLine = tracker.GetActiveCodes()
			currentWidth = 0
		}

		currentLine += g
		currentWidth += gw
	}

	if currentLine != "" {
		// No reset at end of final segment - caller handles continuation
		lines = append(lines, currentLine)
	}

	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// ApplyBackgroundToLine applies background color to a line, padding to full
// width.
func ApplyBackgroundToLine(line string, width int, bgFn func(text string) string) string {
	// Calculate padding needed
	visibleLen := VisibleWidth(line)
	paddingNeeded := width - visibleLen
	if paddingNeeded < 0 {
		paddingNeeded = 0
	}
	padding := strings.Repeat(" ", paddingNeeded)

	// Apply background to content + padding
	return bgFn(line + padding)
}

// ---------------------------------------------------------------------------
// Truncation
// ---------------------------------------------------------------------------

// truncateFragmentToWidth clips a fragment to maxWidth, returning the kept
// text and its visible width.
func truncateFragmentToWidth(text string, maxWidth int) (string, int) {
	if maxWidth <= 0 || text == "" {
		return "", 0
	}

	if isPrintableAscii(text) {
		if len(text) > maxWidth {
			text = text[:maxWidth]
		}
		return text, len(text)
	}

	hasAnsi := strings.Contains(text, "\x1b")
	hasTabs := strings.Contains(text, "\t")
	if !hasAnsi && !hasTabs {
		var result strings.Builder
		w := 0
		forEachGrapheme(text, func(segment string) bool {
			gw := graphemeWidth(segment)
			if w+gw > maxWidth {
				return false
			}
			result.WriteString(segment)
			w += gw
			return true
		})
		return result.String(), w
	}

	var result strings.Builder
	w := 0
	i := 0
	pendingAnsi := ""

	for i < len(text) {
		if ansi := ExtractAnsiCode(text, i); ansi != nil {
			pendingAnsi += ansi.Code
			i += ansi.Length
			continue
		}

		if text[i] == '\t' {
			if w+3 > maxWidth {
				break
			}
			if pendingAnsi != "" {
				result.WriteString(pendingAnsi)
				pendingAnsi = ""
			}
			result.WriteString("\t")
			w += 3
			i++
			continue
		}

		end := i
		for end < len(text) && text[end] != '\t' && ExtractAnsiCode(text, end) == nil {
			end++
		}

		stopped := false
		forEachGrapheme(text[i:end], func(segment string) bool {
			gw := graphemeWidth(segment)
			if w+gw > maxWidth {
				stopped = true
				return false
			}
			if pendingAnsi != "" {
				result.WriteString(pendingAnsi)
				pendingAnsi = ""
			}
			result.WriteString(segment)
			w += gw
			return true
		})
		if stopped {
			return result.String(), w
		}
		i = end
	}

	return result.String(), w
}

func finalizeTruncatedResult(
	prefix string,
	prefixWidth int,
	ellipsis string,
	ellipsisWidth int,
	maxWidth int,
	pad bool,
) string {
	reset := "\x1b[0m"
	hyperlinkClose := getActiveOsc8Close(prefix)
	visibleWidth := prefixWidth + ellipsisWidth
	var result string

	if ellipsis != "" {
		result = prefix + hyperlinkClose + reset + ellipsis + reset
	} else {
		result = prefix + hyperlinkClose + reset
	}

	if pad {
		padLen := maxWidth - visibleWidth
		if padLen < 0 {
			padLen = 0
		}
		result += strings.Repeat(" ", padLen)
	}
	return result
}

// TruncateToWidth truncates text to fit within a maximum visible width, adding
// ellipsis if needed. Optionally pad with spaces to reach exactly maxWidth.
// Properly handles ANSI escape codes (they don't count toward width).
//
// ellipsis: appended when truncating (pi default "..."); pad: pad result with
// spaces to exactly maxWidth (pi default false).
func TruncateToWidth(text string, maxWidth int, ellipsis string, pad bool) string {
	if maxWidth <= 0 {
		return ""
	}

	if text == "" {
		if pad {
			return strings.Repeat(" ", maxWidth)
		}
		return ""
	}

	ellipsisWidth := VisibleWidth(ellipsis)
	if ellipsisWidth >= maxWidth {
		textWidth := VisibleWidth(text)
		if textWidth <= maxWidth {
			if pad {
				return text + strings.Repeat(" ", maxWidth-textWidth)
			}
			return text
		}

		clippedText, clippedWidth := truncateFragmentToWidth(ellipsis, maxWidth)
		if clippedWidth == 0 {
			if pad {
				return strings.Repeat(" ", maxWidth)
			}
			return ""
		}
		return finalizeTruncatedResult("", 0, clippedText, clippedWidth, maxWidth, pad)
	}

	if isPrintableAscii(text) {
		if len(text) <= maxWidth {
			if pad {
				return text + strings.Repeat(" ", maxWidth-len(text))
			}
			return text
		}
		targetWidth := maxWidth - ellipsisWidth
		return finalizeTruncatedResult(text[:targetWidth], targetWidth, ellipsis, ellipsisWidth, maxWidth, pad)
	}

	targetWidth := maxWidth - ellipsisWidth
	var result strings.Builder
	pendingAnsi := ""
	visibleSoFar := 0
	keptWidth := 0
	keepContiguousPrefix := true
	overflowed := false
	exhaustedInput := false
	hasAnsi := strings.Contains(text, "\x1b")
	hasTabs := strings.Contains(text, "\t")

	if !hasAnsi && !hasTabs {
		forEachGrapheme(text, func(segment string) bool {
			w := graphemeWidth(segment)
			if keepContiguousPrefix && keptWidth+w <= targetWidth {
				result.WriteString(segment)
				keptWidth += w
			} else {
				keepContiguousPrefix = false
			}
			visibleSoFar += w
			if visibleSoFar > maxWidth {
				overflowed = true
				return false
			}
			return true
		})
		exhaustedInput = !overflowed
	} else {
		i := 0
		for i < len(text) {
			if ansi := ExtractAnsiCode(text, i); ansi != nil {
				pendingAnsi += ansi.Code
				i += ansi.Length
				continue
			}

			if text[i] == '\t' {
				if keepContiguousPrefix && keptWidth+3 <= targetWidth {
					if pendingAnsi != "" {
						result.WriteString(pendingAnsi)
						pendingAnsi = ""
					}
					result.WriteString("\t")
					keptWidth += 3
				} else {
					keepContiguousPrefix = false
					pendingAnsi = ""
				}
				visibleSoFar += 3
				if visibleSoFar > maxWidth {
					overflowed = true
					break
				}
				i++
				continue
			}

			end := i
			for end < len(text) && text[end] != '\t' && ExtractAnsiCode(text, end) == nil {
				end++
			}

			stopped := false
			forEachGrapheme(text[i:end], func(segment string) bool {
				w := graphemeWidth(segment)
				if keepContiguousPrefix && keptWidth+w <= targetWidth {
					if pendingAnsi != "" {
						result.WriteString(pendingAnsi)
						pendingAnsi = ""
					}
					result.WriteString(segment)
					keptWidth += w
				} else {
					keepContiguousPrefix = false
					pendingAnsi = ""
				}

				visibleSoFar += w
				if visibleSoFar > maxWidth {
					overflowed = true
					stopped = true
					return false
				}
				return true
			})
			if stopped {
				break
			}
			i = end
		}
		exhaustedInput = i >= len(text)
	}

	if !overflowed && exhaustedInput {
		if pad {
			padLen := maxWidth - visibleSoFar
			if padLen < 0 {
				padLen = 0
			}
			return text + strings.Repeat(" ", padLen)
		}
		return text
	}

	return finalizeTruncatedResult(result.String(), keptWidth, ellipsis, ellipsisWidth, maxWidth, pad)
}

// ---------------------------------------------------------------------------
// Column slicing
// ---------------------------------------------------------------------------

// SliceResult is the text and visible width produced by SliceWithWidth.
type SliceResult struct {
	Text  string
	Width int
}

// SliceByColumn extracts a range of visible columns from a line. Handles ANSI
// codes and wide chars. strict: if true, exclude wide chars at boundary that
// would extend past the range.
func SliceByColumn(line string, startCol int, length int, strict bool) string {
	return SliceWithWidth(line, startCol, length, strict).Text
}

// SliceWithWidth is like SliceByColumn but also returns the actual visible
// width of the result.
func SliceWithWidth(line string, startCol int, length int, strict bool) SliceResult {
	if length <= 0 {
		return SliceResult{Text: "", Width: 0}
	}
	endCol := startCol + length
	var result strings.Builder
	resultWidth := 0
	currentCol := 0
	i := 0
	pendingAnsi := ""

	for i < len(line) {
		if ansi := ExtractAnsiCode(line, i); ansi != nil {
			if currentCol >= startCol && currentCol < endCol {
				result.WriteString(ansi.Code)
			} else if currentCol < startCol {
				pendingAnsi += ansi.Code
			}
			i += ansi.Length
			continue
		}

		textEnd := i
		for textEnd < len(line) && ExtractAnsiCode(line, textEnd) == nil {
			textEnd++
		}

		done := false
		forEachGrapheme(line[i:textEnd], func(segment string) bool {
			w := graphemeWidth(segment)
			inRange := currentCol >= startCol && currentCol < endCol
			fits := !strict || currentCol+w <= endCol
			if inRange && fits {
				if pendingAnsi != "" {
					result.WriteString(pendingAnsi)
					pendingAnsi = ""
				}
				result.WriteString(segment)
				resultWidth += w
			}
			currentCol += w
			if currentCol >= endCol {
				done = true
				return false
			}
			return true
		})
		i = textEnd
		if done || currentCol >= endCol {
			break
		}
	}
	return SliceResult{Text: result.String(), Width: resultWidth}
}

// ---------------------------------------------------------------------------
// Segment extraction (overlay compositing)
// ---------------------------------------------------------------------------

// SegmentsResult carries the "before" and "after" slices produced by
// ExtractSegments.
type SegmentsResult struct {
	Before      string
	BeforeWidth int
	After       string
	AfterWidth  int
}

// ExtractSegments extracts "before" and "after" segments from a line in a
// single pass. Used for overlay compositing where we need content before and
// after the overlay region. Preserves styling from before the overlay that
// should affect content after it.
func ExtractSegments(line string, beforeEnd int, afterStart int, afterLen int, strictAfter bool) SegmentsResult {
	var before, after strings.Builder
	beforeWidth, afterWidth := 0, 0
	currentCol, i := 0, 0
	pendingAnsiBefore := ""
	afterStarted := false
	afterEnd := afterStart + afterLen

	// Track styling state so "after" inherits styling from before the overlay.
	// (pi pools one tracker; allocated per call here — behavior identical.)
	tracker := &ansiCodeTracker{}
	tracker.Clear()

	for i < len(line) {
		if ansi := ExtractAnsiCode(line, i); ansi != nil {
			// Track all SGR codes to know styling state at afterStart
			tracker.Process(ansi.Code)
			// Include ANSI codes in their respective segments
			if currentCol < beforeEnd {
				pendingAnsiBefore += ansi.Code
			} else if currentCol >= afterStart && currentCol < afterEnd && afterStarted {
				// Only include after we've started "after" (styling already prepended)
				after.WriteString(ansi.Code)
			}
			i += ansi.Length
			continue
		}

		textEnd := i
		for textEnd < len(line) && ExtractAnsiCode(line, textEnd) == nil {
			textEnd++
		}

		stopped := false
		forEachGrapheme(line[i:textEnd], func(segment string) bool {
			w := graphemeWidth(segment)

			if currentCol < beforeEnd && currentCol+w <= beforeEnd {
				if pendingAnsiBefore != "" {
					before.WriteString(pendingAnsiBefore)
					pendingAnsiBefore = ""
				}
				before.WriteString(segment)
				beforeWidth += w
			} else if currentCol >= afterStart && currentCol < afterEnd {
				fits := !strictAfter || currentCol+w <= afterEnd
				if fits {
					// On first "after" grapheme, prepend inherited styling from before overlay
					if !afterStarted {
						after.WriteString(tracker.GetActiveCodes())
						afterStarted = true
					}
					after.WriteString(segment)
					afterWidth += w
				}
			}

			currentCol += w
			// Early exit: done with "before" only, or done with both segments
			if afterLen <= 0 {
				if currentCol >= beforeEnd {
					stopped = true
					return false
				}
			} else if currentCol >= afterEnd {
				stopped = true
				return false
			}
			return true
		})
		if stopped {
			break
		}
		i = textEnd
		if afterLen <= 0 {
			if currentCol >= beforeEnd {
				break
			}
		} else if currentCol >= afterEnd {
			break
		}
	}

	return SegmentsResult{
		Before:      before.String(),
		BeforeWidth: beforeWidth,
		After:       after.String(),
		AfterWidth:  afterWidth,
	}
}
