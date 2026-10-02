// system_theme.go (pi: src/modes/interactive/theme/system-theme.ts) — the
// `system` theme: pi's colors derived from the terminal's own theme.
//
// Every token belongs to a color family (its hue) and has contrast rules: it
// must reach a contrast level on the background and on the panels it is drawn
// on. Hue and saturation come from the terminal's palette color for the
// family's ANSI slot, or from the family's own hue when the terminal reports
// no palette. Lightness comes from the rules alone. Colors are built in
// OKHSL, whose saturation is relative to the sRGB gamut, and fade toward gray
// near black and white.
//
// A contrast level is a target-lightness curve: the OKLab lightness a token
// needs, given the lightness of the surface below it. The curves were fitted
// to the reference theme design from the "Pi themes: system and light/dark"
// review. On dark backgrounds they aim for nearly fixed lightness; on light
// backgrounds the required difference grows as the background darkens.
//
// Depending on what the terminal reports, the theme is generated in one of
// three tiers:
//   - background and palette: hues from the palette, lightness from the
//     background;
//   - background only: the families' own hues, lightness from the background;
//   - nothing: ANSI palette indices and the default colors, which the
//     terminal renders itself.
package theme

import (
	"math"

	"github.com/ZoroSpace/pig/tui"
)

// SystemThemeName is the reserved name of the generated system theme
// (pi: SYSTEM_THEME_NAME).
const SystemThemeName = "system"

// ============================================================================
// Recipe: color families and their tokens
// ============================================================================

// family is a family's OKHSL hue and saturation: `max` at mid lightness,
// falling toward `min` at black and white (pi: Family).
type family struct {
	hue    float64
	satMin float64
	satMax float64
	// slot is the ANSI palette slot the family takes its hue and saturation from.
	slot int
}

var families = map[string]family{
	"neutral":            {hue: 231.49, satMin: 0.02, satMax: 0.08, slot: 8},
	"blue":               {hue: 231.49, satMin: 0.1, satMax: 0.68, slot: 4},
	"green":              {hue: 158.68, satMin: 0.1, satMax: 0.76, slot: 2},
	"red":                {hue: 20, satMin: 0.1, satMax: 0.92, slot: 1},
	"yellow":             {hue: 82.36, satMin: 0.5, satMax: 1, slot: 3},
	"orange":             {hue: 52, satMin: 0.12, satMax: 0.85, slot: 3},
	"violet":             {hue: 295, satMin: 0.2, satMax: 0.6, slot: 5},
	"calamine":           {hue: 202.43, satMin: 0.1, satMax: 0.74, slot: 6},
	"thinkingSlate":      {hue: 231.49, satMin: 0.08, satMax: 0.2, slot: 4},
	"thinkingBlue":       {hue: 231.49, satMin: 0.2, satMax: 0.45, slot: 4},
	"thinkingPeriwinkle": {hue: 263.25, satMin: 0.3, satMax: 0.6, slot: 6},
	"thinkingViolet":     {hue: 295, satMin: 0.4, satMax: 0.75, slot: 5},
	"thinkingMagenta":    {hue: 337.5, satMin: 0.5, satMax: 0.85, slot: 13},
	"thinkingRed":        {hue: 20, satMin: 0.95, satMax: 1, slot: 1},
}

var tokenFamilies = map[ThemeToken]string{
	"selectedBg":      "blue",
	"searchMatchBg":   "orange",
	"userMessageBg":   "blue",
	"customMessageBg": "violet",
	"toolPendingBg":   "neutral",
	"toolSuccessBg":   "green",
	"toolErrorBg":     "red",

	"text":              "neutral",
	"userMessageText":   "neutral",
	"customMessageText": "neutral",
	"toolTitle":         "neutral",
	"syntaxOperator":    "neutral",
	"syntaxPunctuation": "neutral",
	"muted":             "neutral",
	"dim":               "neutral",
	"thinkingText":      "neutral",
	"toolOutput":        "neutral",
	"mdLinkUrl":         "neutral",
	"mdQuote":           "neutral",
	"mdQuoteBorder":     "neutral",
	"mdHr":              "neutral",
	"mdCodeBlockBorder": "neutral",
	"toolDiffContext":   "neutral",
	"syntaxComment":     "neutral",
	"scrollbarTrack":    "neutral",
	"scrollbarThumb":    "neutral",
	"searchMatchText":   "neutral",
	"borderMuted":       "neutral",

	"accent":             "violet",
	"borderAccent":       "violet",
	"customMessageLabel": "violet",
	"mdCode":             "violet",
	"mdListBullet":       "violet",
	"syntaxType":         "violet",
	"border":             "blue",
	"mdLink":             "blue",
	"syntaxKeyword":      "blue",
	"syntaxVariable":     "calamine",
	"success":            "green",
	"mdCodeBlock":        "green",
	"toolDiffAdded":      "green",
	"bashMode":           "green",
	"syntaxNumber":       "green",
	"error":              "red",
	"toolDiffRemoved":    "red",
	"warning":            "yellow",
	"mdHeading":          "yellow",
	"syntaxFunction":     "yellow",
	"syntaxString":       "orange",

	"thinkingOff":     "neutral",
	"thinkingMinimal": "thinkingSlate",
	"thinkingLow":     "thinkingBlue",
	"thinkingMedium":  "thinkingPeriwinkle",
	"thinkingHigh":    "thinkingViolet",
	"thinkingXhigh":   "thinkingMagenta",
	"thinkingMax":     "thinkingRed",
}

// tokenSlots are palette slots for tokens that would otherwise share a hue
// with a similar token (pi: TOKEN_SLOTS).
var tokenSlots = map[ThemeToken]int{"syntaxString": 2, "syntaxNumber": 5, "searchMatchBg": 3}

// ============================================================================
// Contrast levels and rules
// ============================================================================

// curve is a target-lightness curve: a polynomial in the surface's OKLab
// lightness giving the OKLab lightness a token needs on it. `reachable` is
// the range of surface lightness where the level can be reached; beyond it
// the level is relaxed (pi: Curve).
type curve struct {
	coefficients []float64
	reachable    [2]float64
}

type levelCurves struct {
	dark  curve
	light curve
}

var levels = map[string]levelCurves{
	"panel": {
		dark:  curve{coefficients: []float64{0.29131, -0.39746, 2.33185, -0.85524, -1.2076, 0.86276}, reachable: [2]float64{0, 0.979}},
		light: curve{coefficients: []float64{-3.74073, 27.94549, -78.44258, 112.6798, -79.60015, 22.11277}, reachable: [2]float64{0.348, 1}},
	},
	"track": {
		dark:  curve{coefficients: []float64{0.39028, -0.23015, 0.83573, 2.43829, -4.38292, 2.01582}, reachable: [2]float64{0, 0.946}},
		light: curve{coefficients: []float64{-5.24921, 38.37322, -107.28833, 152.10005, -106.17127, 29.18061}, reachable: [2]float64{0.368, 1}},
	},
	"thinking0": {
		dark:  curve{coefficients: []float64{0.52988, -0.05809, -0.30924, 4.63567, -6.52933, 2.89108}, reachable: [2]float64{0, 0.873}},
		light: curve{coefficients: []float64{-28.27749, 182.85284, -469.62416, 603.15916, -384.59976, 97.35147}, reachable: [2]float64{0.51, 1}},
	},
	"thinking1": {
		dark:  curve{coefficients: []float64{0.55278, -0.03667, -0.45659, 4.95347, -6.90265, 3.0706}, reachable: [2]float64{0, 0.858}},
		light: curve{coefficients: []float64{-37.10484, 235.86282, -596.62344, 754.3633, -474.00763, 118.3551}, reachable: [2]float64{0.535, 1}},
	},
	"thinking2": {
		dark:  curve{coefficients: []float64{0.57486, -0.01765, -0.58987, 5.25227, -7.27175, 3.25532}, reachable: [2]float64{0, 0.842}},
		light: curve{coefficients: []float64{-59.89653, 377.05024, -945.07843, 1182.03145, -734.96375, 181.68658}, reachable: [2]float64{0.556, 1}},
	},
	"thinking3": {
		dark:  curve{coefficients: []float64{0.59621, -0.00062, -0.71148, 5.53588, -7.6392, 3.44606}, reachable: [2]float64{0, 0.827}},
		light: curve{coefficients: []float64{-72.07122, 445.84082, -1099.57352, 1353.88793, -829.53392, 202.26164}, reachable: [2]float64{0.58, 1}},
	},
	"thinking4": {
		dark:  curve{coefficients: []float64{0.61691, 0.01462, -0.82288, 5.80651, -8.00641, 3.64333}, reachable: [2]float64{0, 0.811}},
		light: curve{coefficients: []float64{-110.14338, 674.21488, -1645.75941, 2004.32367, -1215.15899, 293.3183}, reachable: [2]float64{0.6, 1}},
	},
	"thinking5": {
		dark:  curve{coefficients: []float64{0.63702, 0.02826, -0.92498, 6.06465, -8.37246, 3.84651}, reachable: [2]float64{0, 0.795}},
		light: curve{coefficients: []float64{-175.47701, 1063.54495, -2570.70594, 3098.80776, -1860.15527, 444.76392}, reachable: [2]float64{0.62, 1}},
	},
	"thinking6": {
		dark:  curve{coefficients: []float64{0.65658, 0.04044, -1.01835, 6.30989, -8.73529, 4.05439}, reachable: [2]float64{0, 0.779}},
		light: curve{coefficients: []float64{-183.81712, 1094.70055, -2602.68539, 3088.71276, -1826.91131, 430.75931}, reachable: [2]float64{0.643, 1}},
	},
	"subtle": {
		dark:  curve{coefficients: []float64{0.56762, -0.02475, -0.5383, 5.12628, -7.10931, 3.17324}, reachable: [2]float64{0, 0.848}},
		light: curve{coefficients: []float64{-232.85459, 1376.54473, -3249.11801, 3827.91186, -2248.29472, 526.55751}, reachable: [2]float64{0.657, 1}},
	},
	"thumb": {
		dark:  curve{coefficients: []float64{0.60323, 0.00278, -0.73328, 5.57157, -7.68067, 3.46933}, reachable: [2]float64{0, 0.823}},
		light: curve{coefficients: []float64{-82.89897, 511.01355, -1255.98095, 1540.76821, -940.68087, 228.58523}, reachable: [2]float64{0.586, 1}},
	},
	"readable": {
		dark:  curve{coefficients: []float64{0.66937, 0.04704, -1.06871, 6.43941, -8.9332, 4.17229}, reachable: [2]float64{0, 0.77}},
		light: curve{coefficients: []float64{-1554.52576, 8733.56817, -19604.93507, 21977.72696, -12300.99599, 2749.81288}, reachable: [2]float64{0.751, 1}},
	},
	"emphasis": {
		dark:  curve{coefficients: []float64{0.7303, 0.07695, -1.31626, 7.1681, -10.14436, 4.92846}, reachable: [2]float64{0, 0.712}},
		light: curve{coefficients: []float64{-4948.31942, 26870.91986, -58334.48399, 63280.17197, -34298.01053, 7430.30146}, reachable: [2]float64{0.811, 1}},
	},
	"textOnPanel": {
		dark:  curve{coefficients: []float64{0.86713, 0.05232, -0.89428, 4.79014, -5.5432, 1.75023}, reachable: [2]float64{0, 0.542}},
		light: curve{coefficients: []float64{-8570.89457, 43954.60805, -90084.00702, 92220.6791, -47152.15802, 9632.27113}, reachable: [2]float64{0.867, 1}},
	},
	"text": {
		dark:  curve{coefficients: []float64{0.89242, 0.02311, -0.44862, 2.34417, -0.06084, -2.63844}, reachable: [2]float64{0, 0.5}},
		light: curve{coefficients: []float64{-2004.67048, 6664.47299, -6060.70202, -1792.61209, 5133.82359, -1939.85583}, reachable: [2]float64{0.894, 1}},
	},
}

var toolPanels = []ThemeToken{"toolPendingBg", "toolSuccessBg", "toolErrorBg"}
var messagePanels = []ThemeToken{"userMessageBg", "customMessageBg"}
var panels = []ThemeToken{
	"userMessageBg",
	"toolPendingBg",
	"toolSuccessBg",
	"toolErrorBg",
	"selectedBg",
	"searchMatchBg",
	"customMessageBg",
}

var thinkingTokens = []ThemeColor{
	"thinkingOff",
	"thinkingMinimal",
	"thinkingLow",
	"thinkingMedium",
	"thinkingHigh",
	"thinkingXhigh",
	"thinkingMax",
}
var thinkingLevels = []string{"thinking0", "thinking1", "thinking2", "thinking3", "thinking4", "thinking5", "thinking6"}

// rule assigns one token a contrast level on the surfaces it is drawn on.
type rule struct {
	token ThemeToken
	on    []string
	level string
}

func eachRule(tokens []ThemeToken, on []string, level string) []rule {
	out := make([]rule, 0, len(tokens))
	for _, token := range tokens {
		out = append(out, rule{token: token, on: on, level: level})
	}
	return out
}

func appendAll(lists ...[]rule) []rule {
	out := []rule{}
	for _, list := range lists {
		out = append(out, list...)
	}
	return out
}

var rules = appendAll(
	eachRule(panels, []string{"background"}, "panel"),
	[]rule{
		{token: "text", on: []string{"background"}, level: "text"},
		{token: "text", on: []string{"selectedBg"}, level: "textOnPanel"},
		{token: "userMessageText", on: []string{"userMessageBg"}, level: "textOnPanel"},
		{token: "toolTitle", on: toStrings(toolPanels), level: "textOnPanel"},
	},
	eachRule([]ThemeToken{"accent", "success", "error", "warning"},
		append(toStrings([]ThemeToken{"background", "selectedBg"}), toStrings(toolPanels)...), "readable"),
	[]rule{{token: "muted", on: append(toStrings([]ThemeToken{"background", "selectedBg", "customMessageBg"}), toStrings(toolPanels)...), level: "readable"}},
	[]rule{{token: "dim", on: append(toStrings([]ThemeToken{"background", "selectedBg", "customMessageBg"}), toStrings(toolPanels)...), level: "subtle"}},
	[]rule{{token: "thinkingText", on: []string{"background"}, level: "readable"}},
	[]rule{{token: "customMessageText", on: append([]string{"customMessageBg"}, toStrings(toolPanels)...), level: "readable"}},
	[]rule{{token: "customMessageLabel", on: append(toStrings([]ThemeToken{"background", "customMessageBg", "selectedBg"}), toStrings(toolPanels)...), level: "readable"}},
	[]rule{{token: "toolOutput", on: append([]string{"background"}, toStrings(toolPanels)...), level: "readable"}},
	eachRule([]ThemeToken{"mdHeading", "mdLink", "mdLinkUrl", "mdCode", "mdQuote", "mdCodeBlockBorder", "mdListBullet"},
		append(toStrings([]ThemeToken{"background"}), toStrings(messagePanels)...), "readable"),
	[]rule{{token: "mdCodeBlock", on: append(toStrings([]ThemeToken{"background"}), append(toStrings(messagePanels), toStrings(toolPanels)...)...), level: "readable"}},
	eachRule([]ThemeToken{"toolDiffAdded", "toolDiffRemoved", "toolDiffContext"},
		append(toStrings([]ThemeToken{"background"}), toStrings(toolPanels)...), "readable"),
	eachRule([]ThemeToken{
		"syntaxComment",
		"syntaxKeyword",
		"syntaxFunction",
		"syntaxVariable",
		"syntaxString",
		"syntaxNumber",
		"syntaxType",
		"syntaxOperator",
		"syntaxPunctuation",
	}, append(toStrings([]ThemeToken{"background"}), append(toStrings(messagePanels), toStrings(toolPanels)...)...), "readable"),
	[]rule{{token: "searchMatchText", on: []string{"searchMatchBg"}, level: "readable"}},
	eachRule([]ThemeToken{"bashMode", "border", "borderAccent"}, []string{"background"}, "readable"),
	[]rule{{token: "borderMuted", on: []string{"background"}, level: "subtle"}},
	eachRule([]ThemeToken{"mdQuoteBorder", "mdHr"},
		append(toStrings([]ThemeToken{"background"}), append(toStrings(messagePanels), toStrings(toolPanels)...)...), "readable"),
	[]rule{{token: "scrollbarTrack", on: []string{"background"}, level: "track"}},
	[]rule{{token: "scrollbarThumb", on: []string{"scrollbarTrack"}, level: "thumb"}},
	thinkingRules(),
)

func toStrings(tokens []ThemeToken) []string {
	out := make([]string, len(tokens))
	for i, t := range tokens {
		out[i] = string(t)
	}
	return out
}

func thinkingRules() []rule {
	out := make([]rule, len(thinkingTokens))
	for i, token := range thinkingTokens {
		out[i] = rule{token: token, on: []string{"background"}, level: thinkingLevels[i]}
	}
	return out
}

// readableFloor: relaxation compresses levels stronger than this one toward
// it before weakening all levels (pi: READABLE_FLOOR).
var readableFloor = map[TerminalTheme]string{
	TerminalThemeDark:  "readable",
	TerminalThemeLight: "subtle",
}

// foregroundLevel is the level body text uses the terminal's foreground when
// it reaches — clearly stronger than muted (pi: FOREGROUND_LEVEL).
const foregroundLevel = "emphasis"

// foregroundTokens are text-level tokens that take the terminal's foreground.
var foregroundTokens = []ThemeColor{"text", "userMessageText", "toolTitle"}

// textMinimumWcagContrast is the WCAG 2 contrast ratio body text must reach
// on the surfaces it is drawn on.
const textMinimumWcagContrast = 4.5

// solveOrder lists tokens in dependency order: every surface before the
// tokens drawn on it (pi: SOLVE_ORDER).
var solveOrder = buildSolveOrder()

func buildSolveOrder() []ThemeToken {
	var order []ThemeToken
	seen := map[ThemeToken]bool{}
	var visit func(token ThemeToken)
	visit = func(token ThemeToken) {
		if seen[token] {
			return
		}
		for _, r := range rules {
			if r.token != token {
				continue
			}
			for _, surface := range r.on {
				if surface != "background" {
					visit(ThemeToken(surface))
				}
			}
		}
		seen[token] = true
		order = append(order, token)
	}
	for _, r := range rules {
		visit(r.token)
	}
	return order
}

// ============================================================================
// Public API
// ============================================================================

// SystemThemeInput carries the terminal's reported colors (pi:
// SystemThemeInput).
type SystemThemeInput struct {
	Foreground *tui.RgbColor
	Background *tui.RgbColor
	// Palette is ANSI colors 0-15.
	Palette []tui.RgbColor
	// Saturation is a multiplier from 0 (grayscale) to 1. The first frame
	// renders in grayscale until colors arrive. nil = 1.
	Saturation *float64
	// AppearanceHint is the appearance when the terminal did not report its
	// background, e.g. from its light/dark report or COLORFGBG. "" = unset.
	AppearanceHint TerminalTheme
}

// SystemThemeColors is the generated system theme (pi: SystemThemeColors).
type SystemThemeColors struct {
	// Colors maps tokens to hex colors, ANSI palette indices, or "" for the
	// terminal default.
	Colors map[string]ColorValue
	// Dim lists foreground tokens rendered faint (SGR 2), for terminals that
	// did not report colors.
	Dim []ThemeColor
	// Appearance is the detected appearance; nil when the terminal reported
	// nothing (pi: appearance?).
	Appearance *TerminalTheme
}

// oklabLightnessOf is the OKLab lightness of an sRGB color, 0-1.
func oklabLightnessOf(color tui.RgbColor) float64 {
	c, err := tui.NewRgbColor(color.R, color.G, color.B)
	if err != nil {
		return 0
	}
	return tui.ColorToOklch(c).L
}

// RelativeLuminance is the WCAG 2 relative luminance (pi: relativeLuminance).
func RelativeLuminance(c tui.RgbColor) float64 {
	linear := func(channel int) float64 {
		value := float64(channel) / 255
		if value <= 0.04045 {
			return value / 12.92
		}
		return math.Pow((value+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

// WcagContrast is the WCAG 2 contrast ratio, 1-21 (pi: wcagContrast).
func WcagContrast(first, second tui.RgbColor) float64 {
	a := RelativeLuminance(first)
	b := RelativeLuminance(second)
	hi, lo := math.Max(a, b), math.Min(a, b)
	return (hi + 0.05) / (lo + 0.05)
}

// TerminalAppearance reports whether a terminal is dark or light, from its
// reported colors: the direction of its own foreground when text can be
// readable that way, otherwise dark when white text has more contrast on the
// background than black text (pi: terminalAppearance).
func TerminalAppearance(background tui.RgbColor, foreground *tui.RgbColor) TerminalTheme {
	white := tui.RgbColor{R: 255, G: 255, B: 255}
	black := tui.RgbColor{R: 0, G: 0, B: 0}
	whiteContrast := WcagContrast(white, background)
	blackContrast := WcagContrast(black, background)
	if foreground != nil {
		foregroundL := oklabLightnessOf(*foreground)
		backgroundL := oklabLightnessOf(background)
		if math.Abs(foregroundL-backgroundL) > 0.05 {
			appearance := TerminalThemeDark
			if foregroundL < backgroundL {
				appearance = TerminalThemeLight
			}
			best := whiteContrast
			if appearance == TerminalThemeLight {
				best = blackContrast
			}
			if best >= textMinimumWcagContrast {
				return appearance
			}
		}
	}
	if whiteContrast >= blackContrast {
		return TerminalThemeDark
	}
	return TerminalThemeLight
}

// ============================================================================
// Generation
// ============================================================================

func clamp01(value, min, max float64) float64 { return math.Min(max, math.Max(min, value)) }

func hexOf(c tui.RgbColor) string {
	out := make([]byte, 0, 7)
	out = append(out, '#')
	for _, channel := range []int{c.R, c.G, c.B} {
		v := channel
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		const hexDigits = "0123456789abcdef"
		out = append(out, hexDigits[v>>4], hexDigits[v&0xf])
	}
	return string(out)
}

// bellWeight is the saturation weight at a lightness: a Gaussian (center
// 0.5, sigma 0.25), 0 at black and white, 1 in the middle.
func bellWeight(lightness float64) float64 {
	gaussian := func(x float64) float64 { return math.Exp(-((x - 0.5) * (x - 0.5)) / (2 * 0.25 * 0.25)) }
	return (gaussian(lightness) - gaussian(0)) / (1 - gaussian(0))
}

// saturationCurve is a family's saturation curve relative to its maximum:
// 1 at mid lightness, `min / max` at black and white.
func saturationCurve(f family, lightness float64) float64 {
	floor := 1.0
	if f.satMax > 0 {
		floor = f.satMin / f.satMax
	}
	return floor + (1-floor)*bellWeight(lightness)
}

// levelTarget is the target lightness for a level on a surface; ok=false
// where the level cannot be reached (pi: undefined).
func levelTarget(level string, appearance TerminalTheme, surfaceL float64) (float64, bool) {
	curves, ok := levels[level]
	if !ok {
		return 0, false
	}
	c := curves.dark
	if appearance == TerminalThemeLight {
		c = curves.light
	}
	if surfaceL < c.reachable[0] || surfaceL > c.reachable[1] {
		return 0, false
	}
	sum := 0.0
	for power, coefficient := range c.coefficients {
		sum += coefficient * math.Pow(surfaceL, float64(power))
	}
	return sum, true
}

// okhslOf reads a color's OKHSL channels.
func okhslOf(c tui.RgbColor) tui.OkhslChannels {
	color, err := tui.NewRgbColor(c.R, c.G, c.B)
	if err != nil {
		return tui.OkhslChannels{}
	}
	return tui.ColorToOkhsl(color)
}

// sourceColor is a terminal color's OKHSL channels and its OKLCH chroma
// (pi: SourceColor).
type sourceColor struct {
	tui.OkhslChannels
	chroma float64
}

// sourceOf reads a color's OKHSL channels and OKLCH chroma.
func sourceOf(c tui.RgbColor) sourceColor {
	color, err := tui.NewRgbColor(c.R, c.G, c.B)
	if err != nil {
		return sourceColor{}
	}
	return sourceColor{OkhslChannels: tui.ColorToOkhsl(color), chroma: tui.ColorToOklch(color).C}
}

// anchored is a source color's hue at another OKHSL lightness. Its saturation
// applies at its own lightness and falls off toward black and white along the
// family's saturation curve, never rising above it.
//
// OKHSL saturation is relative to the most chroma sRGB allows at a lightness,
// so the same saturation can mean more chroma elsewhere: Catppuccin Frappe's
// pink #f4b8e4 (chroma 0.089) would become #eb76d1 (0.180) at the lightness
// the accent needs. Chroma is therefore also capped at the source's, with the
// same falloff (pi: #10293).
func anchored(source sourceColor, f family, lightness, saturation float64) tui.RgbColor {
	anchor := saturationCurve(f, source.L)
	falloff := 1.0
	if anchor > 0 {
		falloff = math.Min(1, saturationCurve(f, lightness)/anchor)
	}
	color, err := tui.NewOkhslColor(source.H, source.S*falloff*saturation, lightness)
	if err != nil {
		return tui.RgbColor{}
	}
	chromaCap := source.chroma * falloff * saturation
	if channels := tui.ColorToOklch(color); channels.C > chromaCap {
		capped, err := tui.NewOklchColor(channels.L, chromaCap, source.H)
		if err != nil {
			return tui.RgbColor{}
		}
		return tui.ColorToRgb(capped)
	}
	return tui.ColorToRgb(color)
}

// withTextContrast moves a text color toward white or black until it reaches
// the WCAG minimum on every surface.
func withTextContrast(color tui.RgbColor, surfaces []tui.RgbColor, lighter bool) tui.RgbColor {
	meets := func(candidate tui.RgbColor) bool {
		for _, surface := range surfaces {
			if WcagContrast(candidate, surface) < textMinimumWcagContrast {
				return false
			}
		}
		return true
	}
	if meets(color) {
		return color
	}
	okhsl := okhslOf(color)
	at := func(lightness float64) tui.RgbColor {
		c, err := tui.NewOkhslColor(okhsl.H, okhsl.S, lightness)
		if err != nil {
			return tui.RgbColor{}
		}
		return tui.ColorToRgb(c)
	}
	extreme := 0.0
	if lighter {
		extreme = 1
	}
	if !meets(at(extreme)) {
		return at(extreme)
	}
	low, high := okhsl.L, extreme
	for i := 0; i < 20; i++ {
		middle := (low + high) / 2
		if meets(at(middle)) {
			high = middle
		} else {
			low = middle
		}
	}
	return at(high)
}

// indexedColors covers terminals that reported nothing: the terminal renders
// ANSI indices 0-15 and the default colors with its own theme, so they fit
// any background. Neutral tokens below body text are faint (SGR 2) instead of
// bright black, which some themes make nearly invisible. Panels have no
// background (pi: indexedColors).
func indexedColors(saturation float64, appearance *TerminalTheme) SystemThemeColors {
	colors := make(map[string]ColorValue, len(tokenFamilies))
	dim := []ThemeColor{}
	for token, familyName := range tokenFamilies {
		if containsToken(panels, token) {
			colors[string(token)] = ""
			continue
		}
		neutral := familyName == "neutral"
		if !neutral && saturation > 0 {
			if slot, ok := tokenSlots[token]; ok {
				colors[string(token)] = slot
			} else {
				colors[string(token)] = families[familyName].slot
			}
		} else {
			colors[string(token)] = ""
		}
		if neutral && !containsColor(foregroundTokens, token) {
			dim = append(dim, token)
		}
	}
	return SystemThemeColors{Colors: colors, Dim: dim, Appearance: appearance}
}

func containsToken(list []ThemeToken, token ThemeToken) bool {
	for _, t := range list {
		if t == token {
			return true
		}
	}
	return false
}

func containsColor(list []ThemeColor, token ThemeColor) bool {
	for _, t := range list {
		if t == token {
			return true
		}
	}
	return false
}

// GenerateSystemThemeColors generates the system theme's colors from the
// terminal's reported colors (pi: generateSystemThemeColors).
func GenerateSystemThemeColors(input SystemThemeInput) SystemThemeColors {
	saturation := 1.0
	if input.Saturation != nil {
		saturation = clamp01(*input.Saturation, 0, 1)
	}
	background, foreground := input.Background, input.Foreground
	if background == nil {
		return indexedColors(saturation, appearancePointer(input.AppearanceHint))
	}
	var palette map[int]sourceColor
	if len(input.Palette) == 16 {
		palette = make(map[int]sourceColor, 16)
		for i, color := range input.Palette {
			palette[i] = sourceOf(color)
		}
	}

	appearance := TerminalAppearance(*background, foreground)
	lighter := appearance == TerminalThemeDark
	extreme := 0.0
	if lighter {
		extreme = 1
	}
	backgroundL := oklabLightnessOf(*background)

	// paint computes a token's color at an OKLab lightness. With a palette,
	// the palette color's saturation applies at its own lightness and falls
	// off toward black and white along the family's curve, never rising
	// above it.
	paint := func(token ThemeToken, oklabL float64) tui.RgbColor {
		lightness := tui.OklabToOkhslLightness(oklabL)
		f := families[tokenFamilies[token]]
		if palette == nil {
			return mustOkhsl(f.hue, (f.satMin+(f.satMax-f.satMin)*bellWeight(lightness))*saturation, lightness)
		}
		slot := f.slot
		if s, ok := tokenSlots[token]; ok {
			slot = s
		}
		return anchored(palette[slot], f, lightness, saturation)
	}

	// target is the lightness a rule needs on a surface, relaxed by t: from 0
	// to 1, levels stronger than the readable floor move toward it; from 1 to
	// 2, all levels move toward the surface itself.
	target := func(level string, surfaceL, t float64) (float64, bool) {
		reached, reachedOk := levelTarget(level, appearance, surfaceL)
		if !reachedOk && t == 0 {
			return 0, false
		}
		if !reachedOk {
			reached = extreme
		}
		distance := reached - surfaceL
		floor := extreme - surfaceL
		if floorLevel, floorOk := levelTarget(readableFloor[appearance], appearance, surfaceL); floorOk {
			floor = floorLevel - surfaceL
		}
		compressed := distance
		if math.Abs(distance) > math.Abs(floor) {
			compressed = distance - (distance-floor)*math.Min(t, 1)
		}
		return surfaceL + compressed*(1-math.Max(0, t-1)), true
	}

	// limitPanel keeps a panel light enough (or dark enough) that white (or
	// black) text still reaches the body text minimum on it. This only
	// matters for backgrounds near mid-gray, where it barely does on the
	// background.
	extremeText := tui.RgbColor{R: 0, G: 0, B: 0}
	if lighter {
		extremeText = tui.RgbColor{R: 255, G: 255, B: 255}
	}
	readable := func(color tui.RgbColor) bool {
		return WcagContrast(extremeText, color) >= textMinimumWcagContrast
	}
	limitPanel := func(token ThemeToken, l float64) tui.RgbColor {
		color := paint(token, l)
		if readable(color) {
			return color
		}
		low, high := backgroundL, l
		for i := 0; i < 20; i++ {
			middle := (low + high) / 2
			if readable(paint(token, middle)) {
				low = middle
			} else {
				high = middle
			}
		}
		return paint(token, low)
	}

	solve := func(t float64) (map[string]tui.RgbColor, bool) {
		colors := map[string]tui.RgbColor{"background": *background}
		for _, token := range solveOrder {
			var targets []float64
			ok := true
			for _, r := range rules {
				if r.token != token {
					continue
				}
				for _, surface := range r.on {
					surfaceColor, hasSurface := colors[surface]
					if !hasSurface {
						surfaceColor = *background
					}
					value, valueOk := target(r.level, oklabLightnessOf(surfaceColor), t)
					if !valueOk || value < 0 || value > 1 {
						ok = false
						break
					}
					targets = append(targets, value)
				}
				if !ok {
					break
				}
			}
			if !ok {
				return nil, false
			}
			l := targets[0]
			if lighter {
				for _, value := range targets {
					if value > l {
						l = value
					}
				}
			} else {
				for _, value := range targets {
					if value < l {
						l = value
					}
				}
			}
			if containsToken(panels, token) {
				colors[string(token)] = limitPanel(token, l)
			} else {
				colors[string(token)] = paint(token, l)
			}
		}
		return colors, true
	}

	relaxation := 0.0
	colors, ok := solve(0)
	if !ok {
		// Mid-gray backgrounds cannot fit every level: relax as little as
		// possible. Full relaxation always fits.
		low, high := 0.0, 2.0
		colors, _ = solve(high)
		for i := 0; i < 20; i++ {
			middle := (low + high) / 2
			if attempt, attemptOk := solve(middle); attemptOk {
				high = middle
				colors = attempt
			} else {
				low = middle
			}
		}
		relaxation = high
	}
	solved := colors
	if solved == nil {
		solved = map[string]tui.RgbColor{}
	}
	surfacesOf := func(token ThemeToken) []tui.RgbColor {
		var out []tui.RgbColor
		for _, r := range rules {
			if r.token != token {
				continue
			}
			for _, surface := range r.on {
				color, hasColor := solved[surface]
				if !hasColor {
					color = *background
				}
				out = append(out, color)
			}
		}
		return out
	}

	result := make(map[string]ColorValue, len(tokenFamilies))
	for token := range tokenFamilies {
		if color, hasColor := solved[string(token)]; hasColor {
			result[string(token)] = hexOf(color)
		} else {
			result[string(token)] = ""
		}
	}

	for _, token := range foregroundTokens {
		surfaces := surfacesOf(ThemeToken(token))
		// Body text uses the terminal's own foreground where it is clearly
		// stronger than muted text; otherwise the foreground's hue at just
		// enough lightness.
		text, hasText := solved[string(token)]
		if foreground != nil {
			allTargetsValid := true
			var targets []float64
			for _, surface := range surfaces {
				value, valueOk := target(foregroundLevel, oklabLightnessOf(surface), relaxation)
				if !valueOk || value < 0 || value > 1 {
					allTargetsValid = false
					break
				}
				targets = append(targets, value)
			}
			if allTargetsValid {
				needed := targets[0]
				if lighter {
					for _, value := range targets {
						if value > needed {
							needed = value
						}
					}
				} else {
					for _, value := range targets {
						if value < needed {
							needed = value
						}
					}
				}
				foregroundL := oklabLightnessOf(*foreground)
				if lighter && foregroundL >= needed || !lighter && foregroundL <= needed {
					result[string(token)] = ""
					continue
				}
				text = anchored(sourceOf(*foreground), families["neutral"], tui.OklabToOkhslLightness(needed), saturation)
				hasText = true
			}
		}
		// Body text keeps at least 4.5:1 on the surfaces it is drawn on, even
		// on relaxed mid-gray backgrounds.
		if hasText {
			result[string(token)] = hexOf(withTextContrast(text, surfaces, lighter))
		}
	}
	return SystemThemeColors{Colors: result, Dim: []ThemeColor{}, Appearance: &appearance}
}

// mustOkhsl builds an OKHSL color's sRGB form; the inputs are always in
// range by construction (saturation/lightness clamped by callers' curves).
func mustOkhsl(h, s, l float64) tui.RgbColor {
	color, err := tui.NewOkhslColor(h, s, l)
	if err != nil {
		return tui.RgbColor{}
	}
	return tui.ColorToRgb(color)
}

func appearancePointer(hint TerminalTheme) *TerminalTheme {
	if hint == "" {
		return nil
	}
	return &hint
}
