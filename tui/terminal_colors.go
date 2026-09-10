// tui/terminal_colors.go (pi: packages/tui/src/terminal-colors.ts) — OSC 11
// background color responses and color-scheme reports (CSI ? 997 n).
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

// TerminalColorScheme is the terminal's color-scheme preference.
type TerminalColorScheme = string

const (
	TerminalColorSchemeDark  TerminalColorScheme = "dark"
	TerminalColorSchemeLight TerminalColorScheme = "light"
)

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

var osc11BackgroundColorResponsePattern = regexp.MustCompile(`(?i)^\x1b\]11;([^\x07\x1b]*)(?:\x07|\x1b\\)$`)
var colorSchemeReportPattern = regexp.MustCompile(`^(?:\x1b\[\?997;(1|2)n)+$`)
var rgbaPrefixRegex = regexp.MustCompile(`(?i)^rgba?:`)

// IsOsc11BackgroundColorResponse reports whether data is an OSC 11 reply.
func IsOsc11BackgroundColorResponse(data string) bool {
	return osc11BackgroundColorResponsePattern.MatchString(data)
}

// ParseOsc11BackgroundColor parses an OSC 11 background color response.
// Returns nil when data is not a parsable response (pi: undefined).
func ParseOsc11BackgroundColor(data string) *RgbColor {
	m := osc11BackgroundColorResponsePattern.FindStringSubmatch(data)
	if m == nil {
		return nil
	}

	value := strings.TrimSpace(m[1])
	if strings.HasPrefix(value, "#") {
		hex := value[1:]
		isHex := func(s string) bool {
			for _, ch := range s {
				if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
					return false
				}
			}
			return s != ""
		}
		if len(hex) == 6 && isHex(hex) {
			rgb := hexToRgb(hex)
			return &rgb
		}
		if len(hex) == 12 && isHex(hex) {
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
// (CSI ? 997 ; 1 n dark / CSI ? 997 ; 2 n light). Returns "" when absent.
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
