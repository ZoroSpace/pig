// Package theme (pi: src/modes/interactive/theme/theme.ts) — the Theme
// class, color utilities, theme loading/registry, the global theme
// instance, terminal theme detection, HTML export helpers and the TUI
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

// ColorValue is a theme color value: a hex string ("#ff0000"), a variable
// reference ("primary"), an empty string (terminal default), or a
// 256-color index (pi: ColorValue = string | int).
type ColorValue = any

// ThemeJson mirrors the theme JSON schema (pi: ThemeJsonSchema/ThemeJson).
type ThemeJson struct {
	Schema string                `json:"$schema,omitempty"`
	Name   string                `json:"name"`
	Vars   map[string]ColorValue `json:"vars,omitempty"`
	Colors map[string]ColorValue `json:"colors"`
	Export *ThemeExportJson      `json:"export,omitempty"`
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
)

const (
	BgSelectedBg      ThemeBg = "selectedBg"
	BgScrollbarThumb  ThemeBg = "scrollbarThumb"
	BgSearchMatchBg   ThemeBg = "searchMatchBg"
	BgUserMessageBg   ThemeBg = "userMessageBg"
	BgCustomMessageBg ThemeBg = "customMessageBg"
	BgToolPendingBg   ThemeBg = "toolPendingBg"
	BgToolSuccessBg   ThemeBg = "toolSuccessBg"
	BgToolErrorBg     ThemeBg = "toolErrorBg"
)

// optionalThemeColors/optionalThemeBgs are the schema-optional tokens (pi:
// OptionalThemeColor/OptionalThemeBg — thinkingMax, searchMatchText,
// scrollbarThumb, searchMatchBg).
var optionalThemeColors = map[ThemeColor]bool{
	ColorThinkingMax:     true,
	ColorSearchMatchText: true,
}

var optionalThemeBgs = map[ThemeBg]bool{
	BgScrollbarThumb: true,
	BgSearchMatchBg:  true,
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

// requiredThemeBgs lists every required background token (pi: the
// selectedBg/userMessageBg/customMessageBg/customMessageLabel/tool*Bg
// group; customMessageLabel is a fg token, see requiredThemeColors).
var requiredThemeBgs = []ThemeBg{
	BgSelectedBg, BgUserMessageBg, BgCustomMessageBg, BgToolPendingBg,
	BgToolSuccessBg, BgToolErrorBg,
}

// ColorMode is the ANSI encoding mode (pi: ColorMode).
type ColorMode string

const (
	ColorModeTruecolor ColorMode = "truecolor"
	ColorMode256       ColorMode = "256color"
)

// ============================================================================
// Color Utilities
// ============================================================================

type rgb struct{ r, g, b int }

func hexToRgb(hex string) (rgb, error) {
	cleaned := strings.Replace(hex, "#", "", 1)
	if len(cleaned) != 6 {
		return rgb{}, fmt.Errorf("invalid hex color: %s", hex)
	}
	r, err := strconv.ParseInt(cleaned[0:2], 16, 32)
	if err != nil {
		return rgb{}, fmt.Errorf("invalid hex color: %s", hex)
	}
	g, err := strconv.ParseInt(cleaned[2:4], 16, 32)
	if err != nil {
		return rgb{}, fmt.Errorf("invalid hex color: %s", hex)
	}
	b, err := strconv.ParseInt(cleaned[4:6], 16, 32)
	if err != nil {
		return rgb{}, fmt.Errorf("invalid hex color: %s", hex)
	}
	return rgb{int(r), int(g), int(b)}, nil
}

// The 6x6x6 color cube channel values (indices 0-5)
var cubeValues = [6]int{0, 95, 135, 175, 215, 255}

// Grayscale ramp values (indices 232-255, 24 grays from 8 to 238)
var grayValues = func() [24]int {
	var values [24]int
	for i := range values {
		values[i] = 8 + i*10
	}
	return values
}()

func findClosestCubeIndex(value int) int {
	minDist := math.MaxInt
	minIdx := 0
	for i, candidate := range cubeValues {
		dist := value - candidate
		if dist < 0 {
			dist = -dist
		}
		if dist < minDist {
			minDist = dist
			minIdx = i
		}
	}
	return minIdx
}

func findClosestGrayIndex(gray int) int {
	minDist := math.MaxInt
	minIdx := 0
	for i, candidate := range grayValues {
		dist := gray - candidate
		if dist < 0 {
			dist = -dist
		}
		if dist < minDist {
			minDist = dist
			minIdx = i
		}
	}
	return minIdx
}

func colorDistance(r1, g1, b1, r2, g2, b2 int) int {
	// Weighted Euclidean distance (human eye is more sensitive to green)
	dr := r1 - r2
	dg := g1 - g2
	db := b1 - b2
	return dr*dr*299/1000 + dg*dg*587/1000 + db*db*114/1000
}

func rgbTo256(r, g, b int) int {
	// Find closest color in the 6x6x6 cube
	rIdx := findClosestCubeIndex(r)
	gIdx := findClosestCubeIndex(g)
	bIdx := findClosestCubeIndex(b)
	cubeR := cubeValues[rIdx]
	cubeG := cubeValues[gIdx]
	cubeB := cubeValues[bIdx]
	cubeIndex := 16 + 36*rIdx + 6*gIdx + bIdx
	cubeDist := colorDistance(r, g, b, cubeR, cubeG, cubeB)

	// Find closest grayscale
	gray := (299*r + 587*g + 114*b) / 1000
	grayIdx := findClosestGrayIndex(gray)
	grayValue := grayValues[grayIdx]
	grayIndex := 232 + grayIdx
	grayDist := colorDistance(r, g, b, grayValue, grayValue, grayValue)

	// Check if color has noticeable saturation (hue matters)
	// If max-min spread is significant, prefer cube to preserve tint
	maxC := max(r, max(g, b))
	minC := min(r, min(g, b))
	spread := maxC - minC

	// Only consider grayscale if color is nearly neutral (spread < 10)
	// AND grayscale is actually closer
	if spread < 10 && grayDist < cubeDist {
		return grayIndex
	}

	return cubeIndex
}

func hexTo256(hex string) (int, error) {
	c, err := hexToRgb(hex)
	if err != nil {
		return 0, err
	}
	return rgbTo256(c.r, c.g, c.b), nil
}

func fgAnsi(color ColorValue, mode ColorMode) (string, error) {
	switch v := color.(type) {
	case string:
		if v == "" {
			return "\x1b[39m", nil
		}
		if strings.HasPrefix(v, "#") {
			if mode == ColorModeTruecolor {
				c, err := hexToRgb(v)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.r, c.g, c.b), nil
			}
			index, err := hexTo256(v)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("\x1b[38;5;%dm", index), nil
		}
		return "", fmt.Errorf("invalid color value: %v", color)
	case int:
		return fmt.Sprintf("\x1b[38;5;%dm", v), nil
	case float64:
		// JSON-decoded numbers arrive as float64
		return fmt.Sprintf("\x1b[38;5;%dm", int(v)), nil
	}
	return "", fmt.Errorf("invalid color value: %v", color)
}

func bgAnsi(color ColorValue, mode ColorMode) (string, error) {
	switch v := color.(type) {
	case string:
		if v == "" {
			return "\x1b[49m", nil
		}
		if strings.HasPrefix(v, "#") {
			if mode == ColorModeTruecolor {
				c, err := hexToRgb(v)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", c.r, c.g, c.b), nil
			}
			index, err := hexTo256(v)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("\x1b[48;5;%dm", index), nil
		}
		return "", fmt.Errorf("invalid color value: %v", color)
	case int:
		return fmt.Sprintf("\x1b[48;5;%dm", v), nil
	case float64:
		return fmt.Sprintf("\x1b[48;5;%dm", int(v)), nil
	}
	return "", fmt.Errorf("invalid color value: %v", color)
}

func resolveVarRefs(value ColorValue, vars map[string]ColorValue, visited map[string]bool) (ColorValue, error) {
	switch v := value.(type) {
	case int, float64:
		return v, nil
	case string:
		if v == "" || strings.HasPrefix(v, "#") {
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
	out := make(map[string]ColorValue, len(colors)+4)
	for k, v := range colors {
		out[k] = v
	}
	if _, ok := out["thinkingMax"]; !ok {
		if v, ok := colors["thinkingXhigh"]; ok {
			out["thinkingMax"] = v
		}
	}
	if _, ok := out["scrollbarThumb"]; !ok {
		if v, ok := colors["selectedBg"]; ok {
			out["scrollbarThumb"] = v
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

// ============================================================================
// Theme Class
// ============================================================================

// ThemeOptions carries the constructor's optional fields (pi: options
// object; "" = undefined).
type ThemeOptions struct {
	Name       string
	SourcePath string
	SourceInfo *utils.SourceInfo
}

// Theme is the resolved theme (pi: class Theme).
type Theme struct {
	name       string
	sourcePath string
	// SourceInfo is assigned by the resource loader (pi: sourceInfo?).
	SourceInfo *utils.SourceInfo

	mu       sync.RWMutex
	fgColors map[ThemeColor]string
	bgColors map[ThemeBg]string
	mode     ColorMode
}

// NewTheme builds a theme from resolved color values (pi: constructor).
// fgColors/bgColors values are string (hex/""/var-resolved) or int
// (256-color index).
func NewTheme(fgColors map[string]ColorValue, bgColors map[string]ColorValue, mode ColorMode, options *ThemeOptions) (*Theme, error) {
	t := &Theme{
		mode:     mode,
		fgColors: map[ThemeColor]string{},
		bgColors: map[ThemeBg]string{},
	}
	if options != nil {
		t.name = options.Name
		t.sourcePath = options.SourcePath
		t.SourceInfo = options.SourceInfo
	}

	colors := make(map[string]ColorValue, len(fgColors)+2)
	for k, v := range fgColors {
		colors[k] = v
	}
	if _, ok := colors["thinkingMax"]; !ok {
		if v, ok := fgColors["thinkingXhigh"]; ok {
			colors["thinkingMax"] = v
		}
	}
	if _, ok := colors["searchMatchText"]; !ok {
		if v, ok := fgColors["text"]; ok {
			colors["searchMatchText"] = v
		}
	}
	for key, value := range colors {
		ansi, err := fgAnsi(value, mode)
		if err != nil {
			return nil, err
		}
		t.fgColors[ThemeColor(key)] = ansi
	}

	backgrounds := make(map[string]ColorValue, len(bgColors)+2)
	for k, v := range bgColors {
		backgrounds[k] = v
	}
	if _, ok := backgrounds["scrollbarThumb"]; !ok {
		if v, ok := bgColors["selectedBg"]; ok {
			backgrounds["scrollbarThumb"] = v
		}
	}
	if _, ok := backgrounds["searchMatchBg"]; !ok {
		if v, ok := bgColors["selectedBg"]; ok {
			backgrounds["searchMatchBg"] = v
		}
	}
	for key, value := range backgrounds {
		ansi, err := bgAnsi(value, mode)
		if err != nil {
			return nil, err
		}
		t.bgColors[ThemeBg(key)] = ansi
	}
	return t, nil
}

// GetName returns the theme name ("" = unnamed, pi: name?).
func (t *Theme) GetName() string {
	return t.name
}

// GetSourcePath returns the source path ("" = none, pi: sourcePath?).
func (t *Theme) GetSourcePath() string {
	return t.sourcePath
}

// Fg wraps text in the token's foreground color, resetting only the
// foreground (pi: fg).
func (t *Theme) Fg(color ThemeColor, text string) string {
	t.mu.RLock()
	ansi, ok := t.fgColors[color]
	t.mu.RUnlock()
	if !ok {
		panic(fmt.Sprintf("Unknown theme color: %s", color))
	}
	return ansi + text + "\x1b[39m"
}

// Bg wraps text in the token's background color, resetting only the
// background (pi: bg).
func (t *Theme) Bg(color ThemeBg, text string) string {
	t.mu.RLock()
	ansi, ok := t.bgColors[color]
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

// GetFgAnsi returns the raw ANSI prefix for a foreground token (pi:
// getFgAnsi).
func (t *Theme) GetFgAnsi(color ThemeColor) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ansi, ok := t.fgColors[color]
	if !ok {
		panic(fmt.Sprintf("Unknown theme color: %s", color))
	}
	return ansi
}

// GetBgAnsi returns the raw ANSI prefix for a background token (pi:
// getBgAnsi).
func (t *Theme) GetBgAnsi(color ThemeBg) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ansi, ok := t.bgColors[color]
	if !ok {
		panic(fmt.Sprintf("Unknown theme background color: %s", color))
	}
	return ansi
}

// GetColorMode returns the ANSI mode (pi: getColorMode).
func (t *Theme) GetColorMode() ColorMode {
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

	// Built-in themes (pi iterates the Record keys; pig pins dark/light)
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
		return strings.Compare(result[i].Name, result[j].Name) < 0
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

// validateColorValue checks the ColorValue union (string, or integer
// 0-255).
func validateColorValue(path string, value ColorValue) (string, bool) {
	switch v := value.(type) {
	case string:
		return path, true
	case int:
		if v < 0 || v > 255 {
			return path + ": color index must be between 0 and 255", false
		}
		return path, true
	case float64:
		if v != math.Trunc(v) || v < 0 || v > 255 {
			return path + ": color index must be an integer between 0 and 255", false
		}
		return path, true
	}
	return path + ": must be a string or a 256-color index", false
}

// validateThemeJson hand-validates the theme schema (pi: TypeBox Compile).
// Missing required colors keep pi's diagnostic formatting.
func validateThemeJson(label string, parsed any) (*ThemeJson, error) {
	top, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Invalid theme %q:\n  - /: must be object", label)
	}

	var missingColors []string
	var otherErrors []string

	name, hasName := top["name"]
	if !hasName {
		missingColors = append(missingColors, "name")
	} else if _, ok := name.(string); !ok {
		otherErrors = append(otherErrors, "  - /name: must be string")
	}
	if v, ok := top["$schema"]; ok {
		if _, ok := v.(string); !ok {
			otherErrors = append(otherErrors, "  - /$schema: must be string")
		}
	}
	if v, ok := top["vars"]; ok && v != nil {
		vars, ok := v.(map[string]any)
		if !ok {
			otherErrors = append(otherErrors, "  - /vars: must be object")
		} else {
			keys := make([]string, 0, len(vars))
			for k := range vars {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if msg, ok := validateColorValue("/vars/"+k, vars[k]); !ok {
					otherErrors = append(otherErrors, "  - "+msg)
				}
			}
		}
	}

	colorsRaw, hasColors := top["colors"]
	if !hasColors || colorsRaw == nil {
		// Entire colors object missing: report every required color.
		for _, c := range requiredThemeColors {
			missingColors = append(missingColors, string(c))
		}
		for _, c := range requiredThemeBgs {
			missingColors = append(missingColors, string(c))
		}
	} else {
		colors, ok := colorsRaw.(map[string]any)
		if !ok {
			otherErrors = append(otherErrors, "  - /colors: must be object")
		} else {
			for _, c := range requiredThemeColors {
				if _, ok := colors[string(c)]; !ok {
					missingColors = append(missingColors, string(c))
				}
			}
			for _, c := range requiredThemeBgs {
				if _, ok := colors[string(c)]; !ok {
					missingColors = append(missingColors, string(c))
				}
			}
			keys := make([]string, 0, len(colors))
			for k := range colors {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if msg, ok := validateColorValue("/colors/"+k, colors[k]); !ok {
					otherErrors = append(otherErrors, "  - "+msg)
				}
			}
		}
	}

	if v, ok := top["export"]; ok && v != nil {
		exportSection, ok := v.(map[string]any)
		if !ok {
			otherErrors = append(otherErrors, "  - /export: must be object")
		} else {
			for _, k := range []string{"pageBg", "cardBg", "infoBg"} {
				if val, ok := exportSection[k]; ok {
					if msg, ok := validateColorValue("/export/"+k, val); !ok {
						otherErrors = append(otherErrors, "  - "+msg)
					}
				}
			}
		}
	}

	if len(missingColors) > 0 || len(otherErrors) > 0 {
		errorMessage := fmt.Sprintf("Invalid theme %q:\n", label)
		if len(missingColors) > 0 {
			errorMessage += "\nMissing required color tokens:\n"
			sort.Strings(missingColors)
			for _, c := range missingColors {
				errorMessage += "  - " + c + "\n"
			}
			// pi joins with "\n" so the last entry has no trailing newline
			// before the fixed text; rebuild without the trailing newline.
			errorMessage = strings.TrimSuffix(errorMessage, "\n")
			errorMessage += "\n\nPlease add these colors to your theme's \"colors\" object."
			errorMessage += "\nSee the built-in themes (dark.json, light.json) for reference values."
		}
		if len(otherErrors) > 0 {
			errorMessage += "\n\nOther errors:\n" + strings.Join(otherErrors, "\n")
		}
		return nil, fmt.Errorf("%s", errorMessage)
	}

	// Re-marshal into the typed shape via JSON round-trip.
	raw, err := json.Marshal(parsed)
	if err != nil {
		return nil, err
	}
	var themeJson ThemeJson
	if err := json.Unmarshal(raw, &themeJson); err != nil {
		return nil, fmt.Errorf("Failed to parse theme %s: %v", label, err)
	}
	if err := assertThemeNameIsValid(themeJson.Name); err != nil {
		return nil, err
	}
	return &themeJson, nil
}

func parseThemeJsonContent(label string, content string) (*ThemeJson, error) {
	var parsed any
	decoder := json.NewDecoder(strings.NewReader(utils.StripBom(content)))
	if err := decoder.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("Failed to parse theme %s: %v", label, err)
	}
	return validateThemeJson(label, parsed)
}

func loadThemeJson(name string) (*ThemeJson, error) {
	builtin, err := getBuiltinThemes()
	if err != nil {
		return nil, err
	}
	if themeJson, ok := builtin[name]; ok {
		return themeJson, nil
	}
	registered, ok := registeredThemes[name]
	if ok {
		if registered.sourcePath != "" {
			content, err := os.ReadFile(registered.sourcePath)
			if err != nil {
				return nil, err
			}
			return parseThemeJsonContent(registered.sourcePath, string(content))
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

func createTheme(themeJson *ThemeJson, mode ColorMode, sourcePath string) (*Theme, error) {
	colorMode := mode
	if colorMode == "" {
		if tui.GetCapabilities().TrueColor {
			colorMode = ColorModeTruecolor
		} else {
			colorMode = ColorMode256
		}
	}
	resolvedColors, err := resolveThemeColors(withThemeColorFallbacks(themeJson.Colors), themeJson.Vars)
	if err != nil {
		return nil, err
	}
	fgColors := map[string]ColorValue{}
	bgColors := map[string]ColorValue{}
	bgColorKeys := map[string]bool{
		"selectedBg":      true,
		"scrollbarThumb":  true,
		"searchMatchBg":   true,
		"userMessageBg":   true,
		"customMessageBg": true,
		"toolPendingBg":   true,
		"toolSuccessBg":   true,
		"toolErrorBg":     true,
	}
	for key, value := range resolvedColors {
		if bgColorKeys[key] {
			bgColors[key] = value
		} else {
			fgColors[key] = value
		}
	}
	return NewTheme(fgColors, bgColors, colorMode, &ThemeOptions{
		Name:       themeJson.Name,
		SourcePath: sourcePath,
	})
}

// LoadThemeFromPath loads and validates a theme file (pi:
// loadThemeFromPath). mode "" = auto-detect from terminal capabilities.
func LoadThemeFromPath(themePath string, mode ColorMode) (*Theme, error) {
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

func loadTheme(name string, mode ColorMode) (*Theme, error) {
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

// TerminalThemeDetection is pi's TerminalThemeDetection.
type TerminalThemeDetection struct {
	Theme      TerminalTheme
	Source     string // "terminal background" | "COLORFGBG" | "fallback"
	Detail     string
	Confidence string // "high" | "low"
}

// TerminalBackgroundThemeDetector is pi's TerminalBackgroundThemeDetector.
// The blocking query replaces pi's promise; a nil color with nil error is
// pi's `undefined` result, an error is pi's rejection.
type TerminalBackgroundThemeDetector interface {
	QueryTerminalBackgroundColor(timeoutMs int) (*tui.RgbColor, error)
}

// TerminalAutoThemeDetector is pi's TerminalAutoThemeDetector
// (queryTerminalColorScheme is an optional member → capability interface).
type TerminalAutoThemeDetector interface {
	TerminalBackgroundThemeDetector
	QueryTerminalColorScheme(timeoutMs int) (*TerminalTheme, error)
}

// TerminalThemeDetectionOptions carries the env override (pi: { env? };
// nil = process environment).
type TerminalThemeDetectionOptions struct {
	Env map[string]string
}

func (o *TerminalThemeDetectionOptions) lookup(key string) string {
	if o == nil || o.Env == nil {
		return os.Getenv(key)
	}
	return o.Env[key]
}

func getColorFgBgBackgroundIndex(colorfgbg string) (int, bool) {
	parts := strings.Split(colorfgbg, ";")
	for i := len(parts) - 1; i >= 0; i-- {
		bg, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err == nil && bg >= 0 && bg <= 255 {
			return bg, true
		}
	}
	return 0, false
}

func getRgbColorLuminance(c tui.RgbColor) float64 {
	toLinear := func(channel int) float64 {
		value := float64(channel) / 255
		if value <= 0.03928 {
			return value / 12.92
		}
		return math.Pow((value+0.055)/1.055, 2.4)
	}
	return 0.2126*toLinear(c.R) + 0.7152*toLinear(c.G) + 0.0722*toLinear(c.B)
}

func getAnsiColorLuminance(index int) float64 {
	hex, _ := ansi256ToHex(index)
	c, _ := hexToRgb(hex)
	return getRgbColorLuminance(tui.RgbColor{R: c.r, G: c.g, B: c.b})
}

// GetThemeForRgbColor classifies a background color by luminance (pi:
// getThemeForRgbColor).
func GetThemeForRgbColor(rgbColor tui.RgbColor) TerminalTheme {
	if getRgbColorLuminance(rgbColor) >= 0.5 {
		return TerminalThemeLight
	}
	return TerminalThemeDark
}

// DetectTerminalBackgroundFromEnv detects the terminal appearance from
// environment hints (pi: detectTerminalBackgroundFromEnv).
func DetectTerminalBackgroundFromEnv(options *TerminalThemeDetectionOptions) TerminalThemeDetection {
	colorfgbg := options.lookup("COLORFGBG")
	if bg, ok := getColorFgBgBackgroundIndex(colorfgbg); ok {
		theme := TerminalThemeDark
		if getAnsiColorLuminance(bg) >= 0.5 {
			theme = TerminalThemeLight
		}
		return TerminalThemeDetection{
			Theme:      theme,
			Source:     "COLORFGBG",
			Detail:     fmt.Sprintf("background color index %d", bg),
			Confidence: "high",
		}
	}

	return TerminalThemeDetection{
		Theme:      TerminalThemeDark,
		Source:     "fallback",
		Detail:     "no terminal background hint found",
		Confidence: "low",
	}
}

// TerminalBackgroundThemeDetectionOptions carries the detector, timeout and
// env (pi: same-named interface).
type TerminalBackgroundThemeDetectionOptions struct {
	Ui        TerminalBackgroundThemeDetector
	TimeoutMs int
	Env       map[string]string
}

// DetectTerminalBackgroundTheme queries the terminal background and falls
// back to environment hints (pi: detectTerminalBackgroundTheme).
func DetectTerminalBackgroundTheme(options TerminalBackgroundThemeDetectionOptions) TerminalThemeDetection {
	if rgbColor, err := options.Ui.QueryTerminalBackgroundColor(options.TimeoutMs); err == nil && rgbColor != nil {
		return TerminalThemeDetection{
			Theme:      GetThemeForRgbColor(*rgbColor),
			Source:     "terminal background",
			Detail:     fmt.Sprintf("OSC 11 background rgb(%d, %d, %d)", rgbColor.R, rgbColor.G, rgbColor.B),
			Confidence: "high",
		}
	}

	return DetectTerminalBackgroundFromEnv(&TerminalThemeDetectionOptions{Env: options.Env})
}

// TerminalAutoThemeDetectionOptions carries the auto detector, timeout and
// env (pi: same-named interface).
type TerminalAutoThemeDetectionOptions struct {
	Ui        TerminalAutoThemeDetector
	TimeoutMs int
	Env       map[string]string
}

// DetectTerminalThemeForAuto prefers the terminal's color-scheme report
// and falls back to background detection (pi: detectTerminalThemeForAuto).
// Declared deviation: pi starts both queries concurrently; pig's blocking
// detector interface serializes them (color-scheme first, background as
// fallback).
func DetectTerminalThemeForAuto(options TerminalAutoThemeDetectionOptions) TerminalTheme {
	if scheme, err := options.Ui.QueryTerminalColorScheme(options.TimeoutMs); err == nil && scheme != nil {
		return *scheme
	}
	return DetectTerminalBackgroundTheme(TerminalBackgroundThemeDetectionOptions{
		Ui:        options.Ui,
		TimeoutMs: options.TimeoutMs,
		Env:       options.Env,
	}).Theme
}

// GetDefaultTheme returns the env-detected default theme name (pi:
// getDefaultTheme).
func GetDefaultTheme() string {
	return string(DetectTerminalBackgroundFromEnv(nil).Theme)
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

// InitTheme loads and installs the named theme ("" = env-detected
// default), falling back to dark silently (pi: initTheme). enableWatcher
// starts the custom-theme file watcher.
func InitTheme(themeName string, enableWatcher bool) {
	name := themeName
	if name == "" {
		name = GetDefaultTheme()
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
	// Theme is invalid - fall back to dark theme silently
	globalMu.Lock()
	currentThemeName = "dark"
	globalMu.Unlock()
	// Matches pi: the fallback load runs outside the catch guard; a broken
	// dark theme throws (pig: panics).
	SetGlobalTheme(mustLoadTheme("dark"))
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
	// Theme is invalid - fall back to dark theme
	globalMu.Lock()
	currentThemeName = "dark"
	globalMu.Unlock()
	SetGlobalTheme(mustLoadTheme("dark"))
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
// the dark fallback path, which pi leaves unguarded).
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
	if currentName == "" || currentName == "dark" || currentName == "light" {
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

// ansi256ToHex converts a 256-color index to a hex string (pi:
// ansi256ToHex).
func ansi256ToHex(index int) (string, error) {
	// Basic colors (0-15) - approximate common terminal values
	basicColors := [16]string{
		"#000000", "#800000", "#008000", "#808000", "#000080", "#800080",
		"#008080", "#c0c0c0", "#808080", "#ff0000", "#00ff00", "#ffff00",
		"#0000ff", "#ff00ff", "#00ffff", "#ffffff",
	}
	if index < 16 {
		return basicColors[index], nil
	}

	// Color cube (16-231): 6x6x6 = 216 colors
	if index < 232 {
		cubeIndex := index - 16
		r := cubeIndex / 36
		g := (cubeIndex % 36) / 6
		b := cubeIndex % 6
		toHex := func(n int) string {
			v := 0
			if n != 0 {
				v = 55 + n*40
			}
			return fmt.Sprintf("%02x", v)
		}
		return "#" + toHex(r) + toHex(g) + toHex(b), nil
	}

	// Grayscale (232-255): 24 shades
	if index < 256 {
		gray := 8 + (index-232)*10
		grayHex := fmt.Sprintf("%02x", gray)
		return "#" + grayHex + grayHex + grayHex, nil
	}
	return "", fmt.Errorf("invalid 256-color index: %d", index)
}

// GetResolvedThemeColors returns the resolved theme colors as CSS-compatible
// hex strings (pi: getResolvedThemeColors). Panics when the theme cannot be
// loaded, matching pi's unguarded loadThemeJson.
func GetResolvedThemeColors(themeName string) map[string]string {
	name := themeName
	if name == "" {
		globalMu.RLock()
		name = currentThemeName
		globalMu.RUnlock()
		if name == "" {
			name = GetDefaultTheme()
		}
	}
	isLight := name == "light"
	themeJson, err := loadThemeJson(name)
	if err != nil {
		panic(err)
	}
	resolved, err := resolveThemeColors(withThemeColorFallbacks(themeJson.Colors), themeJson.Vars)
	if err != nil {
		panic(err)
	}

	// Default text color for empty values (terminal uses default fg color)
	defaultText := "#e5e5e7"
	if isLight {
		defaultText = "#000000"
	}

	cssColors := make(map[string]string, len(resolved))
	for key, value := range resolved {
		switch v := value.(type) {
		case int:
			hex, _ := ansi256ToHex(v)
			cssColors[key] = hex
		case float64:
			hex, _ := ansi256ToHex(int(v))
			cssColors[key] = hex
		case string:
			if v == "" {
				// Empty means default terminal color - use sensible fallback
				// for HTML
				cssColors[key] = defaultText
			} else {
				cssColors[key] = v
			}
		}
	}
	return cssColors
}

// IsLightTheme reports whether the named theme is light (pi:
// isLightTheme).
func IsLightTheme(themeName string) bool {
	// Currently just check the name - could be extended to analyze colors
	return themeName == "light"
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
			name = GetDefaultTheme()
		}
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
			hex, _ := ansi256ToHex(v)
			return hex
		case float64:
			hex, _ := ansi256ToHex(int(v))
			return hex
		case string:
			if v == "" {
				return ""
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
