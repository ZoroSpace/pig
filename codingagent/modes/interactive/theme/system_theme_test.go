package theme

import (
	"regexp"
	"testing"

	"github.com/ZoroSpace/pig/tui"
)

// Port of test/system-theme.test.ts (v0.99.1).

func mustRgb(t *testing.T, hex string) tui.RgbColor {
	t.Helper()
	color, err := tui.ParseColor(hex)
	if err != nil {
		t.Fatal(err)
	}
	return tui.ColorToRgb(color)
}

func oklchLightness(c tui.RgbColor) float64 {
	return tui.ColorToOklch(tui.Color{Kind: tui.ColorKindRgb, R: c.R, G: c.G, B: c.B}).L
}
func oklchHue(c tui.RgbColor) float64 {
	return tui.ColorToOklch(tui.Color{Kind: tui.ColorKindRgb, R: c.R, G: c.G, B: c.B}).H
}

func systemTestTerminals(t *testing.T) map[string]SystemThemeInput {
	t.Helper()
	var draculaPalette []tui.RgbColor
	for _, hex := range []string{
		"#21222c", "#ff5555", "#50fa7b", "#f1fa8c", "#bd93f9", "#ff79c6", "#8be9fd", "#f8f8f2",
		"#6272a4", "#ff6e6e", "#69ff94", "#ffffa5", "#d6acff", "#ff92df", "#a4ffff", "#ffffff",
	} {
		draculaPalette = append(draculaPalette, mustRgb(t, hex))
	}
	dracula := SystemThemeInput{
		Background: rgbPointer(mustRgb(t, "#282a36")),
		Foreground: rgbPointer(mustRgb(t, "#f8f8f2")),
		Palette:    draculaPalette,
	}
	return map[string]SystemThemeInput{
		"dracula":        dracula,
		"solarizedLight": {Background: rgbPointer(mustRgb(t, "#fdf6e3")), Foreground: rgbPointer(mustRgb(t, "#657b83"))},
		"backgroundOnly": {Background: rgbPointer(mustRgb(t, "#1e1e1e"))},
		"midGray":        {Background: rgbPointer(mustRgb(t, "#808080")), Foreground: rgbPointer(mustRgb(t, "#ffffff"))},
	}
}

func rgbPointer(c tui.RgbColor) *tui.RgbColor { return &c }

var systemTestPanels = []ThemeToken{"userMessageBg", "toolPendingBg", "toolSuccessBg", "toolErrorBg", "selectedBg"}

func resolvedToken(input SystemThemeInput, token ThemeToken) tui.RgbColor {
	value := GenerateSystemThemeColors(input).Colors[string(token)]
	if str, ok := value.(string); ok && str == "" {
		if containsToken(systemTestPanels, token) {
			return *input.Background
		}
		return *input.Foreground
	}
	color, _ := tui.ParseColor(value)
	return tui.ColorToRgb(color)
}

func TestGenerateSystemThemeColors(t *testing.T) {
	terminals := systemTestTerminals(t)
	cleanup := func() { SetTerminalColors(tui.TerminalColors{}) }
	t.Cleanup(cleanup)

	t.Run("keeps body text readable (WCAG 4.5:1) on the background and its panels", func(t *testing.T) {
		for name, input := range terminals {
			text := resolvedToken(input, "text")
			for _, surface := range []tui.RgbColor{*input.Background, resolvedToken(input, "selectedBg")} {
				if WcagContrast(text, surface) < 4.5 {
					t.Errorf("%s: text contrast %.2f", name, WcagContrast(text, surface))
				}
			}
			if WcagContrast(resolvedToken(input, "toolTitle"), resolvedToken(input, "toolErrorBg")) < 4.5 {
				t.Errorf("%s: toolTitle contrast", name)
			}
		}
	})

	t.Run("orders foreground roles by contrast and keeps panels close to the background", func(t *testing.T) {
		for name, input := range terminals {
			background := oklchLightness(*input.Background)
			offset := func(token ThemeToken) float64 { return oklchLightness(resolvedToken(input, token)) - background }
			// On mid-gray the levels collapse to the strongest reachable color.
			if name != "midGray" {
				abs := func(x float64) float64 {
					if x < 0 {
						return -x
					}
					return x
				}
				if !(abs(offset("text")) > abs(offset("muted"))) {
					t.Errorf("%s: text offset %v not above muted %v", name, offset("text"), offset("muted"))
				}
				if !(abs(offset("muted")) > abs(offset("dim"))) {
					t.Errorf("%s: muted offset %v not above dim %v", name, offset("muted"), offset("dim"))
				}
			}
			lighter := GenerateSystemThemeColors(input).Appearance != nil && *GenerateSystemThemeColors(input).Appearance == TerminalThemeDark
			for _, panel := range systemTestPanels {
				if WcagContrast(resolvedToken(input, panel), *input.Background) >= 2 {
					t.Errorf("%s %s: panel contrast >= 2", name, panel)
				}
				if (offset(panel) > 0) != lighter {
					t.Errorf("%s %s: panel offset direction mismatch", name, panel)
				}
			}
		}
	})

	t.Run("uses the terminal foreground and palette hues", func(t *testing.T) {
		dracula := terminals["dracula"]
		if text, _ := GenerateSystemThemeColors(dracula).Colors["text"].(string); text != "" {
			t.Errorf("dracula text = %q, want terminal default", text)
		}
		// Solarized's foreground is below 4.5:1 on its own background, so text is darkened.
		if text, _ := GenerateSystemThemeColors(terminals["solarizedLight"]).Colors["text"].(string); text == "" {
			t.Error("solarizedLight text should not be the terminal default")
		}
		hueDelta := oklchHue(resolvedToken(dracula, "error")) - oklchHue(dracula.Palette[1])
		if hueDelta < -8 || hueDelta > 8 {
			t.Errorf("error hue delta %v", hueDelta)
		}
	})

	// https://github.com/earendil-works/pi/issues/10255
	t.Run("keeps pastel palette colors pastel at other lightnesses", func(t *testing.T) {
		var frappePalette []tui.RgbColor
		for _, hex := range []string{
			"#51576d", "#e78284", "#a6d189", "#e5c890", "#8caaee", "#f4b8e4", "#81c8be", "#b5bfe2",
			"#626880", "#e67172", "#8ec772", "#d9ba73", "#7b9ef0", "#f2a4db", "#5abfb5", "#a5adce",
		} {
			frappePalette = append(frappePalette, mustRgb(t, hex))
		}
		frappe := SystemThemeInput{
			Background: rgbPointer(mustRgb(t, "#303446")),
			Foreground: rgbPointer(mustRgb(t, "#c6d0f5")),
			Palette:    frappePalette,
		}
		oklchChroma := func(c tui.RgbColor) float64 {
			return tui.ColorToOklch(tui.Color{Kind: tui.ColorKindRgb, R: c.R, G: c.G, B: c.B}).C
		}
		pink := frappe.Palette[5]
		accent := resolvedToken(frappe, "accent")
		// The accent is darker than the pink, but must not gain chroma (it was 2x before the cap).
		if oklchLightness(accent) >= oklchLightness(pink)-0.05 {
			t.Errorf("accent lightness %v not below pink %v - 0.05", oklchLightness(accent), oklchLightness(pink))
		}
		if c := oklchChroma(accent); c > oklchChroma(pink)*1.03 {
			t.Errorf("accent chroma %v exceeds pink chroma %v * 1.03", c, oklchChroma(pink))
		}
		for _, panel := range []ThemeToken{"userMessageBg", "customMessageBg"} {
			if c := oklchChroma(resolvedToken(frappe, panel)); c > 0.1 {
				t.Errorf("%s chroma %v > 0.1", panel, c)
			}
		}
	})

	t.Run("renders grayscale at zero saturation", func(t *testing.T) {
		dracula := terminals["dracula"]
		zero := 0.0
		colors := GenerateSystemThemeColors(SystemThemeInput{
			Background: dracula.Background, Foreground: dracula.Foreground, Palette: dracula.Palette, Saturation: &zero,
		}).Colors
		color, err := tui.ParseColor(colors["error"])
		if err != nil {
			t.Fatal(err)
		}
		if c := tui.ColorToOklch(color).C; c >= 0.005 {
			t.Errorf("error chroma %v at zero saturation", c)
		}
	})

	t.Run("falls back to palette indices and faint text without a background", func(t *testing.T) {
		hint := TerminalThemeLight
		zero := 0.0
		out := GenerateSystemThemeColors(SystemThemeInput{AppearanceHint: hint})
		if out.Appearance == nil || *out.Appearance != TerminalThemeLight {
			t.Fatalf("appearance = %v", out.Appearance)
		}
		if out.Colors["error"] != 1 || out.Colors["text"] != "" || out.Colors["userMessageBg"] != "" {
			t.Fatalf("colors = %v/%v/%v, want 1/\"\"/\"\"", out.Colors["error"], out.Colors["text"], out.Colors["userMessageBg"])
		}
		foundMuted := false
		for _, token := range out.Dim {
			if token == ColorMuted {
				foundMuted = true
			}
		}
		if !foundMuted {
			t.Error("dim should contain muted")
		}
		if errColor, _ := GenerateSystemThemeColors(SystemThemeInput{Saturation: &zero}).Colors["error"].(string); errColor != "" {
			t.Errorf("error at zero saturation = %q, want terminal default", errColor)
		}
	})
}

func TestSystemTheme(t *testing.T) {
	t.Cleanup(func() { SetTerminalColors(tui.TerminalColors{}) })

	t.Run("is listed first, has no export colors, and is generated from the terminal colors", func(t *testing.T) {
		if themes := GetAvailableThemes(); len(themes) == 0 || themes[0] != SystemThemeName {
			t.Fatalf("available themes[0] = %v", themes)
		}
		if colors := GetThemeExportColors(SystemThemeName); colors != (ThemeExportColors{}) {
			t.Fatalf("export colors = %+v", colors)
		}

		dracula := systemTestTerminals(t)["dracula"]
		SetTerminalColors(tui.TerminalColors{
			Foreground: dracula.Foreground,
			Background: dracula.Background,
			Palette:    dracula.Palette,
		})
		theme := GetThemeByName(SystemThemeName)
		if theme == nil {
			t.Fatal("system theme not found")
		}
		if theme.Appearance() != TerminalThemeDark {
			t.Fatalf("appearance = %q", theme.Appearance())
		}
		if theme.GetFgAnsi(ColorText) != "\x1b[39m" {
			t.Fatalf("text fgAnsi = %q", theme.GetFgAnsi(ColorText))
		}
		if matched, _ := regexp.MatchString(`^\x1b\[38;`, theme.GetFgAnsi(ColorError)); !matched {
			t.Fatalf("error fgAnsi = %q", theme.GetFgAnsi(ColorError))
		}
	})

	t.Run("renders faint tokens with SGR 2 and closes it", func(t *testing.T) {
		// Without reported colors the system theme renders in indexed mode
		// with faint neutrals (pi resets the terminal colors in afterEach).
		SetTerminalColors(tui.TerminalColors{})
		theme := GetThemeByName(SystemThemeName)
		if theme == nil {
			t.Fatal("system theme not found")
		}
		if got := theme.Fg(ColorMuted, "x"); got != "\x1b[39m\x1b[2mx\x1b[22;39m" {
			t.Fatalf("fg(muted) = %q", got)
		}
		if got := theme.Style("x", ThemeStyle{Fg: ColorMuted}); got != "\x1b[39m\x1b[2mx\x1b[22m\x1b[39m" {
			t.Fatalf("style(muted) = %q", got)
		}
	})
}
