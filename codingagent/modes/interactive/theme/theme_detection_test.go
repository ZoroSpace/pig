package theme

import (
	"fmt"

	"github.com/samber/lo"
	"regexp"
	"testing"

	"github.com/ZoroSpace/pig/tui"
)

// Port of test/theme-detection.test.ts.
//
// Declared adaptations: DetectTerminalThemeForAuto's two concurrency
// assertions (backgroundQueryStarted before resolution, never-resolving
// background) have no blocking-call equivalent; pig serializes
// color-scheme → background (see theme.go deviations), so those two tests
// assert the resolution order instead.

func TestDetectTerminalBackgroundFromEnv(t *testing.T) {
	t.Run("uses the COLORFGBG background color index", func(t *testing.T) {
		d := DetectTerminalBackgroundFromEnv(&TerminalThemeDetectionOptions{Env: map[string]string{"COLORFGBG": "0;15"}})
		if d.Theme != TerminalThemeLight || d.Source != "COLORFGBG" || d.Confidence != "high" {
			t.Fatalf("got %+v", d)
		}
		d = DetectTerminalBackgroundFromEnv(&TerminalThemeDetectionOptions{Env: map[string]string{"COLORFGBG": "15;0"}})
		if d.Theme != TerminalThemeDark || d.Source != "COLORFGBG" || d.Confidence != "high" {
			t.Fatalf("got %+v", d)
		}
	})

	t.Run("uses the last COLORFGBG field as the background", func(t *testing.T) {
		d := DetectTerminalBackgroundFromEnv(&TerminalThemeDetectionOptions{Env: map[string]string{"COLORFGBG": "0;7;15"}})
		if d.Theme != TerminalThemeLight {
			t.Fatalf("got %v", d.Theme)
		}
	})

	t.Run("defaults to dark without terminal background hints", func(t *testing.T) {
		d := DetectTerminalBackgroundFromEnv(&TerminalThemeDetectionOptions{Env: map[string]string{}})
		if d.Theme != TerminalThemeDark || d.Source != "fallback" || d.Confidence != "low" {
			t.Fatalf("got %+v", d)
		}
	})
}

// stubBackgroundDetector records the queried timeout (pi:
// queryTerminalBackgroundColor mock).
type stubBackgroundDetector struct {
	timeoutMs int
	rgb       *tui.RgbColor
	err       error
}

func (s *stubBackgroundDetector) QueryTerminalBackgroundColor(timeoutMs int) (*tui.RgbColor, error) {
	s.timeoutMs = timeoutMs
	return s.rgb, s.err
}

func TestDetectTerminalBackgroundTheme(t *testing.T) {
	t.Run("uses the queried terminal background before environment hints", func(t *testing.T) {
		detector := &stubBackgroundDetector{rgb: &tui.RgbColor{R: 250, G: 250, B: 250}}
		d := DetectTerminalBackgroundTheme(TerminalBackgroundThemeDetectionOptions{
			Env:       map[string]string{"COLORFGBG": "15;0"},
			TimeoutMs: 250,
			Ui:        detector,
		})

		if detector.timeoutMs != 250 {
			t.Fatalf("timeoutMs = %d", detector.timeoutMs)
		}
		if d.Theme != TerminalThemeLight || d.Source != "terminal background" || d.Confidence != "high" {
			t.Fatalf("got %+v", d)
		}
	})

	t.Run("falls back to environment hints when the terminal query returns no color", func(t *testing.T) {
		d := DetectTerminalBackgroundTheme(TerminalBackgroundThemeDetectionOptions{
			Env:       map[string]string{"COLORFGBG": "15;0"},
			TimeoutMs: 250,
			Ui:        &stubBackgroundDetector{rgb: nil},
		})

		if d.Theme != TerminalThemeDark || d.Source != "COLORFGBG" || d.Confidence != "high" {
			t.Fatalf("got %+v", d)
		}
	})

	t.Run("falls back to environment hints when the terminal query fails", func(t *testing.T) {
		d := DetectTerminalBackgroundTheme(TerminalBackgroundThemeDetectionOptions{
			Env:       map[string]string{"COLORFGBG": "0;15"},
			TimeoutMs: 250,
			Ui:        &stubBackgroundDetector{err: fmt.Errorf("terminal write failed")},
		})

		if d.Theme != TerminalThemeLight || d.Source != "COLORFGBG" || d.Confidence != "high" {
			t.Fatalf("got %+v", d)
		}
	})
}

// stubAutoDetector is the auto variant (pi:
// queryTerminalColorScheme mock).
type stubAutoDetector struct {
	stubBackgroundDetector
	scheme    *TerminalTheme
	schemeErr error
}

func (s *stubAutoDetector) QueryTerminalColorScheme(timeoutMs int) (*TerminalTheme, error) {
	return s.scheme, s.schemeErr
}

func TestDetectTerminalThemeForAuto(t *testing.T) {
	t.Run("returns the preferred color-scheme result over the background query", func(t *testing.T) {
		detector := &stubAutoDetector{scheme: lo.ToPtr(TerminalThemeDark)}
		got := DetectTerminalThemeForAuto(TerminalAutoThemeDetectionOptions{TimeoutMs: 100, Ui: detector})
		if got != TerminalThemeDark {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("uses the background result when the color-scheme query fails", func(t *testing.T) {
		detector := &stubAutoDetector{
			stubBackgroundDetector: stubBackgroundDetector{rgb: &tui.RgbColor{R: 250, G: 250, B: 250}},
			schemeErr:              fmt.Errorf("color-scheme query failed"),
		}
		got := DetectTerminalThemeForAuto(TerminalAutoThemeDetectionOptions{TimeoutMs: 100, Ui: detector})
		if got != TerminalThemeLight {
			t.Fatalf("got %v", got)
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

func TestThemeDetectionFromRGB(t *testing.T) {
	t.Run("classifies RGB colors by luminance", func(t *testing.T) {
		if GetThemeForRgbColor(tui.RgbColor{R: 8, G: 8, B: 8}) != TerminalThemeDark {
			t.Fatal("expected dark")
		}
		if GetThemeForRgbColor(tui.RgbColor{R: 250, G: 250, B: 250}) != TerminalThemeLight {
			t.Fatal("expected light")
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
