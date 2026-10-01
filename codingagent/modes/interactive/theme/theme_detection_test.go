package theme

import (
	"regexp"
	"testing"

	"github.com/ZoroSpace/pig/tui"
)

// Port of test/theme-detection.test.ts (v0.99.1 shape). The v0.87.1-era
// detection family (TerminalThemeDetection/Source/Confidence,
// DetectTerminalBackgroundTheme/ForAuto) was removed upstream with #10067;
// its tests are replaced by detectColorFgBgTheme/detectTerminalTheme.

func TestDetectColorFgBgTheme(t *testing.T) {
	t.Run("classifies the last field by palette index like Vim", func(t *testing.T) {
		env := func(key string) string {
			if key == "COLORFGBG" {
				return "15;0"
			}
			return ""
		}
		if got := DetectColorFgBgTheme(env); got != TerminalThemeDark {
			t.Fatalf("15;0: got %q", got)
		}
		env2 := func(key string) string {
			if key == "COLORFGBG" {
				return "0;7;15"
			}
			return ""
		}
		if got := DetectColorFgBgTheme(env2); got != TerminalThemeLight {
			t.Fatalf("0;7;15: got %q", got)
		}
		// Solarized Dark's background is bright black.
		env3 := func(key string) string {
			if key == "COLORFGBG" {
				return "12;8"
			}
			return ""
		}
		if got := DetectColorFgBgTheme(env3); got != TerminalThemeDark {
			t.Fatalf("12;8: got %q", got)
		}
		// rxvt writes "default" when the background is not a palette color.
		env4 := func(key string) string {
			if key == "COLORFGBG" {
				return "15;default"
			}
			return ""
		}
		if got := DetectColorFgBgTheme(env4); got != "" {
			t.Fatalf("15;default: got %q, want unset", got)
		}
		if got := DetectColorFgBgTheme(func(string) string { return "" }); got != "" {
			t.Fatalf("unset: got %q, want unset", got)
		}
	})
}

func TestDetectTerminalTheme(t *testing.T) {
	t.Run("prefers the background, then the reported scheme, then COLORFGBG, then dark", func(t *testing.T) {
		env := func(key string) string {
			if key == "COLORFGBG" {
				return "0;15"
			}
			return ""
		}
		if got := DetectTerminalTheme(&tui.TerminalColors{Background: &tui.RgbColor{R: 8, G: 8, B: 8}}, TerminalThemeLight, env); got != TerminalThemeDark {
			t.Fatalf("background beats scheme: got %q", got)
		}
		if got := DetectTerminalTheme(&tui.TerminalColors{}, TerminalThemeDark, env); got != TerminalThemeDark {
			t.Fatalf("scheme beats env: got %q", got)
		}
		if got := DetectTerminalTheme(&tui.TerminalColors{}, "", env); got != TerminalThemeLight {
			t.Fatalf("env: got %q", got)
		}
		if got := DetectTerminalTheme(&tui.TerminalColors{}, "", func(string) string { return "" }); got != TerminalThemeDark {
			t.Fatalf("fallback: got %q", got)
		}
	})

	t.Run("follows the foreground when text is readable that way", func(t *testing.T) {
		background := tui.RgbColor{R: 118, G: 118, B: 118}
		if got := DetectTerminalTheme(&tui.TerminalColors{Background: &background}, "", nil); got != TerminalThemeLight {
			t.Fatalf("mid background: got %q", got)
		}
		white := tui.RgbColor{R: 255, G: 255, B: 255}
		if got := DetectTerminalTheme(&tui.TerminalColors{Background: &background, Foreground: &white}, "", nil); got != TerminalThemeDark {
			t.Fatalf("white foreground: got %q", got)
		}
		// White text cannot reach 4.5:1 on mid-gray.
		midGray := tui.RgbColor{R: 128, G: 128, B: 128}
		if got := DetectTerminalTheme(&tui.TerminalColors{Background: &midGray, Foreground: &white}, "", nil); got != TerminalThemeLight {
			t.Fatalf("mid-gray: got %q", got)
		}
	})
}

func TestThemeColorMode(t *testing.T) {
	t.Run("uses terminal capabilities", func(t *testing.T) {
		defer tui.ResetCapabilitiesCache()
		tui.SetCapabilities(tui.TerminalCapabilities{Images: nil, TrueColor: false, Hyperlinks: false})
		ansi256Theme := GetThemeByName("dark")
		if ansi256Theme == nil {
			t.Fatal("dark theme not found")
		}
		if ansi256Theme.GetColorMode() != ColorMode256 {
			t.Fatalf("mode = %v", ansi256Theme.GetColorMode())
		}
		if matched, _ := regexp.MatchString(`^\x1b\[38;5;\d+m$`, ansi256Theme.GetFgAnsi(ColorAccent)); !matched {
			t.Fatalf("fgAnsi = %q", ansi256Theme.GetFgAnsi(ColorAccent))
		}

		tui.SetCapabilities(tui.TerminalCapabilities{Images: nil, TrueColor: true, Hyperlinks: false})
		truecolorTheme := GetThemeByName("dark")
		if truecolorTheme == nil {
			t.Fatal("dark theme not found")
		}
		if truecolorTheme.GetColorMode() != ColorModeTruecolor {
			t.Fatalf("mode = %v", truecolorTheme.GetColorMode())
		}
		if matched, _ := regexp.MatchString(`^\x1b\[38;2;\d+;\d+;\d+m$`, truecolorTheme.GetFgAnsi(ColorAccent)); !matched {
			t.Fatalf("fgAnsi = %q", truecolorTheme.GetFgAnsi(ColorAccent))
		}
	})
}

// Regression (2026-10-01,
// docs/fix/2026-10-01-truecolor-detection-missing.md): the theme picked its
// color mode from tui.GetCapabilities().TrueColor, which never detected
// truecolor, so every theme color was silently quantized to the nearest
// 256-color palette entry (the user-visible case: the user message bubble
// rendered light periwinkle instead of the intended light gray-blue).
func TestThemeColorModeFollowsDetectedCapabilities(t *testing.T) {
	clearDetectionEnv := func() {
		for _, key := range []string{"TERM", "TERM_PROGRAM", "TERMINAL_EMULATOR", "COLORTERM", "TMUX", "ITERM_SESSION_ID", "PI_TRUE_COLOR"} {
			t.Setenv(key, "")
		}
		tui.SetCapabilityOverrides(tui.TerminalCapabilityOverrides{})
		tui.ResetCapabilitiesCache()
	}
	t.Cleanup(func() {
		tui.SetCapabilityOverrides(tui.TerminalCapabilityOverrides{})
		tui.ResetCapabilitiesCache()
	})

	t.Run("truecolor terminals get the exact theme colors", func(t *testing.T) {
		clearDetectionEnv()
		t.Setenv("TERM_PROGRAM", "iTerm.app")
		tui.ResetCapabilitiesCache()

		dark := GetThemeByName("dark")
		if dark == nil {
			t.Fatal("dark theme not found")
		}
		if got := dark.GetBgAnsi(BgUserMessageBg); got != "\x1b[48;2;33;59;73m" {
			t.Fatalf("dark userMessageBg = %q, want truecolor #213b49", got)
		}
		light := GetThemeByName("light")
		if light == nil {
			t.Fatal("light theme not found")
		}
		if got := light.GetBgAnsi(BgUserMessageBg); got != "\x1b[48;2;223;231;236m" {
			t.Fatalf("light userMessageBg = %q, want truecolor #dfe7ec", got)
		}
	})

	t.Run("256-color terminals get the nearest palette index", func(t *testing.T) {
		clearDetectionEnv()
		t.Setenv("TERM", "xterm-256color")
		tui.ResetCapabilitiesCache()

		dark := GetThemeByName("dark")
		if dark == nil {
			t.Fatal("dark theme not found")
		}
		if got := dark.GetBgAnsi(BgUserMessageBg); got != "\x1b[48;5;23m" {
			t.Fatalf("dark userMessageBg = %q, want 256-color index 23", got)
		}
		light := GetThemeByName("light")
		if light == nil {
			t.Fatal("light theme not found")
		}
		if got := light.GetBgAnsi(BgUserMessageBg); got != "\x1b[48;5;189m" {
			t.Fatalf("light userMessageBg = %q, want 256-color index 189", got)
		}
	})
}

func TestThemeSettingHelpers(t *testing.T) {
	t.Run("parses and resolves automatic theme settings", func(t *testing.T) {
		pair := ParseAutoThemeSetting("light/dark")
		if pair == nil || pair.LightTheme != "light" || pair.DarkTheme != "dark" {
			t.Fatalf("got %+v", pair)
		}
		if got, _ := ResolveThemeSetting("dark", TerminalThemeLight); got != "dark" {
			t.Fatalf("got %q", got)
		}
		if got, _ := ResolveThemeSetting("light/dark", TerminalThemeLight); got != "light" {
			t.Fatalf("got %q", got)
		}
		if got, _ := ResolveThemeSetting("light/dark", TerminalThemeDark); got != "dark" {
			t.Fatalf("got %q", got)
		}
		if _, ok := ResolveThemeSetting("light/dark/extra", TerminalThemeDark); ok {
			t.Fatal("expected undefined for light/dark/extra")
		}
	})
}
