// tui/terminal_colors.go (pi: packages/tui/src/terminal-colors.ts) — colors
// the terminal reports for its current theme: OSC 10/11/4 color replies and
// color-scheme reports (CSI ? 997 n).
package tui

import (
	"regexp"
	"strconv"
	"strings"
)

// RgbColor is an 8-bit RGB color triple.
type RgbColor struct {
	R int
	G int
	B int
}

// TerminalColorScheme is the terminal's color-scheme preference
// (pi: TerminalColorScheme = "dark" | "light").
type TerminalColorScheme = string

const (
	TerminalColorSchemeDark  TerminalColorScheme = "dark"
	TerminalColorSchemeLight TerminalColorScheme = "light"
)

// TerminalColors holds the colors the terminal reports for its current theme
// (pi: TerminalColors). Palette is only set when the terminal reported all
// 16 entries.
type TerminalColors struct {
	// Foreground is the default foreground (OSC 10); nil = not reported.
	Foreground *RgbColor
	// Background is the default background (OSC 11); nil = not reported.
	Background *RgbColor
	// Palette is ANSI colors 0-15 (OSC 4); nil = not all reported.
	Palette []RgbColor
}

func hexToRgb(hex string) RgbColor {
	normalized := strings.TrimPrefix(hex, "#")
	r, _ := strconv.ParseUint(normalized[0:2], 16, 32)
	g, _ := strconv.ParseUint(normalized[2:4], 16, 32)
	b, _ := strconv.ParseUint(normalized[4:6], 16, 32)
	return RgbColor{R: int(r), G: int(g), B: int(b)}
}

func parseOscHexChannel(channel string) (int, bool) {
	if channel == "" {
		return 0, false
	}
	for _, ch := range channel {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return 0, false
		}
	}
	max := 1
	for range channel {
		max *= 16
	}
	max--
	if max <= 0 {
		return 0, false
	}
	value, err := strconv.ParseUint(channel, 16, 64)
	if err != nil {
		return 0, false
	}
	// Math.round((parseInt(channel, 16) / max) * 255)
	return int((float64(value) / float64(max)) * 255), true
}

// OscColorTarget is what an OSC color reply reports: the default foreground
// (OSC 10), background (OSC 11), or a palette index (OSC 4)
// (pi: OscColorTarget = "foreground" | "background" | number).
type OscColorTarget struct {
	// PaletteIndex >= 0 selects the palette slot; -1 means foreground or background.
	PaletteIndex int
	// Foreground distinguishes foreground from background when PaletteIndex < 0.
	Foreground bool
}

func (t OscColorTarget) key() string {
	if t.PaletteIndex >= 0 {
		return strconv.Itoa(t.PaletteIndex)
	}
	if t.Foreground {
		return "foreground"
	}
	return "background"
}

var oscColorResponsePattern = regexp.MustCompile(`(?i)^\x1b\](?:(1[01])|4;(\d{1,3}));([^\x07\x1b]*)(?:\x07|\x1b\\)$`)
var colorSchemeReportPattern = regexp.MustCompile(`^(?:\x1b\[\?997;(1|2)n)+$`)
var rgbaPrefixRegex = regexp.MustCompile(`(?i)^rgba?:`)
var oscHex6Pattern = regexp.MustCompile(`(?i)^[0-9a-f]{6}$`)
var oscHex12Pattern = regexp.MustCompile(`(?i)^[0-9a-f]{12}$`)

// ParseOscColorResponse parses an OSC 10, 11, or 4 color reply. ok=false when
// data is not such a reply; rgb=nil when it is a reply with an unparseable
// color (pi: parseOscColorResponse).
func ParseOscColorResponse(data string) (OscColorTarget, *RgbColor, bool) {
	m := oscColorResponsePattern.FindStringSubmatch(data)
	if m == nil {
		return OscColorTarget{}, nil, false
	}
	var target OscColorTarget
	switch {
	case m[1] == "10":
		target = OscColorTarget{PaletteIndex: -1, Foreground: true}
	case m[1] == "11":
		target = OscColorTarget{PaletteIndex: -1}
	default:
		index, err := strconv.Atoi(m[2])
		if err != nil {
			return OscColorTarget{}, nil, false
		}
		target = OscColorTarget{PaletteIndex: index}
	}
	return target, parseOscColorValue(m[3]), true
}

func parseOscColorValue(rawValue string) *RgbColor {
	value := strings.TrimSpace(rawValue)
	if strings.HasPrefix(value, "#") {
		hex := value[1:]
		if oscHex6Pattern.MatchString(hex) {
			rgb := hexToRgb(value)
			return &rgb
		}
		if oscHex12Pattern.MatchString(hex) {
			r, okR := parseOscHexChannel(hex[0:4])
			g, okG := parseOscHexChannel(hex[4:8])
			b, okB := parseOscHexChannel(hex[8:12])
			if okR && okG && okB {
				return &RgbColor{R: r, G: g, B: b}
			}
		}
		return nil
	}

	rgbValue := rgbaPrefixRegex.ReplaceAllString(value, "")
	parts := strings.Split(rgbValue, "/")
	if len(parts) < 3 {
		return nil
	}
	r, okR := parseOscHexChannel(parts[0])
	g, okG := parseOscHexChannel(parts[1])
	b, okB := parseOscHexChannel(parts[2])
	if okR && okG && okB {
		return &RgbColor{R: r, G: g, B: b}
	}
	return nil
}

// ParseTerminalColorSchemeReport parses a color-scheme report
// (CSI ? 997 ; 1 n dark / CSI ? 997 ; 2 n light). Returns ok=false when
// absent.
func ParseTerminalColorSchemeReport(data string) (TerminalColorScheme, bool) {
	m := colorSchemeReportPattern.FindStringSubmatch(data)
	if m == nil {
		return "", false
	}
	if m[1] == "2" {
		return TerminalColorSchemeLight, true
	}
	return TerminalColorSchemeDark, true
}
