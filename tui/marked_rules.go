// tui/marked_rules.go — regular-expression rule tables for the marked Lexer
// port (pi consumes npm marked@18.0.5; pig vendors the rule table).
//
// GENERATION: the pattern strings below are the FINAL composed regex
// sources dumped from node (`Lexer` default options: gfm=true, breaks=false,
// pedantic=false) — i.e. exactly what marked's internal regex-builder chain
// (`d()` + string replaces) produces at module load. Regenerating after a
// marked upgrade: dump `new Lexer({}).tokenizer.rules` with
// `v.source` / `v.flags` and re-emit. The normal/pedantic block+inline sets
// are not ported (pi never sets those options); declared in the file header
// of marked.go.
//
// Engine note: RE2 cannot run these (backreferences \1 / \k<a>, lookaheads),
// so the tables compile with github.com/dlclark/regexp2 in ECMAScript mode
// (verified: \s incl. NBSP/BOM, \p{L/P/N/S}, named groups, backrefs,
// IgnoreCase).
package tui

import (
	"fmt"

	"github.com/dlclark/regexp2"
)

// markedOther mirrors marked's `other` rule table (m).
type markedOther struct {
	CodeRemoveIndent         *regexp2.Regexp
	OutputLinkReplace        *regexp2.Regexp
	IndentCodeCompensation   *regexp2.Regexp
	BeginningSpace           *regexp2.Regexp
	EndingHash               *regexp2.Regexp
	StartingSpaceChar        *regexp2.Regexp
	EndingSpaceChar          *regexp2.Regexp
	NonSpaceChar             *regexp2.Regexp
	NewLineCharGlobal        *regexp2.Regexp
	TabCharGlobal            *regexp2.Regexp
	MultipleSpaceGlobal      *regexp2.Regexp
	BlankLine                *regexp2.Regexp
	DoubleBlankLine          *regexp2.Regexp
	BlockquoteStart          *regexp2.Regexp
	BlockquoteSetextReplace  *regexp2.Regexp
	BlockquoteSetextReplace2 *regexp2.Regexp
	ListReplaceNesting       *regexp2.Regexp
	ListIsTask               *regexp2.Regexp
	ListReplaceTask          *regexp2.Regexp
	ListTaskCheckbox         *regexp2.Regexp
	AnyLine                  *regexp2.Regexp
	HrefBrackets             *regexp2.Regexp
	TableDelimiter           *regexp2.Regexp
	TableAlignChars          *regexp2.Regexp
	TableRowBlankLine        *regexp2.Regexp
	TableAlignRight          *regexp2.Regexp
	TableAlignCenter         *regexp2.Regexp
	TableAlignLeft           *regexp2.Regexp
	StartATag                *regexp2.Regexp
	EndATag                  *regexp2.Regexp
	StartPreScriptTag        *regexp2.Regexp
	EndPreScriptTag          *regexp2.Regexp
	StartAngleBracket        *regexp2.Regexp
	EndAngleBracket          *regexp2.Regexp
	UnicodeAlphaNumeric      *regexp2.Regexp
	FindPipe                 *regexp2.Regexp
	SplitPipe                *regexp2.Regexp
	SlashPipe                *regexp2.Regexp
	CarriageReturn           *regexp2.Regexp
	SpaceLine                *regexp2.Regexp
	NotSpaceStart            *regexp2.Regexp
	EndingNewline            *regexp2.Regexp
}

const markedSrcOtherCodeRemoveIndent = "^(?: {1,4}| {0,3}\\t)"
const markedSrcOtherOutputLinkReplace = "\\\\([\\[\\]])"
const markedSrcOtherIndentCodeCompensation = "^(\\s+)(?:```)"
const markedSrcOtherBeginningSpace = "^\\s+"
const markedSrcOtherEndingHash = "#$"
const markedSrcOtherStartingSpaceChar = "^ "
const markedSrcOtherEndingSpaceChar = " $"
const markedSrcOtherNonSpaceChar = "[^ ]"
const markedSrcOtherNewLineCharGlobal = "\\n"
const markedSrcOtherTabCharGlobal = "\\t"
const markedSrcOtherMultipleSpaceGlobal = "\\s+"
const markedSrcOtherBlankLine = "^[ \\t]*$"
const markedSrcOtherDoubleBlankLine = "\\n[ \\t]*\\n[ \\t]*$"
const markedSrcOtherBlockquoteStart = "^ {0,3}>"
const markedSrcOtherBlockquoteSetextReplace = "\\n {0,3}((?:=+|-+) *)(?=\\n|$)"
const markedSrcOtherBlockquoteSetextReplace2 = "^ {0,3}>[ \\t]?"
const markedSrcOtherListReplaceNesting = "^ {1,4}(?=( {4})*[^ ])"
const markedSrcOtherListIsTask = "^\\[[ xX]\\] +\\S"
const markedSrcOtherListReplaceTask = "^\\[[ xX]\\] +"
const markedSrcOtherListTaskCheckbox = "\\[[ xX]\\]"
const markedSrcOtherAnyLine = "\\n.*\\n"
const markedSrcOtherHrefBrackets = "^<(.*)>$"
const markedSrcOtherTableDelimiter = "[:|]"
const markedSrcOtherTableAlignChars = "^\\||\\| *$"
const markedSrcOtherTableRowBlankLine = "\\n[ \\t]*$"
const markedSrcOtherTableAlignRight = "^ *-+: *$"
const markedSrcOtherTableAlignCenter = "^ *:-+: *$"
const markedSrcOtherTableAlignLeft = "^ *:-+ *$"
const markedSrcOtherStartATag = "^<a "
const markedSrcOtherEndATag = "^<\\/a>"
const markedSrcOtherStartPreScriptTag = "^<(pre|code|kbd|script)(\\s|>)"
const markedSrcOtherEndPreScriptTag = "^<\\/(pre|code|kbd|script)(\\s|>)"
const markedSrcOtherStartAngleBracket = "^<"
const markedSrcOtherEndAngleBracket = ">$"
const markedSrcOtherUnicodeAlphaNumeric = "[\\p{L}\\p{N}]"
const markedSrcOtherFindPipe = "\\|"
const markedSrcOtherSplitPipe = " \\|"
const markedSrcOtherSlashPipe = "\\\\\\|"
const markedSrcOtherCarriageReturn = "\\r\\n|\\r"
const markedSrcOtherSpaceLine = "^ +$"
const markedSrcOtherNotSpaceStart = "^\\S*"
const markedSrcOtherEndingNewline = "\\n$"

const markedSrcBlockBlockquote = "^( {0,3}> ?(([^\\n]+(?:\\n(?! {0,3}((?:-[\\t ]*){3,}|(?:_[ \\t]*){3,}|(?:\\*[ \\t]*){3,})(?:\\n+|$)| {0,3}#{1,6}(?:\\s|$)| {0,3}>| {0,3}(?:`{3,}(?=[^`\\n]*\\n)|~{3,})[^\\n]*\\n| {0,3}(?:[*+-]|1[.)])[ \\t]+[^ \\t\\n]|<\\/?(?:address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h[1-6]|head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|meta|nav|noframes|ol|optgroup|option|p|param|search|section|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul)(?: +|\\n|\\/?>)|<(?:script|pre|style|textarea|!--)| +\\n)[^\\n]+)*)|[^\\n]*)(?:\\n|$))+"
const markedSrcBlockCode = "^((?: {4}| {0,3}\\t)[^\\n]+(?:\\n(?:[ \\t]*(?:\\n|$))*)?)+"
const markedSrcBlockDef = "^ {0,3}\\[((?!\\s*\\])(?:\\\\[\\s\\S]|[^\\[\\]\\\\])+)\\]: *(?:\\n[ \\t]*)?([^<\\s][^\\s]*|<.*?>)(?:(?: +(?:\\n[ \\t]*)?| *\\n[ \\t]*)((?:\"(?:\\\\\"?|[^\"\\\\])*\"|'[^'\\n]*(?:\\n[^'\\n]+)*\\n?'|\\([^()]*\\))))? *(?:\\n+|$)"
const markedSrcBlockFences = "^ {0,3}(`{3,}(?=[^`\\n]*(?:\\n|$))|~{3,})([^\\n]*)(?:\\n|$)(?:|([\\s\\S]*?)(?:\\n|$))(?: {0,3}\\1[~`]* *(?=\\n|$)|$)"
const markedSrcBlockHeading = "^ {0,3}(#{1,6})(?=\\s|$)(.*)(?:\\n+|$)"
const markedSrcBlockHr = "^ {0,3}((?:-[\\t ]*){3,}|(?:_[ \\t]*){3,}|(?:\\*[ \\t]*){3,})(?:\\n+|$)"
const markedSrcBlockHtml = "^ {0,3}(?:<(script|pre|style|textarea)[\\s>][\\s\\S]*?(?:<\\/\\1>[^\\n]*\\n+|$)|<!--(?:-?>|[\\s\\S]*?(?:-->|$))[^\\n]*(\\n+|$)|<\\?[\\s\\S]*?(?:\\?>\\n*|$)|<![A-Z][\\s\\S]*?(?:>\\n*|$)|<!\\[CDATA\\[[\\s\\S]*?(?:\\]\\]>\\n*|$)|<\\/?(address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h[1-6]|head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|meta|nav|noframes|ol|optgroup|option|p|param|search|section|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul)(?: +|\\n|\\/?>)[\\s\\S]*?(?:(?:\\n[ \t]*)+\\n|$)|<(?!script|pre|style|textarea)([a-z][\\w-]*)(?: +[a-zA-Z:_][\\w.:-]*(?: *= *\"[^\"\\n]*\"| *= *'[^'\\n]*'| *= *[^\\s\"'=<>`]+)?)*? *\\/?>(?=[ \\t]*(?:\\n|$))[\\s\\S]*?(?:(?:\\n[ \t]*)+\\n|$)|<\\/(?!script|pre|style|textarea)[a-z][\\w-]*\\s*>(?=[ \\t]*(?:\\n|$))[\\s\\S]*?(?:(?:\\n[ \t]*)+\\n|$))"
const markedSrcBlockLheading = "^(?! {0,3}(?:[*+-]|\\d{1,9}[.)]) |(?: {4}| {0,3}\\t)| {0,3}(?:`{3,}|~{3,})| {0,3}>| {0,3}#{1,6}| {0,3}<[^\\n>]+>\\n| {0,3}\\|?(?:[:\\- ]*\\|)+[\\:\\- ]*\\n)((?:.|\\n(?!\\s*?\\n| {0,3}(?:[*+-]|\\d{1,9}[.)]) |(?: {4}| {0,3}\\t)| {0,3}(?:`{3,}|~{3,})| {0,3}>| {0,3}#{1,6}| {0,3}<[^\\n>]+>\\n| {0,3}\\|?(?:[:\\- ]*\\|)+[\\:\\- ]*\\n))+?)\\n {0,3}(=+|-+) *(?:\\n+|$)"
const markedSrcBlockList = "^( {0,3}(?:[*+-]|\\d{1,9}[.)]))([ \\t][^\\n]*?)?(?:\\n|$)"
const markedSrcBlockNewline = "^(?:[ \\t]*(?:\\n|$))+"
const markedSrcBlockParagraph = "^([^\\n]+(?:\\n(?! {0,3}((?:-[\\t ]*){3,}|(?:_[ \\t]*){3,}|(?:\\*[ \\t]*){3,})(?:\\n+|$)| {0,3}#{1,6}(?:\\s|$)| {0,3}>| {0,3}(?:`{3,}(?=[^`\\n]*\\n)|~{3,})[^\\n]*\\n| {0,3}(?:[*+-]|1[.)])[ \\t]+[^ \\t\\n]|<\\/?(?:address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h[1-6]|head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|meta|nav|noframes|ol|optgroup|option|p|param|search|section|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul)(?: +|\\n|\\/?>)|<(?:script|pre|style|textarea|!--)| *([^\\n ].*)\\n {0,3}((?:\\| *)?:?-+:? *(?:\\| *:?-+:? *)*(?:\\| *)?)(?:\\n((?:(?! *\\n| {0,3}((?:-[\\t ]*){3,}|(?:_[ \\t]*){3,}|(?:\\*[ \\t]*){3,})(?:\\n+|$)| {0,3}#{1,6}(?:\\s|$)| {0,3}>|(?: {4}| {0,3}\t)[^\\n]| {0,3}(?:`{3,}(?=[^`\\n]*\\n)|~{3,})[^\\n]*\\n| {0,3}(?:[*+-]|1[.)])[ \\t]|<\\/?(?:address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h[1-6]|head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|meta|nav|noframes|ol|optgroup|option|p|param|search|section|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul)(?: +|\\n|\\/?>)|<(?:script|pre|style|textarea|!--)).*(?:\\n|$))*)\\n*|$)| +\\n)[^\\n]+)*)"
const markedSrcBlockTable = "^ *([^\\n ].*)\\n {0,3}((?:\\| *)?:?-+:? *(?:\\| *:?-+:? *)*(?:\\| *)?)(?:\\n((?:(?! *\\n| {0,3}((?:-[\\t ]*){3,}|(?:_[ \\t]*){3,}|(?:\\*[ \\t]*){3,})(?:\\n+|$)| {0,3}#{1,6}(?:\\s|$)| {0,3}>|(?: {4}| {0,3}\t)[^\\n]| {0,3}(?:`{3,}(?=[^`\\n]*\\n)|~{3,})[^\\n]*\\n| {0,3}(?:[*+-]|1[.)])[ \\t]|<\\/?(?:address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h[1-6]|head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|meta|nav|noframes|ol|optgroup|option|p|param|search|section|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul)(?: +|\\n|\\/?>)|<(?:script|pre|style|textarea|!--)).*(?:\\n|$))*)\\n*|$)"
const markedSrcBlockText = "^[^\\n]+"

const markedSrcInline_backpedal = "(?:[^?!.,:;*_'\"~()&]+|\\([^)]*\\)|&(?![a-zA-Z0-9]+;$)|[?!.,:;*_'\"~)]+(?!$))+"
const markedSrcInlineAnyPunctuation = "\\\\([\\p{P}\\p{S}])"
const markedSrcInlineAutolink = "^<([a-zA-Z][a-zA-Z0-9+.-]{1,31}:[^\\s\\x00-\\x1f<>]*|[a-zA-Z0-9.!#$%&'*+/=?_`{|}~-]+(@)[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+(?![-_]))>"
const markedSrcInlineBlockSkip = "\\[(?:[^\\[\\]`]|(?<a>`+)[^`]+\\k<a>(?!`))*?\\]\\((?:\\\\[\\s\\S]|[^\\\\\\(\\)]|\\((?:\\\\[\\s\\S]|[^\\\\\\(\\)])*\\))*\\)|(?<!`)()(?<b>`+)[^`]+\\k<b>(?!`)|<(?! )[^<>]*?>"
const markedSrcInlineBr = "^( {2,}|\\\\)\\n(?!\\s*$)"
const markedSrcInlineCode = "^(`+)([^`]|[^`][\\s\\S]*?[^`])\\1(?!`)"
const markedSrcInlineDel = "^(~~?)(?=[^\\s~])((?:\\\\[\\s\\S]|[^\\\\])*?(?:\\\\[\\s\\S]|[^\\s~\\\\]))\\1(?=[^~]|$)"
const markedSrcInlineDelLDelim = "^~~?(?:((?!~)[\\p{P}\\p{S}])|[^\\s~])"
const markedSrcInlineDelRDelim = "^[^~]+(?=[^~])|(?!~)[\\p{P}\\p{S}](~~?)(?=[\\s]|$)|[^\\s\\p{P}\\p{S}](~~?)(?!~)(?=[\\s\\p{P}\\p{S}]|$)|(?!~)[\\s\\p{P}\\p{S}](~~?)(?=[^\\s\\p{P}\\p{S}])|[\\s](~~?)(?!~)(?=[\\p{P}\\p{S}])|(?!~)[\\p{P}\\p{S}](~~?)(?!~)(?=[\\p{P}\\p{S}])|[^\\s\\p{P}\\p{S}](~~?)(?=[^\\s\\p{P}\\p{S}])"
const markedSrcInlineEmStrongLDelim = "^(?:\\*+(?:((?!\\*)(?!~)[\\p{P}\\p{S}])|([^\\s*]))?)|^_+(?:((?!_)(?!~)[\\p{P}\\p{S}])|([^\\s_]))?"
const markedSrcInlineEmStrongRDelimAst = "^[^_*]*?__[^_*]*?\\*[^_*]*?(?=__)|[^*]+(?=[^*])|(?!\\*)(?!~)[\\p{P}\\p{S}](\\*+)(?=[\\s]|$)|(?:[^\\s\\p{P}\\p{S}]|~)(\\*+)(?!\\*)(?=(?!~)[\\s\\p{P}\\p{S}]|$)|(?!\\*)(?!~)[\\s\\p{P}\\p{S}](\\*+)(?=(?:[^\\s\\p{P}\\p{S}]|~))|[\\s](\\*+)(?!\\*)(?=(?!~)[\\p{P}\\p{S}])|(?!\\*)(?!~)[\\p{P}\\p{S}](\\*+)(?!\\*)(?=(?!~)[\\p{P}\\p{S}])|(?:[^\\s\\p{P}\\p{S}]|~)(\\*+)(?=(?:[^\\s\\p{P}\\p{S}]|~))"
const markedSrcInlineEmStrongRDelimUnd = "^[^_*]*?\\*\\*[^_*]*?_[^_*]*?(?=\\*\\*)|[^_]+(?=[^_])|(?!_)[\\p{P}\\p{S}](_+)(?=[\\s]|$)|[^\\s\\p{P}\\p{S}](_+)(?!_)(?=[\\s\\p{P}\\p{S}]|$)|(?!_)[\\s\\p{P}\\p{S}](_+)(?=[^\\s\\p{P}\\p{S}])|[\\s](_+)(?!_)(?=[\\p{P}\\p{S}])|(?!_)[\\p{P}\\p{S}](_+)(?!_)(?=[\\p{P}\\p{S}])"
const markedSrcInlineEscape = "^\\\\([!\"#$%&'()*+,\\-./:;<=>?@\\[\\]\\\\^_`{|}~])"
const markedSrcInlineLink = "^!?\\[((?:\\[(?:\\\\[\\s\\S]|[^\\[\\]\\\\])*\\]|\\\\[\\s\\S]|`+(?!`)[^`]*?`+(?!`)|``+(?=\\])|[^\\[\\]\\\\`])*?)\\]\\(\\s*(<(?:\\\\.|[^\\n<>\\\\])+>|[^ \\t\\n\\x00-\\x1f]*)(?:(?:[ \\t]+(?:\\n[ \\t]*)?|\\n[ \\t]*)(\"(?:\\\\\"?|[^\"\\\\])*\"|'(?:\\\\'?|[^'\\\\])*'|\\((?:\\\\\\)?|[^)\\\\])*\\)))?\\s*\\)"
const markedSrcInlineNolink = "^!?\\[((?!\\s*\\])(?:\\\\[\\s\\S]|[^\\[\\]\\\\])+)\\](?:\\[\\])?"
const markedSrcInlinePunctuation = "^((?![*_])[\\s\\p{P}\\p{S}])"
const markedSrcInlineReflink = "^!?\\[((?:\\[(?:\\\\[\\s\\S]|[^\\[\\]\\\\])*\\]|\\\\[\\s\\S]|`+(?!`)[^`]*?`+(?!`)|``+(?=\\])|[^\\[\\]\\\\`])*?)\\]\\[((?!\\s*\\])(?:\\\\[\\s\\S]|[^\\[\\]\\\\])+)\\]"
const markedSrcInlineReflinkSearch = "!?\\[((?:\\[(?:\\\\[\\s\\S]|[^\\[\\]\\\\])*\\]|\\\\[\\s\\S]|`+(?!`)[^`]*?`+(?!`)|``+(?=\\])|[^\\[\\]\\\\`])*?)\\]\\[((?!\\s*\\])(?:\\\\[\\s\\S]|[^\\[\\]\\\\])+)\\]|!?\\[((?!\\s*\\])(?:\\\\[\\s\\S]|[^\\[\\]\\\\])+)\\](?:\\[\\])?(?!\\()"
const markedSrcInlineTag = "^<!--(?:-?>|[\\s\\S]*?-->)|^<\\/[a-zA-Z][\\w:-]*\\s*>|^<[a-zA-Z][\\w-]*(?:\\s+[a-zA-Z:_][\\w.:-]*(?:\\s*=\\s*\"[^\"]*\"|\\s*=\\s*'[^']*'|\\s*=\\s*[^\\s\"'=<>`]+)?)*?\\s*\\/?>|^<\\?[\\s\\S]*?\\?>|^<![a-zA-Z]+\\s[\\s\\S]*?>|^<!\\[CDATA\\[[\\s\\S]*?\\]\\]>"
const markedSrcInlineText = "^([`~]+|[^`~])(?:(?= {2,}\\n)|(?=[a-zA-Z0-9.!#$%&'*+\\/=?_`{\\|}~-]+@)|[\\s\\S]*?(?:(?=[\\\\<!\\[`*~_]|\\b_|[hH][tT][tT][pP][sS]?|[fF][tT][pP]:\\/\\/|www\\.|$)|[^ ](?= {2,}\\n)|[^a-zA-Z0-9.!#$%&'*+\\/=?_`{\\|}~-](?=[a-zA-Z0-9.!#$%&'*+\\/=?_`{\\|}~-]+@)))"
const markedSrcInlineUrl = "^((?:[hH][tT][tT][pP][sS]?|[fF][tT][pP]):\\/\\/|www\\.)(?:[a-zA-Z0-9\\-]+\\.?)+[^\\s<]*|^[A-Za-z0-9._+-]+(@)[a-zA-Z0-9-_]+(?:\\.[a-zA-Z0-9-_]*[a-zA-Z0-9])+(?![-_])"

// Parameterized list-context regexes (marked memoizes them by indent; the
// templates below are the same factories).
const (
	markedListItemRegexSrc   = "^( {0,3}%s)((?:[\t ][^\n]*)?(?:\n|$))"
	markedNextBulletRegexSrc = "^ {0,%d}(?:[*+-]|\\d{1,9}[.)])((?:[\t ][^\n]*)?(?:\n|$))"
	markedHrRegexSrc         = "^ {0,%d}((?:- *){3,}|(?:_ *){3,}|(?:\\* *){3,})(?:\n+|$)"
	markedFencesBeginSrc     = "^ {0,%d}(?:```|~~~)"
	markedHeadingBeginSrc    = "^ {0,%d}#"
	markedHtmlBeginSrc       = "^ {0,%d}<(?:[a-z].*>|!--)"
	markedBlockquoteBeginSrc = "^ {0,%d}>"
)

// markedCompile compiles a dumped pattern source with JS-equivalent flags.
func markedCompile(src string, flags string) *regexp2.Regexp {
	opts := regexp2.ECMAScript
	if flags != "" && containsFlag(flags, 'i') {
		opts |= regexp2.IgnoreCase
	}
	if flags != "" && containsFlag(flags, 'm') {
		opts |= regexp2.Multiline
	}
	return markedCompileRegexOptions(src, regexp2.RegexOptions(opts))
}

func markedCompileRegexOptions(src string, opts regexp2.RegexOptions) *regexp2.Regexp {
	re, err := regexp2.Compile(src, opts)
	if err != nil {
		panic("tui/marked: bad rule " + src + ": " + err.Error())
	}
	return re
}

func containsFlag(flags string, f byte) bool {
	for i := 0; i < len(flags); i++ {
		if flags[i] == f {
			return true
		}
	}
	return false
}

// newMarkedOther compiles the `other` table (marked: m).
func newMarkedOther() *markedOther {
	return &markedOther{
		CodeRemoveIndent:         markedCompile(markedSrcOtherCodeRemoveIndent, "gm"),
		OutputLinkReplace:        markedCompile(markedSrcOtherOutputLinkReplace, "g"),
		IndentCodeCompensation:   markedCompile(markedSrcOtherIndentCodeCompensation, ""),
		BeginningSpace:           markedCompile(markedSrcOtherBeginningSpace, ""),
		EndingHash:               markedCompile(markedSrcOtherEndingHash, ""),
		StartingSpaceChar:        markedCompile(markedSrcOtherStartingSpaceChar, ""),
		EndingSpaceChar:          markedCompile(markedSrcOtherEndingSpaceChar, ""),
		NonSpaceChar:             markedCompile(markedSrcOtherNonSpaceChar, ""),
		NewLineCharGlobal:        markedCompile(markedSrcOtherNewLineCharGlobal, "g"),
		TabCharGlobal:            markedCompile(markedSrcOtherTabCharGlobal, "g"),
		MultipleSpaceGlobal:      markedCompile(markedSrcOtherMultipleSpaceGlobal, "g"),
		BlankLine:                markedCompile(markedSrcOtherBlankLine, ""),
		DoubleBlankLine:          markedCompile(markedSrcOtherDoubleBlankLine, ""),
		BlockquoteStart:          markedCompile(markedSrcOtherBlockquoteStart, ""),
		BlockquoteSetextReplace:  markedCompile(markedSrcOtherBlockquoteSetextReplace, "g"),
		BlockquoteSetextReplace2: markedCompile(markedSrcOtherBlockquoteSetextReplace2, "gm"),
		ListReplaceNesting:       markedCompile(markedSrcOtherListReplaceNesting, "g"),
		ListIsTask:               markedCompile(markedSrcOtherListIsTask, ""),
		ListReplaceTask:          markedCompile(markedSrcOtherListReplaceTask, ""),
		ListTaskCheckbox:         markedCompile(markedSrcOtherListTaskCheckbox, ""),
		AnyLine:                  markedCompile(markedSrcOtherAnyLine, ""),
		HrefBrackets:             markedCompile(markedSrcOtherHrefBrackets, ""),
		TableDelimiter:           markedCompile(markedSrcOtherTableDelimiter, ""),
		TableAlignChars:          markedCompile(markedSrcOtherTableAlignChars, "g"),
		TableRowBlankLine:        markedCompile(markedSrcOtherTableRowBlankLine, ""),
		TableAlignRight:          markedCompile(markedSrcOtherTableAlignRight, ""),
		TableAlignCenter:         markedCompile(markedSrcOtherTableAlignCenter, ""),
		TableAlignLeft:           markedCompile(markedSrcOtherTableAlignLeft, ""),
		StartATag:                markedCompile(markedSrcOtherStartATag, "i"),
		EndATag:                  markedCompile(markedSrcOtherEndATag, "i"),
		StartPreScriptTag:        markedCompile(markedSrcOtherStartPreScriptTag, "i"),
		EndPreScriptTag:          markedCompile(markedSrcOtherEndPreScriptTag, "i"),
		StartAngleBracket:        markedCompile(markedSrcOtherStartAngleBracket, ""),
		EndAngleBracket:          markedCompile(markedSrcOtherEndAngleBracket, ""),
		UnicodeAlphaNumeric:      markedCompile(markedSrcOtherUnicodeAlphaNumeric, "u"),
		FindPipe:                 markedCompile(markedSrcOtherFindPipe, "g"),
		SplitPipe:                markedCompile(markedSrcOtherSplitPipe, ""),
		SlashPipe:                markedCompile(markedSrcOtherSlashPipe, "g"),
		CarriageReturn:           markedCompile(markedSrcOtherCarriageReturn, "g"),
		SpaceLine:                markedCompile(markedSrcOtherSpaceLine, "gm"),
		NotSpaceStart:            markedCompile(markedSrcOtherNotSpaceStart, ""),
		EndingNewline:            markedCompile(markedSrcOtherEndingNewline, ""),
	}
}

// markedBlockRules mirrors marked's gfm block rule set (D.gfm).
type markedBlockRules struct {
	Blockquote *regexp2.Regexp
	Code       *regexp2.Regexp
	Def        *regexp2.Regexp
	Fences     *regexp2.Regexp
	Heading    *regexp2.Regexp
	Hr         *regexp2.Regexp
	Html       *regexp2.Regexp
	Lheading   *regexp2.Regexp
	List       *regexp2.Regexp
	Newline    *regexp2.Regexp
	Paragraph  *regexp2.Regexp
	Table      *regexp2.Regexp
	Text       *regexp2.Regexp
}

// newMarkedBlockRules compiles the gfm block rules.
func newMarkedBlockRules() *markedBlockRules {
	return &markedBlockRules{
		Blockquote: markedCompile(markedSrcBlockBlockquote, ""),
		Code:       markedCompile(markedSrcBlockCode, ""),
		Def:        markedCompile(markedSrcBlockDef, ""),
		Fences:     markedCompile(markedSrcBlockFences, ""),
		Heading:    markedCompile(markedSrcBlockHeading, ""),
		Hr:         markedCompile(markedSrcBlockHr, ""),
		Html:       markedCompile(markedSrcBlockHtml, "i"),
		Lheading:   markedCompile(markedSrcBlockLheading, ""),
		List:       markedCompile(markedSrcBlockList, ""),
		Newline:    markedCompile(markedSrcBlockNewline, ""),
		Paragraph:  markedCompile(markedSrcBlockParagraph, ""),
		Table:      markedCompile(markedSrcBlockTable, ""),
		Text:       markedCompile(markedSrcBlockText, ""),
	}
}

// markedInlineRules mirrors marked's gfm inline rule set (A.gfm).
type markedInlineRules struct {
	_backpedal        *regexp2.Regexp
	AnyPunctuation    *regexp2.Regexp
	Autolink          *regexp2.Regexp
	BlockSkip         *regexp2.Regexp
	Br                *regexp2.Regexp
	Code              *regexp2.Regexp
	Del               *regexp2.Regexp
	DelLDelim         *regexp2.Regexp
	DelRDelim         *regexp2.Regexp
	EmStrongLDelim    *regexp2.Regexp
	EmStrongRDelimAst *regexp2.Regexp
	EmStrongRDelimUnd *regexp2.Regexp
	Escape            *regexp2.Regexp
	Link              *regexp2.Regexp
	Nolink            *regexp2.Regexp
	Punctuation       *regexp2.Regexp
	Reflink           *regexp2.Regexp
	ReflinkSearch     *regexp2.Regexp
	Tag               *regexp2.Regexp
	Text              *regexp2.Regexp
	Url               *regexp2.Regexp
}

// newMarkedInlineRules compiles the gfm inline rules.
func newMarkedInlineRules() *markedInlineRules {
	return &markedInlineRules{
		_backpedal:        markedCompile(markedSrcInline_backpedal, ""),
		AnyPunctuation:    markedCompile(markedSrcInlineAnyPunctuation, "gu"),
		Autolink:          markedCompile(markedSrcInlineAutolink, ""),
		BlockSkip:         markedCompile(markedSrcInlineBlockSkip, "g"),
		Br:                markedCompile(markedSrcInlineBr, ""),
		Code:              markedCompile(markedSrcInlineCode, ""),
		Del:               markedCompile(markedSrcInlineDel, ""),
		DelLDelim:         markedCompile(markedSrcInlineDelLDelim, "u"),
		DelRDelim:         markedCompile(markedSrcInlineDelRDelim, "gu"),
		EmStrongLDelim:    markedCompile(markedSrcInlineEmStrongLDelim, "u"),
		EmStrongRDelimAst: markedCompile(markedSrcInlineEmStrongRDelimAst, "gu"),
		EmStrongRDelimUnd: markedCompile(markedSrcInlineEmStrongRDelimUnd, "gu"),
		Escape:            markedCompile(markedSrcInlineEscape, ""),
		Link:              markedCompile(markedSrcInlineLink, ""),
		Nolink:            markedCompile(markedSrcInlineNolink, ""),
		Punctuation:       markedCompile(markedSrcInlinePunctuation, "u"),
		Reflink:           markedCompile(markedSrcInlineReflink, ""),
		ReflinkSearch:     markedCompile(markedSrcInlineReflinkSearch, "g"),
		Tag:               markedCompile(markedSrcInlineTag, ""),
		Text:              markedCompile(markedSrcInlineText, ""),
		Url:               markedCompile(markedSrcInlineUrl, ""),
	}
}

// markedListItemRegex is marked's other.listItemRegex(bullet) (bullet is a
// pattern source, not an indent).
func markedListItemRegex(bullet string) *regexp2.Regexp {
	return markedCompile(fmt.Sprintf(markedListItemRegexSrc, bullet), "")
}

// markedIndentParam is marked's memoizer clamp (E in the minified source):
// the factories are keyed on max(0, min(3, indent-1)) — the decrement is what
// lets nested list markers at exactly the item indent continue the item.
func markedIndentParam(indent int) int {
	n := indent - 1
	if n < 0 {
		n = 0
	}
	if n > 3 {
		n = 3
	}
	return n
}

// markedIndentRegex family (marked's nextBulletRegex/hrRegex/... memoized
// factories, parameterized by list indent).
func markedNextBulletRegex(indent int) *regexp2.Regexp {
	return markedCompile(fmt.Sprintf(markedNextBulletRegexSrc, markedIndentParam(indent)), "")
}

func markedHrRegex(indent int) *regexp2.Regexp {
	return markedCompile(fmt.Sprintf(markedHrRegexSrc, markedIndentParam(indent)), "")
}

func markedFencesBeginRegex(indent int) *regexp2.Regexp {
	return markedCompile(fmt.Sprintf(markedFencesBeginSrc, markedIndentParam(indent)), "")
}

func markedHeadingBeginRegex(indent int) *regexp2.Regexp {
	return markedCompile(fmt.Sprintf(markedHeadingBeginSrc, markedIndentParam(indent)), "")
}

func markedHtmlBeginRegex(indent int) *regexp2.Regexp {
	return markedCompileRegexOptions(fmt.Sprintf(markedHtmlBeginSrc, indent), regexp2.RegexOptions(regexp2.ECMAScript|regexp2.IgnoreCase))
}

func markedBlockquoteBeginRegex(indent int) *regexp2.Regexp {
	return markedCompile(fmt.Sprintf(markedBlockquoteBeginSrc, markedIndentParam(indent)), "")
}
