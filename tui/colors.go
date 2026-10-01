// tui/colors.go (pi: packages/tui/src/colors.ts) — the Color type (palette
// index, sRGB, OKLCH), parseColor, color math (mix, OKLCH/OKHSL/RGB/hex
// conversions) and ANSI encoding (truecolor and 256-color) with styleText.
package tui

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// TerminalColorMode is the ANSI color encoding mode
// (pi: TerminalColorMode = "256color" | "truecolor").
type TerminalColorMode = string

const (
	ColorMode256color   TerminalColorMode = "256color"
	ColorModeTruecolorN TerminalColorMode = "truecolor"
)

// ColorMixSpace is the color space mixColors interpolates in
// (pi: ColorMixSpace = "oklch" | "srgb").
type ColorMixSpace = string

const (
	MixSpaceOklch ColorMixSpace = "oklch"
	MixSpaceSrgb  ColorMixSpace = "srgb"
)

// ColorKind discriminates the Color union (pi: kind: "indexed" | "rgb" | "oklch").
type ColorKind int

const (
	ColorKindIndexed ColorKind = iota
	ColorKindRgb
	ColorKindOklch
)

// Color is a concrete color: an ANSI palette index, sRGB channels, or OKLCH
// components (pi: Color = IndexedColor | RgbColorValue | OklchColorValue).
// Every color can be converted to sRGB, so color math never fails. The kind
// decides which fields are meaningful.
type Color struct {
	Kind  ColorKind
	Index int // indexed
	R     int // rgb
	G     int
	B     int
	L     float64 // oklch
	C     float64
	H     float64
}

// OklchChannels holds OKLCH channels (pi: OklchChannels).
type OklchChannels struct {
	L float64
	C float64
	H float64
}

// TextAttributes carries the text attributes styleTextWithAnsi applies
// (pi: TextAttributes).
type TextAttributes struct {
	Bold          bool
	Dim           bool
	Italic        bool
	Underline     bool
	Inverse       bool
	Strikethrough bool
}

// TextStyle is TextAttributes plus foreground/background colors (pi: TextStyle).
type TextStyle struct {
	TextAttributes
	Fg *Color
	Bg *Color
}

func requireFinite(value float64, name string) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s must be finite", name)
	}
	return nil
}

// NewIndexedColor builds an indexed (ANSI 256) color (pi: indexedColor).
func NewIndexedColor(index int) (Color, error) {
	if index < 0 || index > 255 {
		return Color{}, fmt.Errorf("ANSI color index must be an integer from 0 to 255: %d", index)
	}
	return Color{Kind: ColorKindIndexed, Index: index}, nil
}

// NewRgbColor builds an sRGB color (pi: rgbColor).
func NewRgbColor(r, g, b int) (Color, error) {
	for _, ch := range []struct {
		name  string
		value int
	}{{"r", r}, {"g", g}, {"b", b}} {
		if ch.value < 0 || ch.value > 255 {
			return Color{}, fmt.Errorf("%s must be between 0 and 255: %d", ch.name, ch.value)
		}
	}
	return Color{Kind: ColorKindRgb, R: r, G: g, B: b}, nil
}

// NewOklchColor builds an OKLCH color (pi: oklchColor).
func NewOklchColor(l, c, h float64) (Color, error) {
	for _, ch := range []struct {
		name  string
		value float64
	}{{"l", l}, {"c", c}, {"h", h}} {
		if err := requireFinite(ch.value, ch.name); err != nil {
			return Color{}, err
		}
	}
	if l < 0 || l > 1 {
		return Color{}, fmt.Errorf("l must be between 0 and 1: %v", l)
	}
	if c < 0 {
		return Color{}, fmt.Errorf("c must not be negative: %v", c)
	}
	return Color{Kind: ColorKindOklch, L: l, C: c, H: math.Mod(math.Mod(h, 360)+360, 360)}, nil
}

// NewOkhslColor builds an OKHSL color, converted to sRGB. Saturation is
// relative to the sRGB gamut at the hue and lightness, so equal saturation
// looks equally colorful across hues and lightness (pi: okhslColor).
//   - h: hue in degrees. s: saturation 0-1. l: lightness 0-1.
func NewOkhslColor(h, s, l float64) (Color, error) {
	for _, ch := range []struct {
		name  string
		value float64
	}{{"h", h}, {"s", s}, {"l", l}} {
		if err := requireFinite(ch.value, ch.name); err != nil {
			return Color{}, err
		}
	}
	if s < 0 || s > 1 {
		return Color{}, fmt.Errorf("s must be between 0 and 1: %v", s)
	}
	if l < 0 || l > 1 {
		return Color{}, fmt.Errorf("l must be between 0 and 1: %v", l)
	}
	rgb := OkhslToRgb(h, s, l)
	return Color{Kind: ColorKindRgb, R: rgb.R, G: rgb.G, B: rgb.B}, nil
}

// ColorToOkhsl reads a color's OKHSL channels (pi: colorToOkhsl).
func ColorToOkhsl(color Color) OkhslChannels {
	return RgbToOkhsl(ColorToRgb(color))
}

var (
	colorHexPattern    = regexp.MustCompile(`(?i)^#([\da-f]{3}|[\da-f]{6})$`)
	colorNumberPattern = `[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?`
	colorOklchPattern  = regexp.MustCompile(`(?i)^oklch\(\s*(` + colorNumberPattern + `)(%)?\s+(` + colorNumberPattern + `)\s+(` + colorNumberPattern + `)(?:deg)?\s*\)$`)
	colorOkhslPattern  = regexp.MustCompile(`(?i)^okhsl\(\s*(` + colorNumberPattern + `)(?:deg)?\s+(` + colorNumberPattern + `)(%)?\s+(` + colorNumberPattern + `)(%)?\s*\)$`)
)

// ParseColor parses a theme color value: a 256-color index (number), a
// #rgb/#rrggbb hex string, or an oklch()/okhsl() string (pi: parseColor).
func ParseColor(value any) (Color, error) {
	switch v := value.(type) {
	case int:
		return NewIndexedColor(v)
	case float64:
		return NewIndexedColor(int(v))
	case string:
		if m := colorHexPattern.FindStringSubmatch(v); m != nil {
			digits := m[1]
			if len(digits) == 3 {
				digits = string([]byte{digits[0], digits[0], digits[1], digits[1], digits[2], digits[2]})
			}
			r, _ := strconv.ParseInt(digits[0:2], 16, 32)
			g, _ := strconv.ParseInt(digits[2:4], 16, 32)
			b, _ := strconv.ParseInt(digits[4:6], 16, 32)
			return Color{Kind: ColorKindRgb, R: int(r), G: int(g), B: int(b)}, nil
		}
		if m := colorOklchPattern.FindStringSubmatch(v); m != nil {
			lightness, err := strconv.ParseFloat(m[1], 64)
			if err != nil {
				return Color{}, fmt.Errorf("invalid color value: %s", v)
			}
			if m[2] == "%" {
				lightness /= 100
			}
			c, err := strconv.ParseFloat(m[3], 64)
			if err != nil {
				return Color{}, fmt.Errorf("invalid color value: %s", v)
			}
			h, err := strconv.ParseFloat(m[4], 64)
			if err != nil {
				return Color{}, fmt.Errorf("invalid color value: %s", v)
			}
			return NewOklchColor(lightness, c, h)
		}
		if m := colorOkhslPattern.FindStringSubmatch(v); m != nil {
			h, err := strconv.ParseFloat(m[1], 64)
			if err != nil {
				return Color{}, fmt.Errorf("invalid color value: %s", v)
			}
			s, err := strconv.ParseFloat(m[2], 64)
			if err != nil {
				return Color{}, fmt.Errorf("invalid color value: %s", v)
			}
			if m[3] == "%" {
				s /= 100
			}
			l, err := strconv.ParseFloat(m[4], 64)
			if err != nil {
				return Color{}, fmt.Errorf("invalid color value: %s", v)
			}
			if m[5] == "%" {
				l /= 100
			}
			return NewOkhslColor(h, s, l)
		}
	}
	return Color{}, fmt.Errorf("invalid color value: %v", value)
}

var basicColors = [16]RgbColor{
	{0, 0, 0}, {128, 0, 0}, {0, 128, 0}, {128, 128, 0},
	{0, 0, 128}, {128, 0, 128}, {0, 128, 128}, {192, 192, 192},
	{128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
	{0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
}

var cubeValues256 = [6]int{0, 95, 135, 175, 215, 255}

func grayValues256() [24]int {
	var values [24]int
	for i := range values {
		values[i] = 8 + i*10
	}
	return values
}

// indexedToRgb maps an ANSI 256 index to its reference sRGB color.
func indexedToRgb(index int) RgbColor {
	if index < 16 {
		return basicColors[index]
	}
	if index < 232 {
		cubeIndex := index - 16
		return RgbColor{
			R: cubeValues256[cubeIndex/36],
			G: cubeValues256[(cubeIndex%36)/6],
			B: cubeValues256[cubeIndex%6],
		}
	}
	gray := 8 + (index-232)*10
	return RgbColor{R: gray, G: gray, B: gray}
}

func isInSrgbGamut(linear vector3) bool {
	const epsilon = 1e-7
	for _, channel := range linear {
		if channel < -epsilon || channel > 1+epsilon {
			return false
		}
	}
	return true
}

// oklchToRgb converts OKLCH to sRGB, reducing chroma by bisection when the
// color leaves the gamut (hue fixed).
func oklchToRgb(l, c, h float64) RgbColor {
	// Gamut mapping keeps the hue fixed, so its direction is computed once and scaled by chroma.
	radians := h * math.Pi / 180
	cos := math.Cos(radians)
	sin := math.Sin(radians)
	atChroma := func(chroma float64) vector3 {
		return OklabToLinearSrgb(vector3{l, chroma * cos, chroma * sin})
	}

	direct := atChroma(c)
	if isInSrgbGamut(direct) {
		return LinearSrgbToRgb(direct)
	}

	// Reduce chroma until the color fits. The achromatic color is always in gamut, so it is the
	// fallback when no bisection step fits, e.g. `oklch(100% 0.3 150)` must map to white.
	linear := atChroma(0)
	low, high := 0.0, c
	for i := 0; i < 20; i++ {
		chroma := (low + high) / 2
		candidate := atChroma(chroma)
		if isInSrgbGamut(candidate) {
			low = chroma
			linear = candidate
		} else {
			high = chroma
		}
	}
	return LinearSrgbToRgb(linear)
}

// ColorToRgb converts any color to its sRGB channels (pi: colorToRgb).
func ColorToRgb(color Color) RgbColor {
	switch color.Kind {
	case ColorKindIndexed:
		return indexedToRgb(color.Index)
	case ColorKindRgb:
		return RgbColor{R: color.R, G: color.G, B: color.B}
	case ColorKindOklch:
		return oklchToRgb(color.L, color.C, color.H)
	}
	return RgbColor{}
}

// ColorToOklch converts any color to OKLCH (pi: colorToOklch).
func ColorToOklch(color Color) OklchChannels {
	if color.Kind == ColorKindOklch {
		return OklchChannels{L: color.L, C: color.C, H: color.H}
	}
	lab := RgbToOklab(ColorToRgb(color))
	l, a, b := lab[0], lab[1], lab[2]
	return OklchChannels{L: l, C: math.Hypot(a, b), H: math.Mod(math.Atan2(b, a)*180/math.Pi+360, 360)}
}

// ColorToHex converts a color to a #rrggbb hex string (pi: colorToHex).
func ColorToHex(color Color) string {
	rgb := ColorToRgb(color)
	channel := func(value int) string {
		return fmt.Sprintf("%02x", value)
	}
	return "#" + channel(rgb.R) + channel(rgb.G) + channel(rgb.B)
}

// MixColors interpolates two colors in oklch (default) or srgb space
// (pi: mixColors). amount 0 = first, 1 = second.
func MixColors(first, second Color, amount float64, space ColorMixSpace) (Color, error) {
	if err := requireFinite(amount, "amount"); err != nil {
		return Color{}, err
	}
	if amount < 0 || amount > 1 {
		return Color{}, fmt.Errorf("amount must be between 0 and 1: %v", amount)
	}

	if space == MixSpaceSrgb {
		a := ColorToRgb(first)
		b := ColorToRgb(second)
		round := func(x float64) int { return int(math.Round(x)) }
		return NewRgbColor(
			round(float64(a.R)+float64(b.R-a.R)*amount),
			round(float64(a.G)+float64(b.G-a.G)*amount),
			round(float64(a.B)+float64(b.B-a.B)*amount),
		)
	}

	a := ColorToOklch(first)
	b := ColorToOklch(second)
	firstHue := a.H
	if a.C < 1e-7 {
		firstHue = b.H
	}
	secondHue := b.H
	if b.C < 1e-7 {
		secondHue = firstHue
	}
	hueDelta := math.Mod(math.Mod(secondHue-firstHue+540, 360), 360) - 180
	return NewOklchColor(a.L+(b.L-a.L)*amount, a.C+(b.C-a.C)*amount, firstHue+hueDelta*amount)
}

func findClosest256(values []int, target int) int {
	closestIndex := 0
	closestDistance := math.MaxFloat64
	for index, value := range values {
		distance := math.Abs(float64(target - value))
		if distance < closestDistance {
			closestIndex = index
			closestDistance = distance
		}
	}
	return closestIndex
}

func colorDistance256(first, second RgbColor) float64 {
	dr := float64(first.R - second.R)
	dg := float64(first.G - second.G)
	db := float64(first.B - second.B)
	return dr*dr*0.299 + dg*dg*0.587 + db*db*0.114
}

// rgbToAnsi256 approximates an sRGB color with the closest ANSI 256 index.
func rgbToAnsi256(color RgbColor) int {
	cubeValuesSlice := cubeValues256[:]
	rIndex := findClosest256(cubeValuesSlice, color.R)
	gIndex := findClosest256(cubeValuesSlice, color.G)
	bIndex := findClosest256(cubeValuesSlice, color.B)
	cubeColor := RgbColor{R: cubeValues256[rIndex], G: cubeValues256[gIndex], B: cubeValues256[bIndex]}
	cubeIndex := 16 + 36*rIndex + 6*gIndex + bIndex

	gray := int(math.Round(0.299*float64(color.R) + 0.587*float64(color.G) + 0.114*float64(color.B)))
	grays := grayValues256()
	grayOffset := findClosest256(grays[:], gray)
	grayValue := grays[grayOffset]
	maxC := max(color.R, max(color.G, color.B))
	minC := min(color.R, min(color.G, color.B))
	spread := maxC - minC
	if spread < 10 &&
		colorDistance256(color, RgbColor{R: grayValue, G: grayValue, B: grayValue}) < colorDistance256(color, cubeColor) {
		return 232 + grayOffset
	}
	return cubeIndex
}

// colorAnsi encodes a color as an SGR foreground/background sequence.
func colorAnsi(color Color, mode TerminalColorMode, background bool) string {
	sgr := 38
	if background {
		sgr = 48
	}
	if color.Kind == ColorKindIndexed {
		return fmt.Sprintf("\x1b[%d;5;%dm", sgr, color.Index)
	}

	rgb := ColorToRgb(color)
	if mode == ColorModeTruecolorN {
		return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", sgr, rgb.R, rgb.G, rgb.B)
	}
	return fmt.Sprintf("\x1b[%d;5;%dm", sgr, rgbToAnsi256(rgb))
}

// ForegroundAnsi encodes a color as an SGR foreground sequence (pi: foregroundAnsi).
func ForegroundAnsi(color Color, mode TerminalColorMode) string {
	return colorAnsi(color, mode, false)
}

// BackgroundAnsi encodes a color as an SGR background sequence (pi: backgroundAnsi).
func BackgroundAnsi(color Color, mode TerminalColorMode) string {
	return colorAnsi(color, mode, true)
}

// StyleText styles text with colors and attributes (pi: styleText).
func StyleText(text string, options TextStyle, mode TerminalColorMode) string {
	var fgAnsi, bgAnsi string
	if options.Fg != nil {
		fgAnsi = ForegroundAnsi(*options.Fg, mode)
	}
	if options.Bg != nil {
		bgAnsi = BackgroundAnsi(*options.Bg, mode)
	}
	return StyleTextWithAnsi(text, fgAnsi, bgAnsi, options.TextAttributes)
}

// StyleTextWithAnsi is like StyleText, but with precomputed color escape
// sequences, e.g. cached theme colors. Colors in options are ignored
// (pi: styleTextWithAnsi).
func StyleTextWithAnsi(text string, fgAnsi string, bgAnsi string, options TextAttributes) string {
	// Resets are prepended so they close in reverse order of the opening sequences.
	prefix := ""
	suffix := ""
	if fgAnsi != "" {
		prefix += fgAnsi
		suffix = "\x1b[39m"
	}
	if bgAnsi != "" {
		prefix += bgAnsi
		suffix = "\x1b[49m" + suffix
	}
	if options.Bold {
		prefix += "\x1b[1m"
	}
	if options.Dim {
		prefix += "\x1b[2m"
	}
	if options.Bold || options.Dim {
		suffix = "\x1b[22m" + suffix
	}
	if options.Italic {
		prefix += "\x1b[3m"
		suffix = "\x1b[23m" + suffix
	}
	if options.Underline {
		prefix += "\x1b[4m"
		suffix = "\x1b[24m" + suffix
	}
	if options.Inverse {
		prefix += "\x1b[7m"
		suffix = "\x1b[27m" + suffix
	}
	if options.Strikethrough {
		prefix += "\x1b[9m"
		suffix = "\x1b[29m" + suffix
	}
	return prefix + text + suffix
}
