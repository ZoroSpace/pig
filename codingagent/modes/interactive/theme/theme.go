// Package theme (pi: src/modes/interactive/theme/theme.ts) — the Theme
// class, color utilities, theme loading/registry, the global theme
// instance, terminal color/theme detection, HTML export helpers and the TUI
// theme adapters.
//
// Declared deviations:
//   - pi stores the global theme on globalThis (cross module-loader
//     sharing); pig uses package state guarded by a mutex. Package-level
//     functions (Fg/Bg/Bold/...) delegate to Current(), keeping call sites
//     shaped like pi's `theme.fg(...)` proxy.
//   - pi's chalk.bold/italic/... are hand-rolled ANSI here (repo-wide
//     precedent: chalk is not ported; test_themes_test.go hand-writes
//     level-3 bytes).
//   - The theme file watcher polls the watched theme file (Go has no
//     fs.watch); pi's dir watcher + filename filter collapses to a
//     watched-file mtime poll. The 100ms reload debounce is preserved.
//   - Validation is a hand-written equivalent of the TypeBox schema
//     (model-config precedent). Missing-color diagnostics keep pi's
//     formatting; other type-error wording is approximate (no pi test
//     locks it).
//   - Syntax highlighting delegates to codingagent/utils (chroma backend;
//     see syntax_highlight.go for the deviation record).
//   - Broken built-in theme JSON surfaces as a panic in InitTheme/SetTheme
//     fallback paths, matching pi's synchronous throw out of the catch
//     block (both are fatal at startup).
//   - The v0.87.1-era terminal background detection family
//     (TerminalThemeDetection/Source/Confidence,
//     DetectTerminalBackgroundTheme/ForAuto, GetDefaultTheme,
//     GetThemeForRgbColor) is removed with pi v0.99.1's #10067 rewrite:
//     DetectTerminalTheme (background → light/dark report → COLORFGBG →
//     dark) replaces it, and the controller queries terminal colors
//     directly. pig's blocking-query deviations (see
//     tui/terminal_query_deadlock_test.go) carry over to
//     tui.QueryTerminalColors unchanged.
//   - pig-only palette tweaks (user decision 2026-09-24, light.json's
//     syntaxVariable/syntaxKeyword overrides) were dropped with the v0.99.1
//     builtin theme redesign: the OKHSL rewrite replaced pi's old palette
//     entirely, so the override no longer applies to the same design (flag
//     in the S10-3 handoff for re-evaluation).
package theme

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/agent"
	codingagent "github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/assets"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// ============================================================================
// Types & Schema
// ============================================================================

// ColorValue is a theme color value: a hex/oklch()/okhsl() string, a variable
// reference ("primary"), an empty string (terminal default), or a 256-color
// index (pi: ColorValue = string | int).
type ColorValue = any

// ThemeJson mirrors the theme JSON schema (pi: ThemeJsonSchema/ThemeJson).
type ThemeJson struct {
	Schema     string                `json:"$schema,omitempty"`
	Name       string                `json:"name"`
	Appearance TerminalTheme         `json:"appearance,omitempty"`
	Vars       map[string]ColorValue `json:"vars,omitempty"`
	Colors     map[string]ColorValue `json:"colors"`
	Export     *ThemeExportJson      `json:"export,omitempty"`
}

// ThemeExportJson is the optional export section (pi: export).
type ThemeExportJson struct {
	PageBg ColorValue `json:"pageBg,omitempty"`
	CardBg ColorValue `json:"cardBg,omitempty"`
	InfoBg ColorValue `json:"infoBg,omitempty"`
}

// ThemeColor names a foreground color token (pi: ThemeColor union).
// Declared deviation: a string alias (not a named type) so that *Theme
// structurally satisfies core.UITheme — package core cannot import this
// package (theme → core for SourceInfo), so renderer signatures in core
// accept the structural interface.
type ThemeColor = string

// ThemeBg names a background color token (pi: ThemeBg union). Alias for
// the same reason as ThemeColor.
type ThemeBg = string

// ThemeToken is ThemeColor | ThemeBg (pi: ThemeToken union).
type ThemeToken = string

// ColorMode is the v0.87.1 pig-era alias for the TUI terminal color mode
// (kept for existing consumers; pi: TerminalColorMode).
type ColorMode = tui.TerminalColorMode

const (
	ColorModeTruecolor ColorMode = tui.ColorModeTruecolorN
	ColorMode256       ColorMode = tui.ColorMode256color
)

const (
	ColorAccent             ThemeColor = "accent"
	ColorBorder             ThemeColor = "border"
	ColorBorderAccent       ThemeColor = "borderAccent"
	ColorBorderMuted        ThemeColor = "borderMuted"
	ColorSuccess            ThemeColor = "success"
	ColorError              ThemeColor = "error"
	ColorWarning            ThemeColor = "warning"
	ColorMuted              ThemeColor = "muted"
	ColorDim                ThemeColor = "dim"
	ColorText               ThemeColor = "text"
	ColorThinkingText       ThemeColor = "thinkingText"
	ColorSearchMatchText    ThemeColor = "searchMatchText"
	ColorUserMessageText    ThemeColor = "userMessageText"
	ColorCustomMessageText  ThemeColor = "customMessageText"
	ColorCustomMessageLabel ThemeColor = "customMessageLabel"
	ColorToolTitle          ThemeColor = "toolTitle"
	ColorToolOutput         ThemeColor = "toolOutput"
	ColorMdHeading          ThemeColor = "mdHeading"
	ColorMdLink             ThemeColor = "mdLink"
	ColorMdLinkUrl          ThemeColor = "mdLinkUrl"
	ColorMdCode             ThemeColor = "mdCode"
	ColorMdCodeBlock        ThemeColor = "mdCodeBlock"
	ColorMdCodeBlockBorder  ThemeColor = "mdCodeBlockBorder"
	ColorMdQuote            ThemeColor = "mdQuote"
	ColorMdQuoteBorder      ThemeColor = "mdQuoteBorder"
	ColorMdHr               ThemeColor = "mdHr"
	ColorMdListBullet       ThemeColor = "mdListBullet"
	ColorToolDiffAdded      ThemeColor = "toolDiffAdded"
	ColorToolDiffRemoved    ThemeColor = "toolDiffRemoved"
	ColorToolDiffContext    ThemeColor = "toolDiffContext"
	ColorSyntaxComment      ThemeColor = "syntaxComment"
	ColorSyntaxKeyword      ThemeColor = "syntaxKeyword"
	ColorSyntaxFunction     ThemeColor = "syntaxFunction"
	ColorSyntaxVariable     ThemeColor = "syntaxVariable"
	ColorSyntaxString       ThemeColor = "syntaxString"
	ColorSyntaxNumber       ThemeColor = "syntaxNumber"
	ColorSyntaxType         ThemeColor = "syntaxType"
	ColorSyntaxOperator     ThemeColor = "syntaxOperator"
	ColorSyntaxPunctuation  ThemeColor = "syntaxPunctuation"
	ColorThinkingOff        ThemeColor = "thinkingOff"
	ColorThinkingMinimal    ThemeColor = "thinkingMinimal"
	ColorThinkingLow        ThemeColor = "thinkingLow"
	ColorThinkingMedium     ThemeColor = "thinkingMedium"
	ColorThinkingHigh       ThemeColor = "thinkingHigh"
	ColorThinkingXhigh      ThemeColor = "thinkingXhigh"
	ColorThinkingMax        ThemeColor = "thinkingMax"
	ColorBashMode           ThemeColor = "bashMode"
	// Scrollbar colors are foreground tokens as of v0.85.1 (pi: moved
	// scrollbarThumb from ThemeBg to ThemeColor and added scrollbarTrack; both
	// optional, falling back to muted/text).
	ColorScrollbarTrack ThemeColor = "scrollbarTrack"
	ColorScrollbarThumb ThemeColor = "scrollbarThumb"
	// ImagePlaceholder is a pig-only optional token (pi has no equivalent):
	// colors the [Image N] clipboard-paste placeholder in the editor
	// (docs/feat/CLIPBOARD-IMAGE-PLACEHOLDER.md). Optional, falling back to
	// accent for themes that do not define it.
	ColorImagePlaceholder ThemeColor = "imagePlaceholder"
)

const (
	BgSelectedBg      ThemeBg = "selectedBg"
	BgSearchMatchBg   ThemeBg = "searchMatchBg"
	BgUserMessageBg   ThemeBg = "userMessageBg"
	BgCustomMessageBg ThemeBg = "customMessageBg"
	BgToolPendingBg   ThemeBg = "toolPendingBg"
	BgToolSuccessBg   ThemeBg = "toolSuccessBg"
	BgToolErrorBg     ThemeBg = "toolErrorBg"
)

// optionalThemeColors/optionalThemeBgs are the schema-optional tokens (pi:
// OptionalThemeColor/OptionalThemeBg — scrollbarTrack | scrollbarThumb |
// thinkingMax | searchMatchText; optionalThemeBg = searchMatchBg only).
var optionalThemeColors = map[ThemeColor]bool{
	ColorScrollbarTrack:  true,
	ColorScrollbarThumb:  true,
	ColorThinkingMax:     true,
	ColorSearchMatchText: true,
	// pig-only (docs/feat/CLIPBOARD-IMAGE-PLACEHOLDER.md).
	ColorImagePlaceholder: true,
}

var optionalThemeBgs = map[ThemeBg]bool{
	BgSearchMatchBg: true,
}

// requiredThemeColors lists every required foreground token, in schema
// declaration order (used by the validator's missing-color diagnostics).
var requiredThemeColors = []ThemeColor{
	ColorAccent, ColorBorder, ColorBorderAccent, ColorBorderMuted,
	ColorSuccess, ColorError, ColorWarning, ColorMuted, ColorDim, ColorText,
	ColorThinkingText,
	ColorUserMessageText, ColorCustomMessageText, ColorCustomMessageLabel,
	ColorToolTitle, ColorToolOutput,
	ColorMdHeading, ColorMdLink, ColorMdLinkUrl, ColorMdCode, ColorMdCodeBlock,
	ColorMdCodeBlockBorder, ColorMdQuote, ColorMdQuoteBorder, ColorMdHr,
	ColorMdListBullet,
	ColorToolDiffAdded, ColorToolDiffRemoved, ColorToolDiffContext,
	ColorSyntaxComment, ColorSyntaxKeyword, ColorSyntaxFunction,
	ColorSyntaxVariable, ColorSyntaxString, ColorSyntaxNumber, ColorSyntaxType,
	ColorSyntaxOperator, ColorSyntaxPunctuation,
	ColorThinkingOff, ColorThinkingMinimal, ColorThinkingLow,
	ColorThinkingMedium, ColorThinkingHigh, ColorThinkingXhigh,
	ColorBashMode,
}

// requiredThemeBgs lists every required background token.
var requiredThemeBgs = []ThemeBg{
	BgSelectedBg, BgUserMessageBg, BgCustomMessageBg, BgToolPendingBg,
	BgToolSuccessBg, BgToolErrorBg,
}

// backgroundTokens is the fixed ThemeBg set (pi: BACKGROUND_TOKENS).
var backgroundTokens = map[string]bool{
	"selectedBg":      true,
	"searchMatchBg":   true,
	"userMessageBg":   true,
	"customMessageBg": true,
	"toolPendingBg":   true,
	"toolSuccessBg":   true,
	"toolErrorBg":     true,
}

// ThemeStyle is a style request for Theme.Style: fg is a ThemeColor token or
// a concrete color, bg is a ThemeBg token or a concrete color (pi:
// ThemeStyle; the any fields hold tui.Color for concrete colors).
type ThemeStyle struct {
	tui.TextAttributes
	// Fg: nil (terminal default untouched), ThemeColor (string), or tui.Color.
	Fg any
	// Bg: nil, ThemeBg (string), or tui.Color.
	Bg any
}

// ============================================================================
// Terminal Color State & Appearance Detection
// ============================================================================

// The terminal's reported colors. Replaced (never mutated) on update, so
// themes can cache resolved colors by identity (pi: module-level
// terminalColors).
var (
	terminalColorsMu      sync.RWMutex
	terminalColors        *tui.TerminalColors
	terminalColorsPending bool
	// terminalColorScheme is the terminal's last light/dark report (mode
	// 2031). Only used while it has not reported a background; nil = unset.
	terminalColorScheme *TerminalTheme
)

// SetTerminalColors records the terminal's reported colors. Themes use the
// default colors for tokens set to "" (terminal default); the system theme is
// generated from all of them. Ends the pending state (pi: setTerminalColors).
func SetTerminalColors(colors tui.TerminalColors) {
	terminalColorsMu.Lock()
	copied := colors
	terminalColors = &copied
	terminalColorsPending = false
	terminalColorsMu.Unlock()
}

// SetTerminalColorScheme records the terminal's light/dark report, the
// fallback for terminals that do not report their background (pi:
// setTerminalColorScheme; "" unsets).
func SetTerminalColorScheme(scheme TerminalTheme) {
	terminalColorsMu.Lock()
	defer terminalColorsMu.Unlock()
	if scheme == "" {
		terminalColorScheme = nil
		return
	}
	terminalColorScheme = &scheme
}

// MarkTerminalColorsPending renders the system theme in grayscale until
// SetTerminalColors reports the terminal's colors (pi:
// markTerminalColorsPending).
func MarkTerminalColorsPending() {
	terminalColorsMu.Lock()
	terminalColorsPending = true
	terminalColorsMu.Unlock()
}

func getTerminalColorsState() (*tui.TerminalColors, bool, *TerminalTheme) {
	terminalColorsMu.RLock()
	defer terminalColorsMu.RUnlock()
	return terminalColors, terminalColorsPending, terminalColorScheme
}

// guessedDefaultColors are the assumed terminal default colors when the
// terminal does not report them (pi: GUESSED_DEFAULT_COLORS).
var guessedDefaultColors = map[TerminalTheme]struct {
	foreground tui.Color
	background tui.Color
}{
	TerminalThemeDark:  {mustParseColor("#e5e5e7"), mustParseColor("#000000")},
	TerminalThemeLight: {mustParseColor("#000000"), mustParseColor("#ffffff")},
}

func mustParseColor(value string) tui.Color {
	color, err := tui.ParseColor(value)
	if err != nil {
		panic(err)
	}
	return color
}

// averageLightness averages the OKLab lightness of concrete colors. Palette
// colors 0-15 follow the user's terminal palette, so they say nothing about
// the theme and are skipped (pi: averageLightness).
func averageLightness(colors []tui.Color) float64 {
	count := 0
	sum := 0.0
	for _, color := range colors {
		if color.Kind == tui.ColorKindIndexed && color.Index < 16 {
			continue
		}
		sum += tui.ColorToOklch(color).L
		count++
	}
	if count == 0 {
		return math.NaN()
	}
	return sum / float64(count)
}

// detectAppearance detects the background a theme is designed for from the
// lightness of its own colors (pi: detectAppearance). "" = undetectable.
func detectAppearance(foregrounds, backgrounds []tui.Color) TerminalTheme {
	fg := averageLightness(foregrounds)
	bg := averageLightness(backgrounds)
	if !math.IsNaN(fg) && !math.IsNaN(bg) {
		if bg < fg {
			return TerminalThemeDark
		}
		return TerminalThemeLight
	}
	if !math.IsNaN(bg) {
		if bg < 0.5 {
			return TerminalThemeDark
		}
		return TerminalThemeLight
	}
	if !math.IsNaN(fg) {
		if fg > 0.5 {
			return TerminalThemeDark
		}
		return TerminalThemeLight
	}
	return ""
}

// ============================================================================
// Theme Class
// ============================================================================

// ThemeOptions carries the constructor's optional fields (pi: options
// object; "" / nil = undefined).
type ThemeOptions struct {
	Name       string
	SourcePath string
	SourceInfo *utils.SourceInfo
	// Appearance is the background the theme is designed for, declared in the
	// theme JSON (pi: options.appearance). "" = detect from the colors.
	Appearance TerminalTheme
	// Dim lists foreground tokens rendered faint (SGR 2) on top of their
	// color (pi: options.dim).
	Dim []ThemeColor
}

// Theme is the resolved theme (pi: class Theme).
type Theme struct {
	name       string
	sourcePath string
	// SourceInfo is assigned by the resource loader (pi: sourceInfo?).
	SourceInfo *utils.SourceInfo

	mu   sync.RWMutex
	mode tui.TerminalColorMode
	// Precomputed escape sequences keep Fg/Bg on the render hot path to a
	// lookup and concat.
	fgAnsi map[ThemeColor]string
	bgAnsi map[ThemeBg]string
	// Tokens set to "" have no color of their own; Colors() fills them from
	// the terminal defaults.
	concreteColors          map[ThemeToken]tui.Color
	defaultForegroundTokens []ThemeToken
	defaultBackgroundTokens []ThemeToken
	// dimTokens are foreground tokens rendered faint (SGR 2).
	dimTokens     map[ThemeColor]bool
	ownAppearance TerminalTheme // "" = undetected

	// Resolved colors cache, invalidated by terminal color identity (pi:
	// resolvedColors).
	resolvedTerminal *tui.TerminalColors
	resolvedColors   map[ThemeToken]tui.Color
}

// NewTheme builds a theme from resolved color values (pi: constructor).
// fgColors/bgColors values are string ("#hex"/"oklch(...)"/"okhsl(...)"/"" =
// terminal default) or int (256-color index).
func NewTheme(fgColors map[string]ColorValue, bgColors map[string]ColorValue, mode tui.TerminalColorMode, options *ThemeOptions) (*Theme, error) {
	t := &Theme{
		mode:                    mode,
		fgAnsi:                  map[ThemeColor]string{},
		bgAnsi:                  map[ThemeBg]string{},
		concreteColors:          map[ThemeToken]tui.Color{},
		dimTokens:               map[ThemeColor]bool{},
		defaultForegroundTokens: []ThemeToken{},
		defaultBackgroundTokens: []ThemeToken{},
	}
	if options != nil {
		t.name = options.Name
		t.sourcePath = options.SourcePath
		t.SourceInfo = options.SourceInfo
		if options.Appearance != "" {
			appearance := options.Appearance
			t.ownAppearance = appearance
		}
		for _, token := range options.Dim {
			t.dimTokens[token] = true
		}
	}

	foregrounds := make(map[string]ColorValue, len(fgColors)+4)
	for k, v := range fgColors {
		foregrounds[k] = v
	}
	// pi: `fgColors.scrollbarTrack ?? fgColors.muted` — the fallback only
	// applies when the source token exists (a missing required token stays
	// missing and errors in addToken).
	if _, ok := foregrounds["scrollbarTrack"]; !ok {
		if v, ok := fgColors["muted"]; ok {
			foregrounds["scrollbarTrack"] = v
		}
	}
	if _, ok := foregrounds["scrollbarThumb"]; !ok {
		if v, ok := fgColors["text"]; ok {
			foregrounds["scrollbarThumb"] = v
		}
	}
	if _, ok := foregrounds["thinkingMax"]; !ok {
		if v, ok := fgColors["thinkingXhigh"]; ok {
			foregrounds["thinkingMax"] = v
		}
	}
	if _, ok := foregrounds["searchMatchText"]; !ok {
		if v, ok := fgColors["text"]; ok {
			foregrounds["searchMatchText"] = v
		}
	}
	backgrounds := make(map[string]ColorValue, len(bgColors)+1)
	for k, v := range bgColors {
		backgrounds[k] = v
	}
	if _, ok := backgrounds["searchMatchBg"]; !ok {
		if v, ok := bgColors["selectedBg"]; ok {
			backgrounds["searchMatchBg"] = v
		}
	}

	var concreteForegrounds, concreteBackgrounds []tui.Color
	// addToken returns the escape sequence for the token's own slot.
	addToken := func(token ThemeToken, value ColorValue, isBackground bool) (string, error) {
		if str, isStr := value.(string); isStr && str == "" {
			if isBackground {
				t.defaultBackgroundTokens = append(t.defaultBackgroundTokens, token)
				return "\x1b[49m", nil
			}
			t.defaultForegroundTokens = append(t.defaultForegroundTokens, token)
			return "\x1b[39m", nil
		}
		color, err := tui.ParseColor(value)
		if err != nil {
			return "", err
		}
		t.concreteColors[token] = color
		if isBackground {
			concreteBackgrounds = append(concreteBackgrounds, color)
		} else {
			concreteForegrounds = append(concreteForegrounds, color)
		}
		if isBackground {
			return tui.BackgroundAnsi(color, mode), nil
		}
		return tui.ForegroundAnsi(color, mode), nil
	}
	for _, token := range sortedColorKeys(foregrounds) {
		ansi, err := addToken(token, foregrounds[token], false)
		if err != nil {
			return nil, err
		}
		t.fgAnsi[ThemeColor(token)] = ansi
	}
	for _, token := range sortedColorKeys(backgrounds) {
		ansi, err := addToken(token, backgrounds[token], true)
		if err != nil {
			return nil, err
		}
		t.bgAnsi[ThemeBg(token)] = ansi
	}
	if t.ownAppearance == "" {
		if detected := detectAppearance(concreteForegrounds, concreteBackgrounds); detected != "" {
			t.ownAppearance = detected
		}
	}
	return t, nil
}

// sortedColorKeys iterates color maps in key order (pi: Object.entries
// insertion order; the order only decides concrete-color list order, which
// feeds appearance detection averaging — order-independent).
func sortedColorKeys(colors map[string]ColorValue) []string {
	keys := make([]string, 0, len(colors))
	for key := range colors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// GetName returns the theme name ("" = unnamed, pi: name?).
func (t *Theme) GetName() string { return t.name }

// GetSourcePath returns the source path ("" = none, pi: sourcePath?).
func (t *Theme) GetSourcePath() string { return t.sourcePath }

// Appearance returns the background the theme is designed for: declared in
// the theme JSON, detected from its colors, or, for themes without usable
// colors, the terminal's appearance (pi: appearance getter).
func (t *Theme) Appearance() TerminalTheme {
	t.mu.RLock()
	own := t.ownAppearance
	t.mu.RUnlock()
	if own != "" {
		return own
	}
	return GetTerminalTheme()
}

// Colors returns concrete colors for all tokens. Tokens set to "" (terminal
// default) use the terminal's reported default colors, or a guess based on
// Appearance() when the terminal did not report them. Faint tokens are
// approximated by mixing their color toward the background (pi: colors
// getter).
func (t *Theme) Colors() map[ThemeToken]tui.Color {
	terminal, _, _ := getTerminalColorsState()
	// ownAppearance is fixed after construction; the fallback queries the
	// terminal state, whose lock must not nest inside t.mu.
	t.mu.RLock()
	own := t.ownAppearance
	t.mu.RUnlock()
	appearance := own
	if appearance == "" {
		appearance = GetTerminalTheme()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.resolvedTerminal == terminal && t.resolvedColors != nil {
		return t.resolvedColors
	}
	guess := guessedDefaultColors[appearance]
	toColor := func(rgb *tui.RgbColor, fallback tui.Color) tui.Color {
		if rgb != nil {
			return tui.Color{Kind: tui.ColorKindRgb, R: rgb.R, G: rgb.G, B: rgb.B}
		}
		return fallback
	}
	foreground := toColor(terminalForeground(terminal), guess.foreground)
	background := toColor(terminalBackground(terminal), guess.background)
	colors := make(map[ThemeToken]tui.Color, len(t.concreteColors)+len(t.defaultForegroundTokens)+len(t.defaultBackgroundTokens))
	for token, color := range t.concreteColors {
		colors[token] = color
	}
	for _, token := range t.defaultForegroundTokens {
		colors[token] = foreground
	}
	for _, token := range t.defaultBackgroundTokens {
		colors[token] = background
	}
	for token := range t.dimTokens {
		if color, ok := colors[token]; ok {
			mixed, err := tui.MixColors(color, background, 0.4, "")
			if err == nil {
				colors[token] = mixed
			}
		}
	}
	t.resolvedTerminal = terminal
	t.resolvedColors = colors
	return colors
}

func terminalForeground(colors *tui.TerminalColors) *tui.RgbColor {
	if colors == nil {
		return nil
	}
	return colors.Foreground
}

func terminalBackground(colors *tui.TerminalColors) *tui.RgbColor {
	if colors == nil {
		return nil
	}
	return colors.Background
}

// tokenAnsi returns the cached escape sequence for a token (pi: tokenAnsi).
func tokenAnsi[T ~string](ansi map[T]string, token T) string {
	value, ok := ansi[token]
	if !ok {
		panic(fmt.Sprintf("Unknown theme color: %s", string(token)))
	}
	return value
}

// Style styles text with theme tokens or concrete colors plus attributes
// (pi: style).
func (t *Theme) Style(text string, options ThemeStyle) string {
	if token, ok := options.Fg.(string); ok && t.isDim(token) {
		options.Dim = true
	}
	t.mu.RLock()
	mode := t.mode
	t.mu.RUnlock()
	fgAnsi := ""
	if options.Fg != nil {
		switch fg := options.Fg.(type) {
		case string:
			fgAnsi = tokenAnsi(t.fgAnsi, ThemeColor(fg))
		case tui.Color:
			fgAnsi = tui.ForegroundAnsi(fg, mode)
		}
	}
	bgAnsi := ""
	if options.Bg != nil {
		switch bg := options.Bg.(type) {
		case string:
			bgAnsi = tokenAnsi(t.bgAnsi, ThemeBg(bg))
		case tui.Color:
			bgAnsi = tui.BackgroundAnsi(bg, mode)
		}
	}
	return tui.StyleTextWithAnsi(text, fgAnsi, bgAnsi, options.TextAttributes)
}

func (t *Theme) isDim(color ThemeColor) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.dimTokens[color]
}

// Fg wraps text in the token's foreground color, resetting only the
// foreground (pi: fg). Faint tokens include SGR 2.
func (t *Theme) Fg(color ThemeColor, text string) string {
	t.mu.RLock()
	ansi, ok := t.fgAnsi[color]
	dim := t.dimTokens[color]
	t.mu.RUnlock()
	if !ok {
		panic(fmt.Sprintf("Unknown theme color: %s", color))
	}
	if dim {
		return ansi + "\x1b[2m" + text + "\x1b[22;39m"
	}
	return ansi + text + "\x1b[39m"
}

// Bg wraps text in the token's background color, resetting only the
// background (pi: bg).
func (t *Theme) Bg(color ThemeBg, text string) string {
	t.mu.RLock()
	ansi, ok := t.bgAnsi[color]
	t.mu.RUnlock()
	if !ok {
		panic(fmt.Sprintf("Unknown theme background color: %s", color))
	}
	return ansi + text + "\x1b[49m"
}

// Bold mirrors chalk.bold.
func (t *Theme) Bold(text string) string { return "\x1b[1m" + text + "\x1b[22m" }

// Italic mirrors chalk.italic.
func (t *Theme) Italic(text string) string { return "\x1b[3m" + text + "\x1b[23m" }

// Underline mirrors chalk.underline.
func (t *Theme) Underline(text string) string { return "\x1b[4m" + text + "\x1b[24m" }

// Inverse mirrors chalk.inverse.
func (t *Theme) Inverse(text string) string { return "\x1b[7m" + text + "\x1b[27m" }

// Strikethrough mirrors chalk.strikethrough.
func (t *Theme) Strikethrough(text string) string { return "\x1b[9m" + text + "\x1b[29m" }

// GetFgAnsi returns the opening escape sequence for a foreground token.
// Faint tokens include SGR 2, which `\x1b[22m` closes (pi: getFgAnsi).
func (t *Theme) GetFgAnsi(color ThemeColor) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ansi, ok := t.fgAnsi[color]
	if !ok {
		panic(fmt.Sprintf("Unknown theme color: %s", color))
	}
	if t.dimTokens[color] {
		return ansi + "\x1b[2m"
	}
	return ansi
}

// GetBgAnsi returns the raw ANSI prefix for a background token (pi:
// getBgAnsi).
func (t *Theme) GetBgAnsi(color ThemeBg) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ansi, ok := t.bgAnsi[color]
	if !ok {
		panic(fmt.Sprintf("Unknown theme background color: %s", color))
	}
	return ansi
}

// GetColorMode returns the ANSI mode (pi: getColorMode).
func (t *Theme) GetColorMode() tui.TerminalColorMode {
	return t.mode
}

// GetThinkingBorderColor maps thinking levels to dedicated theme colors
// (pi: getThinkingBorderColor).
func (t *Theme) GetThinkingBorderColor(level agent.ThinkingLevel) func(string) string {
	switch level {
	case "off":
		return func(s string) string { return t.Fg(ColorThinkingOff, s) }
	case "minimal":
		return func(s string) string { return t.Fg(ColorThinkingMinimal, s) }
	case "low":
		return func(s string) string { return t.Fg(ColorThinkingLow, s) }
	case "medium":
		return func(s string) string { return t.Fg(ColorThinkingMedium, s) }
	case "high":
		return func(s string) string { return t.Fg(ColorThinkingHigh, s) }
	case "xhigh":
		return func(s string) string { return t.Fg(ColorThinkingXhigh, s) }
	case "max":
		return func(s string) string { return t.Fg(ColorThinkingMax, s) }
	default:
		return func(s string) string { return t.Fg(ColorThinkingOff, s) }
	}
}

// GetBashModeBorderColor returns the bash-mode border colorizer (pi:
// getBashModeBorderColor).
func (t *Theme) GetBashModeBorderColor() func(string) string {
	return func(s string) string { return t.Fg(ColorBashMode, s) }
}

// ============================================================================
// Theme Loading
// ============================================================================

var (
	builtinThemesOnce sync.Once
	builtinThemes     map[string]*ThemeJson
	builtinThemesErr  error
)

func getBuiltinThemes() (map[string]*ThemeJson, error) {
	builtinThemesOnce.Do(func() {
		dark, err := loadBuiltinThemeJson("dark")
		if err != nil {
			builtinThemesErr = err
			return
		}
		light, err := loadBuiltinThemeJson("light")
		if err != nil {
			builtinThemesErr = err
			return
		}
		builtinThemes = map[string]*ThemeJson{"dark": dark, "light": light}
	})
	return builtinThemes, builtinThemesErr
}

// loadBuiltinThemeJson loads a built-in theme by name: disk first (pi:
// theme/<name>.json next to the executable, overridable via PI_PACKAGE_DIR),
// then the embedded assets (pig: go:embed so the static binary is
// self-contained).
func loadBuiltinThemeJson(name string) (*ThemeJson, error) {
	path := filepath.Join(codingagent.GetThemesDir(), name+".json")
	content, err := os.ReadFile(path)
	if err != nil {
		// Fall back to the embedded theme assets.
		embedded, embedErr := assets.Theme.ReadFile("theme/" + name + ".json")
		if embedErr != nil {
			return nil, err
		}
		return parseThemeJsonContent("embedded:"+name, string(embedded))
	}
	return parseThemeJsonContent(path, string(content))
}

func loadThemeJsonFile(path string) (*ThemeJson, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseThemeJsonContent(path, string(content))
}

// ThemeInfo is pi's ThemeInfo.
type ThemeInfo struct {
	Name string
	Path string // "" = none (pi: path?: string)
}

func GetAvailableThemes() []string {
	infos := GetAvailableThemesWithPaths()
	names := make([]string, len(infos))
	for i, info := range infos {
		names[i] = info.Name
	}
	return names
}

func GetAvailableThemesWithPaths() []ThemeInfo {
	themesDir := codingagent.GetThemesDir()
	var result []ThemeInfo
	seen := map[string]bool{}
	addTheme := func(info ThemeInfo) {
		if seen[info.Name] {
			return
		}
		seen[info.Name] = true
		result = append(result, info)
	}

	// Built-in themes (pi iterates the Record keys; pig pins dark/light).
	// The system theme comes first: it is the default and adapts to every
	// terminal.
	addTheme(ThemeInfo{Name: SystemThemeName})
	if _, err := getBuiltinThemes(); err == nil {
		for _, name := range []string{"dark", "light"} {
			addTheme(ThemeInfo{Name: name, Path: filepath.Join(themesDir, name+".json")})
		}
	}

	// Custom themes
	for _, info := range getCustomThemeInfos() {
		addTheme(info)
	}

	for _, entry := range registeredThemesSnapshot() {
		addTheme(ThemeInfo{Name: entry.Name, Path: entry.SourcePath})
	}

	sort.SliceStable(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.Name == SystemThemeName {
			return true
		}
		if b.Name == SystemThemeName {
			return false
		}
		return strings.Compare(a.Name, b.Name) < 0
	})
	return result
}

func getCustomThemeInfos() []ThemeInfo {
	customThemesDir := codingagent.GetCustomThemesDir()
	var result []ThemeInfo
	entries, err := os.ReadDir(customThemesDir)
	if err != nil {
		return result
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		themePath := filepath.Join(customThemesDir, entry.Name())
		customTheme, err := LoadThemeFromPath(themePath, "")
		if err != nil {
			// Invalid themes are ignored here; the resource loader reports
			// them during normal startup/reload.
			continue
		}
		if customTheme.GetName() != "" {
			result = append(result, ThemeInfo{Name: customTheme.GetName(), Path: themePath})
		}
	}
	return result
}

func assertThemeNameIsValid(name string) error {
	if strings.Contains(name, "/") {
		return fmt.Errorf("invalid theme name %q: theme names cannot contain \"/\" because it is reserved for automatic light/dark theme settings.", name)
	}
	return nil
}

func parseThemeJsonContent(label string, content string) (*ThemeJson, error) {
	var parsed any
	decoder := json.NewDecoder(strings.NewReader(utils.StripBom(content)))
	if err := decoder.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("Failed to parse theme %s: %v", label, err)
	}
	return parseThemeJson(label, parsed)
}

// parseThemeJson validates one parsed theme document (pi: parseThemeJson).
// Full validation runs through the installed validator (see theme_json.go);
// without one, documents are accepted with a minimal structural check — the
// equivalent of pi's `json as ThemeJson` cast, with the JSON round-trip pig
// needs to produce the typed shape. Note the validator-less path also skips
// the "/" name check, matching pi.
func parseThemeJson(label string, parsed any) (*ThemeJson, error) {
	if themeJsonValidator != nil {
		return themeJsonValidator(label, parsed)
	}
	top, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Invalid theme %q: expected an object with a \"colors\" map.", label)
	}
	if _, hasColors := top["colors"]; !hasColors {
		return nil, fmt.Errorf("Invalid theme %q: expected an object with a \"colors\" map.", label)
	}
	raw, err := json.Marshal(parsed)
	if err != nil {
		return nil, err
	}
	var themeJson ThemeJson
	if err := json.Unmarshal(raw, &themeJson); err != nil {
		return nil, fmt.Errorf("Failed to parse theme %s: %v", label, err)
	}
	return &themeJson, nil
}

func loadThemeJson(name string) (*ThemeJson, error) {
	builtin, err := getBuiltinThemes()
	if err != nil {
		return nil, err
	}
	if themeJson, ok := builtin[name]; ok {
		return themeJson, nil
	}
	registeredMu.RLock()
	registered, ok := registeredThemes[name]
	registeredMu.RUnlock()
	if ok {
		if registered.GetSourcePath() != "" {
			content, err := os.ReadFile(registered.GetSourcePath())
			if err != nil {
				return nil, err
			}
			return parseThemeJsonContent(registered.GetSourcePath(), string(content))
		}
		return nil, fmt.Errorf("Theme %q does not have a source path for export", name)
	}
	customThemesDir := codingagent.GetCustomThemesDir()
	themePath := filepath.Join(customThemesDir, name+".json")
	if _, err := os.Stat(themePath); err != nil {
		return nil, fmt.Errorf("Theme not found: %s", name)
	}
	content, err := os.ReadFile(themePath)
	if err != nil {
		return nil, err
	}
	return parseThemeJsonContent(name, string(content))
}

// resolveVarRefs resolves variable references to concrete color values.
// Hex, oklch(), okhsl(), "" and numbers are terminal values (pi:
// resolveVarRefs).
func resolveVarRefs(value ColorValue, vars map[string]ColorValue, visited map[string]bool) (ColorValue, error) {
	switch v := value.(type) {
	case int, float64:
		return v, nil
	case string:
		if v == "" || strings.HasPrefix(v, "#") || isOklchOrOkhsl(v) {
			return v, nil
		}
		if visited[v] {
			return nil, fmt.Errorf("circular variable reference detected: %s", v)
		}
		varValue, ok := vars[v]
		if !ok {
			return nil, fmt.Errorf("variable reference not found: %s", v)
		}
		visited[v] = true
		return resolveVarRefs(varValue, vars, visited)
	}
	return nil, fmt.Errorf("invalid color value: %v", value)
}

func isOklchOrOkhsl(v string) bool {
	lower := strings.ToLower(v)
	return strings.HasPrefix(lower, "oklch(") || strings.HasPrefix(lower, "okhsl(")
}

func resolveThemeColors(colors map[string]ColorValue, vars map[string]ColorValue) (map[string]ColorValue, error) {
	resolved := make(map[string]ColorValue, len(colors))
	for key, value := range colors {
		r, err := resolveVarRefs(value, vars, map[string]bool{})
		if err != nil {
			return nil, err
		}
		resolved[key] = r
	}
	return resolved, nil
}

// withThemeColorFallbacks applies the optional-token fallbacks (pi:
// withThemeColorFallbacks). Returns a new map.
func withThemeColorFallbacks(colors map[string]ColorValue) map[string]ColorValue {
	out := make(map[string]ColorValue, len(colors)+5)
	for k, v := range colors {
		out[k] = v
	}
	if _, ok := out["scrollbarTrack"]; !ok {
		if v, ok := colors["muted"]; ok {
			out["scrollbarTrack"] = v
		}
	}
	if _, ok := out["scrollbarThumb"]; !ok {
		if v, ok := colors["text"]; ok {
			out["scrollbarThumb"] = v
		}
	}
	// pig-only: [Image N] placeholder falls back to accent (builtin themes
	// define it as the vars.blue reference; see
	// docs/feat/CLIPBOARD-IMAGE-PLACEHOLDER.md).
	if _, ok := out["imagePlaceholder"]; !ok {
		if v, ok := colors["accent"]; ok {
			out["imagePlaceholder"] = v
		}
	}
	if _, ok := out["thinkingMax"]; !ok {
		if v, ok := colors["thinkingXhigh"]; ok {
			out["thinkingMax"] = v
		}
	}
	if _, ok := out["searchMatchBg"]; !ok {
		if v, ok := colors["selectedBg"]; ok {
			out["searchMatchBg"] = v
		}
	}
	if _, ok := out["searchMatchText"]; !ok {
		if v, ok := colors["text"]; ok {
			out["searchMatchText"] = v
		}
	}
	return out
}

func splitThemeColors(colors map[string]ColorValue) (fgColors, bgColors map[string]ColorValue) {
	fgColors = map[string]ColorValue{}
	bgColors = map[string]ColorValue{}
	for key, value := range colors {
		if backgroundTokens[key] {
			bgColors[key] = value
		} else {
			fgColors[key] = value
		}
	}
	return fgColors, bgColors
}

func createTheme(themeJson *ThemeJson, mode tui.TerminalColorMode, sourcePath string) (*Theme, error) {
	colorMode := mode
	if colorMode == "" {
		if tui.GetCapabilities().TrueColor {
			colorMode = tui.ColorModeTruecolorN
		} else {
			colorMode = tui.ColorMode256color
		}
	}
	resolvedColors, err := resolveThemeColors(withThemeColorFallbacks(themeJson.Colors), themeJson.Vars)
	if err != nil {
		return nil, err
	}
	fgColors, bgColors := splitThemeColors(resolvedColors)
	return NewTheme(fgColors, bgColors, colorMode, &ThemeOptions{
		Name:       themeJson.Name,
		SourcePath: sourcePath,
		Appearance: themeJson.Appearance,
	})
}

// createSystemTheme generates the system theme from the terminal's reported
// colors (grayscale while they are pending) (pi: createSystemTheme).
func createSystemTheme(mode tui.TerminalColorMode) (*Theme, error) {
	terminal, pending, scheme := getTerminalColorsState()
	colorMode := mode
	if colorMode == "" {
		if tui.GetCapabilities().TrueColor {
			colorMode = tui.ColorModeTruecolorN
		} else {
			colorMode = tui.ColorMode256color
		}
	}
	saturation := 1.0
	if pending {
		saturation = 0
	}
	input := SystemThemeInput{
		Saturation:     &saturation,
		AppearanceHint: GetTerminalTheme(),
	}
	if terminal != nil {
		input.Foreground = terminal.Foreground
		input.Background = terminal.Background
		input.Palette = terminal.Palette
	}
	_ = scheme
	generated := GenerateSystemThemeColors(input)
	fgColors, bgColors := splitThemeColors(generated.Colors)
	// pig-only: the [Image N] placeholder token follows accent in generated
	// themes (builtin/custom themes cover it via withThemeColorFallbacks).
	fgColors[string(ColorImagePlaceholder)] = generated.Colors[string(ColorAccent)]
	return NewTheme(fgColors, bgColors, colorMode, &ThemeOptions{
		Name:       SystemThemeName,
		Appearance: stringOrEmpty(generated.Appearance),
		Dim:        generated.Dim,
	})
}

func stringOrEmpty(value *TerminalTheme) TerminalTheme {
	if value == nil {
		return ""
	}
	return *value
}

// LoadThemeFromPath loads and validates a theme file (pi:
// loadThemeFromPath). mode "" = auto-detect from terminal capabilities.
func LoadThemeFromPath(themePath string, mode tui.TerminalColorMode) (*Theme, error) {
	content, err := os.ReadFile(themePath)
	if err != nil {
		return nil, err
	}
	themeJson, err := parseThemeJsonContent(themePath, string(content))
	if err != nil {
		return nil, err
	}
	return createTheme(themeJson, mode, themePath)
}

// loadTheme loads a theme by name (pi: loadTheme). The system theme name is
// reserved: it takes precedence over custom themes of the same name.
func loadTheme(name string, mode tui.TerminalColorMode) (*Theme, error) {
	if name == SystemThemeName {
		return createSystemTheme(mode)
	}
	registeredMu.RLock()
	registered, ok := registeredThemes[name]
	registeredMu.RUnlock()
	if ok {
		return registered, nil
	}
	themeJson, err := loadThemeJson(name)
	if err != nil {
		return nil, err
	}
	return createTheme(themeJson, mode, "")
}

// GetThemeByName loads a theme by name, returning nil when it cannot be
// loaded (pi: getThemeByName).
func GetThemeByName(name string) *Theme {
	t, err := loadTheme(name, "")
	if err != nil {
		return nil
	}
	return t
}

// ============================================================================
// Terminal Light/Dark Detection
// ============================================================================

// TerminalTheme is "dark" | "light" (pi: TerminalTheme).
type TerminalTheme string

const (
	TerminalThemeDark  TerminalTheme = "dark"
	TerminalThemeLight TerminalTheme = "light"
)

// AutoThemePair is parseAutoThemeSetting's resolved pair.
type AutoThemePair struct {
	LightTheme string
	DarkTheme  string
}

// ParseAutoThemeSetting parses "<light>/<dark>" theme settings (pi:
// parseAutoThemeSetting). Returns nil for unset/invalid settings.
func ParseAutoThemeSetting(themeSetting string) *AutoThemePair {
	if themeSetting == "" {
		return nil
	}
	slashIndex := strings.Index(themeSetting, "/")
	if slashIndex == -1 || strings.Index(themeSetting[slashIndex+1:], "/") != -1 {
		return nil
	}

	lightTheme := strings.TrimSpace(themeSetting[:slashIndex])
	darkTheme := strings.TrimSpace(themeSetting[slashIndex+1:])
	if lightTheme == "" || darkTheme == "" {
		return nil
	}
	return &AutoThemePair{LightTheme: lightTheme, DarkTheme: darkTheme}
}

// ResolveThemeSetting resolves a theme setting against the detected
// terminal appearance (pi: resolveThemeSetting).
func ResolveThemeSetting(themeSetting string, terminalTheme TerminalTheme) (string, bool) {
	if autoTheme := ParseAutoThemeSetting(themeSetting); autoTheme != nil {
		if terminalTheme == TerminalThemeLight {
			return autoTheme.LightTheme, true
		}
		return autoTheme.DarkTheme, true
	}
	if strings.Contains(themeSetting, "/") {
		return "", false
	}
	if themeSetting != "" {
		return themeSetting, true
	}
	return "", false
}

// DetectColorFgBgTheme reads dark or light from the `COLORFGBG` environment
// variable some terminals set (pi: detectColorFgBgTheme). The value is
// `fg;bg` or `fg;xpm;bg` (rxvt), where a field is an ANSI color index or
// `default` when the color is not in the palette. The index refers to the
// terminal's own palette, whose colors are unknown here, so it is classified
// by index like Vim does: 0-6 and 8 (bright black, e.g. Solarized Dark's
// background) are dark, 7 and 9-15 are light.
func DetectColorFgBgTheme(lookup func(key string) string) TerminalTheme {
	if lookup == nil {
		lookup = os.Getenv
	}
	fields := strings.Split(lookup("COLORFGBG"), ";")
	if len(fields) == 0 {
		return ""
	}
	bg := strings.TrimSpace(fields[len(fields)-1])
	if len(bg) == 0 || len(bg) > 2 {
		return ""
	}
	for _, ch := range bg {
		if ch < '0' || ch > '9' {
			return ""
		}
	}
	index, err := strconv.Atoi(bg)
	if err != nil || index > 15 {
		return ""
	}
	if index <= 6 || index == 8 {
		return TerminalThemeDark
	}
	return TerminalThemeLight
}

// DetectTerminalTheme reports whether the terminal is dark or light (pi:
// detectTerminalTheme). The background it renders decides, classified the
// same way the system theme does. Without a reported background: the
// terminal's light/dark report, then COLORFGBG, then dark.
func DetectTerminalTheme(colors *tui.TerminalColors, reportedScheme TerminalTheme, lookup func(key string) string) TerminalTheme {
	if colors != nil && colors.Background != nil {
		return TerminalAppearance(*colors.Background, colors.Foreground)
	}
	if reportedScheme != "" {
		return reportedScheme
	}
	if env := DetectColorFgBgTheme(lookup); env != "" {
		return env
	}
	return TerminalThemeDark
}

// GetTerminalTheme reports whether the terminal is dark or light, from
// everything it reported so far (pi: getTerminalTheme).
func GetTerminalTheme() TerminalTheme {
	colors, _, scheme := getTerminalColorsState()
	var reported TerminalTheme
	if scheme != nil {
		reported = *scheme
	}
	return DetectTerminalTheme(colors, reported, nil)
}

// ============================================================================
// Global Theme Instance
// ============================================================================

var (
	globalMu               sync.RWMutex
	globalTheme            *Theme
	currentThemeName       string
	onThemeChangeCallback  func()
	registeredThemes       = map[string]*Theme{}
	registeredMu           sync.RWMutex
	themeWatcherCancel     chan struct{}
	themeWatcherCancelLock sync.Mutex
	themeReloadTimer       *time.Timer
	themeReloadTimerMu     sync.Mutex
)

// Current returns the active global theme. It panics when the theme has
// not been initialized, matching pi's proxy throw.
func Current() *Theme {
	globalMu.RLock()
	defer globalMu.RUnlock()
	if globalTheme == nil {
		panic("Theme not initialized. Call initTheme() first.")
	}
	return globalTheme
}

// Fg colors text with the global theme (pi: theme.fg).
func Fg(color ThemeColor, text string) string { return Current().Fg(color, text) }

// Bg colors text with the global theme (pi: theme.bg).
func Bg(color ThemeBg, text string) string { return Current().Bg(color, text) }

// Bold styles text with the global theme (pi: theme.bold).
func Bold(text string) string { return Current().Bold(text) }

// Italic styles text with the global theme (pi: theme.italic).
func Italic(text string) string { return Current().Italic(text) }

// Underline styles text with the global theme (pi: theme.underline).
func Underline(text string) string { return Current().Underline(text) }

// Inverse styles text with the global theme (pi: theme.inverse).
func Inverse(text string) string { return Current().Inverse(text) }

// Strikethrough styles text with the global theme (pi: theme.strikethrough).
func Strikethrough(text string) string { return Current().Strikethrough(text) }

// CurrentName returns the active theme name ("" = unset; pi:
// currentThemeName module state, exposed for controller consumption).
func CurrentName() string {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return currentThemeName
}

// SetGlobalTheme installs a theme instance (pi: setGlobalTheme — private,
// exported for SetThemeInstance's cross-package consumers and tests).
func SetGlobalTheme(t *Theme) {
	globalMu.Lock()
	globalTheme = t
	globalMu.Unlock()
}

// RegisteredThemeEntry is one registeredThemes snapshot item.
type RegisteredThemeEntry struct {
	Name       string
	Theme      *Theme
	SourcePath string
}

func registeredThemesSnapshot() []RegisteredThemeEntry {
	registeredMu.RLock()
	defer registeredMu.RUnlock()
	names := make([]string, 0, len(registeredThemes))
	for name := range registeredThemes {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]RegisteredThemeEntry, 0, len(names))
	for _, name := range names {
		t := registeredThemes[name]
		out = append(out, RegisteredThemeEntry{Name: name, Theme: t, SourcePath: t.GetSourcePath()})
	}
	return out
}

// SetRegisteredThemes replaces the extension/theme-loader registered
// themes (pi: setRegisteredThemes).
func SetRegisteredThemes(themes []*Theme) {
	registeredMu.Lock()
	defer registeredMu.Unlock()
	for k := range registeredThemes {
		delete(registeredThemes, k)
	}
	for _, t := range themes {
		if t.GetName() != "" {
			if err := assertThemeNameIsValid(t.GetName()); err != nil {
				panic(err)
			}
			registeredThemes[t.GetName()] = t
		}
	}
}

// InitTheme loads and installs the named theme ("" = the system theme),
// falling back to the system theme silently (pi: initTheme). enableWatcher
// starts the custom-theme file watcher.
func InitTheme(themeName string, enableWatcher bool) {
	name := themeName
	if name == "" {
		name = SystemThemeName
	}
	globalMu.Lock()
	currentThemeName = name
	globalMu.Unlock()
	t, err := loadTheme(name, "")
	if err == nil {
		SetGlobalTheme(t)
		if enableWatcher {
			startThemeWatcher()
		}
		return
	}
	// Theme is invalid - fall back to the system theme silently
	globalMu.Lock()
	currentThemeName = SystemThemeName
	globalMu.Unlock()
	// Matches pi: the fallback load runs outside the catch guard; a broken
	// system theme throws (pig: panics).
	SetGlobalTheme(mustLoadTheme(SystemThemeName))
}

// SetThemeResult is pi's { success: boolean; error?: string }.
type SetThemeResult struct {
	Success bool
	Error   string
}

// SetTheme switches the global theme by name (pi: setTheme).
func SetTheme(name string, enableWatcher bool) SetThemeResult {
	globalMu.Lock()
	currentThemeName = name
	globalMu.Unlock()
	t, err := loadTheme(name, "")
	if err == nil {
		SetGlobalTheme(t)
		if enableWatcher {
			startThemeWatcher()
		}
		globalMu.RLock()
		cb := onThemeChangeCallback
		globalMu.RUnlock()
		if cb != nil {
			cb()
		}
		return SetThemeResult{Success: true}
	}
	// Theme is invalid - fall back to the system theme
	globalMu.Lock()
	currentThemeName = SystemThemeName
	globalMu.Unlock()
	SetGlobalTheme(mustLoadTheme(SystemThemeName))
	return SetThemeResult{Success: false, Error: err.Error()}
}

// SetThemeInstance installs a direct theme instance (pi: setThemeInstance).
func SetThemeInstance(themeInstance *Theme) {
	SetGlobalTheme(themeInstance)
	globalMu.Lock()
	currentThemeName = "<in-memory>"
	globalMu.Unlock()
	stopThemeWatcher() // Can't watch a direct instance
	globalMu.RLock()
	cb := onThemeChangeCallback
	globalMu.RUnlock()
	if cb != nil {
		cb()
	}
}

// OnThemeChange registers the change callback (pi: onThemeChange).
func OnThemeChange(callback func()) {
	globalMu.Lock()
	onThemeChangeCallback = callback
	globalMu.Unlock()
}

// stopThemeWatcher cancels the watcher and pending reloads (pi:
// stopThemeWatcher).
func stopThemeWatcher() {
	themeReloadTimerMu.Lock()
	if themeReloadTimer != nil {
		themeReloadTimer.Stop()
		themeReloadTimer = nil
	}
	themeReloadTimerMu.Unlock()
	themeWatcherCancelLock.Lock()
	if themeWatcherCancel != nil {
		close(themeWatcherCancel)
		themeWatcherCancel = nil
	}
	themeWatcherCancelLock.Unlock()
}

// mustLoadTheme is loadTheme with pi's throw-on-error semantics (used on
// the fallback path, which pi leaves unguarded).
func mustLoadTheme(name string) *Theme {
	t, err := loadTheme(name, "")
	if err != nil {
		panic(err)
	}
	return t
}

// startThemeWatcher polls the active custom theme file for changes (pi:
// startThemeWatcher with fs.watch; pig polls — see package header).
func startThemeWatcher() {
	stopThemeWatcher()

	// Only watch if it's a custom theme (not built-in)
	globalMu.RLock()
	currentName := currentThemeName
	globalMu.RUnlock()
	if currentName == "" || currentName == "dark" || currentName == "light" || currentName == SystemThemeName {
		return
	}

	customThemesDir := codingagent.GetCustomThemesDir()
	watchedThemeName := currentName
	watchedFileName := watchedThemeName + ".json"
	themeFile := filepath.Join(customThemesDir, watchedFileName)

	// Only watch if the file exists
	if _, err := os.Stat(themeFile); err != nil {
		return
	}

	cancel := make(chan struct{})
	themeWatcherCancelLock.Lock()
	themeWatcherCancel = cancel
	themeWatcherCancelLock.Unlock()

	scheduleReload := func() {
		themeReloadTimerMu.Lock()
		if themeReloadTimer != nil {
			themeReloadTimer.Stop()
		}
		themeReloadTimer = time.AfterFunc(100*time.Millisecond, func() {
			themeReloadTimerMu.Lock()
			themeReloadTimer = nil
			themeReloadTimerMu.Unlock()

			// Ignore stale timers after switching themes or stopping the
			// watcher
			globalMu.RLock()
			name := currentThemeName
			globalMu.RUnlock()
			if name != watchedThemeName {
				return
			}

			// Keep the last successfully loaded theme active if the file is
			// temporarily missing
			if _, err := os.Stat(themeFile); err != nil {
				return
			}

			// Reload the theme from disk and refresh the registry cache
			reloadedTheme, err := LoadThemeFromPath(themeFile, "")
			if err != nil {
				// Ignore errors (file might be in invalid state while being
				// edited)
				return
			}
			registeredMu.Lock()
			registeredThemes[watchedThemeName] = reloadedTheme
			registeredMu.Unlock()
			SetGlobalTheme(reloadedTheme)
			// Notify callback (to invalidate UI)
			globalMu.RLock()
			cb := onThemeChangeCallback
			globalMu.RUnlock()
			if cb != nil {
				cb()
			}
		})
		themeReloadTimerMu.Unlock()
	}

	// Poll mtime/size every 500ms (pi: fs.watch on the directory filtered
	// to the watched filename).
	var lastModTime time.Time
	var lastSize int64
	if stat, err := os.Stat(themeFile); err == nil {
		lastModTime = stat.ModTime()
		lastSize = stat.Size()
	}
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-cancel:
				return
			case <-ticker.C:
				globalMu.RLock()
				name := currentThemeName
				globalMu.RUnlock()
				if name != watchedThemeName {
					return
				}
				stat, err := os.Stat(themeFile)
				if err != nil {
					continue
				}
				if !stat.ModTime().Equal(lastModTime) || stat.Size() != lastSize {
					lastModTime = stat.ModTime()
					lastSize = stat.Size()
					scheduleReload()
				}
			}
		}
	}()
}

// ============================================================================
// HTML Export Helpers
// ============================================================================

// GetResolvedThemeColors returns the resolved theme colors as CSS-compatible
// hex strings (pi: getResolvedThemeColors). Panics when the theme cannot be
// loaded, matching pi's unguarded loadTheme.
func GetResolvedThemeColors(themeName string) map[string]string {
	name := themeName
	if name == "" {
		globalMu.RLock()
		name = currentThemeName
		globalMu.RUnlock()
		if name == "" {
			name = SystemThemeName
		}
	}
	t, err := loadTheme(name, "")
	if err != nil {
		panic(err)
	}
	cssColors := make(map[string]string, len(t.Colors()))
	for token, color := range t.Colors() {
		cssColors[string(token)] = tui.ColorToHex(color)
	}
	return cssColors
}

// IsLightTheme reports whether the named theme is light (pi: isLightTheme).
func IsLightTheme(themeName string) bool {
	name := themeName
	if name == "" {
		globalMu.RLock()
		name = currentThemeName
		globalMu.RUnlock()
		if name == "" {
			name = SystemThemeName
		}
	}
	t, err := loadTheme(name, "")
	if err != nil {
		return false
	}
	return t.Appearance() == TerminalThemeLight
}

// ThemeExportColors is pi's { pageBg?, cardBg?, infoBg? } ("" = unset).
type ThemeExportColors struct {
	PageBg string
	CardBg string
	InfoBg string
}

// GetThemeExportColors reads the explicit export colors from a theme's
// export section, resolving variable references (pi: getThemeExportColors).
// Unset colors are "" (pi: undefined); load failures yield the zero struct
// (pi: {}).
func GetThemeExportColors(themeName string) ThemeExportColors {
	name := themeName
	if name == "" {
		globalMu.RLock()
		name = currentThemeName
		globalMu.RUnlock()
		if name == "" {
			name = SystemThemeName
		}
	}
	if name == SystemThemeName {
		return ThemeExportColors{}
	}
	themeJson, err := loadThemeJson(name)
	if err != nil {
		return ThemeExportColors{}
	}
	exportSection := themeJson.Export
	if exportSection == nil {
		return ThemeExportColors{}
	}

	vars := themeJson.Vars
	// Export colors end up in CSS, which understands hex and oklch() values
	// directly but not okhsl().
	resolve := func(value ColorValue) string {
		if value == nil {
			return ""
		}
		resolved, err := resolveVarRefs(value, vars, map[string]bool{})
		if err != nil {
			return ""
		}
		switch v := resolved.(type) {
		case int:
			color, err := tui.NewIndexedColor(v)
			if err != nil {
				return ""
			}
			return tui.ColorToHex(color)
		case float64:
			color, err := tui.NewIndexedColor(int(v))
			if err != nil {
				return ""
			}
			return tui.ColorToHex(color)
		case string:
			if v == "" {
				return ""
			}
			if isOklchOrOkhsl(v) && strings.HasPrefix(strings.ToLower(v), "okhsl(") {
				color, err := tui.ParseColor(v)
				if err != nil {
					return ""
				}
				return tui.ColorToHex(color)
			}
			return v
		}
		return ""
	}

	return ThemeExportColors{
		PageBg: resolve(exportSection.PageBg),
		CardBg: resolve(exportSection.CardBg),
		InfoBg: resolve(exportSection.InfoBg),
	}
}

// ============================================================================
// TUI Helpers
// ============================================================================

type cliHighlightTheme = utils.HighlightTheme

var (
	highlightThemeMu        sync.Mutex
	cachedHighlightThemeFor *Theme
	cachedCliHighlightTheme cliHighlightTheme
)

func buildCliHighlightTheme(t *Theme) cliHighlightTheme {
	return cliHighlightTheme{
		"keyword":     func(s string) string { return t.Fg(ColorSyntaxKeyword, s) },
		"built_in":    func(s string) string { return t.Fg(ColorSyntaxType, s) },
		"literal":     func(s string) string { return t.Fg(ColorSyntaxNumber, s) },
		"number":      func(s string) string { return t.Fg(ColorSyntaxNumber, s) },
		"regexp":      func(s string) string { return t.Fg(ColorSyntaxString, s) },
		"string":      func(s string) string { return t.Fg(ColorSyntaxString, s) },
		"comment":     func(s string) string { return t.Fg(ColorSyntaxComment, s) },
		"doctag":      func(s string) string { return t.Fg(ColorSyntaxComment, s) },
		"meta":        func(s string) string { return t.Fg(ColorMuted, s) },
		"function":    func(s string) string { return t.Fg(ColorSyntaxFunction, s) },
		"title":       func(s string) string { return t.Fg(ColorSyntaxFunction, s) },
		"class":       func(s string) string { return t.Fg(ColorSyntaxType, s) },
		"type":        func(s string) string { return t.Fg(ColorSyntaxType, s) },
		"tag":         func(s string) string { return t.Fg(ColorSyntaxPunctuation, s) },
		"name":        func(s string) string { return t.Fg(ColorSyntaxKeyword, s) },
		"attr":        func(s string) string { return t.Fg(ColorSyntaxVariable, s) },
		"variable":    func(s string) string { return t.Fg(ColorSyntaxVariable, s) },
		"params":      func(s string) string { return t.Fg(ColorSyntaxVariable, s) },
		"operator":    func(s string) string { return t.Fg(ColorSyntaxOperator, s) },
		"punctuation": func(s string) string { return t.Fg(ColorSyntaxPunctuation, s) },
		"emphasis":    t.Italic,
		"strong":      t.Bold,
		"link":        t.Underline,
		"addition":    func(s string) string { return t.Fg(ColorToolDiffAdded, s) },
		"deletion":    func(s string) string { return t.Fg(ColorToolDiffRemoved, s) },
	}
}

func getCliHighlightTheme(t *Theme) cliHighlightTheme {
	highlightThemeMu.Lock()
	defer highlightThemeMu.Unlock()
	if cachedHighlightThemeFor != t || cachedCliHighlightTheme == nil {
		cachedHighlightThemeFor = t
		cachedCliHighlightTheme = buildCliHighlightTheme(t)
	}
	return cachedCliHighlightTheme
}

// highlightCodeWithTheme is highlightCode against an explicit theme
// (getMarkdownTheme binds Current(); the explicit form keeps the two pi
// call sites' shared logic in one place).
func highlightCodeWithTheme(t *Theme, code string, lang string) []string {
	// Validate language before highlighting to avoid stderr spam (pi
	// comment). Skip highlighting when no valid language is specified.
	validLang := ""
	if lang != "" && utils.SupportsLanguage(lang) {
		validLang = lang
	}
	if validLang == "" {
		return splitLinesFg(t, code, ColorMdCodeBlock)
	}
	// pi: highlight(...) output is already theme-colored; only split.
	return strings.Split(utils.Highlight(code, utils.HighlightOptions{
		Language:       validLang,
		IgnoreIllegals: true,
		Theme:          getCliHighlightTheme(t),
	}), "\n")
}

func splitLinesFg(t *Theme, code string, color ThemeColor) []string {
	lines := strings.Split(code, "\n")
	for i, line := range lines {
		lines[i] = t.Fg(color, line)
	}
	return lines
}

// HighlightCode highlights code with syntax coloring based on language
// (pi: highlightCode).
func HighlightCode(code string, lang string) []string {
	return highlightCodeWithTheme(Current(), code, lang)
}

// GetLanguageFromPath maps a file extension to a language identifier (pi:
// getLanguageFromPath).
func GetLanguageFromPath(filePath string) (string, bool) {
	ext := filepath.Ext(filePath)
	if ext == "" {
		return "", false
	}
	ext = strings.ToLower(ext[1:])
	extToLang := map[string]string{
		"ts": "typescript", "tsx": "typescript",
		"js": "javascript", "jsx": "javascript", "mjs": "javascript", "cjs": "javascript",
		"py":    "python",
		"rb":    "ruby",
		"rs":    "rust",
		"go":    "go",
		"java":  "java",
		"kt":    "kotlin",
		"swift": "swift",
		"c":     "c", "h": "c",
		"cpp": "cpp", "cc": "cpp", "cxx": "cpp", "hpp": "cpp",
		"cs":  "csharp",
		"php": "php",
		"sh":  "bash", "bash": "bash", "zsh": "bash",
		"fish": "fish",
		"ps1":  "powershell",
		"sql":  "sql",
		"html": "html", "htm": "html",
		"css":  "css",
		"scss": "scss",
		"sass": "sass",
		"less": "less",
		"json": "json",
		"yaml": "yaml", "yml": "yaml",
		"toml": "toml",
		"xml":  "xml",
		"md":   "markdown", "markdown": "markdown",
		"dockerfile": "dockerfile",
		"makefile":   "makefile",
		"cmake":      "cmake",
		"lua":        "lua",
		"perl":       "perl",
		"r":          "r",
		"scala":      "scala",
		"clj":        "clojure",
		"ex":         "elixir", "exs": "elixir",
		"erl":     "erlang",
		"hs":      "haskell",
		"ml":      "ocaml",
		"vim":     "vim",
		"graphql": "graphql",
		"proto":   "protobuf",
		"tf":      "hcl", "hcl": "hcl",
	}
	lang, ok := extToLang[ext]
	return lang, ok
}

// GetMarkdownTheme builds the Markdown theme from the global theme (pi:
// getMarkdownTheme).
func GetMarkdownTheme() tui.MarkdownTheme {
	return tui.MarkdownTheme{
		Heading:         func(text string) string { return Fg(ColorMdHeading, text) },
		Link:            func(text string) string { return Fg(ColorMdLink, text) },
		LinkURL:         func(text string) string { return Fg(ColorMdLinkUrl, text) },
		Code:            func(text string) string { return Fg(ColorMdCode, text) },
		CodeBlock:       func(text string) string { return Fg(ColorMdCodeBlock, text) },
		CodeBlockBorder: func(text string) string { return Fg(ColorMdCodeBlockBorder, text) },
		Quote:           func(text string) string { return Fg(ColorMdQuote, text) },
		QuoteBorder:     func(text string) string { return Fg(ColorMdQuoteBorder, text) },
		Hr:              func(text string) string { return Fg(ColorMdHr, text) },
		ListBullet:      func(text string) string { return Fg(ColorMdListBullet, text) },
		Bold:            Bold,
		Italic:          Italic,
		Underline:       Underline,
		Strikethrough:   Strikethrough,
		HighlightCode: func(code string, lang string) []string {
			// Validate language before highlighting to avoid stderr spam
			validLang := ""
			if lang != "" && utils.SupportsLanguage(lang) {
				validLang = lang
			}
			if validLang == "" {
				return splitLinesFg(Current(), code, ColorMdCodeBlock)
			}
			// pi: the highlight output is already theme-colored (only the
			// no-language fallback colors per-line); utils.Highlight never
			// errors, so pi's catch branch (mdCodeBlock-colored lines) is
			// unreachable here.
			return strings.Split(utils.Highlight(code, utils.HighlightOptions{
				Language:       validLang,
				IgnoreIllegals: true,
				Theme:          getCliHighlightTheme(Current()),
			}), "\n")
		},
	}
}

// GetSelectListTheme builds the select-list theme (pi: getSelectListTheme).
func GetSelectListTheme() tui.SelectListTheme {
	return tui.SelectListTheme{
		SelectedPrefix: func(text string) string { return Fg(ColorAccent, text) },
		SelectedText:   func(text string) string { return Fg(ColorAccent, text) },
		Description:    func(text string) string { return Fg(ColorMuted, text) },
		ScrollInfo:     func(text string) string { return Fg(ColorMuted, text) },
		NoMatch:        func(text string) string { return Fg(ColorMuted, text) },
	}
}

// GetEditorTheme builds the editor theme (pi: getEditorTheme).
func GetEditorTheme() tui.EditorTheme {
	return tui.EditorTheme{
		BorderColor: func(text string) string { return Fg(ColorBorderMuted, text) },
		SelectList:  GetSelectListTheme(),
	}
}

// GetSettingsListTheme builds the settings-list theme (pi:
// getSettingsListTheme).
func GetSettingsListTheme() tui.SettingsListTheme {
	return tui.SettingsListTheme{
		Label: func(text string, selected bool) string {
			if selected {
				return Fg(ColorAccent, text)
			}
			return text
		},
		Value: func(text string, selected bool) string {
			if selected {
				return Fg(ColorAccent, text)
			}
			return Fg(ColorMuted, text)
		},
		Description: func(text string) string { return Fg(ColorDim, text) },
		Cursor:      Fg(ColorAccent, "→ "),
		Hint:        func(text string) string { return Fg(ColorDim, text) },
	}
}
