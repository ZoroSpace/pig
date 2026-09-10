// tui/test_themes_test.go (pi: packages/tui/test/test-themes.ts)
//
// Default themes for TUI tests using chalk level 3 — hand-written ANSI bytes
// (testEditorTheme precedent; byte forms verified against chalk@level 3).
package tui

// chalk level-3 fragments
const (
	chalkBoldOpen  = "\x1b[1m"
	chalkBoldClose = "\x1b[22m"

	chalkFgBlack   = "\x1b[30m"
	chalkFgRed     = "\x1b[31m"
	chalkFgGreen   = "\x1b[32m"
	chalkFgYellow  = "\x1b[33m"
	chalkFgBlue    = "\x1b[34m"
	chalkFgMagenta = "\x1b[35m"
	chalkFgCyan    = "\x1b[36m"
	chalkFgGray    = "\x1b[90m"
	chalkFgDefault = "\x1b[39m"

	chalkItalicOpen     = "\x1b[3m"
	chalkItalicClose    = "\x1b[23m"
	chalkUnderlineOpen  = "\x1b[4m"
	chalkUnderlineClose = "\x1b[24m"
	chalkStrikeOpen     = "\x1b[9m"
	chalkStrikeClose    = "\x1b[29m"
	chalkDimOpen        = "\x1b[2m"
	chalkDimClose       = "\x1b[22m"
)

func chalkBold(text string) string      { return chalkBoldOpen + text + chalkBoldClose }
func chalkItalic(text string) string    { return chalkItalicOpen + text + chalkItalicClose }
func chalkDim(text string) string       { return chalkDimOpen + text + chalkDimClose }
func chalkStrike(text string) string    { return chalkStrikeOpen + text + chalkStrikeClose }
func chalkUnderline(text string) string { return chalkUnderlineOpen + text + chalkUnderlineClose }

func chalkCyan(text string) string    { return chalkFgCyan + text + chalkFgDefault }
func chalkGray(text string) string    { return chalkFgGray + text + chalkFgDefault }
func chalkYellow(text string) string  { return chalkFgYellow + text + chalkFgDefault }
func chalkGreen(text string) string   { return chalkFgGreen + text + chalkFgDefault }
func chalkMagenta(text string) string { return chalkFgMagenta + text + chalkFgDefault }

// chalkBoldCyan is chalk.bold.cyan (outer style wraps the inner one).
func chalkBoldCyan(text string) string { return chalkBold(chalkCyan(text)) }

// defaultMarkdownTheme mirrors test-themes.ts.
var defaultMarkdownTheme = MarkdownTheme{
	Heading:         chalkBoldCyan,
	Link:            chalkFgBlueFn(),
	LinkURL:         chalkDim,
	Code:            chalkYellow,
	CodeBlock:       chalkGreen,
	CodeBlockBorder: chalkDim,
	Quote:           chalkItalic,
	QuoteBorder:     chalkDim,
	Hr:              chalkDim,
	ListBullet:      chalkCyan,
	Bold:            chalkBold,
	Italic:          chalkItalic,
	Strikethrough:   chalkStrike,
	Underline:       chalkUnderline,
}

func chalkFgBlueFn() func(string) string {
	return func(text string) string { return chalkFgBlue + text + chalkFgDefault }
}
