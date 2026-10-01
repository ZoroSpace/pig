// External test package: it needs core.SettingsManager (real settings
// manager fixture), and core → core/tools → theme makes an internal test
// package a compile cycle. All identifiers used here are exported.
package theme_test

import (
	"strings"
	"testing"

	core "github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// Port of test/theme-controller.test.ts (v0.99.1 shape).
//
// Test adaptations (declared): pi's queryTerminalColors returns a promise;
// pig's fake TUI answers synchronously (the blocking QueryTerminalColors),
// records the late-reply callback, and exposes deliverLateReply so tests
// can emulate replies arriving after the timeout. Tests that stub env use
// the fake only; no real terminal is queried.

var (
	darkTerminalColors  = tui.TerminalColors{Foreground: &tui.RgbColor{R: 248, G: 248, B: 242}, Background: &tui.RgbColor{R: 40, G: 42, B: 54}}
	lightTerminalColors = tui.TerminalColors{Foreground: &tui.RgbColor{R: 30, G: 30, B: 30}, Background: &tui.RgbColor{R: 250, G: 250, B: 250}}
)

// fakeControllerTUI records the controller's TUI interactions (pi:
// createUi's vi.fn() mocks).
type fakeControllerTUI struct {
	queryTerminalColorsCalls  int
	queryTerminalColorsResult tui.TerminalColors
	lateReply                 func(colors tui.TerminalColors)
	setNotificationsCalls     []bool
	colorSchemeListener       func(scheme tui.TerminalColorScheme)
	unsubscribed              int
	invalidateCalls           int
	requestRenderCalls        int
}

func (f *fakeControllerTUI) Invalidate() { f.invalidateCalls++ }

func (f *fakeControllerTUI) RequestRender(force bool) { f.requestRenderCalls++ }

func (f *fakeControllerTUI) SetTerminalColorSchemeNotifications(enabled bool) {
	f.setNotificationsCalls = append(f.setNotificationsCalls, enabled)
}

func (f *fakeControllerTUI) OnTerminalColorSchemeChange(listener func(scheme tui.TerminalColorScheme)) func() {
	f.colorSchemeListener = listener
	return func() { f.unsubscribed++ }
}

func (f *fakeControllerTUI) QueryTerminalColors(timeoutMs int, onLateReply func(colors tui.TerminalColors)) tui.TerminalColors {
	f.queryTerminalColorsCalls++
	f.lateReply = onLateReply
	return f.queryTerminalColorsResult
}

func (f *fakeControllerTUI) emitTerminalColorScheme(terminalTheme tui.TerminalColorScheme) {
	if f.colorSchemeListener != nil {
		f.colorSchemeListener(terminalTheme)
	}
}

func newSettingsManager(t *testing.T, settings core.Settings) *core.SettingsManager {
	t.Helper()
	return core.InMemorySettingsManager(settings, nil)
}

func TestInteractiveThemeController(t *testing.T) {
	cleanup := func() {
		theme.SetTerminalColors(tui.TerminalColors{})
		theme.SetTerminalColorScheme("")
		theme.InitTheme("dark", false)
	}
	t.Cleanup(cleanup)

	t.Run("uses the initial theme without persisting it", func(t *testing.T) {
		ui := &fakeControllerTUI{}
		manager := newSettingsManager(t, core.Settings{"theme": "dark"})
		controller := theme.NewInteractiveThemeController(ui, theme.InteractiveThemeControllerOptions{
			GetSettingsManager:  func() theme.ThemeSettingsManager { return manager },
			ShowError:           func(string) {},
			OnChanged:           func() {},
			InitialThemeSetting: "light",
		})

		if got := theme.Current().GetName(); got != "light" {
			t.Fatalf("theme.name = %q", got)
		}
		if _, selection := controller.GetThemeSelection(); selection != true {
			t.Fatal("expected a theme selection")
		}
		controller.ApplyFromSettings()

		if ui.queryTerminalColorsCalls != 1 {
			t.Fatalf("queryTerminalColors calls = %d", ui.queryTerminalColorsCalls)
		}
		// pi asserts manager.setTheme/flush were not called; pig asserts the
		// setting is unchanged (setTheme would overwrite it).
		if setting, _ := manager.GetThemeSetting(); setting != "dark" {
			t.Fatalf("theme setting = %q", setting)
		}
	})

	t.Run("applies the theme immediately and lets startup wait for the colors", func(t *testing.T) {
		ui := &fakeControllerTUI{}
		manager := newSettingsManager(t, nil)
		controller := theme.NewInteractiveThemeController(ui, theme.InteractiveThemeControllerOptions{
			GetSettingsManager: func() theme.ThemeSettingsManager { return manager },
			ShowError:          func(string) {},
			OnChanged:          func() {},
		})

		// Grayscale until the terminal answers: the constructor marked the
		// colors pending before creating the system theme (pi asserts the
		// same state after applyFromSettings, whose query resolves
		// asynchronously; pig's blocking query applies the empty answer
		// inside ApplyFromSettings, so the grayscale window ends there).
		if got := theme.Current().GetFgAnsi(theme.ColorError); got != "\x1b[39m" {
			t.Fatalf("pending fgAnsi(error) = %q", got)
		}

		// An empty (timed-out) answer falls back to palette indices.
		controller.ApplyFromSettings()
		if got := theme.Current().GetFgAnsi(theme.ColorError); got != "\x1b[38;5;1m" {
			t.Fatalf("fallback fgAnsi(error) = %q", got)
		}

		ui.queryTerminalColorsResult = darkTerminalColors
		controller.ApplyFromSettings()
		controller.WaitTerminalColors()
		ansi := theme.Current().GetFgAnsi(theme.ColorError)
		if !strings.HasPrefix(ansi, "\x1b[38;") {
			t.Fatalf("fgAnsi(error) = %q", ansi)
		}
	})

	t.Run("falls back to palette indices, then applies colors that arrive after the timeout", func(t *testing.T) {
		ui := &fakeControllerTUI{}
		manager := newSettingsManager(t, nil)
		controller := theme.NewInteractiveThemeController(ui, theme.InteractiveThemeControllerOptions{
			GetSettingsManager: func() theme.ThemeSettingsManager { return manager },
			ShowError:          func(string) {},
			OnChanged:          func() {},
		})
		controller.ApplyFromSettings()
		if got := theme.Current().GetFgAnsi(theme.ColorError); got != "\x1b[38;5;1m" {
			t.Fatalf("fallback fgAnsi(error) = %q", got)
		}

		ui.lateReply(darkTerminalColors)
		if kind := theme.Current().Colors()[theme.ColorError].Kind; kind != tui.ColorKindRgb {
			t.Fatalf("colors.error.kind = %v", kind)
		}
	})

	t.Run("re-queries the colors on appearance changes and lets them decide", func(t *testing.T) {
		ui := &fakeControllerTUI{}
		manager := newSettingsManager(t, nil)
		controller := theme.NewInteractiveThemeController(ui, theme.InteractiveThemeControllerOptions{
			GetSettingsManager: func() theme.ThemeSettingsManager { return manager },
			ShowError:          func(string) {},
			OnChanged:          func() {},
		})
		controller.SetThemeSetting("light/dark")
		if len(ui.setNotificationsCalls) == 0 || ui.setNotificationsCalls[len(ui.setNotificationsCalls)-1] != true {
			t.Fatal("expected notifications enabled")
		}
		ui.queryTerminalColorsResult = lightTerminalColors
		controller.ApplyFromSettings()
		if theme.CurrentName() != "light" {
			t.Fatalf("theme = %q", theme.CurrentName())
		}

		ui.queryTerminalColorsResult = darkTerminalColors
		// The report says light, but the terminal renders dark.
		ui.emitTerminalColorScheme("light")
		if theme.CurrentName() != "dark" {
			t.Fatalf("theme = %q", theme.CurrentName())
		}
	})

	t.Run("uses the reported scheme for the system theme when the terminal reports no colors", func(t *testing.T) {
		t.Setenv("COLORFGBG", "")
		// pi's afterEach resets the global scheme between tests; clear the
		// light/dark report the previous subtest leaked.
		theme.SetTerminalColorScheme("")
		ui := &fakeControllerTUI{}
		manager := newSettingsManager(t, nil)
		controller := theme.NewInteractiveThemeController(ui, theme.InteractiveThemeControllerOptions{
			GetSettingsManager: func() theme.ThemeSettingsManager { return manager },
			ShowError:          func(string) {},
			OnChanged:          func() {},
		})
		controller.ApplyFromSettings()
		if theme.Current().Appearance() != theme.TerminalThemeDark {
			t.Fatalf("appearance = %q", theme.Current().Appearance())
		}

		ui.emitTerminalColorScheme("light")
		if theme.Current().Appearance() != theme.TerminalThemeLight {
			t.Fatalf("appearance = %q", theme.Current().Appearance())
		}
		if controller.GetTerminalTheme() != theme.TerminalThemeLight {
			t.Fatalf("getTerminalTheme = %q", controller.GetTerminalTheme())
		}
	})

	t.Run("re-renders only when the reported colors change", func(t *testing.T) {
		ui := &fakeControllerTUI{}
		manager := newSettingsManager(t, core.Settings{"theme": "dark"})
		controller := theme.NewInteractiveThemeController(ui, theme.InteractiveThemeControllerOptions{
			GetSettingsManager: func() theme.ThemeSettingsManager { return manager },
			ShowError:          func(string) {},
			OnChanged:          func() {},
		})
		query := func(colors tui.TerminalColors) {
			ui.queryTerminalColorsResult = colors
			controller.ApplyFromSettings()
		}

		query(darkTerminalColors)
		firstCount := ui.requestRenderCalls
		if firstCount == 0 {
			t.Fatal("expected an initial render request")
		}
		// A timeout keeps the known colors; erasing them would count as a
		// change and re-render.
		query(tui.TerminalColors{})
		query(darkTerminalColors)
		if ui.requestRenderCalls != firstCount {
			t.Fatalf("requestRender calls = %d, want %d", ui.requestRenderCalls, firstCount)
		}
	})

	t.Run("disables terminal appearance updates when disposed", func(t *testing.T) {
		ui := &fakeControllerTUI{}
		manager := newSettingsManager(t, core.Settings{"theme": "light/dark"})
		controller := theme.NewInteractiveThemeController(ui, theme.InteractiveThemeControllerOptions{
			GetSettingsManager: func() theme.ThemeSettingsManager { return manager },
			ShowError:          func(string) {},
			OnChanged:          func() {},
		})
		controller.ApplyFromSettings()

		controller.Dispose()

		if last := ui.setNotificationsCalls[len(ui.setNotificationsCalls)-1]; last != false {
			t.Fatal("expected notifications disabled")
		}
		if ui.unsubscribed != 1 {
			t.Fatalf("unsubscribe calls = %d", ui.unsubscribed)
		}
	})

	t.Run("lets an explicit selection replace the initial theme", func(t *testing.T) {
		ui := &fakeControllerTUI{}
		firstManager := newSettingsManager(t, core.Settings{"theme": "dark"})
		secondManager := newSettingsManager(t, core.Settings{"theme": "light"})
		manager := firstManager
		controller := theme.NewInteractiveThemeController(ui, theme.InteractiveThemeControllerOptions{
			GetSettingsManager:  func() theme.ThemeSettingsManager { return manager },
			ShowError:           func(string) {},
			OnChanged:           func() {},
			InitialThemeSetting: "light",
		})
		controller.ApplyFromSettings()

		if result := controller.SetThemeName("dark", false); !result.Success {
			t.Fatalf("setThemeName: %+v", result)
		}
		manager = secondManager
		controller.ApplyFromSettings()

		if selection, _ := controller.GetThemeSelection(); selection != "dark" {
			t.Fatalf("selection = %q", selection)
		}
		if theme.CurrentName() != "dark" {
			t.Fatalf("theme = %q", theme.CurrentName())
		}
	})

	t.Run("reloads theme settings when no initial theme was supplied", func(t *testing.T) {
		ui := &fakeControllerTUI{}
		manager := newSettingsManager(t, core.Settings{"theme": "dark"})
		controller := theme.NewInteractiveThemeController(ui, theme.InteractiveThemeControllerOptions{
			GetSettingsManager: func() theme.ThemeSettingsManager { return manager },
			ShowError:          func(string) {},
			OnChanged:          func() {},
		})
		controller.ApplyFromSettings()

		if theme.CurrentName() != "dark" {
			t.Fatalf("theme = %q", theme.CurrentName())
		}
		replacement := newSettingsManager(t, core.Settings{"theme": "light"})
		manager = replacement
		controller.ApplyFromSettings()
		if theme.CurrentName() != "light" {
			t.Fatalf("theme = %q", theme.CurrentName())
		}

		replacement2 := newSettingsManager(t, core.Settings{"theme": "dark"})
		manager = replacement2
		controller.ApplyFromSettings()
		if theme.CurrentName() != "dark" {
			t.Fatalf("theme = %q", theme.CurrentName())
		}
	})
}
